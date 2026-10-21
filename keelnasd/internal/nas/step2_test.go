package nas

// Tests of the second step (docs/04 section 9): disks, addresses, snapshots, scrub, SMART,
// alerts, restore, and their handlers.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"keelnas/internal/sys"
)

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseAddresses(t *testing.T) {
	out := "vtnet0: flags=1008843<UP,BROADCAST,RUNNING> metric 0 mtu 1500\n\toptions=880028<VLAN_MTU>\n\tinet 192.0.2.10 netmask 0xffffff00 broadcast 192.0.2.255\n\tinet 10.1.1.2 netmask 0xff000000\n" +
		"lo0: flags=1008049<UP,LOOPBACK,RUNNING> metric 0 mtu 16384\n\tinet 127.0.0.1 netmask 0xff000000\n" +
		"vtnet1: flags=8802<BROADCAST,SIMPLEX,MULTICAST> metric 0 mtu 1500\n\toptions=80028<VLAN_MTU>\n"
	got := ParseAddresses(out)
	if strings.Join(got, ",") != "192.0.2.10,10.1.1.2" {
		t.Errorf("addresses %v", got)
	}
	if got := ParseAddresses(""); got == nil || len(got) != 0 {
		t.Errorf("empty: %#v", got)
	}
}

func TestClassAt(t *testing.T) {
	r := DefaultRetention
	for ts, want := range map[string]string{
		"2026-10-02T11:00:00Z": "hourly",
		"2026-10-02T11:01:00Z": "",
		"2026-10-03T00:00:00Z": "daily",  // Saturday
		"2026-10-05T00:00:00Z": "weekly", // Monday
		"2026-10-05T01:00:00Z": "hourly",
	} {
		if got := ClassAt(utc(ts), r); got != want {
			t.Errorf("ClassAt(%s) = %q, want %q", ts, got, want)
		}
	}
	// A class that is off gives way to the next one down.
	if got := ClassAt(utc("2026-10-05T00:00:00Z"), Retention{Hourly: 24, Daily: 7}); got != "daily" {
		t.Errorf("weekly off: %q", got)
	}
	if got := ClassAt(utc("2026-10-05T00:00:00Z"), Retention{Hourly: 24}); got != "hourly" {
		t.Errorf("weekly and daily off: %q", got)
	}
	if got := ClassAt(utc("2026-10-02T11:00:00Z"), Retention{Daily: 7}); got != "" {
		t.Errorf("hourly off: %q", got)
	}
	// Times in another zone count in UTC.
	if got := ClassAt(utc("2026-10-05T09:00:00+09:00"), r); got != "weekly" {
		t.Errorf("zone: %q", got)
	}
}

func TestSnapNames(t *testing.T) {
	n := SnapName(utc("2026-10-05T09:30:15+09:00"))
	if n != "keelnas-20261005-003015" {
		t.Errorf("name %s", n)
	}
	if tm, ok := SnapTime(n); !ok || !tm.Equal(utc("2026-10-05T00:30:15Z")) {
		t.Errorf("time %v %v", tm, ok)
	}
	if _, ok := SnapTime("auto-20261005"); ok {
		t.Error("a foreign name parsed")
	}
	out := "tank/shares/a@keelnas-20261002-110000\t57344\thourly\n" +
		"tank/shares/a@keelnas-20261002-120000\t0\t-\n" +
		"tank/shares/a@other\t1\t-\n" +
		"tank/luns/l1@keelnas-20261005-000000\t4096\tweekly\n"
	m := ParseSnapshots(out)
	a := m["tank/shares/a"]
	if len(a) != 2 || a[0].Name != "keelnas-20261002-120000" || a[0].Class != "manual" || a[1].Used != 57344 || a[1].Time != "2026-10-02T11:00:00Z" {
		t.Errorf("snapshots %+v", a)
	}
	if len(m["tank/luns/l1"]) != 1 {
		t.Errorf("lun %+v", m)
	}
}

func TestPrunePlan(t *testing.T) {
	var snaps []Snap
	add := func(ts, class string) {
		snaps = append(snaps, Snap{Name: SnapName(utc(ts)), Class: class})
	}
	for h := 0; h < 6; h++ {
		add(fmt.Sprintf("2026-10-02T%02d:00:00Z", 10+h), "hourly")
	}
	add("2026-10-01T00:00:00Z", "daily")
	add("2026-10-02T00:00:00Z", "daily")
	add("2026-09-28T00:00:00Z", "weekly")
	add("2026-01-01T12:00:00Z", "manual") // the oldest of all: never pruned
	got := PrunePlan(snaps, Retention{Hourly: 2, Daily: 1, Weekly: 1})
	sort.Strings(got)
	want := []string{"keelnas-20261001-000000", "keelnas-20261002-100000", "keelnas-20261002-110000", "keelnas-20261002-120000", "keelnas-20261002-130000"}
	if !slices.Equal(got, want) {
		t.Errorf("prune %v, want %v", got, want)
	}
	// 0 = the class is off: its snapshots go; manual ones stay.
	got = PrunePlan(snaps, Retention{})
	if len(got) != len(snaps)-1 || slices.Contains(got, "keelnas-20260101-120000") {
		t.Errorf("all off: %v", got)
	}
}

// snapWorld adds snapshots and "written" to the fake world.
type snapWorld struct {
	snaps   map[string]map[string]string // dataset -> name -> class
	written map[string]int64
	fail    map[string]bool // dataset whose snapshot fails
}

func addSnapWorld(w *world) *snapWorld {
	sw := &snapWorld{snaps: map[string]map[string]string{}, written: map[string]int64{}, fail: map[string]bool{}}
	w.extra = func(line string, a []string) (string, error, bool) {
		switch {
		case strings.HasPrefix(line, "zfs list -H -p -t snapshot"):
			var b strings.Builder
			for ds, m := range sw.snaps {
				for n, c := range m {
					fmt.Fprintf(&b, "%s@%s\t4096\t%s\n", ds, n, c)
				}
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "zfs get -H -p -o name,value written"):
			var b strings.Builder
			for _, ds := range a[7:] {
				if _, ok := w.datasets[ds]; ok {
					fmt.Fprintf(&b, "%s\t%d\n", ds, sw.written[ds])
				}
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "zfs snapshot"):
			ds, n, _ := strings.Cut(a[len(a)-1], "@")
			if sw.fail[ds] {
				return "out of space", sys.ErrFail, true
			}
			if sw.snaps[ds] == nil {
				sw.snaps[ds] = map[string]string{}
			}
			sw.snaps[ds][n] = strings.TrimPrefix(a[3], ClassProp+"=")
			sw.written[ds] = 0
			return "", nil, true
		case strings.HasPrefix(line, "zfs destroy") && strings.Contains(line, "@"):
			ds, n, _ := strings.Cut(a[len(a)-1], "@")
			delete(sw.snaps[ds], n)
			return "", nil, true
		}
		return "", nil, false
	}
	return sw
}

// A server with pool tank, user alice, share a and LUN l1, and snapshots in the fake.
func newSnapServer(t *testing.T) (*Server, *world, *snapWorld, *sys.Fake, *httptest.Server) {
	s, w, f, ts := newTestServer(t)
	sw := addSnapWorld(w)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	do(t, ts, "POST", "/v1/users", `{"name":"alice","password":"x"}`)
	if c, m := do(t, ts, "POST", "/v1/shares", `{"name":"a","rw":["alice"]}`); c != 200 {
		t.Fatalf("share: %d %v", c, m)
	}
	if c, m := do(t, ts, "POST", "/v1/luns", `{"name":"l1","size_gib":1}`); c != 200 {
		t.Fatalf("lun: %d %v", c, m)
	}
	return s, w, sw, f, ts
}

func TestSnapshotTick(t *testing.T) {
	s, w, sw, f, _ := newSnapServer(t)
	const A, L = "tank/shares/a", "tank/luns/l1"
	sw.written[A], sw.written[L] = 1000, 5000

	// Not on the hour: nothing at all.
	r := s.Tick(utc("2026-10-02T11:30:00Z"), nil, []string{JobSnapshots})
	if f.Ran("zfs snapshot") || r.Class != "" {
		t.Errorf("snapshot off the hour: %+v", r)
	}
	// On the hour: both, hourly.
	r = s.Tick(utc("2026-10-02T11:00:00Z"), nil, nil)
	if len(r.Created) != 2 || sw.snaps[A]["keelnas-20261002-110000"] != "hourly" || sw.snaps[L]["keelnas-20261002-110000"] != "hourly" {
		t.Fatalf("hourly: %+v %v", r, sw.snaps)
	}
	if !f.Ran("zpool sync tank") {
		t.Error("no zpool sync before reading written")
	}
	// Nothing written since: no snapshot.
	r = s.Tick(utc("2026-10-02T12:00:00Z"), nil, nil)
	if len(r.Created) != 0 || len(r.Skipped) != 2 || len(sw.snaps[A]) != 1 {
		t.Errorf("nothing written: %+v", r)
	}
	// Day and week boundaries.
	sw.written[A] = 1
	r = s.Tick(utc("2026-10-03T00:00:00Z"), nil, nil)
	if r.Class != "daily" || sw.snaps[A]["keelnas-20261003-000000"] != "daily" || len(sw.snaps[L]) != 1 {
		t.Errorf("daily: %+v", r)
	}
	sw.written[A] = 1
	s.Tick(utc("2026-10-05T00:00:00Z"), nil, nil)
	if sw.snaps[A]["keelnas-20261005-000000"] != "weekly" {
		t.Errorf("weekly: %v", sw.snaps[A])
	}
	// Retention: keep 2 hourly; a manual one is never pruned.
	sw.snaps[A]["keelnas-20200101-000000"] = "manual"
	s.mu.Lock()
	s.st.Snapshots = Retention{Hourly: 2, Daily: 1, Weekly: 1}
	s.mu.Unlock()
	for h := 1; h <= 4; h++ {
		sw.written[A] = 1
		s.Tick(utc(fmt.Sprintf("2026-10-05T%02d:00:00Z", h)), nil, nil)
	}
	var hourly []string
	for n, c := range sw.snaps[A] {
		if c == "hourly" {
			hourly = append(hourly, n)
		}
	}
	sort.Strings(hourly)
	if strings.Join(hourly, " ") != "keelnas-20261005-030000 keelnas-20261005-040000" {
		t.Errorf("hourly kept: %v", hourly)
	}
	if sw.snaps[A]["keelnas-20200101-000000"] != "manual" || sw.snaps[A]["keelnas-20261003-000000"] != "daily" || sw.snaps[A]["keelnas-20261005-000000"] != "weekly" {
		t.Errorf("after pruning: %v", sw.snaps[A])
	}
	// The LUN's old hourly one is pruned too (keep 2, it has 1: stays).
	if len(sw.snaps[L]) != 1 {
		t.Errorf("lun: %v", sw.snaps[L])
	}
	// A failure is kept for the alert, and the next good round clears it.
	sw.written[A], sw.fail[A] = 1, true
	s.Tick(utc("2026-10-05T05:00:00Z"), nil, nil)
	a, _ := s.alerts(nil)
	if !hasAlert(a, "snapshot_failed", "a") {
		t.Errorf("no snapshot_failed: %+v", a)
	}
	sw.fail[A] = false
	s.Tick(utc("2026-10-05T06:00:00Z"), nil, nil)
	a, _ = s.alerts(nil)
	if hasAlert(a, "snapshot_failed", "a") {
		t.Errorf("snapshot_failed stayed: %+v", a)
	}
	// A dataset that is not there (pool not imported) is left out.
	delete(w.datasets, L)
	sw.written[L] = 1
	r = s.Tick(utc("2026-10-05T07:00:00Z"), nil, nil)
	if slices.ContainsFunc(r.Created, func(x string) bool { return strings.HasPrefix(x, L) }) {
		t.Errorf("snapshot of a missing dataset: %+v", r)
	}
}

func hasAlert(a []Alert, code, obj string) bool {
	return slices.ContainsFunc(a, func(x Alert) bool { return x.Code == code && x.Object == obj })
}

func TestSnapshotAPI(t *testing.T) {
	s, _, sw, _, ts := newSnapServer(t)
	s.Now = func() time.Time { return utc("2026-10-02T11:22:33Z") }
	sw.snaps["tank/shares/a"] = map[string]string{"keelnas-20261002-110000": "hourly", "keelnas-20261002-100000": "hourly"}

	c, m := do(t, ts, "GET", "/v1/shares/a/snapshots", "")
	list := m["snapshots"].([]any)
	if c != 200 || len(list) != 2 || list[0].(map[string]any)["name"] != "keelnas-20261002-110000" {
		t.Fatalf("list: %d %v", c, m)
	}
	for _, k := range []string{"name", "time", "class", "used"} {
		if _, ok := list[0].(map[string]any)[k]; !ok {
			t.Errorf("snapshot lacks %s", k)
		}
	}
	c, m = do(t, ts, "POST", "/v1/shares/a/snapshots", "")
	if c != 200 || m["name"] != "keelnas-20261002-112233" || m["class"] != "manual" || m["time"] != "2026-10-02T11:22:33Z" {
		t.Fatalf("create: %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/shares/a/snapshots", ""); c != 409 {
		t.Errorf("same second: %d", c)
	}
	if c, _ := do(t, ts, "GET", "/v1/shares/nope/snapshots", ""); c != 404 {
		t.Errorf("no share: %d", c)
	}
	if c, _ := do(t, ts, "DELETE", "/v1/shares/a/snapshots/foo", ""); c != 400 {
		t.Errorf("foreign name: %d", c)
	}
	if c, _ := do(t, ts, "DELETE", "/v1/shares/a/snapshots/keelnas-20000101-000000", ""); c != 404 {
		t.Errorf("missing snapshot: %d", c)
	}
	if c, m := do(t, ts, "DELETE", "/v1/shares/a/snapshots/keelnas-20261002-100000", ""); c != 200 || m["ok"] != true {
		t.Errorf("delete: %d %v", c, m)
	}
	if _, ok := sw.snaps["tank/shares/a"]["keelnas-20261002-100000"]; ok {
		t.Error("not destroyed")
	}
	// LUNs have the same.
	if c, m := do(t, ts, "POST", "/v1/luns/l1/snapshots", ""); c != 200 || m["class"] != "manual" {
		t.Errorf("lun snapshot: %d %v", c, m)
	}
	if c, m := do(t, ts, "GET", "/v1/luns/l1/snapshots", ""); c != 200 || len(m["snapshots"].([]any)) != 1 {
		t.Errorf("lun list: %d %v", c, m)
	}
	// smb4.conf has the previous versions.
	conf, _ := s.Sys.ReadFile(SmbConf)
	for _, want := range []string{"vfs objects = shadow_copy2", "shadow:snapdir = .zfs/snapshot",
		"shadow:format = keelnas-%Y%m%d-%H%M%S", "shadow:sort = desc", "shadow:localtime = no"} {
		if !strings.Contains(string(conf), "\n  "+want+"\n") {
			t.Errorf("smb4.conf lacks %q", want)
		}
	}
}

func TestSettingsAPI(t *testing.T) {
	s, _, f, ts := newTestServer(t)
	c, m := do(t, ts, "GET", "/v1/settings", "")
	sn := m["snapshots"].(map[string]any)
	if c != 200 || sn["hourly"] != float64(24) || sn["daily"] != float64(7) || sn["weekly"] != float64(4) {
		t.Fatalf("defaults: %d %v", c, m)
	}
	c, m = do(t, ts, "PUT", "/v1/settings", `{"snapshots":{"hourly":2,"weekly":0}}`)
	sn = m["snapshots"].(map[string]any)
	if c != 200 || sn["hourly"] != float64(2) || sn["daily"] != float64(7) || sn["weekly"] != float64(0) {
		t.Fatalf("put: %d %v", c, m)
	}
	b, _ := f.ReadFile(StatePath)
	if !strings.Contains(string(b), `"hourly": 2`) || s.State().Snapshots.Weekly != 0 {
		t.Errorf("not saved: %s", b)
	}
	if c, _ := do(t, ts, "PUT", "/v1/settings", `{"snapshots":{"hourly":-1}}`); c != 400 {
		t.Errorf("negative: %d", c)
	}
	if c, _ := do(t, ts, "PUT", "/v1/settings", `{"snapshot":{}}`); c != 400 {
		t.Errorf("unknown field: %d", c)
	}
	// An old state.json without "snapshots" gets the defaults; a partial one keeps the rest.
	st, err := ParseState([]byte(`{"version":1,"key":"k","snapshots":{"hourly":0}}`))
	if err != nil || st.Snapshots != (Retention{0, 7, 4}) {
		t.Errorf("partial: %+v %v", st.Snapshots, err)
	}
	st, _ = ParseState([]byte(`{"version":1,"key":"k"}`))
	if st.Snapshots != DefaultRetention {
		t.Errorf("missing: %+v", st.Snapshots)
	}
}

func TestScrubDue(t *testing.T) {
	now := utc("2026-11-05T03:00:00Z")
	created := utc("2026-10-02T10:00:00Z").Unix()
	young := utc("2026-10-20T10:00:00Z").Unix()
	for _, tc := range []struct {
		name    string
		scan    Scan
		created int64
		want    bool
	}{
		{"never scrubbed, old pool", Scan{}, created, true},
		{"never scrubbed, young pool", Scan{}, young, false},
		{"recent scrub", Scan{"SCRUB", "FINISHED", utc("2026-10-20T03:10:00Z").Unix(), 0}, created, false},
		{"old scrub", Scan{"SCRUB", "FINISHED", utc("2026-10-04T03:10:00Z").Unix(), 0}, created, true},
		{"running", Scan{"SCRUB", "SCANNING", 0, 0}, created, false},
		{"resilvering", Scan{"RESILVER", "SCANNING", 0, 0}, created, false},
		{"after a resilver: by creation", Scan{"RESILVER", "FINISHED", utc("2026-11-01T00:00:00Z").Unix(), 0}, created, true},
		{"creation unknown", Scan{}, 0, false},
	} {
		if got := ScrubDue(PoolInfo{Scan: tc.scan}, tc.created, now); got != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

func TestScrubTick(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "zfs get -H -p -o value creation tank" {
			return fmt.Sprint(utc("2026-09-01T00:00:00Z").Unix()), nil, true
		}
		return "", nil, false
	}
	f.Reset()
	s.Tick(utc("2026-10-05T02:00:00Z"), nil, []string{JobScrub})
	if f.Ran("zpool scrub") {
		t.Error("scrub at 02:00")
	}
	r := s.Tick(utc("2026-10-05T03:00:00Z"), nil, []string{JobScrub})
	if !f.Ran("zpool scrub tank") || len(r.Scrubs) != 1 {
		t.Errorf("no scrub at 03:00: %+v", r)
	}
	// The scan line of the parsed status counts: a recent scrub means none.
	w.mu.Lock()
	w.status = strings.Replace(w.status, `"pool_guid":777,`, fmt.Sprintf(`"pool_guid":777,"scan_stats":{"function":"SCRUB","state":"FINISHED","end_time":%d,"errors":0},`, utc("2026-10-05T03:05:00Z").Unix()), 1)
	w.mu.Unlock()
	f.Reset()
	s.Tick(utc("2026-10-06T03:00:00Z"), nil, []string{JobScrub})
	if f.Ran("zpool scrub") {
		t.Error("scrub a day after the last one")
	}
}

func TestParseSmart(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	i := func(v int) *int { return &v }
	for file, want := range map[string]SmartInfo{
		"smart-ata-ok.json":      {Status: "ok", Temperature: 37, Health: "passed", Protocol: "ATA", Hours: i64(12034), Reallocated: i64(0), Pending: i64(0)},
		"smart-ata-failing.json": {Status: "failing", Temperature: 41, Health: "failed", Protocol: "ATA", Reallocated: i64(4012), Pending: i64(88)},
		"smart-nvme.json":        {Status: "ok", Temperature: 44, Health: "passed", Protocol: "NVMe", Hours: i64(8000), Wear: i(3), MediaErrors: i64(0)},
		"smart-virtio.json":      {Status: "unknown"}, // real, from the QEMU test VM
	} {
		if got := ParseSmart(readTestdata(t, file)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %s, want %s", file, smartText(got), smartText(want))
		}
	}
	if got := ParseSmart("not json"); got.Status != "unknown" {
		t.Errorf("garbage: %+v", got)
	}
	// Bit 3 of the exit status alone also means failing.
	if got := ParseSmart(`{"smartctl":{"exit_status":8},"smart_status":{"passed":true}}`); got.Status != "failing" || got.Health != "failed" {
		t.Errorf("bit 3: %+v", got)
	}
	// -n standby on a sleeping disk: bit 1, no SMART data.
	if got := ParseSmart(`{"smartctl":{"exit_status":2,"messages":[{"string":"Device is in STANDBY mode, exit(2)","severity":"info"}]},"device":{"protocol":"ATA"}}`); !got.Standby || got.Status != "unknown" {
		t.Errorf("standby: %+v", got)
	}
	// Bit 1 without that message (bhyve's NVMe: no SMART log) is not standby.
	if got := ParseSmart(`{"smartctl":{"exit_status":2,"messages":[{"string":"Read SMART/Health Information Log failed","severity":"error"}]},"device":{"protocol":"NVMe"}}`); got.Standby {
		t.Errorf("bit 1 alone: %+v", got)
	}
	// A vendor's high bits above the 32-bit raw value are not sectors.
	if got := ParseSmart(`{"smart_status":{"passed":true},"ata_smart_attributes":{"table":[{"id":5,"raw":{"value":4294967297}}]}}`); got.Reallocated == nil || *got.Reallocated != 1 {
		t.Errorf("raw high bits: %+v", got)
	}
}

func smartText(x SmartInfo) string {
	b, _ := json.Marshal(x)
	return string(b)
}

func TestSmartTickAndDisks(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	failing := readTestdata(t, "smart-ata-failing.json")
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "smartctl -H -A -j /dev/vtbd1" {
			return failing, sys.ErrFail, true
		}
		return "", nil, false
	}
	// Before the first run every disk is unknown.
	_, m := do(t, ts, "GET", "/v1/disks", "")
	d1 := m["disks"].([]any)[1].(map[string]any)
	if d1["smart"] != "unknown" || d1["temperature"] != float64(0) {
		t.Errorf("before: %v", d1)
	}
	// The first tick runs SMART (on pool disks only), the next one within 24 h does not.
	s.Tick(utc("2026-10-02T11:07:00Z"), nil, nil)
	if !f.Ran("smartctl -H -A -j /dev/vtbd1") || f.Ran("smartctl -H -A -j /dev/vtbd0") {
		t.Errorf("smartctl calls: %v", f.Calls)
	}
	f.Reset()
	s.Tick(utc("2026-10-03T11:06:00Z"), nil, nil)
	if f.Ran("smartctl") {
		t.Error("SMART again within 24 h")
	}
	s.Tick(utc("2026-10-03T11:07:00Z"), nil, nil)
	if !f.Ran("smartctl") {
		t.Error("no SMART after 24 h")
	}
	_, m = do(t, ts, "GET", "/v1/disks", "")
	d1 = m["disks"].([]any)[1].(map[string]any)
	if d1["smart"] != "failing" || d1["temperature"] != float64(41) {
		t.Errorf("after: %v", d1)
	}
	_, m = do(t, ts, "GET", "/v1/alerts", "")
	if !strings.Contains(fmt.Sprint(m), "disk_failing") {
		t.Errorf("alerts: %v", m)
	}
}

func TestComputeAlerts(t *testing.T) {
	in := AlertInput{
		Pools: []PoolInfo{
			{Name: "tank", Health: "DEGRADED", Size: 100, Used: 10, Protected: true},
			{Name: "tank2", Health: "ONLINE", Size: 100, Used: 90, Protected: false},
			{Name: "tank3", Health: "ONLINE", Size: 100, Used: 89, Protected: true, Scan: Scan{"SCRUB", "FINISHED", 1, 3}},
			{Name: "tank4", Health: "MISSING"},
		},
		Disks:     []Disk{{Name: "ada1", Smart: "failing"}, {Name: "ada2", Smart: "ok"}, {Name: "vtbd1", Smart: "unknown"}},
		ApplyErrs: []string{"pool tank4: cannot import", "share x: pool tank4 is not imported"},
		SnapFails: map[string]string{"docs": "out of space", "l1": "busy"},
	}
	got := map[string]string{}
	for _, a := range ComputeAlerts(in) {
		got[a.Code+"/"+a.Object] = a.Level
		if a.Text == "" {
			t.Errorf("%s without text", a.Code)
		}
	}
	want := map[string]string{
		"pool_degraded/tank": "error", "pool_degraded/tank4": "error",
		"pool_no_protection/tank2": "warning", "pool_full/tank2": "warning",
		"scrub_errors/tank3": "error", "disk_failing/ada1": "error",
		"apply_failed/pool tank4: cannot import": "error",
		"snapshot_failed/docs":                   "warning", "snapshot_failed/l1": "warning",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("alerts\n got %v\nwant %v", got, want)
	}
	if a := ComputeAlerts(AlertInput{Pools: []PoolInfo{{Name: "t", Health: "ONLINE", Size: 100, Used: 50, Protected: true}}}); len(a) != 0 {
		t.Errorf("healthy: %+v", a)
	}

	// since: kept while the alert stays, forgotten when it goes.
	since := map[string]time.Time{}
	a1 := ComputeAlerts(AlertInput{Pools: []PoolInfo{{Name: "t", Health: "DEGRADED", Protected: true}}})
	stampAlerts(a1, since, utc("2026-10-02T10:00:00Z"))
	a2 := ComputeAlerts(AlertInput{Pools: []PoolInfo{{Name: "t", Health: "DEGRADED", Protected: true}}})
	stampAlerts(a2, since, utc("2026-10-02T11:00:00Z"))
	if a2[0].Since != "2026-10-02T10:00:00Z" {
		t.Errorf("since moved: %s", a2[0].Since)
	}
	stampAlerts(nil, since, utc("2026-10-02T12:00:00Z"))
	a3 := ComputeAlerts(AlertInput{Pools: []PoolInfo{{Name: "t", Health: "DEGRADED", Protected: true}}})
	stampAlerts(a3, since, utc("2026-10-02T13:00:00Z"))
	if a3[0].Since != "2026-10-02T13:00:00Z" {
		t.Errorf("since not reset: %s", a3[0].Since)
	}
}

func TestStatusNewFields(t *testing.T) {
	_, w, _, ts := newTestServer(t)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "ifconfig -a inet" {
			return "vtnet0: flags=8843<UP>\n\tinet 192.0.2.10 netmask 0xffffff00\nlo0: flags=8049<UP,LOOPBACK>\n\tinet 127.0.0.1 netmask 0xff000000\n", nil, true
		}
		return "", nil, false
	}
	c, m := do(t, ts, "GET", "/v1/status", "")
	// The fake pool is a single disk: one pool_no_protection alert.
	if c != 200 || fmt.Sprint(m["addresses"]) != "[192.0.2.10]" || m["alerts"] != float64(1) || m["restored_from"] != "" {
		t.Errorf("status: %v", m)
	}
	_, m = do(t, ts, "GET", "/v1/alerts", "")
	al := m["alerts"].([]any)
	if len(al) != 1 {
		t.Fatalf("alerts %v", m)
	}
	for _, k := range []string{"level", "code", "object", "text", "since"} {
		if _, ok := al[0].(map[string]any)[k]; !ok {
			t.Errorf("alert lacks %s", k)
		}
	}
}

// The test hook is on the Unix socket's handler only.
func TestDebugTickLocalOnly(t *testing.T) {
	s, _, _, ts := newTestServer(t)
	if c, _ := do(t, ts, "POST", "/v1/debug/tick", `{}`); c != 404 {
		t.Errorf("debug tick on the API: %d", c)
	}
	local := httptest.NewServer(s.LocalHandler())
	defer local.Close()
	c, m := do(t, local, "POST", "/v1/debug/tick", `{"time":"2026-10-02T03:00:00Z","only":["scrub"]}`)
	if c != 200 || m["time"] != "2026-10-02T03:00:00Z" {
		t.Errorf("debug tick: %d %v", c, m)
	}
	if c, _ := do(t, local, "POST", "/v1/debug/tick", `{"force":["coffee"]}`); c != 400 {
		t.Errorf("bad job: %d", c)
	}
	if c, _ := do(t, local, "GET", "/v1/settings", ""); c != 200 {
		t.Errorf("local handler lacks the API: %d", c)
	}
}

func TestBestCopy(t *testing.T) {
	if BestCopy(nil) != nil {
		t.Error("best of none")
	}
	f := []FoundCopy{
		{Pool: "tank", Copy: PoolCopy{Serial: 5, Time: "2026-10-02T10:00:00Z"}},
		{Pool: "tank2", Copy: PoolCopy{Serial: 7, Time: "2026-10-01T10:00:00Z"}},
		{Pool: "tank3", Copy: PoolCopy{Serial: 7, Time: "2026-10-01T09:00:00Z"}},
	}
	if b := BestCopy(f); b.Pool != "tank2" {
		t.Errorf("best %s", b.Pool)
	}
}

// restoreWorld: three pools to import; tank (serial 4) and tank2 (serial 9) have copies,
// other has none.
func restoreWorld(t *testing.T) (*world, *sys.Fake, map[string]bool) {
	w, f := newWorld(t)
	mounted := map[string]bool{}
	copyOf := func(serial int, pools ...Pool) []byte {
		c := PoolCopy{State: State{Version: 1, Key: fmt.Sprintf("key%d", serial), Listen: "10.0.0.5:8440", Pools: pools,
			Users:  []User{{"anna", 2001}},
			Shares: []Share{{Name: "a", Pool: "tank", RW: []string{"anna"}, RO: []string{}}},
			LUNs:   []LUN{{Name: "l1", Pool: "tank2", SizeGiB: 1}}, Snapshots: Retention{5, 6, 7}},
			Serial: int64(serial), Time: "2026-10-02T10:00:00Z"}
		b, _ := json.Marshal(c)
		return b
	}
	both := []Pool{{Name: "tank", GUID: "111"}, {Name: "tank2", GUID: "222"}}
	f.Files[probeRoot+"/tank/.keelnas/state.json"] = copyOf(4, both...)
	f.Files[probeRoot+"/tank/.keelnas/smbpasswd"] = []byte("anna:2001:X:OLDHASH:[U ]:LCT-1:\n")
	f.Files[probeRoot+"/tank2/.keelnas/state.json"] = copyOf(9, both...)
	f.Files[probeRoot+"/tank2/.keelnas/smbpasswd"] = []byte("anna:2001:X:NEWHASH:[U ]:LCT-2:\n")
	guids := map[string]string{"111": "tank", "222": "tank2", "333": "other"}
	w.extra = func(line string, a []string) (string, error, bool) {
		switch {
		case line == "zpool import":
			return "   pool: tank\n     id: 111\n  state: ONLINE\n action: The pool can be imported.\n config:\n\n\ttank   ONLINE\n\n   pool: tank2\n     id: 222\n  state: ONLINE\n\n   pool: other\n     id: 333\n  state: ONLINE\n", nil, true
		case strings.HasPrefix(line, "zpool import -o readonly=on"):
			return "cannot import: last accessed by another system", sys.ErrFail, true
		case strings.HasPrefix(line, "zpool import -f -o readonly=on"):
			mounted[guids[a[len(a)-1]]] = false
			return "", nil, true
		case strings.HasPrefix(line, "zfs mount "):
			mounted[a[2]] = true
			return "", nil, true
		case strings.HasPrefix(line, "zfs get -H -o value mountpoint"):
			p := a[len(a)-1]
			if m, ok := mounted[p]; ok && m {
				return probeRoot + "/" + p + "\n", nil, true
			}
			return "/" + p + "\n", nil, true
		case strings.HasPrefix(line, "zpool export"):
			delete(mounted, a[len(a)-1])
			return "", nil, true
		case strings.HasPrefix(line, "zpool import "):
			// The normal import by guid of apply.
			w.pools[a[2]] = a[3]
			return "", nil, true
		}
		return "", nil, false
	}
	return w, f, mounted
}

func TestRestore(t *testing.T) {
	w, f, mounted := restoreWorld(t)
	s, err := NewServer(f, StatePath, "test", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	pool, errs, err := s.Restore()
	if err != nil || pool != "tank2" {
		t.Fatalf("restore: %q %v %v", pool, errs, err)
	}
	st := s.State()
	if st.Key != "key9" || st.Listen != "10.0.0.5:8440" || len(st.Pools) != 2 || st.User("anna") == nil || st.Snapshots != (Retention{5, 6, 7}) {
		t.Errorf("restored state %+v", st)
	}
	b, _ := f.ReadFile(StatePath)
	if !strings.Contains(string(b), "key9") {
		t.Errorf("state.json not written: %s", b)
	}
	// Every probed pool is exported again; the chosen ones are imported normally by apply.
	if len(mounted) != 0 {
		t.Errorf("left imported read-only: %v", mounted)
	}
	if !f.Ran("zpool export other") || f.Ran("zpool import 333") {
		t.Error("the pool without a copy was not left alone")
	}
	if !f.Ran("zpool import 111 tank") || !f.Ran("zpool import 222 tank2") {
		t.Errorf("pools not imported: %v", f.Calls)
	}
	// Passwords: the newest copy's, imported after the user exists, before the copies are rewritten.
	pi, ui := -1, -1
	for i, c := range f.Calls {
		if c == "pdbedit -s "+SmbConf+" -i smbpasswd:"+restorePDB {
			pi = i
		}
		if strings.HasPrefix(c, "pw useradd -n anna") {
			ui = i
		}
	}
	if pi < 0 || ui < 0 || pi < ui {
		t.Errorf("pdbedit at %d, useradd at %d", pi, ui)
	}
	if w.users["anna"].UID != 2001 {
		t.Errorf("anna %+v", w.users["anna"])
	}
	if s.RestoredFrom() != "tank2" {
		t.Errorf("restored_from %q", s.RestoredFrom())
	}
	// The copies are written again with the next serial.
	var c PoolCopy
	json.Unmarshal(f.Files["/tank/.keelnas/state.json"], &c)
	if c.Serial != 10 || c.Key != "key9" {
		t.Errorf("new copy: serial %d key %s", c.Serial, c.Key)
	}
	// Not fresh any more: a second restore is refused.
	if _, _, err := s.Restore(); err != errNotFresh {
		t.Errorf("second restore: %v", err)
	}
}

func TestRestoreNotFreshOrNothing(t *testing.T) {
	// Not fresh: nothing is looked at.
	_, f, _ := restoreWorld(t)
	st := &State{Version: 1, Key: "mine", Users: []User{{"bob", 2001}}}
	f.WriteFile(StatePath, st.Marshal(), 0o600)
	s, _ := NewServer(f, StatePath, "test", log.New(io.Discard, "", 0))
	if _, _, err := s.Restore(); err != errNotFresh || f.Ran("zpool import") {
		t.Errorf("not fresh: %v %v", err, f.Calls)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	if c, _ := do(t, ts, "POST", "/v1/restore", ""); c != 409 {
		t.Errorf("POST /v1/restore not fresh: %d", c)
	}
	// Fresh, but no pool has a copy.
	w2, f2 := newWorld(t)
	w2.extra = func(line string, a []string) (string, error, bool) {
		if line == "zpool import" {
			return "no pools available to import\n", nil, true
		}
		return "", nil, false
	}
	s2, _ := NewServer(f2, StatePath, "test", log.New(io.Discard, "", 0))
	ts2 := httptest.NewServer(s2.Handler())
	defer ts2.Close()
	if c, _ := do(t, ts2, "POST", "/v1/restore", ""); c != 404 {
		t.Errorf("nothing to restore: %d", c)
	}
	if !s2.State().Fresh() {
		t.Error("state changed")
	}
}

func TestCopiesCarryPasswordsAndSerial(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "pdbedit -s "+SmbConf+" -L -w" {
			return "alice:2001:X:HASH:[U ]:LCT-1:\n", nil, true
		}
		return "", nil, false
	}
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	do(t, ts, "POST", "/v1/users", `{"name":"alice","password":"x"}`)
	var c1 PoolCopy
	json.Unmarshal(f.Files["/tank/.keelnas/state.json"], &c1)
	if string(f.Files["/tank/.keelnas/smbpasswd"]) != "alice:2001:X:HASH:[U ]:LCT-1:\n" || c1.Serial < 1 || c1.Time == "" {
		t.Fatalf("copy: %+v %q", c1, f.Files["/tank/.keelnas/smbpasswd"])
	}
	// A password change alone (no state change) refreshes the copies.
	do(t, ts, "PUT", "/v1/users/alice", `{"password":"y"}`)
	var c2 PoolCopy
	json.Unmarshal(f.Files["/tank/.keelnas/state.json"], &c2)
	if c2.Serial != c1.Serial+1 {
		t.Errorf("serial %d -> %d", c1.Serial, c2.Serial)
	}
	// A new server (restart) goes on from the serial in the pool.
	s2, _ := NewServer(f, StatePath, "test", log.New(io.Discard, "", 0))
	s2.Apply()
	var c3 PoolCopy
	json.Unmarshal(f.Files["/tank/.keelnas/state.json"], &c3)
	if c3.Serial != c2.Serial+1 {
		t.Errorf("after restart serial %d -> %d", c2.Serial, c3.Serial)
	}
	_ = s
}

// TestAutoSnapshotChoice: the storage page's per-dataset switch (PUT …/auto-snapshot): a share's
// dataset is on until switched off, any other dataset off until switched on; the default is
// not stored.
func TestAutoSnapshotChoice(t *testing.T) {
	s, _, sw, _, ts := newSnapServer(t)
	const A, L, P = "tank/shares/a", "tank/luns/l1", "tank/shares"
	path := func(ds string) string { return "/v1/storage/tank/datasets/" + url.PathEscape(ds) + "/auto-snapshot" }
	auto := func() map[string]any {
		_, m := do(t, ts, "GET", "/v1/storage/tank/datasets", "")
		got := map[string]any{}
		for _, x := range m["datasets"].([]any) {
			d := x.(map[string]any)
			got[d["name"].(string)] = d["auto_snapshot"]
		}
		return got
	}
	if a := auto(); a[A] != true || a[P] != false {
		t.Fatalf("defaults: %v", a)
	}
	if c, _ := do(t, ts, "PUT", path(A), `{}`); c != 400 {
		t.Errorf("no on: %d", c)
	}
	if c, _ := do(t, ts, "PUT", path("tank/nope"), `{"on":true}`); c != 404 {
		t.Errorf("unknown dataset: %d", c)
	}
	if c, m := do(t, ts, "PUT", path(A), `{"on":false}`); c != 200 || m["auto_snapshot"] != false {
		t.Fatalf("off: %d %v", c, m)
	}
	if c, m := do(t, ts, "PUT", path(P), `{"on":true}`); c != 200 {
		t.Fatalf("on: %d %v", c, m)
	}
	if a := auto(); a[A] != false || a[P] != true {
		t.Errorf("after switching: %v", a)
	}
	sw.written[A], sw.written[L], sw.written[P] = 1, 1, 1
	r := s.Tick(utc("2026-10-02T11:00:00Z"), nil, nil)
	sort.Strings(r.Created)
	if strings.Join(r.Created, " ") != "tank/luns/l1@keelnas-20261002-110000 tank/shares@keelnas-20261002-110000" {
		t.Errorf("taken: %v", r.Created)
	}
	// back to the default: nothing stored for it
	do(t, ts, "PUT", path(A), `{"on":true}`)
	if v, ok := s.State().AutoSnap[A]; ok {
		t.Errorf("default stored: %v", v)
	}
	if s.State().AutoSnap[P] != true {
		t.Errorf("lost the other: %v", s.State().AutoSnap)
	}
}

func TestHardware(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	ok := readTestdata(t, "smart-ata-ok.json")
	virtio := readTestdata(t, "smart-virtio.json")
	standby := `{"smartctl":{"exit_status":2,"messages":[{"string":"Device is in STANDBY mode, exit(2)"}]},"device":{"protocol":"ATA"}}`
	asleep := false
	w.extra = func(line string, a []string) (string, error, bool) {
		switch line {
		case "smartctl -j -n standby -i -H -A /dev/vtbd1":
			if asleep {
				return standby, sys.ErrFail, true
			}
			return ok, nil, true
		case "smartctl -j -n standby -i -H -A /dev/vtbd0":
			return virtio, sys.ErrFail, true
		case "smartctl -H -A -j /dev/vtbd1":
			return ok, nil, true
		case "smartctl -x /dev/vtbd1":
			return "smartctl 7.5 ...\nSMART overall-health self-assessment test result: PASSED\n", sys.ErrFail, true
		}
		return "", nil, false
	}
	now := utc("2026-10-14T01:00:00Z")
	s.Now = func() time.Time { return now }
	code, m := do(t, ts, "GET", "/v1/hardware", "")
	if code != 200 || m["read_at"] != "2026-10-14T01:00:00Z" {
		t.Fatalf("hardware: %d %v", code, m)
	}
	disks := m["disks"].([]any)
	if len(disks) != 2 {
		t.Fatalf("disks: %v", disks)
	}
	d0, d1 := disks[0].(map[string]any), disks[1].(map[string]any)
	if d0["name"] != "vtbd0" || d0["pool"] != nil || d0["smart"] != "unknown" || d0["health"] != nil {
		t.Errorf("vtbd0: %v", d0)
	}
	if d1["name"] != "vtbd1" || d1["pool"] != "tank" || d1["health"] != "passed" || d1["temperature"] != float64(37) || d1["hours"] != float64(12034) || d1["protocol"] != "ATA" {
		t.Errorf("vtbd1: %v", d1)
	}
	// Within 30 s the answer is kept; after that SMART is read again.
	f.Reset()
	do(t, ts, "GET", "/v1/hardware", "")
	if f.Ran("smartctl") {
		t.Error("smartctl again within 30 s")
	}
	now = now.Add(31 * time.Second)
	do(t, ts, "GET", "/v1/hardware", "")
	if !f.Ran("smartctl -j -n standby -i -H -A /dev/vtbd1") {
		t.Errorf("no SMART after 30 s: %v", f.Calls)
	}
	// A sleeping disk keeps the daily check's values and is marked standby.
	s.Tick(now, nil, nil)
	asleep = true
	now = now.Add(time.Minute)
	_, m = do(t, ts, "GET", "/v1/hardware", "")
	d1 = m["disks"].([]any)[1].(map[string]any)
	if d1["standby"] != true || d1["temperature"] != float64(37) || d1["health"] != "passed" {
		t.Errorf("standby: %v", d1)
	}
	// One disk in full, and no such disk.
	code, m = do(t, ts, "GET", "/v1/hardware/disks/vtbd1", "")
	if code != 200 || m["device"] != "/dev/vtbd1" || !strings.Contains(m["smart"].(string), "PASSED") {
		t.Errorf("detail: %d %v", code, m)
	}
	if code, _ := do(t, ts, "GET", "/v1/hardware/disks/vtbd9", ""); code != 404 {
		t.Errorf("no such disk: %d", code)
	}
	if code, _ := do(t, ts, "GET", "/v1/hardware/disks/..%2Fetc", ""); code != 404 {
		t.Errorf("odd name: %d", code)
	}
}

func TestDiskLess(t *testing.T) {
	in := []string{"da10", "da2", "vtbd0", "da1", "nda0", "da11"}
	sort.Slice(in, func(i, j int) bool { return diskLess(in[i], in[j]) })
	if got := strings.Join(in, " "); got != "da1 da2 da10 da11 nda0 vtbd0" {
		t.Error(got)
	}
}

func TestControllers(t *testing.T) {
	cam := `scbus0 on mpr0 bus 0:
<ATA TOSHIBA MD05ACA8 GX0B>        at scbus0 target 0 lun 0 (pass0,da0)
<ATA TOSHIBA MD05ACA8 GX0B>        at scbus0 target 11 lun 0 (da11,pass11)
<LSI SAS3x28 0705>                 at scbus0 target 12 lun 0 (ses0,pass12)
<>                                 at scbus0 target -1 lun ffffffff ()
scbus1 on ahcich2 bus 0:
<SATA SSD SBFM61.3>                at scbus1 target 0 lun 0 (pass13,ada0)
scbus2 on nvme0 bus 0:
<SAMSUNG MZ1LW960 CXV8>            at scbus2 target 0 lun 1 (pass14,nda0)
scbus-1 on xpt0 bus 0:
<>                                 at scbus-1 target -1 lun ffffffff (xpt0)
`
	got := ParseCamDevlist(cam)
	want := map[string]string{"da0": "mpr0", "da11": "mpr0", "ada0": "ahcich2", "nda0": "nvme0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	s, w, _, ts := newTestServer(t)
	w.extra = func(line string, a []string) (string, error, bool) {
		switch line {
		case "camcontrol devlist -v":
			return strings.NewReplacer("da0", "vtbd1", "ada0", "vtbd0").Replace(cam), nil, true
		case "sysctl -n dev.mpr.0.%desc":
			return "Avago Technologies (LSI) SAS3008\n", nil, true
		case "sysctl -n dev.mpr.0.%location":
			return "slot=0 function=0 dbsf=pci0:0:6:0\n", nil, true
		case "sysctl -n dev.ahcich.2.%parent":
			return "ahci0\n", nil, true
		case "sysctl -n dev.ahci.0.%desc":
			return "Intel ICH9 AHCI SATA controller\n", nil, true
		}
		return "", nil, false
	}
	c := s.controllers()
	if x := c["vtbd1"]; x != (Controller{"mpr0", "Avago Technologies (LSI) SAS3008", "pci0:0:6:0"}) {
		t.Errorf("mpr: %+v", x)
	}
	if x := c["vtbd0"]; x != (Controller{Name: "ahci0", Desc: "Intel ICH9 AHCI SATA controller"}) {
		t.Errorf("ahci: %+v", x)
	}
	_, m := do(t, ts, "GET", "/v1/hardware", "")
	d1 := m["disks"].([]any)[1].(map[string]any)
	if cc, _ := d1["controller"].(map[string]any); cc == nil || cc["name"] != "mpr0" || cc["pci"] != "pci0:0:6:0" {
		t.Errorf("hardware: %v", d1)
	}
}

func TestPage(t *testing.T) {
	_, _, _, ts := newTestServer(t)
	code, m := do(t, ts, "GET", "/v1/page", "")
	js, _ := m["js"].(string)
	if code != 200 || !strings.Contains(js, "export const pageAPI = 1;") || !strings.Contains(js, "export default function Page(") {
		t.Fatalf("page: %d, %d bytes", code, len(js))
	}
	// the page may import keel's modules by these relative paths only (the contract)
	for _, line := range strings.Split(js, "\n") {
		if strings.HasPrefix(line, "import ") {
			ok := false
			for _, from := range []string{`"preact/hooks"`, `"../../../app/api.js"`, `"../../../app/i18n.js"`, `"../../../app/time.js"`, `"../../../app/ui.js"`} {
				ok = ok || strings.HasSuffix(line, "from "+from+";")
			}
			if !ok {
				t.Errorf("import outside the contract: %s", line)
			}
		}
	}
}

package nas

// Tests of the ported pool check (scrub.go, scrubperf.go; docs/04 §17.1).

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestScanFromStats(t *testing.T) {
	now := utc("2026-10-23T10:00:00Z").Unix()
	// running: 1 GiB of 4 GiB (skipped 0) issued in the 100 s of this pass → 10 MiB/s, 300 s left
	sc := Scan{Function: "SCRUB", State: "SCANNING", Start: now - 100, PassStart: now - 100, ToExamine: 4 << 30, Examined: 2 << 30, Issued: 1 << 30, PassIssued: 1 << 30}
	v := ScanFromStats(sc, time.Unix(now, 0))
	if v.Kind != "scrub" || v.State != "running" || v.Pct != 25 || v.Rate != (1<<30)/100 || v.ETA != 300 || v.Seconds != 100 || v.Total != 4<<30 {
		t.Errorf("running: %+v", v)
	}
	// paused for the last 50 s: rate over the 50 s it ran, no ETA
	sc.Pause = now - 50
	v = ScanFromStats(sc, time.Unix(now, 0))
	if v.State != "paused" || v.Rate != (1<<30)/50 || v.ETA != -1 {
		t.Errorf("paused: %+v", v)
	}
	// skipped bytes are not to be issued; the real sample from testdata
	v = ScanFromStats(Scan{Function: "RESILVER", State: "FINISHED", Start: 1790934833, End: 1790934838, ToExamine: 5375819776, Skipped: 933888, Issued: 5374885888, Processed: 4096}, time.Unix(now, 0))
	if v.Kind != "resilver" || v.State != "finished" || v.Pct != 100 || v.Seconds != 5 || v.Repaired != 4096 || v.End != "2026-10-02T09:53:58Z" || v.ETA != -1 {
		t.Errorf("finished: %+v", v)
	}
	if v := ScanFromStats(Scan{}, time.Unix(now, 0)); v.State != "none" || v.Kind != "" {
		t.Errorf("none: %+v", v)
	}
	if v := ScanFromStats(Scan{Function: "SCRUB", State: "CANCELED", Start: 10, End: 70}, time.Unix(now, 0)); v.State != "canceled" || v.Seconds != 60 {
		t.Errorf("canceled: %+v", v)
	}
}

func TestNextScrub(t *testing.T) {
	now := utc("2026-10-23T10:00:00Z") // a Friday
	for _, tc := range []struct {
		p    ScrubPlan
		want string
	}{
		{ScrubPlan{Every: "week", Weekday: 0, Hour: 3}, "2026-10-25T03:00:00Z"},
		{ScrubPlan{Every: "week", Weekday: 5, Hour: 11}, "2026-10-23T11:00:00Z"},
		{ScrubPlan{Every: "week", Weekday: 5, Hour: 9}, "2026-10-30T09:00:00Z"},
		{ScrubPlan{Every: "month", Weekday: 0, Hour: 3}, "2026-11-01T03:00:00Z"},
		{ScrubPlan{Every: "auto", Hour: 3}, "2026-10-24T03:00:00Z"},
		{ScrubPlan{Every: "off"}, ""},
	} {
		got := NextScrub(tc.p, now)
		s := ""
		if !got.IsZero() {
			s = got.Format(time.RFC3339)
		}
		if s != tc.want {
			t.Errorf("%+v: %s, want %s", tc.p, s, tc.want)
		}
	}
	once := utc("2026-10-24T01:30:00Z")
	if got := NextScrub(ScrubPlan{Every: "week", Weekday: 0, Hour: 3, Once: &once}, now); !got.Equal(once) {
		t.Errorf("once: %v", got)
	}
}

func TestScrubAPI(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	s.Now = func() time.Time { return utc("2026-10-23T10:00:00Z") }
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	scrubbing := false
	w.extra = func(line string, a []string) (string, error, bool) {
		switch line {
		case "zfs get -H -p -o value creation tank":
			return fmt.Sprint(utc("2026-10-20T00:00:00Z").Unix()), nil, true
		case "zpool scrub tank":
			if scrubbing {
				return "cannot scrub tank: currently scrubbing; use 'zpool scrub -s' to cancel the current scrub", fmt.Errorf("exit status 1"), true
			}
			scrubbing = true
			return "", nil, true
		case "zpool scrub -s tank":
			if !scrubbing {
				return "cannot cancel scrubbing tank: there is no active scrub", fmt.Errorf("exit status 1"), true
			}
			scrubbing = false
			return "", nil, true
		}
		return "", nil, false
	}
	c, m := do(t, ts, "GET", "/v1/storage/tank/scrub", "")
	sch := m["schedule"].(map[string]any)
	// auto: the first 03:00 more than 30 days after the creation (2026-10-20 00:00)
	if c != 200 || sch["every"] != "auto" || sch["hour"] != float64(3) || m["running"] != false || m["next"] != "2026-11-19T03:00:00Z" {
		t.Fatalf("get: %d %v", c, m)
	}
	if c, _ := do(t, ts, "GET", "/v1/storage/nope/scrub", ""); c != 404 {
		t.Errorf("no pool: %d", c)
	}
	if c, m := do(t, ts, "POST", "/v1/storage/tank/scrub/start", ""); c != 200 || !f.Ran("zpool scrub tank") {
		t.Errorf("start: %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/scrub/start", ""); c != 409 {
		t.Errorf("second start: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/scrub/stop", ""); c != 200 {
		t.Errorf("stop: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/scrub/stop", ""); c != 409 {
		t.Errorf("second stop: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/scrub/pause", ""); c != 404 {
		t.Errorf("unknown action: %d", c)
	}
	for _, b := range []string{`{"every":"daily","weekday":0,"hour":3}`, `{"every":"week","weekday":7,"hour":3}`, `{"every":"week","weekday":0,"hour":24}`,
		`{"every":"week","weekday":0,"hour":3,"once":"2026-10-01T00:00:00Z"}`, `{"every":"week","x":1}`} {
		if c, _ := do(t, ts, "PUT", "/v1/storage/tank/scrub", b); c != 400 {
			t.Errorf("%s: %d", b, c)
		}
	}
	c, m = do(t, ts, "PUT", "/v1/storage/tank/scrub", `{"every":"week","weekday":0,"hour":4,"once":"2026-10-24T12:00:00+09:00"}`)
	if c != 200 || m["next"] != "2026-10-24T03:00:00Z" || m["schedule"].(map[string]any)["once"] != "2026-10-24T03:00:00Z" {
		t.Fatalf("put: %d %v", c, m)
	}
	if b, _ := f.ReadFile(StatePath); !strings.Contains(string(b), `"every": "week"`) {
		t.Errorf("not saved: %s", b)
	}
	// the tick: the once at 03:00 starts a check and is removed; the weekly one on Sunday 04:00
	scrubbing = false
	f.Reset()
	r := s.Tick(utc("2026-10-24T03:00:00Z"), nil, []string{JobScrub})
	if len(r.Scrubs) != 1 || s.State().Pool("tank").Scrub.Once != nil || s.State().Pool("tank").Scrub.Every != "week" {
		t.Errorf("once: %+v %+v", r, s.State().Pool("tank").Scrub)
	}
	scrubbing = false
	f.Reset()
	if r := s.Tick(utc("2026-10-25T03:59:00Z"), nil, []string{JobScrub}); len(r.Scrubs) != 0 || f.Ran("zpool") {
		t.Errorf("03:59 on Sunday: %+v %v", r, f.Calls)
	}
	if r := s.Tick(utc("2026-10-25T04:00:00Z"), nil, []string{JobScrub}); len(r.Scrubs) != 1 {
		t.Errorf("weekly: %+v", r)
	}
	// off: nothing, not even forced
	do(t, ts, "PUT", "/v1/storage/tank/scrub", `{"every":"off","weekday":0,"hour":3}`)
	scrubbing = false
	f.Reset()
	if r := s.Tick(utc("2026-12-25T03:00:00Z"), []string{JobScrub}, []string{JobScrub}); len(r.Scrubs) != 0 {
		t.Errorf("off: %+v", r)
	}
	// a running scrub shows in GET /v1/storage with its progress
	now := utc("2026-10-23T10:00:00Z").Unix()
	w.mu.Lock()
	w.status = strings.Replace(w.status, `"pool_guid":777,`, fmt.Sprintf(`"pool_guid":777,"scan_stats":{"function":"SCRUB","state":"SCANNING","start_time":%d,"pass_start":%d,"to_examine":400,"examined":300,"issued":100,"issued_bytes_per_scan":100,"skipped":0,"errors":0,"scrub_pause":0,"scrub_spent_paused":0,"processed":0,"end_time":0},`, now-10, now-10), 1)
	w.mu.Unlock()
	c, m = do(t, ts, "GET", "/v1/storage", "")
	sc := m["storage"].([]any)[0].(map[string]any)["scan"].(map[string]any)
	if c != 200 || sc["state"] != "running" || sc["pct"] != float64(25) || sc["eta_s"] != float64(30) || sc["rate"] != float64(10) {
		t.Errorf("storage scan: %d %v", c, sc)
	}
}

func TestScrubWatch(t *testing.T) {
	s, w, _, ts := newTestServer(t)
	clock := utc("2026-10-23T10:00:00Z")
	s.Now = func() time.Time { return clock }
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	base := w.status
	set := func(state string, issued, end int64) {
		w.mu.Lock()
		w.status = strings.Replace(base, `"pool_guid":777,`, fmt.Sprintf(`"pool_guid":777,"scan_stats":{"function":"SCRUB","state":"%s","start_time":%d,"pass_start":%d,"end_time":%d,"to_examine":1000000,"examined":1000000,"issued":%d,"issued_bytes_per_scan":%d,"skipped":0,"errors":0,"processed":0,"scrub_pause":0,"scrub_spent_paused":0},`,
			state, utc("2026-10-23T10:00:00Z").Unix(), utc("2026-10-23T10:00:00Z").Unix(), end, issued, issued), 1)
		w.mu.Unlock()
	}
	w.extra = func(line string, a []string) (string, error, bool) {
		if strings.HasPrefix(line, "zpool iostat -vpHPy tank 5 1") {
			return "tank\t1\t2\t30\t0\t2000\t0\n/dev/vtbd1\t1\t2\t30\t0\t2000\t0\n", nil, true
		}
		return "", nil, false
	}
	set("SCANNING", 100000, 0)
	clock = clock.Add(10 * time.Second)
	if !s.scrubs.Step() {
		t.Fatal("no sample")
	}
	set("SCANNING", 300000, 0)
	clock = clock.Add(10 * time.Second)
	s.scrubs.Step()
	c, m := do(t, ts, "GET", "/v1/storage/tank/scrub/perf", "")
	live, _ := m["live"].(map[string]any)
	if c != 200 || live == nil || len(live["samples"].([]any)) != 2 || live["peak"] != float64(20000) {
		t.Fatalf("live: %d %v", c, m)
	}
	devs := live["devs"].([]any)
	if len(devs) != 1 || devs[0].(map[string]any)["name"] != "vtbd1" || devs[0].(map[string]any)["avg"] != float64(2000) {
		t.Errorf("devs %v", devs)
	}
	set("FINISHED", 1000000, utc("2026-10-23T10:01:40Z").Unix())
	if s.scrubs.Step() {
		t.Error("finished but sampled")
	}
	s.scrubs.Step() // a second look records nothing more
	c, m = do(t, ts, "GET", "/v1/storage/tank/scrub/perf", "")
	runs := m["runs"].([]any)
	if c != 200 || m["live"] != nil || len(runs) != 1 {
		t.Fatalf("runs: %d %v", c, m)
	}
	r := runs[0].(map[string]any)
	if r["seconds"] != float64(100) || r["bytes"] != float64(1000000) || r["avg"] != float64(10000) || r["peak"] != float64(20000) {
		t.Errorf("run %v", r)
	}
	if b, err := s.Sys.ReadFile("/var/db/keelnas/scrubs.json"); err != nil || !strings.Contains(string(b), `"kind": "scrub"`) {
		t.Errorf("scrubs.json: %s %v", b, err)
	}
	if c, _ := do(t, ts, "GET", "/v1/storage/nope/scrub/perf", ""); c != 404 {
		t.Errorf("no pool: %d", c)
	}
}

// A scan that a newer one replaced before its end was seen is recorded as superseded.
func TestScrubSuperseded(t *testing.T) {
	s, w, _, ts := newTestServer(t)
	clock := utc("2026-10-23T10:00:00Z")
	s.Now = func() time.Time { return clock }
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	base := w.status
	set := func(fn string, start, issued int64) {
		w.mu.Lock()
		w.status = strings.Replace(base, `"pool_guid":777,`, fmt.Sprintf(`"pool_guid":777,"scan_stats":{"function":"%s","state":"SCANNING","start_time":%d,"pass_start":%d,"end_time":0,"to_examine":1000,"examined":1000,"issued":%d,"issued_bytes_per_scan":%d,"skipped":0,"errors":0,"processed":0,"scrub_pause":0,"scrub_spent_paused":0},`, fn, start, start, issued, issued), 1)
		w.mu.Unlock()
	}
	t0 := clock.Unix()
	set("SCRUB", t0, 500)
	clock = clock.Add(10 * time.Second)
	s.scrubs.Step()
	set("RESILVER", t0+20, 10) // the scrub ended unseen, a resilver runs
	clock = clock.Add(20 * time.Second)
	s.scrubs.Step()
	_, m := do(t, ts, "GET", "/v1/storage/tank/scrub/perf", "")
	runs := m["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["superseded"] != true || runs[0].(map[string]any)["kind"] != "scrub" || runs[0].(map[string]any)["bytes"] != float64(500) {
		t.Fatalf("runs %v", m)
	}
	if l := m["live"].(map[string]any); l["scan"].(map[string]any)["kind"] != "resilver" {
		t.Errorf("live %v", l)
	}
}

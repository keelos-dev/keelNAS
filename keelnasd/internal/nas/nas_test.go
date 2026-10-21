package nas

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"keelnas/internal/sys"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestValidName(t *testing.T) {
	for n, want := range map[string]bool{
		"alice": true, "a": true, "docs-2_x": true, strings.Repeat("a", 32): true,
		"": false, "Alice": false, "2a": false, "-a": false, "a b": false, "a.b": false,
		strings.Repeat("a", 33): false, "a/b": false,
	} {
		if ValidName(n) != want {
			t.Errorf("ValidName(%q) = %v", n, !want)
		}
	}
}

func TestLoadStateNewKey(t *testing.T) {
	f := sys.NewFake()
	st, created, err := LoadState(f, StatePath)
	if err != nil || !created || len(st.Key) != 64 || st.Version != 1 {
		t.Fatalf("new state: %+v %v %v", st, created, err)
	}
	if b, _ := json.Marshal(st); !strings.Contains(string(b), `"users":[]`) {
		t.Errorf("empty lists must be [] in JSON: %s", b)
	}
	if err := SaveState(f, StatePath, st); err != nil {
		t.Fatal(err)
	}
	st2, created, err := LoadState(f, StatePath)
	if err != nil || created || st2.Key != st.Key {
		t.Fatalf("reload: %v %v", created, err)
	}
}

func TestCloneIsDeep(t *testing.T) {
	st := &State{Shares: []Share{{Name: "a", RW: []string{"x"}}}}
	c := st.Clone()
	c.Shares[0].RW[0] = "y"
	if st.Shares[0].RW[0] != "x" {
		t.Fatal("clone shares the RW slice")
	}
}

func TestNextUIDAndPool(t *testing.T) {
	st := &State{Users: []User{{"a", 2001}, {"b", 2005}}}
	if u := st.NextUID(map[int]bool{2006: true}); u != 2007 {
		t.Errorf("NextUID = %d", u)
	}
	if u := (&State{}).NextUID(nil); u != FirstUID {
		t.Errorf("first uid = %d", u)
	}
	st.Pools = []Pool{{Name: "tank"}}
	if n := st.NextPoolName(map[string]bool{"tank2": true}); n != "tank3" {
		t.Errorf("NextPoolName = %s", n)
	}
}

func TestSmbConfig(t *testing.T) {
	st := &State{Shares: []Share{
		{Name: "zeta", Dataset: "tank/shares/zeta", RW: []string{"bob"}, RO: []string{}},
		{Name: "docs", Dataset: "tank/shares/docs", RW: []string{"alice"}, RO: []string{"bob"}},
		{Name: "home", Dataset: "tank/home", RW: []string{"alice"}, RO: []string{}},
		{Name: "gone", Dataset: "tank/gone", RW: []string{"alice"}, RO: []string{}},
	}}
	got := SmbConfig(st, map[string]string{"zeta": "/shares/zeta", "docs": "/shares/docs", "home": "/pools/tank/usr/home"})
	if !strings.HasPrefix(got, "# Written by keelnasd") {
		t.Error("no header")
	}
	for _, want := range []string{
		"map to guest = Never",
		"passdb backend = tdbsam:/var/db/keelnas/samba/passdb.tdb",
		"private dir = /var/db/keelnas/samba",
		"[docs]\n  path = /shares/docs\n  valid users = alice bob\n  write list = alice\n  read only = yes\n  force group = keelnas\n  create mask = 0660\n  directory mask = 2770\n",
		// a dataset keelNAS did not make (its owner changed to a user): the same as ours
		"[home]\n  path = /pools/tank/usr/home\n  valid users = alice\n  write list = alice\n  read only = yes\n  force group = keelnas\n  create mask = 0660\n  directory mask = 2770\n",
		// not mounted: off
		"[gone]\n  # dataset tank/gone is not mounted\n  path = /nonexistent\n  available = no\n[home]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("smb4.conf lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "force user") {
		t.Error("force user is gone (docs/06 §2)")
	}
	if strings.Index(got, "[docs]") > strings.Index(got, "[zeta]") {
		t.Error("shares not sorted")
	}
}

func TestCtlConfig(t *testing.T) {
	st := &State{LUNs: []LUN{{Name: "lun0", Pool: "tank", SizeGiB: 2}}}
	got := CtlConfig(st, "keelnas")
	for _, want := range []string{
		"portal-group pg0 {",
		"target iqn.2026-10.dev.keelos:keelnas:lun0 {",
		"path /dev/zvol/tank/luns/lun0",
		"auth-group no-authentication",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ctl.conf lacks %q:\n%s", want, got)
		}
	}
	s := lunSerial(st.LUNs[0])
	if len(s) > 16 || s != lunSerial(st.LUNs[0]) {
		t.Errorf("serial %q", s)
	}
}

func TestParsers(t *testing.T) {
	disks := ParseGeomDisks(readTestdata(t, "geom-disk-list.txt"))
	if len(disks) != 3 || disks[2].Name != "cd0" || RealDisk(disks[2]) || !RealDisk(disks[1]) {
		t.Fatalf("cd0: %+v", disks)
	}
	disks = disks[:2]
	if len(disks) != 2 || disks[1].Name != "vtbd1" || disks[1].Size != 8589934592 || disks[1].Serial == "" {
		t.Fatalf("disks %+v", disks)
	}
	pools, err := ParsePools(readTestdata(t, "zpool-status.json"), readTestdata(t, "zpool-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 2 || pools[1].Name != "tank" || pools[1].GUID != "13474932555875689274" ||
		pools[1].Size != 8053063680 || pools[1].Protected || len(pools[1].Members) != 1 || pools[1].Members[0].Path != "/dev/vtbd1" {
		t.Fatalf("pools %+v", pools)
	}
	labels := ParseGlabel(readTestdata(t, "glabel.txt"))
	names := []string{"vtbd0", "vtbd1", "vtbd10"}
	for path, want := range map[string]string{"/dev/gpt/knsys": "vtbd0", "/dev/vtbd1": "vtbd1", "vtbd10p2": "vtbd10", "/dev/vtbd1p1": "vtbd1", "/dev/nothing": ""} {
		if got := DiskOf(path, labels, names); got != want {
			t.Errorf("DiskOf(%s) = %q, want %q", path, got, want)
		}
	}
	u := ParsePwUsers("root:*:0:0::0:0:Charlie &:/root:/bin/sh\nalice:*:2001:2000::0:0:keelNAS user:/nonexistent:/usr/sbin/nologin\n")
	if u["alice"].UID != 2001 || u["alice"].GID != 2000 || u["root"].UID != 0 {
		t.Errorf("pw %+v", u)
	}
	if bt := ParseBoottime("{ sec = 1790934600, usec = 312793 } Fri Oct  2 09:50:00 2026"); bt != 1790934600 {
		t.Errorf("boottime %d", bt)
	}
	mirror := `{"pools":{"m":{"name":"m","state":"DEGRADED","pool_guid":1,"vdevs":{"m":{"name":"m","vdev_type":"root","vdevs":{"mirror-0":{"name":"mirror-0","vdev_type":"mirror","vdevs":{"vtbd1":{"name":"vtbd1","vdev_type":"disk","path":"/dev/vtbd1","state":"ONLINE"},"vtbd2":{"name":"vtbd2","vdev_type":"disk","path":"/dev/vtbd2","state":"FAULTED"}}}}}}}}}`
	mp, err := ParsePools(mirror, "")
	if err != nil || !mp[0].Protected || len(mp[0].Members) != 2 || mp[0].Members[1].State != "FAULTED" {
		t.Errorf("mirror %+v %v", mp, err)
	}
}

const noLabel = "failed to unpack label 0\nfailed to unpack label 1\nfailed to unpack label 2\nfailed to unpack label 3\n"

// world is a small model of the system behind the fake: users, datasets, pools, daemons.
type world struct {
	mu       sync.Mutex
	users    map[string]PwUser
	datasets map[string]string // name -> type
	mps      map[string]string // name -> mountpoint as zfs lists it (with the altroot)
	altroot  map[string]string // pool -> altroot
	owners   map[string]string // path -> "uid:gid" (stat -f %u:%g); missing = 0:0
	pools    map[string]string // guid -> name
	status   string            // zpool status -j for the pools
	smbd     bool
	ctld     bool
	failSmbp bool
	// what is on the disks (foreign.go): `zpool import`, `gpart show -p`, zdb -l and fstyp per
	// device; a device that is not in zdb/fstyp has no label / no file system
	importOut string
	gpart     string
	zdb       map[string]string
	fstyp     map[string]string
	// extra answers first (tests of the second step add commands here); called with mu held.
	extra func(line string, a []string) (string, error, bool)
}

func newWorld(t *testing.T) (*world, *sys.Fake) {
	w := &world{
		users:    map[string]PwUser{"root": {"root", 0, 0}, "keel": {"keel", 1001, 1001}},
		datasets: map[string]string{},
		mps:      map[string]string{},
		altroot:  map[string]string{},
		owners:   map[string]string{},
		pools:    map[string]string{},
		status:   `{"pools":{}}`,
		zdb:      map[string]string{},
		fstyp:    map[string]string{},
	}
	f := sys.NewFake()
	f.Handle = func(line, stdin string) (string, error, bool) {
		w.mu.Lock()
		defer w.mu.Unlock()
		a := strings.Fields(line)
		if w.extra != nil {
			if out, err, ok := w.extra(line, a); ok {
				return out, err, true
			}
		}
		switch {
		case line == "zpool import":
			return w.importOut, nil, true
		case line == "gpart show -p":
			return w.gpart, nil, true
		case strings.HasPrefix(line, "zdb -l /dev/"):
			if out, ok := w.zdb[strings.TrimPrefix(a[2], "/dev/")]; ok {
				return out, nil, true
			}
			return noLabel, sys.ErrFail, true
		case strings.HasPrefix(line, "fstyp /dev/"):
			if out, ok := w.fstyp[strings.TrimPrefix(a[1], "/dev/")]; ok {
				return out + "\n", nil, true
			}
			return "fstyp: " + a[1] + ": filesystem not recognized\n", sys.ErrFail, true
		case line == "pw usershow -a":
			var b strings.Builder
			for _, u := range w.users {
				fmt.Fprintf(&b, "%s:*:%d:%d::0:0::/nonexistent:/usr/sbin/nologin\n", u.Name, u.UID, u.GID)
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "pw groupshow"):
			return "keelnas:*:2000:", nil, true
		case strings.HasPrefix(line, "pw useradd"):
			uid, _ := strconv.Atoi(a[5])
			w.users[a[3]] = PwUser{a[3], uid, GroupID}
			return "", nil, true
		case strings.HasPrefix(line, "pw userdel"):
			delete(w.users, a[3])
			return "", nil, true
		case strings.HasPrefix(line, "smbpasswd"):
			if w.failSmbp {
				return "Failed to add entry", sys.ErrFail, true
			}
			return "Added user", nil, true
		case strings.HasPrefix(line, "zfs list -H -p -r -t filesystem -o name,used,avail,mountpoint,mounted"):
			pool := a[len(a)-1]
			var b strings.Builder
			for n, typ := range w.datasets {
				if typ != "filesystem" || (n != pool && !strings.HasPrefix(n, pool+"/")) {
					continue
				}
				mp := w.mp(n)
				mounted := "no"
				if strings.HasPrefix(mp, "/") {
					mounted = "yes"
				}
				fmt.Fprintf(&b, "%s\t2048\t4096\t%s\t%s\n", n, mp, mounted)
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "zfs list -H -p -t snapshot"):
			return "", nil, true
		case strings.HasPrefix(line, "zfs list"):
			var b strings.Builder
			for n, typ := range w.datasets {
				fmt.Fprintf(&b, "%s\t%s\t1024\t%s\n", n, typ, w.mp(n))
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "zfs create"):
			typ := "filesystem"
			if strings.Contains(line, " -V ") {
				typ = "volume"
			}
			n := a[len(a)-1]
			w.datasets[n] = typ
			for _, x := range a {
				if mp, ok := strings.CutPrefix(x, "mountpoint="); ok {
					if mp != "none" {
						mp = w.altroot[strings.Split(n, "/")[0]] + mp
					}
					w.mps[n] = mp
				}
			}
			return "", nil, true
		case strings.HasPrefix(line, "zfs destroy"):
			n := a[len(a)-1]
			for x := range w.datasets {
				if x == n || strings.HasPrefix(x, n+"/") {
					delete(w.datasets, x)
				}
			}
			return "", nil, true
		case strings.HasPrefix(line, "zfs get -H -o value mountpoint"):
			return w.mp(a[len(a)-1]) + "\n", nil, true
		case strings.HasPrefix(line, "stat -f %u:%g %N "):
			var b strings.Builder
			for _, p := range a[4:] {
				o, ok := w.owners[p]
				if !ok {
					o = "0:0"
				}
				fmt.Fprintf(&b, "%s %s\n", o, p)
			}
			return b.String(), nil, true
		case line == "zpool list -H -p -o guid,name":
			var b strings.Builder
			for g, n := range w.pools {
				fmt.Fprintf(&b, "%s\t%s\n", g, n)
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "zpool create"):
			name := a[len(a)-2]
			if a[len(a)-3] == "mirror" || name == "mirror" {
				name = a[len(a)-4]
			}
			for i, x := range a {
				if x == "xattr=sa" {
					name = a[i+1]
				}
			}
			w.pools["777"] = name
			w.status = `{"pools":{"` + name + `":{"name":"` + name + `","state":"ONLINE","pool_guid":777,"vdevs":{"` + name + `":{"name":"` + name + `","vdev_type":"root","vdevs":{"vtbd1":{"name":"vtbd1","vdev_type":"disk","path":"/dev/vtbd1","state":"ONLINE"}}}}}}}`
			return "", nil, true
		case strings.HasPrefix(line, "zpool get -H -p -o value guid"):
			return "777\n", nil, true
		case strings.HasPrefix(line, "zpool status -j"):
			return w.status, nil, true
		case strings.HasPrefix(line, "zpool list -j"):
			return `{"pools":{}}`, nil, true
		case line == "geom disk list":
			return readTestdata(t, "geom-disk-list.txt"), nil, true
		case line == "pgrep -x smbd":
			if w.smbd {
				return "1", nil, true
			}
			return "", sys.ErrFail, true
		case line == "pgrep -x ctld":
			if w.ctld {
				return "1", nil, true
			}
			return "", sys.ErrFail, true
		case strings.HasPrefix(line, "/usr/local/sbin/smbd"):
			w.smbd = true
			return "", nil, true
		case line == "service ctld onestart":
			w.ctld = true
			return "", nil, true
		case line == "sysctl -n kern.hostname":
			return "keelnas\n", nil, true
		}
		return "", nil, false
	}
	return w, f
}

// mp is a dataset's mountpoint: its own, or inherited from its parent; a pool's root is
// mounted at /<pool> below its altroot. Called with mu held.
func (w *world) mp(n string) string {
	if mp, ok := w.mps[n]; ok {
		return mp
	}
	if w.datasets[n] == "volume" {
		return "-"
	}
	i := strings.LastIndexByte(n, '/')
	if i < 0 {
		return w.altroot[n] + "/" + n
	}
	if p := w.mp(n[:i]); strings.HasPrefix(p, "/") {
		return p + n[i:]
	}
	return "none"
}

func newTestServer(t *testing.T) (*Server, *world, *sys.Fake, *httptest.Server) {
	w, f := newWorld(t)
	s, err := NewServer(f, StatePath, "test", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, w, f, ts
}

func do(t *testing.T, ts *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s %s: not JSON: %s", method, path, b)
	}
	return resp.StatusCode, m
}

func TestAPIFlow(t *testing.T) {
	s, w, f, ts := newTestServer(t)

	// Storage: one disk, or two with protect; the system disk is taken.
	if c, _ := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1","vtbd0"]}`); c != 400 {
		t.Errorf("two disks without protect: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd9"]}`); c != 404 {
		t.Errorf("unknown disk: %d", c)
	}
	c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	if c != 200 || m["name"] != "tank" {
		t.Fatalf("create storage: %d %v", c, m)
	}
	if !f.Ran("zpool create -o ashift=12 -o autotrim=on -O compression=lz4 -O atime=off -O xattr=sa tank vtbd1") {
		t.Errorf("zpool create: %v", f.Calls)
	}
	if st := s.State(); len(st.Pools) != 1 || st.Pools[0].GUID != "777" {
		t.Errorf("pools in state: %+v", st.Pools)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`); c != 409 {
		t.Errorf("disk in use: %d", c)
	}

	// Users.
	if c, _ := do(t, ts, "POST", "/v1/users", `{"name":"Alice","password":"x"}`); c != 400 {
		t.Errorf("bad name: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/users", `{"name":"keel","password":"x"}`); c != 409 {
		t.Errorf("system account: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/users", `{"name":"alice"}`); c != 400 {
		t.Errorf("no password: %d", c)
	}
	f.Reset()
	c, m = do(t, ts, "POST", "/v1/users", `{"name":"alice","password":"s3cret"}`)
	if c != 200 || m["uid"] != float64(2001) {
		t.Fatalf("add alice: %d %v", c, m)
	}
	for i, call := range f.Calls {
		if strings.HasPrefix(call, "smbpasswd") && f.Stdin[i] != "s3cret\ns3cret\n" {
			t.Errorf("smbpasswd stdin %q", f.Stdin[i])
		}
		if strings.Contains(call, "s3cret") {
			t.Errorf("password on a command line: %s", call)
		}
	}
	do(t, ts, "POST", "/v1/users", `{"name":"bob","password":"b"}`)
	if c, _ := do(t, ts, "POST", "/v1/users", `{"name":"alice","password":"x"}`); c != 409 {
		t.Errorf("duplicate user: %d", c)
	}
	w.failSmbp = true
	if c, _ := do(t, ts, "POST", "/v1/users", `{"name":"carol","password":"x"}`); c != 500 {
		t.Errorf("smbpasswd failure: %d", c)
	}
	w.failSmbp = false
	if _, ok := w.users["carol"]; ok || s.State().User("carol") != nil {
		t.Error("a failed add left carol behind")
	}

	// Shares.
	if c, _ := do(t, ts, "POST", "/v1/shares", `{"name":"docs","pool":"tank","rw":[],"ro":[]}`); c != 400 {
		t.Errorf("guest share: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/shares", `{"name":"docs","pool":"tank","rw":["nobody"]}`); c != 400 {
		t.Errorf("unknown user: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/shares", `{"name":"global","pool":"tank","rw":["alice"]}`); c != 400 {
		t.Errorf("reserved name: %d", c)
	}
	c, m = do(t, ts, "POST", "/v1/shares", `{"name":"docs","pool":"tank","rw":["alice"],"ro":["bob"]}`)
	if c != 200 || m["path"] != "/shares/docs" || m["used"] == nil {
		t.Fatalf("add share: %d %v", c, m)
	}
	if w.datasets["tank/shares/docs"] != "filesystem" || w.datasets["tank/shares"] == "" {
		t.Errorf("datasets %v", w.datasets)
	}
	conf, _ := f.ReadFile(SmbConf)
	if !strings.Contains(string(conf), "[docs]") || !strings.Contains(string(conf), "valid users = alice bob") {
		t.Errorf("smb4.conf:\n%s", conf)
	}
	if !w.smbd || !w.ctld {
		t.Error("services not started")
	}
	if c, _ := do(t, ts, "POST", "/v1/shares", `{"name":"docs","pool":"tank","rw":["alice"]}`); c != 409 {
		t.Errorf("duplicate share: %d", c)
	}

	// LUN.
	c, m = do(t, ts, "POST", "/v1/luns", `{"name":"lun0","pool":"tank","size_gib":2}`)
	if c != 200 || m["iqn"] != "iqn.2026-10.dev.keelos:keelnas:lun0" {
		t.Fatalf("add lun: %d %v", c, m)
	}
	if !f.Ran("zfs create -V 2G -o volmode=dev tank/luns/lun0") {
		t.Error("no zvol")
	}
	ctl, _ := f.ReadFile(CtlConf)
	if !strings.Contains(string(ctl), "/dev/zvol/tank/luns/lun0") {
		t.Errorf("ctl.conf:\n%s", ctl)
	}

	// Lists have the documented shapes.
	_, m = do(t, ts, "GET", "/v1/shares", "")
	if sh := m["shares"].([]any); len(sh) != 1 {
		t.Errorf("shares %v", m)
	}
	_, m = do(t, ts, "GET", "/v1/users", "")
	if len(m["users"].([]any)) != 2 {
		t.Errorf("users %v", m)
	}
	_, m = do(t, ts, "GET", "/v1/luns", "")
	if len(m["luns"].([]any)) != 1 {
		t.Errorf("luns %v", m)
	}
	c, m = do(t, ts, "GET", "/v1/disks", "")
	ds := m["disks"].([]any)
	if c != 200 || len(ds) != 2 || ds[1].(map[string]any)["pool"] != "tank" || ds[0].(map[string]any)["pool"] != "" {
		t.Errorf("disks %v", m)
	}
	c, m = do(t, ts, "GET", "/v1/status", "")
	if c != 200 || m["hostname"] != "keelnas" || m["services"].(map[string]any)["smbd"] != true {
		t.Errorf("status %v", m)
	}
	for _, k := range []string{"version", "uptime_s", "memory", "pools", "last_apply"} {
		if _, ok := m[k]; !ok {
			t.Errorf("status lacks %s", k)
		}
	}
	c, m = do(t, ts, "GET", "/v1/storage", "")
	st0 := m["storage"].([]any)[0].(map[string]any)
	if c != 200 || st0["name"] != "tank" || len(st0["disks"].([]any)) != 1 {
		t.Errorf("storage %v", m)
	}

	// Deleting a user takes it out of every share and off the system.
	c, _ = do(t, ts, "DELETE", "/v1/users/bob", "")
	if c != 200 {
		t.Fatalf("delete bob: %d", c)
	}
	if sh := s.State().Share("docs"); len(sh.RO) != 0 {
		t.Errorf("bob still in docs: %+v", sh)
	}
	if _, ok := w.users["bob"]; ok {
		t.Error("bob is still a system user")
	}
	if c, _ := do(t, ts, "DELETE", "/v1/users/bob", ""); c != 404 {
		t.Errorf("delete missing: %d", c)
	}

	// Share delete keeps the data unless purge.
	f.Reset()
	do(t, ts, "DELETE", "/v1/shares/docs", "")
	if f.Ran("zfs destroy") {
		t.Error("delete without purge destroyed data")
	}
	do(t, ts, "DELETE", "/v1/luns/lun0?purge=1", "")
	if !f.Ran("zfs destroy tank/luns/lun0") {
		t.Error("purge did not destroy the zvol")
	}

	// The state is on disk and its copy in the pool.
	b, _ := f.ReadFile(StatePath)
	var saved State
	json.Unmarshal(b, &saved)
	if len(saved.Users) != 1 || saved.Users[0].Name != "alice" || len(saved.Shares) != 0 {
		t.Errorf("saved state %s", b)
	}
	if _, err := f.ReadFile("/tank/.keelnas/state.json"); err != nil {
		t.Error("no copy of the state in the pool")
	}
}

// After a new boot environment the system users and generated files are gone: apply rebuilds them
// with the uids from the state.
func TestApplyRebuilds(t *testing.T) {
	w, f := newWorld(t)
	st := &State{Version: 1, Key: "k",
		Pools:  []Pool{{Name: "tank", GUID: "777"}},
		Users:  []User{{"alice", 2001}, {"bob", 2002}},
		Shares: []Share{{Name: "docs", Pool: "tank", RW: []string{"alice"}, RO: []string{"bob"}}},
	}
	f.WriteFile(StatePath, st.Marshal(), 0o600)
	w.pools["777"] = "tank"
	w.users["stale"] = PwUser{"stale", 2009, GroupID}
	s, err := NewServer(f, StatePath, "test", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if errs := s.Apply(); len(errs) != 0 {
		t.Fatalf("apply: %v", errs)
	}
	if w.users["alice"].UID != 2001 || w.users["bob"].UID != 2002 {
		t.Errorf("users %v", w.users)
	}
	if _, ok := w.users["stale"]; ok {
		t.Error("a user not in the state stayed")
	}
	if _, err := f.ReadFile(SmbConf); err != nil {
		t.Error("smb4.conf not written")
	}
	// Nothing changed: no reload on a second apply.
	f.Reset()
	s.Apply()
	if f.Ran("smbcontrol") || f.Ran("service ctld onereload") {
		t.Errorf("reload without a change: %v", f.Calls)
	}
	// A pool that is not imported is imported by guid.
	delete(w.pools, "777")
	f.Reset()
	s.Apply()
	if !f.Ran("zpool import 777 tank") {
		t.Errorf("no import: %v", f.Calls)
	}
}

func TestRequireKey(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	key := s.State().Key
	ts := httptest.NewServer(RequireKey(key, s.Handler()))
	defer ts.Close()
	for _, tc := range []struct {
		auth string
		want int
	}{{"", 401}, {"Bearer wrong", 401}, {key, 401}, {"Bearer " + key, 200}} {
		req, _ := http.NewRequest("GET", ts.URL+"/v1/users", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("auth %q: %d, want %d", tc.auth, resp.StatusCode, tc.want)
		}
	}
}

func TestUnknownPathAndBadJSON(t *testing.T) {
	_, _, _, ts := newTestServer(t)
	if c, m := do(t, ts, "GET", "/v1/nothing", ""); c != 404 || m["error"] == nil {
		t.Errorf("unknown path: %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/users", `{"name":`); c != 400 {
		t.Errorf("bad JSON: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/users", `{"name":"a","password":"b","admin":true}`); c != 400 {
		t.Errorf("unknown field: %d", c)
	}

}

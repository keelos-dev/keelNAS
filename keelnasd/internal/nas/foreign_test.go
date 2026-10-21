package nas

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"keelnas/internal/sys"
)

const tankID = "5476079027047100592"

func TestParseImportTank12(t *testing.T) {
	ps := ParseImport(readTestdata(t, "zpool-import-tank12.txt"))
	if len(ps) != 1 {
		t.Fatalf("pools %+v", ps)
	}
	p := ps[0]
	if p.Name != "tank" || p.GUID != tankID || p.State != "ONLINE" || p.Layout != "raidz2" || !p.OtherHost() {
		t.Errorf("tank %+v", p)
	}
	if !strings.Contains(p.Action, "the '-f' flag.") {
		t.Errorf("action not joined: %q", p.Action)
	}
	if len(p.Devices) != 12 {
		t.Fatalf("devices %+v", p.Devices)
	}
	for i, d := range p.Devices {
		if d.Path != fmt.Sprintf("gpt/slot%02d", i+1) || d.State != "ONLINE" {
			t.Errorf("device %d: %+v", i, d)
		}
	}
}

func TestParseImportMany(t *testing.T) {
	out := `   pool: old
     id: 111
  state: DEGRADED
status: One or more devices are missing from the system.
 action: The pool can be imported despite missing or damaged devices.  The
	fault tolerance of the pool may be compromised if imported.
 config:

	old                       DEGRADED
	  mirror-0                DEGRADED
	    vtbd1                 ONLINE
	    9876543210123456789   UNAVAIL  cannot open
	logs
	  vtbd2                   ONLINE
	cache
	  vtbd3
	spares
	  vtbd4                   AVAIL

   pool: single
     id: 222
  state: ONLINE
 action: The pool can be imported using its name or numeric identifier.
 config:

	single      ONLINE
	  diskid/DISK-X  ONLINE
`
	ps := ParseImport(out)
	if len(ps) != 2 {
		t.Fatalf("pools %+v", ps)
	}
	o := ps[0]
	if o.Name != "old" || o.GUID != "111" || o.State != "DEGRADED" || o.Layout != "mirror" || o.OtherHost() {
		t.Errorf("old %+v", o)
	}
	var paths []string
	for _, d := range o.Devices {
		paths = append(paths, d.Path)
	}
	// vtbd3 (cache, no state column) is not listed; the log and the spare are: their disks are
	// the pool's too
	if strings.Join(paths, " ") != "vtbd1 9876543210123456789 vtbd2 vtbd4" {
		t.Errorf("old devices %v", paths)
	}
	if s := ps[1]; s.Name != "single" || s.Layout != "disk" || len(s.Devices) != 1 || s.Devices[0].Path != "diskid/DISK-X" || s.OtherHost() {
		t.Errorf("single %+v", s)
	}
	if ParseImport("no pools available to import\n") != nil {
		t.Error("no pools")
	}
}

func TestParseZdbLabel(t *testing.T) {
	l, ok := ParseZdbLabel(readTestdata(t, "zdb-label-tank.txt"))
	if !ok || !l.Found || l.Name != "tank" || l.GUID != tankID || l.Hostname != "oldnas" || l.State != "0" {
		t.Errorf("tank label %+v %v", l, ok)
	}
	if l, ok := ParseZdbLabel(noLabel); !ok || l.Found {
		t.Errorf("no label: %+v %v", l, ok)
	}
	if _, ok := ParseZdbLabel("zdb: can't open '/dev/x': Permission denied\n"); ok {
		t.Error("an error must not read as 'no label'")
	}
	if _, ok := ParseZdbLabel(""); ok {
		t.Error("no output must not read as 'no label'")
	}
}

func TestParseGpartShow(t *testing.T) {
	ts := ParseGpartShow(readTestdata(t, "hba-gpart-show.txt"))
	if len(ts) != 14 || ts[0].Provider != "vtbd0" || len(ts[0].Parts) != 3 || ts[1].Provider != "vtbd3" ||
		strings.Join(ts[1].Parts, " ") != "vtbd3p1" || ts[2].Provider != "da0" || ts[2].Scheme != "GPT" || ts[2].Parts[0] != "da0p1" {
		t.Errorf("tables %+v", ts)
	}
}

// hbaWorld: the system disk vtbd0 (pool knsys), four test disks and the 12 disks of a raidz2
// pool tank that is NOT imported, last used by another system: what a keelNAS sees when a
// host gives it an HBA with an existing pool. vtbd1 is empty, vtbd2 has UFS, vtbd3 a GPT with
// an NTFS partition, vtbd4 the label of a destroyed pool.
func hbaWorld(t *testing.T) (*world, *sys.Fake) {
	w, f := newWorld(t)
	var zs map[string]any // zpool-status.json without its data pool: only the system pool
	json.Unmarshal([]byte(readTestdata(t, "zpool-status.json")), &zs)
	delete(zs["pools"].(map[string]any), "tank")
	b, _ := json.Marshal(zs)
	w.status = string(b)
	w.importOut = readTestdata(t, "zpool-import-tank12.txt")
	w.gpart = readTestdata(t, "hba-gpart-show.txt")
	w.fstyp["vtbd2"] = "ufs"
	w.zdb["vtbd4"] = strings.Replace(strings.Replace(readTestdata(t, "zdb-label-tank.txt"), "name: 'tank'", "name: 'gone'", 1), "state: 0", "state: 2", 1)
	label := readTestdata(t, "zdb-label-tank.txt")
	for i := 0; i < 12; i++ {
		w.zdb[fmt.Sprintf("da%dp1", i)] = label
		w.zdb[fmt.Sprintf("gpt/slot%02d", i+1)] = label
	}
	w.pools["5389397623543364451"] = "knsys"
	w.extra = func(line string, a []string) (string, error, bool) {
		switch {
		case line == "geom disk list":
			return readTestdata(t, "hba-geom-disk-list.txt"), nil, true
		case line == "glabel status -s":
			return readTestdata(t, "hba-glabel.txt"), nil, true
		case line == "zpool list -H -o name,bootfs":
			return "knsys\tknsys/ROOT/default\n", nil, true
		case strings.HasPrefix(line, "zpool import ") && strings.Contains(line, tankID):
			if !strings.Contains(line, " -f ") {
				return "cannot import 'tank': pool was previously in use from another system.", sys.ErrFail, true
			}
			name := "tank"
			if a[len(a)-1] != tankID {
				name = a[len(a)-1]
			}
			w.pools[tankID] = name
			w.importOut = ""
			return "", nil, true
		case strings.HasPrefix(line, "zpool create"), strings.HasPrefix(line, "zpool attach"),
			strings.HasPrefix(line, "zpool labelclear"), strings.HasPrefix(line, "gpart"), strings.HasPrefix(line, "newfs"):
			if strings.Contains(line, "da") {
				t.Errorf("wrote a disk of tank: %s", line)
			}
		}
		return "", nil, false
	}
	return w, f
}

func TestForeignPoolDisksRefused(t *testing.T) {
	w, f := hbaWorld(t)
	s, ts := serverOn(t, f)

	_, m := do(t, ts, "GET", "/v1/disks", "")
	byName := map[string]map[string]any{}
	for _, d := range m["disks"].([]any) {
		byName[d.(map[string]any)["name"].(string)] = d.(map[string]any)
	}
	if len(byName) != 17 {
		t.Fatalf("disks %v", m)
	}
	for i := 0; i < 12; i++ {
		d := byName[fmt.Sprintf("da%d", i)]
		if d["pool"] != "tank" || d["pool_id"] != tankID || d["pool_state"] != "not_imported" || d["free"] != false {
			t.Errorf("da%d: %v", i, d)
		}
	}
	for n, want := range map[string][3]any{
		"vtbd0": {"knsys", "imported", ""},
		"vtbd1": {"", nil, ""},
		"vtbd2": {"", nil, "filesystem ufs"},
		"vtbd3": {"", nil, "partitions GPT"},
		"vtbd4": {"gone", "destroyed", ""},
	} {
		d := byName[n]
		c, _ := d["content"].(string)
		if d["pool"] != want[0] || d["pool_state"] != want[1] || c != want[2] || d["free"] != (n == "vtbd1") {
			t.Errorf("%s: %v", n, d)
		}
	}
	if byName["vtbd0"]["system"] != true {
		t.Errorf("vtbd0 is the system: %v", byName["vtbd0"])
	}

	// every disk of tank is refused, alone, as a mirror, and as a mirror half
	for i := 0; i < 12; i++ {
		disk := fmt.Sprintf("da%d", i)
		c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["`+disk+`"]}`)
		if c != 409 || !strings.Contains(m["error"].(string), "pool tank") || !strings.Contains(m["error"].(string), "not imported") {
			t.Errorf("storage on %s: %d %v", disk, c, m)
		}
		if err := s.freeDisk(disk); err == nil {
			t.Errorf("freeDisk(%s) = nil", disk)
		}
		if c, _ := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1","`+disk+`"],"protect":true}`); c != 409 {
			t.Errorf("mirror with %s: %d", disk, c)
		}
	}
	for _, d := range []string{"vtbd0", "vtbd2", "vtbd3", "vtbd4"} {
		if c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["`+d+`"]}`); c != 409 {
			t.Errorf("storage on %s: %d %v", d, c, m)
		}
	}
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "zpool create") || strings.HasPrefix(c, "zpool attach") {
			t.Errorf("ran %s", c)
		}
	}

	// the empty disk is taken, without -f, and not under tank's name
	c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	if c != 200 || m["name"] != "tank2" {
		t.Fatalf("storage on vtbd1: %d %v", c, m)
	}
	if !f.Ran("zpool create -o ashift=12 -o autotrim=on -O compression=lz4 -O atime=off -O xattr=sa tank2 vtbd1") {
		t.Errorf("create: %v", f.Calls)
	}
	// a mirror half: tank's disk is refused for protect too
	w.mu.Lock()
	w.status = `{"pools":{"tank2":{"name":"tank2","state":"ONLINE","pool_guid":777,"vdevs":{"tank2":{"name":"tank2","vdev_type":"root","vdevs":{"vtbd1":{"name":"vtbd1","vdev_type":"disk","path":"/dev/vtbd1","state":"ONLINE"}}}}}}}`
	w.mu.Unlock()
	if c, m := do(t, ts, "POST", "/v1/storage/tank2/protect", `{"disk":"da3"}`); c != 409 || !strings.Contains(m["error"].(string), "pool tank") {
		t.Errorf("protect with da3: %d %v", c, m)
	}
	if f.Ran("zpool attach") {
		t.Error("attach ran")
	}
}

// A disk keelnasd cannot check (zdb says something unexpected) is refused, not taken.
func TestUncheckableDiskRefused(t *testing.T) {
	w, f := newWorld(t)
	w.zdb["vtbd1"] = "zdb: something unexpected"
	_, ts := serverOn(t, f)
	c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	if c != 409 || !strings.Contains(m["error"].(string), "cannot tell") {
		t.Errorf("unknown disk: %d %v", c, m)
	}
}

// A disk with data in its first MiB but nothing a tool recognises is not empty.
func TestDataOnDiskRefused(t *testing.T) {
	_, f := newWorld(t)
	f.Heads["vtbd1"] = append(make([]byte, 4096), 'L', 'A', 'B', 'E', 'L', 'O', 'N', 'E')
	_, ts := serverOn(t, f)
	c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	if c != 409 || !strings.Contains(m["error"].(string), "not empty (data)") {
		t.Errorf("disk with data: %d %v", c, m)
	}
}

func TestImportForeignPool(t *testing.T) {
	w, f := hbaWorld(t)
	s, ts := serverOn(t, f)

	_, m := do(t, ts, "GET", "/v1/importable", "")
	ps := m["pools"].([]any)
	if len(ps) != 1 {
		t.Fatalf("importable %v", m)
	}
	p := ps[0].(map[string]any)
	if p["name"] != "tank" || p["id"] != tankID || p["other_host"] != true || p["last_host"] != "oldnas" ||
		p["layout"] != "raidz2" || p["name_taken"] != false || p["size"] != float64(12*8001563222016) {
		t.Errorf("tank %v", p)
	}
	ds := p["disks"].([]any)
	if len(ds) != 12 || ds[0].(map[string]any)["name"] != "da0" || ds[11].(map[string]any)["device"] != "gpt/slot12" {
		t.Errorf("disks %v", ds)
	}

	// last used by another system: only with force
	if c, m := do(t, ts, "POST", "/v1/importable/"+tankID+"/import", `{}`); c != 409 || !strings.Contains(m["error"].(string), "oldnas") {
		t.Errorf("import without force: %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/importable/123/import", `{"force":true}`); c != 404 {
		t.Errorf("unknown id: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/importable/"+tankID+"/import", `{"force":true,"name":"Bad Name"}`); c != 400 {
		t.Errorf("bad name: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/importable/"+tankID+"/import", `{"force":true,"name":"knsys"}`); c != 409 {
		t.Errorf("name of an imported pool: %d", c)
	}
	f.Reset()
	c, m := do(t, ts, "POST", "/v1/importable/"+tankID+"/import", `{"force":true}`)
	if c != 200 || m["altroot"] != "/pools/tank" {
		t.Fatalf("import: %d %v", c, m)
	}
	if !f.Ran("zpool import -f -R /pools/tank " + tankID) {
		t.Errorf("import: %v", f.Calls)
	}
	for _, c := range f.Calls {
		if strings.Contains(c, "upgrade") || strings.HasPrefix(c, "zfs set") || strings.HasPrefix(c, "zpool set") {
			t.Errorf("changed the pool: %s", c)
		}
	}
	st := s.State()
	if len(st.Pools) != 1 || st.Pools[0] != (Pool{Name: "tank", GUID: tankID, Altroot: "/pools/tank"}) {
		t.Errorf("state pools %+v", st.Pools)
	}

	// a share on it is mounted below the altroot
	w.mu.Lock()
	w.altroot["tank"] = "/pools/tank"
	w.mu.Unlock()
	do(t, ts, "POST", "/v1/users", `{"name":"alice","password":"x"}`)
	c, m = do(t, ts, "POST", "/v1/shares", `{"name":"docs","pool":"tank","rw":["alice"]}`)
	if c != 200 || m["path"] != "/pools/tank/shares/docs" {
		t.Errorf("share: %d %v", c, m)
	}
	if !f.Ran("zfs create -o mountpoint=/shares/docs tank/shares/docs") {
		t.Errorf("share dataset: %v", f.Calls)
	}
	conf, _ := f.ReadFile(SmbConf)
	if !strings.Contains(string(conf), "path = /pools/tank/shares/docs") {
		t.Errorf("smb4.conf:\n%s", conf)
	}

	// at the next start it is imported again below its altroot
	w.mu.Lock()
	delete(w.pools, tankID)
	w.mu.Unlock()
	f.Reset()
	s.Apply()
	if !f.Ran("zpool import -R /pools/tank " + tankID + " tank") {
		t.Errorf("re-import: %v", f.Calls)
	}
}

// serverOn is newTestServer on a given fake.
func serverOn(t *testing.T, f *sys.Fake) (*Server, *httptest.Server) {
	t.Helper()
	s, err := NewServer(f, StatePath, "test", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

package nas

// Tests of the ported mirror, export, expand, delete and labelclear (mirror.go, pools.go;
// docs/04 §17.2–17.3).

import (
	"fmt"
	"strings"
	"testing"
)

// geomDisks: a geom disk list of vtbdN with these sizes in GiB.
func geomDisks(gib ...int64) string {
	var b strings.Builder
	for i, g := range gib {
		fmt.Fprintf(&b, "Geom name: vtbd%d\nProviders:\n1. Name: vtbd%d\n   Mediasize: %d (%dG)\n   Sectorsize: 512\n   Mode: r0w0e0\n   descr: \n   ident: S%d\n   rotationrate: unknown\n\n", i, i, g<<30, g, i)
	}
	return b.String()
}

// poolStatus: zpool status -j of tank (guid 777) with these top-level vdevs.
func poolStatus(state, vdevs string) string {
	return `{"pools":{"tank":{"name":"tank","state":"` + state + `","pool_guid":777,"vdevs":{"tank":{"name":"tank","vdev_type":"root","vdevs":{` + vdevs + `}}}}}}`
}

func leaf(name, guid, state string, gib int64) string {
	path := "/dev/" + name
	if strings.HasPrefix(name, "1") { // a missing device: named by its guid, path = where it was
		path = "/dev/vtbd2"
	}
	return fmt.Sprintf(`"%s":{"name":"%s","vdev_type":"disk","guid":%s,"path":"%s","state":"%s","total_space":%d,"phys_space":%d}`, name, name, guid, path, state, (gib<<30)-(512<<20), gib<<30)
}

func mirror(gib int64, leaves ...string) string {
	return fmt.Sprintf(`"mirror-0":{"name":"mirror-0","vdev_type":"mirror","guid":500,"state":"ONLINE","total_space":%d,"vdevs":{%s}}`, (gib<<30)-(512<<20), strings.Join(leaves, ","))
}

func TestMirrorOps(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	// vtbd1, vtbd2 4G; vtbd3 2G; vtbd4 8G; vtbd5 carries another pool's label
	w.zdb["vtbd5"] = readTestdata(t, "zdb-label-tank.txt")
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "geom disk list" {
			return geomDisks(20, 4, 4, 2, 8, 4), nil, true
		}
		return "", nil, false
	}
	set := func(st string) { w.mu.Lock(); w.status = st; w.mu.Unlock(); f.Reset() }
	set(poolStatus("ONLINE", leaf("vtbd1", "11", "ONLINE", 4)))

	if c, m := do(t, ts, "POST", "/v1/storage/tank/attach", `{"disk":"vtbd3"}`); c != 409 || !strings.Contains(m["error"].(string), "smaller") {
		t.Errorf("small disk: %d %v", c, m)
	}
	if c, m := do(t, ts, "POST", "/v1/storage/tank/attach", `{"disk":"vtbd5"}`); c != 409 || !strings.Contains(m["error"].(string), "tank") {
		t.Errorf("foreign disk: %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/attach", `{"disk":"vtbd1"}`); c != 409 {
		t.Errorf("own disk: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/nope/attach", `{"disk":"vtbd2"}`); c != 404 {
		t.Errorf("no pool: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/detach", `{"disk":"vtbd1"}`); c != 409 {
		t.Errorf("detach a single disk: %d", c)
	}
	if c, m := do(t, ts, "POST", "/v1/storage/tank/attach", `{"disk":"vtbd2"}`); c != 200 || !f.Ran("zpool attach tank vtbd1 vtbd2") {
		t.Errorf("attach: %d %v %v", c, m, f.Calls)
	}
	for _, c := range f.Calls {
		if strings.Contains(c, " -f") && strings.HasPrefix(c, "zpool") {
			t.Errorf("-f: %s", c)
		}
	}

	// a mirror: the device list has guids and the vdev
	set(poolStatus("ONLINE", mirror(4, leaf("vtbd1", "11", "ONLINE", 4), leaf("vtbd2", "12", "ONLINE", 4))))
	c, m := do(t, ts, "GET", "/v1/storage", "")
	p := m["storage"].([]any)[0].(map[string]any)
	ds := p["disks"].([]any)
	if c != 200 || len(ds) != 2 || ds[1].(map[string]any)["guid"] != "12" || ds[1].(map[string]any)["vdev"] != "mirror-0" || p["vdevs"].([]any)[0].(map[string]any)["type"] != "mirror" {
		t.Fatalf("storage: %v", p)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/detach", `{"disk":"vtbd2"}`); c != 200 || !f.Ran("zpool detach tank 12") || !f.Ran("zpool labelclear -f /dev/vtbd2") || !f.Ran("dd if=/dev/zero of=/dev/vtbd2 bs=1m count=4") {
		t.Errorf("detach: %d %v", c, f.Calls)
	}

	// a member is missing (named by its guid): GET shows it, the working one cannot go, the
	// missing one can be replaced by its guid
	set(poolStatus("DEGRADED", mirror(4, leaf("vtbd1", "11", "ONLINE", 4), leaf("12", "12", "UNAVAIL", 0))))
	_, m = do(t, ts, "GET", "/v1/storage", "")
	var d map[string]any
	for _, x := range m["storage"].([]any)[0].(map[string]any)["disks"].([]any) {
		if x.(map[string]any)["guid"] == "12" {
			d = x.(map[string]any)
		}
	}
	if d["missing"] != true || d["name"] != "12" || d["was"] != "vtbd2" {
		t.Errorf("missing: %v", d)
	}
	_, m = do(t, ts, "GET", "/v1/disks", "")
	for _, x := range m["disks"].([]any) {
		if x := x.(map[string]any); x["name"] == "vtbd2" && x["pool"] != "" {
			t.Errorf("vtbd2 counted as the missing member: %v", x)
		}
	}
	if c, m := do(t, ts, "POST", "/v1/storage/tank/detach", `{"disk":"vtbd1"}`); c != 409 || !strings.Contains(m["error"].(string), "last working") {
		t.Errorf("detach the last working: %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/replace", `{"old":"12","disk":"vtbd3"}`); c != 409 {
		t.Errorf("replace with a small disk: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/replace", `{"old":"99","disk":"vtbd4"}`); c != 404 {
		t.Errorf("replace an unknown member: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/replace", `{"old":"12"}`); c != 400 {
		t.Errorf("replace without a disk: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/replace", `{"old":"12","disk":"vtbd4"}`); c != 200 || !f.Ran("zpool replace tank 12 vtbd4") {
		t.Errorf("replace: %d %v", c, f.Calls)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/detach", `{"disk":"12"}`); c != 200 || !f.Ran("zpool detach tank 12") || f.Ran("zpool labelclear") {
		t.Errorf("detach the missing one: %d %v", c, f.Calls)
	}
	_ = s
}

func TestPoolOps(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	w.zdb["vtbd5"] = strings.Replace(strings.Replace(readTestdata(t, "zdb-label-tank.txt"), "name: 'tank'", "name: 'gone'", 1), "state: 0", "state: 2", 1)
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "geom disk list" {
			return geomDisks(20, 8, 4, 4, 4, 4), nil, true
		}
		return "", nil, false
	}
	set := func(st string) { w.mu.Lock(); w.status = st; w.mu.Unlock(); f.Reset() }
	// a 4G pool on what is now an 8G disk: 4 GiB to gain
	set(poolStatus("ONLINE", strings.Replace(leaf("vtbd1", "11", "ONLINE", 8), fmt.Sprintf(`"total_space":%d`, (8<<30)-(512<<20)), fmt.Sprintf(`"total_space":%d`, (4<<30)-(512<<20)), 1)))
	_, m := do(t, ts, "GET", "/v1/storage", "")
	if e := m["storage"].([]any)[0].(map[string]any)["expand"]; e != float64((8<<30)-(4<<30)+(512<<20)) {
		t.Errorf("expand %v", e)
	}
	if c, m := do(t, ts, "POST", "/v1/storage/tank/expand", ""); c != 200 || !f.Ran("zpool online -e tank /dev/vtbd1") {
		t.Errorf("expand: %d %v %v", c, m, f.Calls)
	}
	set(poolStatus("ONLINE", leaf("vtbd1", "11", "ONLINE", 4)))
	_, m = do(t, ts, "GET", "/v1/storage", "")
	if e := m["storage"].([]any)[0].(map[string]any)["expand"]; e != float64(0) {
		t.Errorf("nothing to gain: %v", e)
	}

	// export: confirm, no shares on it
	if c, _ := do(t, ts, "POST", "/v1/storage/tank/export", `{"confirm":"tan"}`); c != 400 {
		t.Errorf("export without confirm: %d", c)
	}
	s.mu.Lock()
	s.st.Shares = append(s.st.Shares, Share{Name: "docs", Dataset: "tank/shares/docs", RW: []string{"x"}})
	s.mu.Unlock()
	if c, m := do(t, ts, "POST", "/v1/storage/tank/export", `{"confirm":"tank"}`); c != 409 || !strings.Contains(m["error"].(string), "share docs") {
		t.Errorf("export with a share: %d %v", c, m)
	}
	if c, _ := do(t, ts, "DELETE", "/v1/storage/tank?confirm=tank", ""); c != 409 {
		t.Errorf("delete with a share: %d", c)
	}
	s.mu.Lock()
	s.st.Shares = nil
	s.mu.Unlock()
	if c, m := do(t, ts, "POST", "/v1/storage/tank/export", `{"confirm":"tank"}`); c != 200 || !f.Ran("zpool export tank") || s.State().Pool("tank") != nil {
		t.Errorf("export: %d %v", c, m)
	}

	// delete: confirm; destroy, the disks' labels cleared, the state forgets the pool
	set(`{"pools":{}}`)
	w.mu.Lock()
	delete(w.pools, "777") // exported
	w.mu.Unlock()
	if c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`); c != 200 {
		t.Fatalf("new storage: %d %v", c, m)
	}
	set(poolStatus("ONLINE", mirror(4, leaf("vtbd1", "11", "ONLINE", 4), leaf("vtbd2", "12", "ONLINE", 4))))
	if c, _ := do(t, ts, "DELETE", "/v1/storage/tank", ""); c != 400 {
		t.Errorf("delete without confirm: %d", c)
	}
	c, m := do(t, ts, "DELETE", "/v1/storage/tank?confirm=tank", "")
	if c != 200 || !f.Ran("zpool destroy tank") || !f.Ran("zpool labelclear -f /dev/vtbd1") || !f.Ran("zpool labelclear -f /dev/vtbd2") || s.State().Pool("tank") != nil {
		t.Errorf("delete: %d %v %v", c, m, f.Calls)
	}

	// labelclear: only a disk with a label of a pool that is not imported, confirmed by its name
	set(poolStatus("ONLINE", leaf("vtbd1", "11", "ONLINE", 4)))
	if c, _ := do(t, ts, "POST", "/v1/disks/vtbd5/labelclear", `{"confirm":"vtbd5"}`); c != 400 {
		t.Errorf("labelclear without the pool's name: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/disks/vtbd3/labelclear", `{"confirm":"vtbd3"}`); c != 409 {
		t.Errorf("labelclear without a label: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/disks/vtbd9/labelclear", `{"confirm":"x"}`); c != 404 {
		t.Errorf("no disk: %d", c)
	}
	c, m = do(t, ts, "POST", "/v1/disks/vtbd5/labelclear", `{"confirm":"gone"}`)
	if c != 200 || !f.Ran("zpool labelclear -f /dev/vtbd5") || !f.Ran("dd if=/dev/zero of=/dev/vtbd5 bs=1m count=4") {
		t.Errorf("labelclear: %d %v %v", c, m, f.Calls)
	}
}

// After a replace the old disk's label goes once ZFS has let it go (QEMU 2026-10-23: it stayed,
// and the disk showed as a member of a pool that is not imported).
func TestReleaseReplaced(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "geom disk list" {
			return geomDisks(20, 4, 4), nil, true
		}
		return "", nil, false
	}
	set := func(st string) { w.mu.Lock(); w.status = st; w.mu.Unlock(); f.Reset() }
	set(poolStatus("ONLINE", leaf("vtbd1", "11", "ONLINE", 4)))
	if c, m := do(t, ts, "POST", "/v1/storage/tank/replace", `{"old":"vtbd1","disk":"vtbd2"}`); c != 200 {
		t.Fatalf("replace: %d %v", c, m)
	}
	// resilvering: both are there; nothing is cleared
	set(poolStatus("ONLINE", `"replacing-0":{"name":"replacing-0","vdev_type":"replacing","guid":600,"vdevs":{`+leaf("vtbd1", "11", "ONLINE", 4)+","+leaf("vtbd2", "13", "ONLINE", 4)+"}}"))
	s.scrubs.Step()
	if f.Ran("zpool labelclear") {
		t.Error("cleared while resilvering")
	}
	// done: vtbd1 is out, its label still says pool 777
	w.mu.Lock()
	w.zdb["vtbd1"] = strings.Replace(readTestdata(t, "zdb-label-tank.txt"), "pool_guid: 5476079027047100592", "pool_guid: 777", 1)
	w.mu.Unlock()
	set(poolStatus("ONLINE", leaf("vtbd2", "13", "ONLINE", 4)))
	s.scrubs.Step()
	if !f.Ran("zpool labelclear -f /dev/vtbd1") {
		t.Errorf("not cleared: %v", f.Calls)
	}
	f.Reset()
	s.scrubs.Step()
	if f.Ran("zpool labelclear") || f.Ran("zdb") {
		t.Errorf("cleared twice: %v", f.Calls)
	}
}

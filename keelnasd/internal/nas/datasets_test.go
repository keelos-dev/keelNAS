package nas

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"keelnas/internal/sys"
)

// datasetServer: storage tank on vtbd1, users alice and bob, a dataset tank/home keelNAS did
// not make (mounted at a real directory, owned 1001:1001 = the system account keel) and an
// unmounted tank/old.
func datasetServer(t *testing.T) (*Server, *world, func(string, string, string) (int, map[string]any), func() string, string) {
	s, w, f, ts := newTestServer(t)
	call := func(method, path, body string) (int, map[string]any) { return do(t, ts, method, path, body) }
	if c, m := call("POST", "/v1/storage", `{"disks":["vtbd1"]}`); c != 200 {
		t.Fatalf("storage: %d %v", c, m)
	}
	call("POST", "/v1/users", `{"name":"alice","password":"a"}`)
	call("POST", "/v1/users", `{"name":"bob","password":"b"}`)
	home := t.TempDir()
	w.mu.Lock()
	w.datasets["tank"] = "filesystem"
	w.datasets["tank/home"] = "filesystem"
	w.mps["tank/home"] = home
	w.owners[home] = "1001:1001"
	w.datasets["tank/old"] = "filesystem"
	w.mps["tank/old"] = "none"
	w.mu.Unlock()
	conf := func() string { b, _ := f.ReadFile(SmbConf); return string(b) }
	return s, w, func(m, p, b string) (int, map[string]any) { return call(m, p, b) }, conf, home
}

func dsRow(m map[string]any, name string) map[string]any {
	for _, x := range m["datasets"].([]any) {
		if r := x.(map[string]any); r["name"] == name {
			return r
		}
	}
	return nil
}

func TestDatasetsAPI(t *testing.T) {
	s, w, call, _, home := datasetServer(t)
	f := s.Sys.(*sys.Fake)

	if c, _ := call("GET", "/v1/storage/nope/datasets", ""); c != 404 {
		t.Errorf("unknown pool: %d", c)
	}
	c, m := call("GET", "/v1/storage/tank/datasets", "")
	if c != 200 {
		t.Fatalf("list: %d %v", c, m)
	}
	h := dsRow(m, "tank/home")
	if h == nil || h["ours"] != false || h["share"] != nil || h["mounted"] != true || h["mountpoint"] != home {
		t.Errorf("tank/home: %v", m)
	}
	if o := dsRow(m, "tank/old"); o == nil || o["mounted"] != false {
		t.Errorf("tank/old: %v", o)
	}

	// make datasets
	for body, want := range map[string]int{
		`{"parent":"tank/shares","name":"Bad"}`:  400,
		`{"parent":"other/x","name":"docs"}`:     400,
		`{"parent":"tank/nope","name":"docs"}`:   404,
		`{"parent":"tank/shares","name":"docs"}`: 200,
	} {
		if c, m := call("POST", "/v1/storage/tank/datasets", body); c != want {
			t.Errorf("POST datasets %s: %d %v", body, c, m)
		}
	}
	if c, _ := call("POST", "/v1/storage/nope/datasets", `{"parent":"nope","name":"x"}`); c != 404 {
		t.Errorf("unknown pool: %d", c)
	}
	if c, _ := call("POST", "/v1/storage/tank/datasets", `{"parent":"tank/shares","name":"docs"}`); c != 409 {
		t.Errorf("exists: %d", c)
	}
	if !f.Ran("zfs create -o mountpoint=/shares/docs tank/shares/docs") || !f.Ran("chown root:keelnas /shares/docs") {
		t.Error("tank/shares/docs not made like a share's dataset")
	}
	if c, m := call("POST", "/v1/storage/tank/datasets", `{"parent":"tank/shares/docs","name":"sub"}`); c != 200 || m["mountpoint"] != "/shares/docs/sub" || m["ours"] != true {
		t.Errorf("child: %d %v", c, m)
	}
	if c, m := call("POST", "/v1/storage/tank/datasets", `{"parent":"tank/old","name":"x"}`); c != 200 || m["mountpoint"] != "/tank/old/x" {
		t.Errorf("below an unmounted parent: %d %v", c, m)
	}
	if st := s.State(); !st.Ours("tank/shares/docs") || !st.Ours("tank/shares/docs/sub") || st.Ours("tank/home") {
		t.Errorf("ours: %v", st.Datasets)
	}

	// delete: never a foreign one, not one with children, not a shared one
	if c, _ := call("DELETE", "/v1/storage/tank/datasets/tank%2Fhome", ""); c != 403 {
		t.Errorf("delete foreign: %d", c)
	}
	if c, _ := call("DELETE", "/v1/storage/tank/datasets/tank%2Fnope", ""); c != 404 {
		t.Errorf("delete unknown: %d", c)
	}
	if c, _ := call("DELETE", "/v1/storage/tank/datasets/tank%2Fshares%2Fdocs", ""); c != 409 {
		t.Errorf("delete with a child: %d", c)
	}
	if c, m := call("POST", "/v1/shares", `{"dataset":"tank/shares/docs/sub","rw":["alice"]}`); c != 200 || m["name"] != "sub" || m["ours"] != true || m["owner"] != nil {
		t.Errorf("share of ours: %d %v", c, m)
	}
	if c, _ := call("DELETE", "/v1/storage/tank/datasets/tank%2Fshares%2Fdocs%2Fsub", ""); c != 409 {
		t.Errorf("delete shared: %d", c)
	}
	call("DELETE", "/v1/shares/sub", "")
	if c, m := call("DELETE", "/v1/storage/tank/datasets/tank%2Fshares%2Fdocs%2Fsub", ""); c != 200 {
		t.Errorf("delete: %d %v", c, m)
	}
	if !f.Ran("zfs destroy -r tank/shares/docs/sub") || s.State().Ours("tank/shares/docs/sub") {
		t.Error("not destroyed / still ours")
	}
	for _, line := range f.Calls {
		if strings.HasPrefix(line, "zfs destroy") && strings.Contains(line, "tank/home") {
			t.Errorf("destroyed a foreign dataset: %s", line)
		}
	}
	_ = w
}

func TestShareForeignDataset(t *testing.T) {
	s, w, call, conf, home := datasetServer(t)
	fk := s.Sys.(*sys.Fake)

	// tank/home belongs to uid 1001 (the system account keel, not a keelNAS user): not shared
	// until its owner is changed; the tree says so
	_, m := call("GET", "/v1/storage/tank/datasets", "")
	if h := dsRow(m, "tank/home"); h["shareable"] != false || h["owner"].(map[string]any)["uid"] != float64(1001) || h["owner"].(map[string]any)["user"] != "" {
		t.Errorf("tree: %v", h)
	}
	if o := dsRow(m, "tank/old"); o["owner"] != nil || o["shareable"] != false {
		t.Errorf("unmounted: %v", o)
	}
	for body, want := range map[string]int{
		`{"dataset":"tank/home","pool":"tank","rw":["alice"]}`: 400, // both forms
		`{"dataset":"tank/home","rw":[]}`:                      400, // nobody
		`{"dataset":"tank/home","rw":["alice"],"owner":"bob"}`: 400, // no owner field any more
		`{"dataset":"tank/home","name":"Home","rw":["alice"]}`: 400,
		`{"dataset":"nope/home","rw":["alice"]}`:               404,
		`{"dataset":"tank/nope","rw":["alice"]}`:               404,
		`{"dataset":"tank/old","rw":["alice"]}`:                409, // not mounted
		`{"dataset":"tank","rw":["alice"]}`:                    409, // root's
		`{"dataset":"tank/home","rw":["alice"]}`:               409, // uid 1001's
	} {
		if c, m := call("POST", "/v1/shares", body); c != want {
			t.Errorf("POST /v1/shares %s: %d (want %d) %v", body, c, want, m)
		}
	}
	if _, m := call("POST", "/v1/shares", `{"dataset":"tank/home","rw":["alice"]}`); !strings.Contains(m["error"].(string), "change the owner") {
		t.Errorf("message: %v", m)
	}
	// once its files belong to alice (the owner change, TestChownJob) it is shared like ours
	w.mu.Lock()
	w.owners[home] = "2001:2000"
	w.mu.Unlock()
	_, m = call("GET", "/v1/storage/tank/datasets", "")
	if h := dsRow(m, "tank/home"); h["shareable"] != true || h["owner"].(map[string]any)["user"] != "alice" {
		t.Errorf("tree after the change: %v", h)
	}
	fk.Reset()
	c, m := call("POST", "/v1/shares", `{"dataset":"tank/home","rw":["alice"],"ro":["bob"]}`)
	if c != 200 || m["name"] != "home" || m["ours"] != false || m["path"] != home || m["pool"] != "tank" || m["owner"] != nil {
		t.Fatalf("share tank/home: %d %v", c, m)
	}
	for _, line := range fk.Calls {
		if strings.HasPrefix(line, "chown") || strings.HasPrefix(line, "chmod") || strings.HasPrefix(line, "zfs set") || strings.HasPrefix(line, "zfs create") || strings.HasPrefix(line, "pw useradd") || strings.HasPrefix(line, "pw groupadd") {
			t.Errorf("sharing changed something: %s", line)
		}
	}
	if want := "[home]\n  path = " + home + "\n  valid users = alice bob\n  write list = alice\n  read only = yes\n  force group = keelnas\n  create mask = 0660\n  directory mask = 2770\n"; !strings.Contains(conf(), want) || strings.Contains(conf(), "force user") {
		t.Errorf("smb4.conf lacks %q:\n%s", want, conf())
	}
	if c, _ := call("POST", "/v1/shares", `{"dataset":"tank/home","name":"again","rw":["alice"]}`); c != 409 {
		t.Errorf("second share of a dataset: %d", c)
	}
	if c, _ := call("POST", "/v1/shares", `{"dataset":"tank/home","name":"home","rw":["alice"]}`); c != 409 {
		t.Errorf("share name taken: %d", c)
	}
	if c, _ := call("PUT", "/v1/shares/home", `{"rw":["alice"],"ro":[],"owner":"bob"}`); c != 400 {
		t.Errorf("PUT with owner: %d", c)
	}
	_, m = call("GET", "/v1/storage/tank/datasets", "")
	if sh, _ := dsRow(m, "tank/home")["share"].(map[string]any); sh == nil || sh["name"] != "home" {
		t.Errorf("tree share: %v", dsRow(m, "tank/home"))
	}

	// never destroyed
	fk.Reset()
	if c, _ := call("DELETE", "/v1/shares/home?purge=1", ""); c != 403 {
		t.Errorf("purge foreign: %d", c)
	}
	if s.State().Share("home") == nil || fk.Ran("zfs destroy") {
		t.Error("a refused purge changed something")
	}
	if c, _ := call("DELETE", "/v1/shares/home", ""); c != 200 || fk.Ran("zfs destroy") {
		t.Errorf("unshare: %d", c)
	}
	w.mu.Lock()
	_, still := w.datasets["tank/home"]
	w.mu.Unlock()
	if !still {
		t.Error("tank/home is gone")
	}

	// the old form still makes <pool>/shares/<name>, ours, and purge destroys it
	c, m = call("POST", "/v1/shares", `{"name":"files","pool":"tank","rw":["alice"]}`)
	if c != 200 || m["dataset"] != "tank/shares/files" || m["ours"] != true || m["path"] != "/shares/files" {
		t.Fatalf("old form: %d %v", c, m)
	}
	if !strings.Contains(conf(), "[files]\n  path = /shares/files\n  valid users = alice\n  write list = alice\n  read only = yes\n  force group = keelnas\n  create mask = 0660\n") {
		t.Errorf("ours keeps today's config:\n%s", conf())
	}
	if c, _ := call("DELETE", "/v1/shares/files?purge=1", ""); c != 200 || !fk.Ran("zfs destroy -r tank/shares/files") || s.State().Ours("tank/shares/files") {
		t.Errorf("purge ours: %d %v", c, s.State().Datasets)
	}
}

// The owner change: everything on the dataset gets user:keelnas, directories 2770, files 0660;
// links are changed, not followed; .keelnas and .zfs stay as they are. (A child dataset's
// mountpoint is another device and is left out: that needs a mount, tested on a real keelNAS,
// docs/06 §6.)
func TestChownJob(t *testing.T) {
	s, w, call, _, home := datasetServer(t)
	s.syncJobs = true
	oldGID := chownGID
	chownGID = os.Getgid()
	t.Cleanup(func() { chownGID = oldGID })
	s.mu.Lock()
	s.st.Users = append(s.st.Users, User{"me", os.Getuid()})
	s.mu.Unlock()

	os.MkdirAll(filepath.Join(home, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(home, "a", "b", "f.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(home, "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	os.MkdirAll(filepath.Join(home, ".keelnas"), 0o700)
	os.WriteFile(filepath.Join(home, ".keelnas", "state.json"), []byte("{}"), 0o600)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("x"), 0o644)
	os.Symlink(outside, filepath.Join(home, "out"))

	base := "/v1/storage/tank/datasets/"
	for path, want := range map[string]int{
		base + "tank%2Fhome/owner|{\"user\":\"nobody\"}":             400,
		base + "tank/owner|{\"user\":\"me\"}":                        400, // the storage's root
		base + "tank%2Fnope/owner|{\"user\":\"me\"}":                 404,
		base + "tank%2Fold/owner|{\"user\":\"me\"}":                  409, // not mounted
		"/v1/storage/nope/datasets/nope%2Fx/owner|{\"user\":\"me\"}": 404,
	} {
		p, body, _ := strings.Cut(path, "|")
		if c, m := call("POST", p, body); c != want {
			t.Errorf("POST %s %s: %d (want %d) %v", p, body, c, want, m)
		}
	}
	c, m := call("POST", base+"tank%2Fhome/owner", `{"user":"me"}`)
	if c != 200 || m["state"] != "done" || m["kind"] != "chown" || m["dataset"] != "tank/home" || m["failed"] != float64(0) {
		t.Fatalf("job: %d %v", c, m)
	}
	// root, a, a/b, a/b/f.txt, run.sh, out = 6; not .keelnas
	if m["total"] != float64(6) || m["files"] != float64(6) || m["total_bytes"] != float64(5+10) || m["bytes"] != m["total_bytes"] {
		t.Errorf("counts: %v", m)
	}
	mode := func(p string) os.FileMode {
		fi, err := os.Lstat(filepath.Join(home, p))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode() & (os.ModePerm | os.ModeSetgid)
	}
	for p, want := range map[string]os.FileMode{
		".": os.ModeSetgid | 0o770, "a": os.ModeSetgid | 0o770, "a/b": os.ModeSetgid | 0o770,
		"a/b/f.txt": 0o660, "run.sh": 0o660, ".keelnas": 0o700, ".keelnas/state.json": 0o600,
	} {
		if got := mode(p); got != want {
			t.Errorf("%s: mode %v, want %v", p, got, want)
		}
	}
	if fi, _ := os.Stat(outside); fi.Mode().Perm() != 0o644 {
		t.Errorf("the link was followed: %v", fi.Mode())
	}
	id := m["id"].(string)
	if c, m := call("GET", "/v1/jobs/"+id, ""); c != 200 || m["state"] != "done" {
		t.Errorf("GET job: %d %v", c, m)
	}
	if c, m := call("GET", "/v1/jobs", ""); c != 200 || len(m["jobs"].([]any)) != 1 {
		t.Errorf("GET jobs: %d %v", c, m)
	}
	if c, _ := call("GET", "/v1/jobs/99", ""); c != 404 {
		t.Errorf("unknown job: %d", c)
	}

	// one job per dataset at a time; the tree shows the running one and sharing waits for it
	s.jobs.mu.Lock()
	s.jobs.list = append(s.jobs.list, &job{mu: Job{ID: "7", Kind: "chown", Dataset: "tank/home", State: "running"}})
	s.jobs.mu.Unlock()
	if c, _ := call("POST", base+"tank%2Fhome/owner", `{"user":"me"}`); c != 409 {
		t.Errorf("second job: %d", c)
	}
	w.mu.Lock()
	w.owners[home] = "2001:2000"
	w.mu.Unlock()
	_, m = call("GET", "/v1/storage/tank/datasets", "")
	if j, _ := dsRow(m, "tank/home")["job"].(map[string]any); j == nil || j["id"] != "7" {
		t.Errorf("tree job: %v", dsRow(m, "tank/home"))
	}
	if c, m := call("POST", "/v1/shares", `{"dataset":"tank/home","rw":["alice"]}`); c != 409 || !strings.Contains(m["error"].(string), "being changed") {
		t.Errorf("share during the job: %d %v", c, m)
	}
}

func TestDatasetFiles(t *testing.T) {
	_, w, call, _, home := datasetServer(t)
	os.MkdirAll(filepath.Join(home, "a", "deep"), 0o755)
	os.WriteFile(filepath.Join(home, "b.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(home, "a", "c"), []byte("x"), 0o644)
	os.Symlink("/etc", filepath.Join(home, "out"))
	os.Symlink("../../..", filepath.Join(home, "a", "up"))
	os.Symlink("a", filepath.Join(home, "in"))
	base := "/v1/storage/tank/datasets/tank%2Fhome/files"

	c, m := call("GET", base, "")
	if c != 200 || m["path"] != "" {
		t.Fatalf("root: %d %v", c, m)
	}
	ents := m["entries"].([]any)
	names := []string{}
	for _, e := range ents {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "a,b.txt,in,out" {
		t.Errorf("entries %v", names)
	}
	for _, e := range ents {
		e := e.(map[string]any)
		switch e["name"] {
		case "a":
			if e["dir"] != true {
				t.Errorf("a: %v", e)
			}
		case "b.txt":
			if e["size"] != float64(5) || e["dir"] != false || e["mtime"] == "" {
				t.Errorf("b.txt: %v", e)
			}
		case "out", "in":
			if e["link"] != true || e["dir"] != false {
				t.Errorf("%s is not listed as a link: %v", e["name"], e)
			}
		}
	}
	if c, m := call("GET", base+"?path=a", ""); c != 200 || len(m["entries"].([]any)) != 3 {
		t.Errorf("a: %d %v", c, m)
	}
	if c, m := call("GET", base+"?path=in", ""); c != 200 || len(m["entries"].([]any)) != 3 {
		t.Errorf("a link inside: %d %v", c, m)
	}
	for q, want := range map[string]int{
		"..": 400, "../": 400, "a/../..": 400, "a/../b": 400, "/..": 400,
		"out": 400, "a/up": 400, "a/up/etc": 400, // links out of the dataset
		"b.txt": 400, "nope": 404, "%00": 400,
	} {
		if c, m := call("GET", base+"?path="+q, ""); c != want {
			t.Errorf("path %q: %d (want %d) %v", q, c, want, m)
		}
	}
	if c, _ := call("GET", "/v1/storage/tank/datasets/tank%2Fold/files", ""); c != 409 {
		t.Errorf("unmounted: %d", c)
	}
	if c, _ := call("GET", "/v1/storage/tank/datasets/other%2Fx/files", ""); c != 404 {
		t.Errorf("other pool: %d", c)
	}
	if c, _ := call("GET", "/v1/storage/tank/datasets/tank%2Fnope/files", ""); c != 404 {
		t.Errorf("unknown dataset: %d", c)
	}
	_ = w
}

func TestStateRewrite(t *testing.T) {
	_, f := newWorld(t)
	f.Files[StatePath] = []byte(`{"version":1,"key":"k","pools":[{"name":"tank","guid":"777"}],
		"users":[{"name":"alice","uid":2001}],
		"shares":[{"name":"docs","pool":"tank","rw":["alice"],"ro":[]},
			{"name":"home","dataset":"tank/home","rw":["alice"],"ro":[],"owner":"1500:1500"}]}`)
	s, err := NewServer(f, StatePath, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if sh := st.Share("docs"); sh == nil || sh.Dataset != "tank/shares/docs" || sh.Pool != "" || !st.Ours("tank/shares/docs") {
		t.Fatalf("rewritten %+v %v", sh, st.Datasets)
	}
	b := string(f.Files[StatePath])
	if !strings.Contains(b, `"dataset": "tank/shares/docs"`) || strings.Contains(b, `"pool": "tank"`) || strings.Contains(b, `"owner"`) || !strings.Contains(b, `"datasets": [`) {
		t.Errorf("state.json not rewritten:\n%s", b)
	}
	if _, created, err := LoadState(f, StatePath); err != nil || created {
		t.Errorf("a rewritten state is written again: %v %v", created, err)
	}
	// copies from the pools and imports of the old form are turned the same way
	if p, err := ParseState([]byte(`{"shares":[{"name":"a","pool":"t2","rw":[],"ro":[]}]}`)); err != nil || p.Shares[0].Dataset != "t2/shares/a" || !p.Ours("t2/shares/a") {
		t.Errorf("ParseState: %+v %v", p, err)
	}
}

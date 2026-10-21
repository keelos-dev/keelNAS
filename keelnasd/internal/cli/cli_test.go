package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	a, err := parseArgs([]string{"add", "docs", "--pool", "tank", "--rw=alice,bob", "--purge", "--ro", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(a.pos, " ") != "add docs" || a.flags["pool"] != "tank" || a.flags["rw"] != "alice,bob" || a.flags["purge"] != "1" || a.flags["ro"] != "c" {
		t.Errorf("%+v", a)
	}
	if _, err := parseArgs([]string{"--pool"}); err == nil {
		t.Error("--pool without a value accepted")
	}
	if got := list(" a, ,b "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("list %v", got)
	}
}

// The CLI sends the documented requests and prints the answers.
func TestCLIRequests(t *testing.T) {
	type req struct{ method, path, body string }
	var got []req
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, req{r.Method, r.URL.RequestURI(), string(b)})
		switch {
		case r.URL.Path == "/v1/users" && r.Method == "POST":
			w.Write([]byte(`{"name":"alice","uid":2001}`))
		case r.URL.Path == "/v1/shares" && r.Method == "GET":
			w.Write([]byte(`{"shares":[{"name":"docs","pool":"tank","rw":["alice"],"ro":[],"path":"/shares/docs","used":1048576}]}`))
		case r.URL.Path == "/v1/storage" && r.Method == "GET":
			w.Write([]byte(`{"storage":[{"name":"tank"}]}`))
		case r.URL.Path == "/v1/storage/tank/datasets" && r.Method == "GET":
			w.Write([]byte(`{"datasets":[{"name":"tank/home","mountpoint":"/pools/tank/usr/home","mounted":true,"used":2048,"ours":false,"share":{"name":"home"},"snapshots":3,"owner":{"uid":1500,"gid":1500,"user":""}}]}`))
		case strings.HasSuffix(r.URL.Path, "/owner"):
			w.Write([]byte(`{"id":"3","state":"counting","files":0,"total":0}`))
		case r.URL.Path == "/v1/jobs/3":
			w.Write([]byte(`{"id":"3","state":"done","files":12,"total":12,"errors":[]}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Write([]byte(`{"entries":[{"name":"a","dir":true},{"name":"b.txt","size":5,"mtime":"2026-10-07T00:00:00Z"},{"name":"l","link":true}]}`))
		case r.URL.Path == "/v1/luns":
			w.WriteHeader(409)
			w.Write([]byte(`{"error":"LUN lun0 exists"}`))
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer ts.Close()
	run := func(stdin string, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		c := &CLI{HTTP: ts.Client(), Base: ts.URL, In: strings.NewReader(stdin), Out: &out, Err: &errb}
		return c.Run(args), out.String(), errb.String()
	}
	if rc, out, _ := run("pw1\n", "user", "add", "alice"); rc != 0 || !strings.Contains(out, "uid 2001") {
		t.Errorf("user add: %d %q", rc, out)
	}
	var body map[string]string
	json.Unmarshal([]byte(got[0].body), &body)
	if body["password"] != "pw1" || body["name"] != "alice" {
		t.Errorf("user add body %s", got[0].body)
	}
	if rc, out, _ := run("", "share", "list"); rc != 0 || !strings.Contains(out, "/shares/docs") || !strings.Contains(out, "1.0M") {
		t.Errorf("share list: %d %q", rc, out)
	}
	if rc, out, _ := run("", "-j", "share", "list"); rc != 0 || !strings.Contains(out, `"path": "/shares/docs"`) {
		t.Errorf("share list -j: %d %q", rc, out)
	}
	if rc, _, e := run("", "lun", "add", "lun0", "--pool", "tank", "--size", "2"); rc != 1 || !strings.Contains(e, "LUN lun0 exists") {
		t.Errorf("lun add error: %d %q", rc, e)
	}
	if b := got[len(got)-1].body; !strings.Contains(b, `"size_gib":2`) {
		t.Errorf("lun body %s", b)
	}
	run("", "share", "add", "home", "--dataset", "tank/home", "--rw", "alice")
	if r := got[len(got)-1]; r.method != "POST" || r.path != "/v1/shares" || !strings.Contains(r.body, `"dataset":"tank/home"`) || strings.Contains(r.body, `"pool"`) {
		t.Errorf("share add --dataset %+v", r)
	}
	run("", "share", "add", "docs", "--rw", "alice")
	if r := got[len(got)-1]; !strings.Contains(r.body, `"pool":""`) || strings.Contains(r.body, "owner") {
		t.Errorf("share add (old form) %+v", r)
	}
	if rc, out, _ := run("", "dataset", "list"); rc != 0 || !strings.Contains(out, "tank/home") || !strings.Contains(out, "uid 1500") || !strings.Contains(out, "other") {
		t.Errorf("dataset list: %d %q", rc, out)
	}
	if rc, out, _ := run("", "-j", "dataset", "list"); rc != 0 || !strings.Contains(out, `"tank/home"`) {
		t.Errorf("dataset list -j: %d %q", rc, out)
	}
	run("", "dataset", "add", "tank/shares/docs")
	if r := got[len(got)-1]; r.method != "POST" || r.path != "/v1/storage/tank/datasets" || r.body != `{"name":"docs","parent":"tank/shares"}` {
		t.Errorf("dataset add %+v", r)
	}
	run("", "dataset", "del", "tank/shares/docs")
	if r := got[len(got)-1]; r.method != "DELETE" || r.path != "/v1/storage/tank/datasets/tank%2Fshares%2Fdocs" {
		t.Errorf("dataset del %+v", r)
	}
	if rc, out, _ := run("", "files", "tank/home", "a b"); rc != 0 || !strings.Contains(out, "a/") || !strings.Contains(out, "l@") || !strings.Contains(out, "b.txt") {
		t.Errorf("files: %d %q", rc, out)
	}
	if r := got[len(got)-1]; r.path != "/v1/storage/tank/datasets/tank%2Fhome/files?path=a+b" {
		t.Errorf("files %+v", r)
	}
	jobPoll = time.Millisecond
	if rc, out, _ := run("", "dataset", "chown", "tank/home", "alice"); rc != 0 || !strings.Contains(out, "done, 12 of 12") {
		t.Errorf("dataset chown: %d %q", rc, out)
	}
	if r := got[len(got)-2]; r.method != "POST" || r.path != "/v1/storage/tank/datasets/tank%2Fhome/owner" || r.body != `{"user":"alice"}` {
		t.Errorf("dataset chown request %+v", r)
	}
	if r := got[len(got)-1]; r.path != "/v1/jobs/3" {
		t.Errorf("job poll %+v", r)
	}
	run("", "share", "del", "docs", "--purge")
	if r := got[len(got)-1]; r.method != "DELETE" || r.path != "/v1/shares/docs?purge=1" {
		t.Errorf("share del %+v", r)
	}
	run("", "storage", "create", "--protect", "vtbd1", "vtbd2")
	if b := got[len(got)-1].body; !strings.Contains(b, `"disks":["vtbd1","vtbd2"]`) || !strings.Contains(b, `"protect":true`) {
		t.Errorf("storage create body %s", b)
	}
	if rc, _, e := run("", "nonsense"); rc != 1 || !strings.Contains(e, "usage") {
		t.Errorf("usage: %d %q", rc, e)
	}
}

// The commands of the second step: snapshots, settings, alerts, restore, the test hook.
func TestCLIStep2(t *testing.T) {
	type req struct{ method, path, body string }
	var got []req
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, req{r.Method, r.URL.RequestURI(), string(b)})
		switch {
		case strings.HasSuffix(r.URL.Path, "/snapshots") && r.Method == "GET":
			w.Write([]byte(`{"snapshots":[{"name":"keelnas-20261002-110000","time":"2026-10-02T11:00:00Z","class":"hourly","used":4096}]}`))
		case strings.HasSuffix(r.URL.Path, "/snapshots") && r.Method == "POST":
			w.Write([]byte(`{"name":"keelnas-20261002-112233","time":"2026-10-02T11:22:33Z","class":"manual","used":0}`))
		case r.URL.Path == "/v1/settings":
			w.Write([]byte(`{"snapshots":{"hourly":2,"daily":7,"weekly":4}}`))
		case r.URL.Path == "/v1/alerts":
			w.Write([]byte(`{"alerts":[{"level":"error","code":"pool_degraded","object":"tank","text":"Storage tank is DEGRADED.","since":"2026-10-02T11:00:00Z"}]}`))
		case r.URL.Path == "/v1/restore":
			w.Write([]byte(`{"ok":true,"restored_from":"tank","errors":[]}`))
		case r.URL.Path == "/v1/status":
			w.Write([]byte(`{"hostname":"keelnas","addresses":["192.0.2.10"],"alerts":2,"restored_from":"tank","keel":{"registered":true,"url":"https://192.0.2.1:443","time":"2026-10-03T10:00:00Z","error":""},"pools":[],"services":{},"last_apply":{"errors":[]}}`))
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer ts.Close()
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		c := &CLI{HTTP: ts.Client(), Base: ts.URL, In: strings.NewReader(""), Out: &out, Err: &errb}
		return c.Run(args), out.String(), errb.String()
	}
	last := func() req { return got[len(got)-1] }
	if rc, out, _ := run("snapshot", "list", "a"); rc != 0 || last().path != "/v1/shares/a/snapshots" || !strings.Contains(out, "keelnas-20261002-110000") || !strings.Contains(out, "hourly") {
		t.Errorf("snapshot list: %d %q %+v", rc, out, last())
	}
	if rc, out, _ := run("snapshot", "create", "a"); rc != 0 || last().method != "POST" || !strings.Contains(out, "manual") {
		t.Errorf("snapshot create: %d %q", rc, out)
	}
	run("snapshot", "del", "a", "keelnas-20261002-110000")
	if r := last(); r.method != "DELETE" || r.path != "/v1/shares/a/snapshots/keelnas-20261002-110000" {
		t.Errorf("snapshot del %+v", r)
	}
	run("snapshot", "list", "l1", "--lun")
	if r := last(); r.path != "/v1/luns/l1/snapshots" {
		t.Errorf("lun snapshot list %+v", r)
	}
	if rc, out, _ := run("settings"); rc != 0 || last().method != "GET" || !strings.Contains(out, "hourly 2") {
		t.Errorf("settings: %d %q", rc, out)
	}
	run("settings", "--hourly", "2", "--weekly=0")
	if r := last(); r.method != "PUT" || r.body != `{"snapshots":{"hourly":2,"weekly":0}}` {
		t.Errorf("settings put %+v", r)
	}
	if rc, _, e := run("settings", "--daily", "x"); rc != 1 || !strings.Contains(e, "number") {
		t.Errorf("settings bad number: %d %q", rc, e)
	}
	if rc, out, _ := run("alert", "list"); rc != 0 || !strings.Contains(out, "pool_degraded") {
		t.Errorf("alerts: %d %q", rc, out)
	}
	if rc, out, _ := run("restore"); rc != 0 || last().path != "/v1/restore" || !strings.Contains(out, "from tank") {
		t.Errorf("restore: %d %q", rc, out)
	}
	if rc, out, _ := run("status"); rc != 0 || !strings.Contains(out, "192.0.2.10") || !strings.Contains(out, "alerts    2") || !strings.Contains(out, "restored  from storage tank") ||
		!strings.Contains(out, "keel      registered = true, https://192.0.2.1:443 at 2026-10-03T10:00:00Z") {
		t.Errorf("status: %d %q", rc, out)
	}
	run("debug", "tick", "2026-10-05T00:00:00Z", "--only", "snapshots")
	if r := last(); r.path != "/v1/debug/tick" || r.body != `{"force":[],"only":["snapshots"],"time":"2026-10-05T00:00:00Z"}` {
		t.Errorf("debug tick %+v", r)
	}
}

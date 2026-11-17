package nas

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestArchiveSearch(t *testing.T) {
	s, _, f, ts := newTestServer(t)
	if c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`); c != 200 {
		t.Fatal(c, m)
	}
	s.mu.Lock()
	ns := s.st.Clone()
	ns.Backups = []BackupSource{{Name: "mail", Kind: "keelmail", Pool: "tank", Host: "mail.example.com", Port: 443}, {Name: "auth", Kind: "keelauth", Pool: "tank"}}
	s.commit(ns)
	s.mu.Unlock()
	f.WriteFile(s.bkKeyPath(), []byte(strings.Repeat("a", 64)), 0o600)
	ds := "tank/keelbackup/mail"
	// two copies: 11-10 (holds 11-01 … 11-10) and 11-17 (holds 10-18 … 11-17, but its 11-03 expired early)
	snaps := map[string]time.Time{"keel-d-2026-11-10": time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC), "keel-d-2026-11-17": time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)}
	mp := func(snap string) string {
		return archMountRoot + "/" + strings.NewReplacer("/", "_", "@", "_").Replace(ds+"@"+snap)
	}
	put := func(snap, day, id, subj, text string) {
		d := mp(snap) + "/keelmail/archive/" + strings.ReplaceAll(day, "-", "/")
		tm, _ := time.Parse(time.DateOnly, day)
		ix := f.Files[d+"/index.jsonl"]
		ix = append(ix, []byte(fmt.Sprintf(`{"id":%q,"t":%q,"dir":"in","from":"tanaka@partner.example","to":["alice@example.com"],"subject":%q,"size":10,"att":["a.pdf"]}`+"\n", id, tm.Add(3*time.Hour).Format(time.RFC3339), subj))...)
		f.Files[d+"/index.jsonl"] = ix
		f.Files[d+"/text.jsonl"] = append(f.Files[d+"/text.jsonl"], []byte(fmt.Sprintf(`{"id":%q,"text":%q}`+"\n", id, text))...)
		f.Files[d+"/"+id+".eml"] = []byte("Subject: " + subj + "\r\n\r\n" + text)
	}
	put("keel-d-2026-11-17", "2026-11-15", "20261115T030000Z-00000001", "見積依頼", "請求書 A-778")
	put("keel-d-2026-11-10", "2026-11-03", "20261103T030000Z-00000002", "打ち合わせ", "来週の件")
	put("keel-d-2026-11-17", "2026-11-09", "20261109T030000Z-00000003", "same", "x") // in both copies
	put("keel-d-2026-11-10", "2026-11-09", "20261109T030000Z-00000003", "same", "x")
	put("keel-d-2026-11-17", "2026-11-02", "20261102T030000Z-00000004", "fallback", "y") // not in its nearest copy (11-10)
	var mounts []string
	old := f.Handle
	f.Handle = func(line, stdin string) (string, error, bool) {
		switch {
		case strings.HasPrefix(line, "zfs list -H -p -t snapshot") && strings.HasSuffix(line, ds):
			var b strings.Builder
			for n, tm := range snaps {
				fmt.Fprintf(&b, "%s@%s\t%d\t1\n", ds, n, tm.Unix())
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "zfs list -H -o name tank/keelbackup"), strings.HasPrefix(line, "zfs get -H -o value keystatus"):
			return "available\n", nil, true
		case strings.HasPrefix(line, "mount -t zfs -o ro "):
			mounts = append(mounts, strings.Fields(line)[5])
			return "", nil, true
		case strings.HasPrefix(line, "mkdir -p "+archMountRoot), strings.HasPrefix(line, "umount "), strings.HasPrefix(line, "rmdir "):
			return "", nil, true
		}
		return old(line, stdin)
	}
	c, m := do(t, ts, "GET", "/v1/backups/mail/archive?from=2026-11-01&to=2026-11-20", "")
	if c != 200 || m["total"] != 4.0 {
		t.Fatalf("%d %v", c, m)
	}
	// 11-18 … 11-20 are in no copy yet (on the mail server)
	if miss := fmt.Sprint(m["missing"]); miss != "[2026-11-18 2026-11-19 2026-11-20]" {
		t.Errorf("missing %s", miss)
	}
	if len(mounts) > 2 {
		t.Errorf("mounted %v", mounts)
	}
	for q, n := range map[string]float64{"&subject=見積": 1, "&body=A-778": 1, "&body=来週": 1, "&att=*": 4, "&subject=fallback": 1, "&sender=nobody": 0} {
		if _, m := do(t, ts, "GET", "/v1/backups/mail/archive?from=2026-11-01&to=2026-11-17"+q, ""); m["total"] != n {
			t.Errorf("%s: %v", q, m["total"])
		}
	}
	// read one; download it
	c, m = do(t, ts, "GET", "/v1/backups/mail/archive/msg/20261103T030000Z-00000002?snap=keel-d-2026-11-10", "")
	if c != 200 || m["text"] != "来週の件" {
		t.Errorf("view %d %v", c, m)
	}
	_, m = do(t, ts, "GET", "/v1/backups/mail/archive/msg/20261103T030000Z-00000002?snap=keel-d-2026-11-10&eml=1", "")
	if b, _ := base64.StdEncoding.DecodeString(m["eml"].(string)); !strings.Contains(string(b), "打ち合わせ") {
		t.Errorf("eml %q", b)
	}
	if c, _ := do(t, ts, "GET", "/v1/backups/mail/archive/msg/..%2f..%2fx?snap=keel-d-2026-11-10", ""); c != 404 {
		t.Errorf("bad id: %d", c)
	}
	if c, _ := do(t, ts, "GET", "/v1/backups/auth/archive?from=2026-11-01&to=2026-11-02", ""); c != 400 {
		t.Errorf("keelauth: %d", c)
	}
	_, m = do(t, ts, "GET", "/v1/backups/archive-audit", "")
	if a := m["audit"].([]any); len(a) < 7 || a[0].(map[string]any)["action"] != "download" {
		t.Errorf("audit %v", a)
	}
}

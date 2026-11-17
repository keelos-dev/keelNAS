package nas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParsePullToken(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	h, p, s, pn, err := ParsePullToken("kbk1.mail.example.com:443.AbC-_123456789012345678901234567890123456789." + pin)
	if err != nil || h != "mail.example.com" || p != 443 || s != "AbC-_123456789012345678901234567890123456789" || pn != pin {
		t.Errorf("%q %d %q %q %v", h, p, s, pn, err)
	}
	for _, bad := range []string{"", "cstm1.x:1.a." + pin, "kbk1.host.secretsecretsecretsecret." + pin, "kbk1.h:99999.secretsecretsecretsecret." + pin, "kbk1.h:1.secretsecretsecretsecret.zz"} {
		if _, _, _, _, err := ParsePullToken(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}

func TestBackupPrunePlan(t *testing.T) {
	var l []bkSnap
	day := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	for i := 0; i < 400; i++ { // 400 days of dailies, an hourly beside each
		d := day.AddDate(0, 0, i)
		l = append(l, bkSnap{Name: "keel-h-" + d.Format("20060102T15Z"), Time: d.Add(-time.Minute)}, bkSnap{Name: "keel-d-" + d.Format("2006-01-02"), Time: d})
	}
	l = append(l, bkSnap{Name: "keel-h-last", Time: day.AddDate(0, 0, 400)})
	del := map[string]bool{}
	for _, n := range BackupPrunePlan(l, BackupKeep{Daily: 30, Monthly: 12}) {
		del[n] = true
	}
	var kept []string
	for _, x := range l {
		if !del[x.Name] {
			kept = append(kept, x.Name)
		}
	}
	// the newest (an hourly, the next fetch's base) + 30 dailies + the first of 12 months (one
	// of them, 2027-02-01, is among the 30 already)
	if len(kept) != 1+30+11 || kept[len(kept)-1] != "keel-h-last" || !slicesContain(kept, "keel-d-2026-03-01") || slicesContain(kept, "keel-d-2026-02-01") {
		t.Errorf("%d kept: %v", len(kept), kept)
	}
}

func slicesContain(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// fakeSource is a keelMail's /backup/v1 with snapshots; zfs on keelNAS is a map.
type fakeSource struct {
	mu     sync.Mutex
	snaps  []string
	sends  []string
	dones  []string
	secret string
}

func (f *fakeSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.secret || r.TLS == nil || r.TLS.ServerName != backupSNI {
		w.WriteHeader(401)
		return
	}
	switch r.URL.Path {
	case "/backup/v1/info":
		var l []map[string]any
		for i, n := range f.snaps {
			l = append(l, map[string]any{"name": n, "time": time.Unix(int64(i+1), 0).UTC()})
		}
		json.NewEncoder(w).Encode(map[string]any{"kind": "keelmail", "name": "Acme Mail", "host": "mail.example.com", "stream": "zfs", "snapshots": l})
	case "/backup/v1/send":
		f.sends = append(f.sends, r.URL.RawQuery)
		fmt.Fprintf(w, "STREAM %s", r.URL.RawQuery)
	case "/backup/v1/done":
		b, _ := io.ReadAll(r.Body)
		f.dones = append(f.dones, string(b))
	}
}

func TestBackupFetch(t *testing.T) {
	s, _, f, ts := newTestServer(t)
	s.syncJobs = true
	if c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`); c != 200 {
		t.Fatal(c, m)
	}
	src := &fakeSource{snaps: []string{"keel-d-2026-11-16", "keel-d-2026-11-17"}, secret: "Sekret-sekret-sekret-1"}
	srv := httptest.NewTLSServer(src)
	defer srv.Close()
	sum := sha256.Sum256(srv.Certificate().Raw)
	pin := hex.EncodeToString(sum[:])
	port := srv.Listener.Addr().(interface{ String() string }).String()
	token := "kbk1." + port + "." + src.secret + "." + pin

	// zfs on keelNAS: datasets and snapshots of the copies
	var zmu sync.Mutex
	ds := map[string]bool{}
	snaps := map[string][]string{}
	var recvArgs [][]string
	bkRecv = func(_ context.Context, stream io.Reader, args ...string) error {
		b, _ := io.ReadAll(stream)
		zmu.Lock()
		defer zmu.Unlock()
		recvArgs = append(recvArgs, args)
		d := args[len(args)-1]
		ds[d] = true
		to := strings.TrimPrefix(string(b), "STREAM ")
		to = to[strings.LastIndex(to, "=")+1:]
		snaps[d] = append(snaps[d], to)
		return nil
	}
	old := f.Handle
	f.Handle = func(line, stdin string) (string, error, bool) {
		zmu.Lock()
		defer zmu.Unlock()
		fl := strings.Fields(line)
		switch {
		case strings.HasPrefix(line, "zfs list -H -o name tank/keelbackup"):
			if ds[fl[len(fl)-1]] {
				return fl[len(fl)-1] + "\n", nil, true
			}
			return "", fmt.Errorf("does not exist"), true
		case strings.HasPrefix(line, "zfs create -o encryption=aes-256-gcm"):
			if !strings.Contains(line, "keylocation=file://") {
				return "", fmt.Errorf("no key"), true
			}
			ds[fl[len(fl)-1]] = true
			return "", nil, true
		case strings.HasPrefix(line, "zfs get -H -o value keystatus"):
			return "available\n", nil, true
		case strings.HasPrefix(line, "zfs list -H -p -t snapshot") && strings.Contains(line, "keelbackup"):
			d := fl[len(fl)-1]
			var b strings.Builder
			for i, n := range snaps[d] {
				fmt.Fprintf(&b, "%s@%s\t%d\t100\n", d, n, i+1)
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "zfs list -H -p -o used tank/keelbackup"):
			return "4096\n", nil, true
		case strings.HasPrefix(line, "zfs destroy -r tank/keelbackup/"):
			d := fl[len(fl)-1]
			if a, sn, ok := strings.Cut(d, "@"); ok {
				snaps[a] = deleteStr(snaps[a], sn)
			} else {
				delete(ds, d)
				delete(snaps, d)
			}
			return "", nil, true
		}
		return old(line, stdin)
	}

	// a bad token, a wrong pin
	if c, _ := do(t, ts, "POST", "/v1/backups", `{"token":"nope"}`); c != 400 {
		t.Errorf("bad token: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/backups", `{"token":"kbk1.`+port+`.`+src.secret+`.`+strings.Repeat("0", 64)+`"}`); c != 502 {
		t.Errorf("wrong pin: %d", c)
	}
	c, m := do(t, ts, "POST", "/v1/backups", `{"token":"`+token+`"}`)
	if c != 200 || m["name"] != "keelmail-127" || m["label"] != "Acme Mail" {
		t.Fatalf("add %d %v", c, m)
	}
	name := m["name"].(string)
	if c, _ := do(t, ts, "POST", "/v1/backups", `{"token":"`+token+`"}`); c != 409 {
		t.Errorf("twice: %d", c)
	}
	// the first fetch: full, into an encrypted base, not mounted, readonly
	if c, m := do(t, ts, "POST", "/v1/backups/"+name+"/fetch", ``); c != 200 {
		t.Fatal(c, m)
	}
	if len(src.sends) != 1 || src.sends[0] != "to=keel-d-2026-11-17" || len(src.dones) != 1 {
		t.Fatalf("sends %v dones %v", src.sends, src.dones)
	}
	if got := strings.Join(recvArgs[0], " "); got != "-u -x mountpoint -x encryption -o readonly=on tank/keelbackup/"+name {
		t.Errorf("receive %s", got)
	}
	if _, err := f.ReadFile(s.bkKeyPath()); err != nil || !strings.HasPrefix(s.bkKeyPath(), "/var/db/keelnas/") {
		t.Errorf("key %s: %v", s.bkKeyPath(), err)
	}
	// nothing new: no send; a new snapshot: incremental from the newest both have
	do(t, ts, "POST", "/v1/backups/"+name+"/fetch", ``)
	if len(src.sends) != 1 {
		t.Errorf("fetched without news: %v", src.sends)
	}
	src.snaps = append(src.snaps[1:], "keel-d-2026-11-18")
	do(t, ts, "POST", "/v1/backups/"+name+"/fetch", ``)
	if len(src.sends) != 2 || src.sends[1] != "from=keel-d-2026-11-17&to=keel-d-2026-11-18" {
		t.Errorf("incremental %v", src.sends)
	}
	c, m = do(t, ts, "GET", "/v1/backups", ``)
	l := m["sources"].([]any)[0].(map[string]any)
	if c != 200 || l["last_snap"] != "keel-d-2026-11-18" || l["secret"] != nil || m["key_set"] != true || len(l["snapshots"].([]any)) != 2 {
		t.Errorf("list %d %v", c, m)
	}
	// a failure: an alert, retried hourly; nothing for two days: error
	src.secret = "revoked-revoked-revoked"
	now := time.Now().Add(3 * time.Hour)
	s.Now = func() time.Time { return now }
	s.backupTick(time.Date(2026, 11, 19, DefaultBackupHour, 0, 0, 0, time.UTC))
	if a := s.backupAlerts(); len(a) != 1 || a[0].Code != "backup_failed" || !strings.Contains(a[0].Text, "refused") {
		t.Errorf("alerts %+v", a)
	}
	now = now.Add(49 * time.Hour)
	if a := s.backupAlerts(); len(a) != 1 || a[0].Code != "backup_stale" {
		t.Errorf("stale %+v", a)
	}
	// paused: no alert, no fetch; delete with the data
	do(t, ts, "PUT", "/v1/backups/"+name, `{"paused":true}`)
	if a := s.backupAlerts(); len(a) != 0 {
		t.Errorf("paused %+v", a)
	}
	if c, _ := do(t, ts, "DELETE", "/v1/backups/"+name+"?data=1", ``); c != 200 || ds["tank/keelbackup/"+name] {
		t.Errorf("delete %d %v", c, ds)
	}
	// the key: shown; a second one is refused
	if c, m := do(t, ts, "GET", "/v1/backups/key", ``); c != 200 || len(m["key"].(string)) != 64 {
		t.Errorf("key %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/backups/key", `{"key":"`+strings.Repeat("1", 64)+`"}`); c != 409 {
		t.Errorf("second key: %d", c)
	}
	_ = sort.Strings
}

func deleteStr(l []string, s string) []string {
	var out []string
	for _, x := range l {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

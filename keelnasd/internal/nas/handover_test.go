package nas

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandover(t *testing.T) {
	// the old keelNAS: a pool, a user, a share, a fixed LAN address
	s, w, _, ts := newTestServer(t)
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "pdbedit -s "+SmbConf+" -L -w" {
			return "alice:2001:X:HASH:[U ]:LCT-1:\n", nil, true
		}
		return "", nil, false
	}
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	do(t, ts, "POST", "/v1/users", `{"name":"alice","password":"x"}`)
	if c, m := do(t, ts, "POST", "/v1/shares", `{"name":"docs","pool":"tank","rw":["alice"]}`); c != 200 {
		t.Fatalf("share: %d %v", c, m)
	}
	s.mu.Lock()
	s.st.Key, s.st.Listen = "oldkey", ":8440"
	s.st.Network = &Network{Mode: "static", Address: "192.0.2.20/24", Gateway: "192.0.2.1", DNS: []string{"192.0.2.1"}}
	s.mu.Unlock()
	c, m := do(t, ts, "GET", "/v1/state/export", "")
	if c != 200 {
		t.Fatalf("export: %d %v", c, m)
	}
	b, _ := json.Marshal(m)
	var h Handover
	json.Unmarshal(b, &h)
	if h.State == nil || h.State.Key != "" || h.State.Listen != "" || h.Smbpasswd != "alice:2001:X:HASH:[U ]:LCT-1:\n" || h.Version != "test" {
		t.Fatalf("export: %s", b)
	}

	// the new system disk: fresh, the seed's key
	_, f2 := newWorld(t)
	s2, err := NewServer(f2, StatePath, "test2", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	s2.st.Key, s2.st.Listen = "seedkey", ":8440"
	ts2 := httptest.NewServer(s2.Handler())
	defer ts2.Close()
	if c, m := do(t, ts2, "POST", "/v1/state/import", `{"state":`); c != 400 {
		t.Errorf("bad JSON: %d %v", c, m)
	}
	if c, m := do(t, ts2, "POST", "/v1/state/import", string(b)); c != 200 {
		t.Fatalf("import: %d %v", c, m)
	}
	st := s2.State()
	if st.Key != "seedkey" || st.User("alice") == nil || st.Pool("tank") == nil || len(st.Shares) != 1 ||
		st.Network == nil || st.Network.Address != "192.0.2.20/24" {
		t.Errorf("imported %+v", st)
	}
	if !f2.Ran("pdbedit -s " + SmbConf + " -i smbpasswd:" + restorePDB) {
		t.Errorf("passwords not imported: %v", f2.Calls)
	}
	if s2.RestoredFrom() != "handover" {
		t.Errorf("restored_from %q", s2.RestoredFrom())
	}
	sb, _ := f2.ReadFile(StatePath)
	if !strings.Contains(string(sb), "seedkey") || strings.Contains(string(sb), "oldkey") {
		t.Errorf("state.json %s", sb)
	}
	// not fresh any more: refused
	if c, _ := do(t, ts2, "POST", "/v1/state/import", string(b)); c != 409 {
		t.Errorf("second import: %d", c)
	}
}

// The backup key is on the system disk only: it goes with the hand-over and is put back even
// when the new system already took its settings from a pool (import answers 409 then).
func TestHandoverBackupKey(t *testing.T) {
	s, _, _, ts := newTestServer(t)
	key := strings.Repeat("ab", 32)
	if err := s.Sys.WriteFile(s.bkKeyPath(), []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	_, m := do(t, ts, "GET", "/v1/state/export", "")
	if m["backup_key"] != key {
		t.Fatalf("export without the backup key: %v", m)
	}
	b, _ := json.Marshal(m)

	_, f2 := newWorld(t)
	s2, err := NewServer(f2, StatePath, "test2", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ts2 := httptest.NewServer(s2.Handler())
	defer ts2.Close()
	do(t, ts2, "POST", "/v1/users", `{"name":"bob","password":"x"}`) // not fresh: as if restored from a pool
	if c, _ := do(t, ts2, "POST", "/v1/state/import", string(b)); c != 409 {
		t.Fatalf("import into a restored system: %d", c)
	}
	if got, err := f2.ReadFile(s2.bkKeyPath()); err != nil || string(got) != key {
		t.Fatalf("backup key not put back: %q %v", got, err)
	}
	// a key there already is never replaced
	other := strings.Repeat("cd", 32)
	s2.importBackupKey(other)
	if got, _ := f2.ReadFile(s2.bkKeyPath()); string(got) != key {
		t.Errorf("key replaced: %q", got)
	}
}

// The key is lost (system disk replaced without it): the page is told, a wrong key is refused,
// the right one is taken, or the copies are discarded.
func TestBackupKeyLost(t *testing.T) {
	s, w, _, ts := newTestServer(t)
	do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`)
	good := strings.Repeat("ab", 32)
	destroyed := false
	w.extra = func(line string, a []string) (string, error, bool) {
		switch {
		case line == "zfs list -H -o name tank/"+BackupDS:
			if destroyed {
				return "", errors.New("does not exist"), true
			}
			return "tank/" + BackupDS + "\n", nil, true
		case strings.HasPrefix(line, "zfs load-key -n -L file://"):
			k, _ := s.Sys.ReadFile(strings.TrimPrefix(a[4], "file://"))
			if string(k) != good {
				return "", errors.New("incorrect key provided"), true
			}
			return "", nil, true
		case line == "zfs destroy -r tank/"+BackupDS:
			destroyed = true
			return "", nil, true
		}
		return "", nil, false
	}
	_, m := do(t, ts, "GET", "/v1/backups", "")
	if l, _ := m["key_lost"].([]any); len(l) != 1 || l[0] != "tank/"+BackupDS {
		t.Fatalf("key_lost %v", m["key_lost"])
	}
	if c, _ := do(t, ts, "POST", "/v1/backups/key", `{"key":"zz"}`); c != 400 {
		t.Errorf("bad key: %d", c)
	}
	if c, _ := do(t, ts, "POST", "/v1/backups/key/reset", `{}`); c != 400 {
		t.Errorf("reset without confirm: %d", c)
	}
	if c, m := do(t, ts, "POST", "/v1/backups/key/reset", `{"confirm":"discard"}`); c != 200 || !destroyed {
		t.Fatalf("reset: %d %v", c, m)
	}
	_, m = do(t, ts, "GET", "/v1/backups", "")
	if l, _ := m["key_lost"].([]any); len(l) != 0 {
		t.Errorf("still lost after the reset: %v", l)
	}
	destroyed = false // the copies are there again: a wrong key is refused, the right one taken
	if c, _ := do(t, ts, "POST", "/v1/backups/key", `{"key":"`+strings.Repeat("cd", 32)+`"}`); c != 400 {
		t.Errorf("wrong key: %d", c)
	}
	if _, err := s.Sys.ReadFile(s.bkKeyPath()); err == nil {
		t.Errorf("wrong key written")
	}
	if c, m := do(t, ts, "POST", "/v1/backups/key", `{"key":"`+good+`"}`); c != 200 {
		t.Fatalf("put key: %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/backups/key/reset", `{"confirm":"discard"}`); c != 409 {
		t.Errorf("reset with a key: %d", c)
	}
}

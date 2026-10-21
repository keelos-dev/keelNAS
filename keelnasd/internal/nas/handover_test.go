package nas

import (
	"encoding/json"
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

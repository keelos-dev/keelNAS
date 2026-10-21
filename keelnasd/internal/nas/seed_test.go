package nas

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"keelnas/internal/sys"
)

const testSeedKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

var testFP = strings.Repeat("AB:", 31) + "AB"

func seedJSON(keel []string, fp string) []byte {
	b, _ := json.Marshal(map[string]any{"keelnas_seed": 1, "key": testSeedKey, "port": 8440, "keel": keel, "fingerprint": fp})
	return b
}

// padded is a seed disk's first SeedSize bytes: the JSON, then zeros.
func padded(b []byte) []byte {
	out := make([]byte, SeedSize)
	copy(out, b)
	return out
}

func TestParseSeed(t *testing.T) {
	good := seedJSON([]string{"https://192.0.2.1:443", "https://192.0.2.2"}, testFP)
	for _, b := range [][]byte{good, padded(good)} {
		sd, err := ParseSeed(b)
		if err != nil || sd.Key != testSeedKey || sd.Port != 8440 || len(sd.Keel) != 2 || len(sd.fp) != 32 || SeedListen(sd) != ":8440" {
			t.Fatalf("good seed (%d bytes): %+v %v", len(b), sd, err)
		}
	}
	// Not a seed at all: silently skipped.
	garbage := make([]byte, SeedSize)
	for i := range garbage {
		garbage[i] = byte(i*7 + 3)
	}
	gpt := padded(append(make([]byte, 510), 0x55, 0xaa, 'E', 'F', 'I', ' ', 'P', 'A', 'R', 'T'))
	for name, b := range map[string][]byte{
		"zeros":     make([]byte, SeedSize),
		"empty":     nil,
		"garbage":   garbage,
		"gpt":       gpt,
		"not json":  padded([]byte(`{"keelnas_seed": 1, "key": `)),
		"truncated": padded(good[:len(good)/2]),
		"no magic":  padded([]byte(`{"key":"x","port":8440}`)),
		"array":     padded([]byte(`[1,2]`)),
		"magic str": padded([]byte(`{"keelnas_seed":"1"}`)),
	} {
		if _, err := ParseSeed(b); !errors.Is(err, ErrNotSeed) {
			t.Errorf("%s: %v, want ErrNotSeed", name, err)
		}
	}
	// A seed, but broken: an error that is logged.
	mk := func(edit func(m map[string]any)) []byte {
		var m map[string]any
		json.Unmarshal(good, &m)
		edit(m)
		b, _ := json.Marshal(m)
		return padded(b)
	}
	for name, b := range map[string][]byte{
		"wrong magic":    mk(func(m map[string]any) { m["keelnas_seed"] = 2 }),
		"short key":      mk(func(m map[string]any) { m["key"] = "0011" }),
		"key not hex":    mk(func(m map[string]any) { m["key"] = strings.Repeat("zz", 32) }),
		"no port":        mk(func(m map[string]any) { delete(m, "port") }),
		"port too big":   mk(func(m map[string]any) { m["port"] = 70000 }),
		"no keel":        mk(func(m map[string]any) { m["keel"] = []string{} }),
		"http keel":      mk(func(m map[string]any) { m["keel"] = []string{"http://192.0.2.1"} }),
		"short fp":       mk(func(m map[string]any) { m["fingerprint"] = "AB:CD" }),
		"fp not hex":     mk(func(m map[string]any) { m["fingerprint"] = strings.Repeat("XY:", 31) + "XY" }),
		"key wrong type": mk(func(m map[string]any) { m["key"] = 5 }),
	} {
		if _, err := ParseSeed(b); err == nil || errors.Is(err, ErrNotSeed) {
			t.Errorf("%s: %v, want a seed error", name, err)
		}
	}
}

func TestFingerprintFormat(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	s := FormatFingerprint(sum[:])
	if len(s) != 95 || strings.ToUpper(s) != s || strings.Count(s, ":") != 31 {
		t.Errorf("format %q", s)
	}
	b, err := ParseFingerprint(s)
	if err != nil || !bytes.Equal(b, sum[:]) {
		t.Errorf("parse back: %v", err)
	}
}

func TestAdoptSeed(t *testing.T) {
	sd, _ := ParseSeed(seedJSON([]string{"https://192.0.2.1"}, testFP))
	st := &State{Key: "old", Listen: ""}
	if !AdoptSeed(st, sd) || st.Key != testSeedKey || st.Listen != ":8440" {
		t.Fatalf("adopt: %+v", st)
	}
	if AdoptSeed(st, sd) {
		t.Error("second adoption reports a change")
	}
	st.Listen = "192.0.2.10:8440"
	if !AdoptSeed(st, sd) || st.Listen != ":8440" {
		t.Error("listen not changed")
	}
}

// seedWorld is the test world with a third disk vtbd2 holding a seed.
func seedWorld(t *testing.T, seed []byte) (*Server, *world, *sys.Fake, *httptest.Server) {
	s, w, f, ts := newTestServer(t)
	geom := readTestdata(t, "geom-disk-list.txt") + "\nGeom name: vtbd2\nProviders:\n1. Name: vtbd2\n   Mediasize: 1048576 (1.0M)\n   descr: (null)\n   ident: (null)\n"
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "geom disk list" {
			return geom, nil, true
		}
		return "", nil, false
	}
	f.Heads["vtbd0"] = padded([]byte("\xeb\x3c\x90BSD4.4  not a seed"))
	if seed != nil {
		f.Heads["vtbd2"] = padded(seed)
	}
	return s, w, f, ts
}

func TestUseSeedSavesOnlyChanges(t *testing.T) {
	s, _, f, _ := seedWorld(t, seedJSON([]string{"https://192.0.2.1"}, testFP))
	if sd := s.UseSeed(); sd == nil || s.SeedDisk() != "vtbd2" {
		t.Fatalf("no seed found (%q)", s.SeedDisk())
	}
	st, err := ParseState(f.Files[StatePath])
	if err != nil || st.Key != testSeedKey || st.Listen != ":8440" {
		t.Fatalf("saved state: %+v %v", st, err)
	}
	// Same seed at the next start: nothing to write.
	delete(f.Files, StatePath)
	if s.UseSeed() == nil {
		t.Fatal("seed lost")
	}
	if _, ok := f.Files[StatePath]; ok {
		t.Error("state written though nothing changed")
	}

	// No seed: nothing changes.
	s2, _, f2, _ := seedWorld(t, nil)
	before := string(f2.Files[StatePath])
	if s2.UseSeed() != nil || s2.SeedDisk() != "" || string(f2.Files[StatePath]) != before {
		t.Error("a seed was found where there is none")
	}
}

func TestSeedDiskHidden(t *testing.T) {
	s, _, f, ts := seedWorld(t, seedJSON([]string{"https://192.0.2.1"}, testFP))
	// Before the seed is known vtbd2 is an ordinary disk.
	_, m := do(t, ts, "GET", "/v1/disks", "")
	if !strings.Contains(jsonOf(m), `"vtbd2"`) {
		t.Fatalf("vtbd2 missing before: %s", jsonOf(m))
	}
	s.UseSeed()
	_, m = do(t, ts, "GET", "/v1/disks", "")
	if j := jsonOf(m); strings.Contains(j, `"vtbd2"`) || !strings.Contains(j, `"vtbd1"`) {
		t.Errorf("disks with the seed: %s", j)
	}
	f.Reset()
	if c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd2"]}`); c != 409 || !strings.Contains(m["error"].(string), "seed") {
		t.Errorf("storage on the seed disk: %d %v", c, m)
	}
	if c, _ := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1","vtbd2"],"protect":true}`); c != 409 {
		t.Errorf("mirror with the seed disk: %d", c)
	}
	if f.Ran("zpool create") {
		t.Fatal("zpool create ran with the seed disk")
	}
	if c, _ := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`); c != 200 {
		t.Fatalf("storage on vtbd1: %d", c)
	}
	if c, m := do(t, ts, "POST", "/v1/storage/tank/protect", `{"disk":"vtbd2"}`); c != 409 || !strings.Contains(m["error"].(string), "seed") {
		t.Errorf("protect with the seed disk: %d %v", c, m)
	}
	if f.Ran("zpool attach") {
		t.Error("zpool attach ran with the seed disk")
	}
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

// keelStandIn is keel's /nas/register on TLS; it records what arrives.
type keelStandIn struct {
	ts   *httptest.Server
	mu   sync.Mutex
	reqs []recorded
	code int
}

type recorded struct {
	path, auth string
	body       RegisterBody
}

func newKeel(t *testing.T) *keelStandIn {
	k := &keelStandIn{code: 200}
	k.ts = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b RegisterBody
		json.NewDecoder(r.Body).Decode(&b)
		k.mu.Lock()
		k.reqs = append(k.reqs, recorded{r.Method + " " + r.URL.Path, r.Header.Get("Authorization"), b})
		code := k.code
		k.mu.Unlock()
		w.WriteHeader(code)
		w.Write([]byte(`{"ok":true}`))
	}))
	k.ts.Config.ErrorLog = log.New(io.Discard, "", 0) // failed handshakes
	k.ts.StartTLS()
	t.Cleanup(k.ts.Close)
	return k
}

func (k *keelStandIn) fp() string {
	sum := sha256.Sum256(k.ts.Certificate().Raw)
	return FormatFingerprint(sum[:])
}

func (k *keelStandIn) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.reqs)
}

func testBody() RegisterBody {
	return RegisterBody{Addresses: []string{"192.0.2.10"}, Port: 8440, Version: "v-test", Hostname: "keelnas"}
}

func newReg(t *testing.T, keel []string, fp string, lg *log.Logger) *Registrar {
	sd, err := ParseSeed(seedJSON(keel, fp))
	if err != nil {
		t.Fatal(err)
	}
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	return NewRegistrar(sd, testBody, lg)
}

func TestRegisterRightFingerprint(t *testing.T) {
	k := newKeel(t)
	r := newReg(t, []string{k.ts.URL}, k.fp(), nil)
	r.Now = func() time.Time { return utc("2026-10-03T10:00:00Z") }
	if err := r.Attempt(); err != nil {
		t.Fatal(err)
	}
	if k.count() != 1 {
		t.Fatalf("%d requests", k.count())
	}
	got := k.reqs[0]
	if got.path != "POST /nas/register" || got.auth != "Bearer "+testSeedKey || jsonOf(got.body) != jsonOf(testBody()) {
		t.Errorf("request %+v", got)
	}
	if st := r.Status(); !st.Registered || st.URL != k.ts.URL || st.Time != "2026-10-03T10:00:00Z" || st.Error != "" {
		t.Errorf("status %+v", st)
	}
}

func TestRegisterWrongFingerprint(t *testing.T) {
	k := newKeel(t)
	sum := sha256.Sum256([]byte("another certificate"))
	r := newReg(t, []string{k.ts.URL}, FormatFingerprint(sum[:]), nil)
	err := r.Attempt()
	if err == nil || !strings.Contains(err.Error(), "not the one in the seed") {
		t.Errorf("err %v", err)
	}
	if k.count() != 0 {
		t.Fatalf("%d requests reached keel with the wrong certificate", k.count())
	}
	if st := r.Status(); st.Registered || st.Error == "" {
		t.Errorf("status %+v", st)
	}
}

func TestRegisterSecondURL(t *testing.T) {
	down := newKeel(t)
	downURL := down.ts.URL
	down.ts.Close()
	k := newKeel(t)
	r := newReg(t, []string{downURL, k.ts.URL}, k.fp(), nil)
	if err := r.Attempt(); err != nil {
		t.Fatal(err)
	}
	if st := r.Status(); !st.Registered || st.URL != k.ts.URL || k.count() != 1 {
		t.Errorf("status %+v, %d requests", st, k.count())
	}
	// A refusal (not 200) also moves on; when every address fails the status says why.
	k.mu.Lock()
	k.code = 401
	k.mu.Unlock()
	if err := r.Attempt(); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err %v", err)
	}
	if st := r.Status(); st.Registered || !strings.Contains(st.Error, "401") || st.URL != k.ts.URL {
		t.Errorf("status after 401 %+v", st)
	}
}

func TestRegisterLogging(t *testing.T) {
	k := newKeel(t)
	var buf bytes.Buffer
	r := newReg(t, []string{k.ts.URL}, k.fp(), log.New(&buf, "", 0))
	now := utc("2026-10-03T10:00:00Z")
	r.Now = func() time.Time { return now }
	lines := func() int { return strings.Count(buf.String(), "\n") }
	r.Attempt()
	r.Attempt()
	if lines() != 1 || !strings.Contains(buf.String(), "registered with keel") {
		t.Fatalf("success logged: %q", buf.String())
	}
	k.mu.Lock()
	k.code = 500
	k.mu.Unlock()
	for i := 0; i < 10; i++ { // ten failures within nine minutes: one line
		now = now.Add(time.Minute)
		r.Attempt()
	}
	if lines() != 2 {
		t.Fatalf("failures logged: %q", buf.String())
	}
	now = now.Add(time.Minute) // 11 minutes after the first failure
	r.Attempt()
	if lines() != 3 {
		t.Errorf("failure after 10 minutes not logged: %q", buf.String())
	}
}

func TestRegisterLoop(t *testing.T) {
	k := newKeel(t)
	r := newReg(t, []string{k.ts.URL}, k.fp(), nil)
	ticks := make(chan time.Time)
	waits := make(chan time.Duration, 10)
	r.After = func(d time.Duration) <-chan time.Time { waits <- d; return ticks }
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { r.Run(stop); close(done) }()
	for i := 1; i <= 3; i++ {
		if d := <-waits; d != RegisterInterval {
			t.Fatalf("waits %v", d)
		}
		if k.count() != i {
			t.Fatalf("after %d waits: %d requests", i, k.count())
		}
		if i < 3 {
			ticks <- time.Time{}
		}
	}
	close(stop)
	<-done
}

func TestStatusKeelField(t *testing.T) {
	s, _, _, ts := newTestServer(t)
	_, m := do(t, ts, "GET", "/v1/status", "")
	if _, ok := m["keel"]; ok {
		t.Errorf("keel in status without a seed: %v", m["keel"])
	}
	k := newKeel(t)
	r := newReg(t, []string{k.ts.URL}, k.fp(), nil)
	s.hmu.Lock()
	s.reg = r
	s.hmu.Unlock()
	_, m = do(t, ts, "GET", "/v1/status", "")
	if j := jsonOf(m["keel"]); j != `{"error":"","registered":false,"time":"","url":""}` {
		t.Errorf("keel before an attempt: %s", j)
	}
	r.Attempt()
	_, m = do(t, ts, "GET", "/v1/status", "")
	kk, _ := m["keel"].(map[string]any)
	if kk["registered"] != true || kk["url"] != k.ts.URL || kk["time"] == "" {
		t.Errorf("keel after an attempt: %v", m["keel"])
	}
}

// The body is built from the system: addresses without the loopback, the seed's port.
func TestRegisterBody(t *testing.T) {
	s, w, _, _ := newTestServer(t)
	w.extra = func(line string, a []string) (string, error, bool) {
		if line == "ifconfig -a inet" {
			return "vtnet0: flags=8843<UP>\n\tinet 192.0.2.10 netmask 0xffffff00\nlo0: flags=8049<UP,LOOPBACK>\n\tinet 127.0.0.1 netmask 0xff000000\n", nil, true
		}
		return "", nil, false
	}
	b := s.registerBody(8440)
	if jsonOf(b) != `{"addresses":["192.0.2.10"],"port":8440,"version":"test","hostname":"keelnas"}` {
		t.Errorf("body %s", jsonOf(b))
	}
}

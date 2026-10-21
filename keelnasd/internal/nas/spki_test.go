package nas

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// keel re-issues its certificate with the same key when its name or address changes (keelOS
// web.ExtendCert) and serves the old one until keel serve starts again: the seed's spki pins the
// key, so both certificates are keel.

func spkiCert(t *testing.T, key *ecdsa.PrivateKey, name string) tls.Certificate {
	t.Helper()
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{name},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func spkiKey(t *testing.T) *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func certFP(c tls.Certificate) string { s := sha256.Sum256(c.Certificate[0]); return FormatFingerprint(s[:]) }

func keySPKI(t *testing.T, c tls.Certificate) string {
	l, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(l.RawSubjectPublicKeyInfo)
	return FormatFingerprint(s[:])
}

// spkiKeel serves c and counts the requests that reach it.
func spkiKeel(t *testing.T, c tls.Certificate) (*httptest.Server, *int32) {
	var n int32
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.Write([]byte(`{"ok":true}`))
	}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{c}}
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts, &n
}

func spkiReg(t *testing.T, url, fp, spki string) *Registrar {
	t.Helper()
	m := map[string]any{"keelnas_seed": 1, "key": strings.Repeat("ab", 32), "port": 8440, "keel": []string{url}, "fingerprint": fp}
	if spki != "" {
		m["spki"] = spki
	}
	b, _ := json.Marshal(m)
	sd, err := ParseSeed(b)
	if err != nil {
		t.Fatal(err)
	}
	return NewRegistrar(sd, func() RegisterBody { return RegisterBody{Port: 8440} }, log.New(io.Discard, "", 0))
}

func TestSPKIPinSurvivesReissue(t *testing.T) {
	key := spkiKey(t)
	served, reissued := spkiCert(t, key, "keelos"), spkiCert(t, key, "nas2")
	// the seed names the re-issued certificate (keel wrote it from the file) and its key;
	// keel still serves the one it loaded at its start
	ts, n := spkiKeel(t, served)
	if err := spkiReg(t, ts.URL, certFP(reissued), keySPKI(t, reissued)).Attempt(); err != nil || atomic.LoadInt32(n) != 1 {
		t.Fatalf("same key, other certificate: %v, %d requests", err, atomic.LoadInt32(n))
	}
}

func TestSPKIPinOtherKey(t *testing.T) {
	served := spkiCert(t, spkiKey(t), "keelos")
	mine := spkiCert(t, spkiKey(t), "keelos")
	ts, n := spkiKeel(t, served)
	// fingerprint matches the served certificate, spki does not: spki decides
	err := spkiReg(t, ts.URL, certFP(served), keySPKI(t, mine)).Attempt()
	if err == nil || !strings.Contains(err.Error(), "not the one in the seed") {
		t.Fatalf("other key: %v", err)
	}
	if atomic.LoadInt32(n) != 0 {
		t.Fatalf("%d requests reached a server with another key", atomic.LoadInt32(n))
	}
}

func TestOldSeedPinsCertificate(t *testing.T) {
	key := spkiKey(t)
	served, reissued := spkiCert(t, key, "keelos"), spkiCert(t, key, "nas2")
	ts, n := spkiKeel(t, served)
	if err := spkiReg(t, ts.URL, certFP(served), "").Attempt(); err != nil || atomic.LoadInt32(n) != 1 {
		t.Fatalf("old seed, served certificate: %v", err)
	}
	// without spki only that certificate: the re-issued one (same key) is refused
	err := spkiReg(t, ts.URL, certFP(reissued), "").Attempt()
	if err == nil || atomic.LoadInt32(n) != 1 {
		t.Fatalf("old seed, other certificate: %v, %d requests", err, atomic.LoadInt32(n))
	}
}

func TestSeedBadSPKI(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"keelnas_seed": 1, "key": strings.Repeat("ab", 32), "port": 8440,
		"keel": []string{"https://192.0.2.1"}, "fingerprint": strings.Repeat("AB:", 31) + "AB", "spki": "AB:CD"})
	if _, err := ParseSeed(b); err == nil || !strings.Contains(err.Error(), "spki") {
		t.Fatalf("short spki: %v", err)
	}
}

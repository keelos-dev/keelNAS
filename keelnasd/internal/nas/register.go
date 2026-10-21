package nas

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Registration (docs/04 section 10.2): with a seed, keelnasd tells keel where it is, at the
// start and every minute, so keel learns the address DHCP gave the NAS.

// RegisterInterval is the time between two registrations.
const RegisterInterval = 60 * time.Second

// failLogEvery limits how often a failing registration is logged.
const failLogEvery = 10 * time.Minute

// KeelStatus is "keel" in GET /v1/status. URL and Time are those of the last success; Error is
// the reason of the last failure ("" after a success).
type KeelStatus struct {
	Registered bool   `json:"registered"`
	URL        string `json:"url"`
	Time       string `json:"time"`
	Error      string `json:"error"`
}

// RegisterBody is what POST <keel>/nas/register carries.
type RegisterBody struct {
	Addresses []string `json:"addresses"`
	Port      int      `json:"port"`
	Version   string   `json:"version"`
	Hostname  string   `json:"hostname"`
}

// Registrar registers with keel.
type Registrar struct {
	Seed *Seed
	Body func() RegisterBody
	Log  *log.Logger
	// The clock and the wait between attempts; nil = time.Now and time.After. Tests set them.
	Now      func() time.Time
	After    func(time.Duration) <-chan time.Time
	Interval time.Duration // 0 = RegisterInterval

	client *http.Client

	mu          sync.Mutex
	st          KeelStatus
	everOK      bool
	lastFailLog time.Time
}

// NewRegistrar makes a registrar whose TLS connections accept only the seed's key (spki) or,
// with an older seed, only the seed's certificate.
func NewRegistrar(sd *Seed, body func() RegisterBody, lg *log.Logger) *Registrar {
	tr := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{
			// The chain is not verified (keel's certificate is self-signed); instead the SHA-256
			// of the leaf's public key must be the seed's spki (or, for a seed without one, the
			// leaf's SHA-256 its fingerprint). VerifyConnection runs inside the handshake, so with
			// another key (certificate) not a byte of the request is sent.
			InsecureSkipVerify: true,
			VerifyConnection:   pinKeel(sd),
		},
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &Registrar{Seed: sd, Body: body, Log: lg, client: &http.Client{
		Transport: tr,
		Timeout:   20 * time.Second,
		// A redirect would carry the key elsewhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// pinKeel: the seed's spki when it has one, else its certificate fingerprint.
func pinKeel(sd *Seed) func(tls.ConnectionState) error {
	if sd.spki != nil {
		return pinSPKI(sd.spki)
	}
	return pinFingerprint(sd.fp)
}

// pinSPKI accepts a leaf whose SubjectPublicKeyInfo has this SHA-256: any certificate keel
// issued with its key (a new name or address re-issues it with the same key).
func pinSPKI(want []byte) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("keel sent no certificate")
		}
		sum := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
		if subtle.ConstantTimeCompare(sum[:], want) != 1 {
			return fmt.Errorf("keel's key %s is not the one in the seed", FormatFingerprint(sum[:]))
		}
		return nil
	}
}

func pinFingerprint(want []byte) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("keel sent no certificate")
		}
		sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(sum[:], want) != 1 {
			return fmt.Errorf("keel's certificate %s is not the one in the seed", FormatFingerprint(sum[:]))
		}
		return nil
	}
}

func (r *Registrar) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// post sends one registration to one keel address.
func (r *Registrar) post(base string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/nas/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.Seed.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s %s", base, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// Attempt registers once: each keel address in order until one answers 200.
func (r *Registrar) Attempt() error {
	body, _ := json.Marshal(r.Body())
	var errs []string
	ok := ""
	for _, base := range r.Seed.Keel {
		if err := r.post(base, body); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		ok = base
		break
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if ok != "" {
		r.st = KeelStatus{Registered: true, URL: ok, Time: now.Format(time.RFC3339)}
		if !r.everOK {
			r.everOK = true
			r.Log.Printf("registered with keel at %s", ok)
		}
		return nil
	}
	err := errors.New(strings.Join(errs, "; "))
	r.st.Registered = false
	r.st.Error = err.Error()
	if r.lastFailLog.IsZero() || now.Sub(r.lastFailLog) >= failLogEvery {
		r.lastFailLog = now
		r.Log.Printf("registration with keel failed (logged at most every 10 minutes): %v", err)
	}
	return err
}

// Run registers now and then every Interval until stop is closed (nil = never).
func (r *Registrar) Run(stop <-chan struct{}) {
	after, every := r.After, r.Interval
	if after == nil {
		after = time.After
	}
	if every == 0 {
		every = RegisterInterval
	}
	for {
		r.Attempt()
		select {
		case <-stop:
			return
		case <-after(every):
		}
	}
}

// Status is the registration's state for GET /v1/status.
func (r *Registrar) Status() KeelStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st
}

// registerBody is what this NAS says about itself.
func (s *Server) registerBody(port int) RegisterBody {
	b := RegisterBody{Addresses: []string{}, Port: port, Version: s.versionString(), Hostname: s.hostname()}
	if out, err := s.Sys.Run("", "ifconfig", "-a", "inet"); err == nil {
		b.Addresses = ParseAddresses(out)
	}
	return b
}

// StartRegistration starts the registration loop for a seed.
func (s *Server) StartRegistration(sd *Seed) {
	r := NewRegistrar(sd, func() RegisterBody { return s.registerBody(sd.Port) }, s.Log)
	s.hmu.Lock()
	s.reg = r
	s.hmu.Unlock()
	go r.Run(nil)
}

func (s *Server) keelStatus() *KeelStatus {
	s.hmu.Lock()
	r := s.reg
	s.hmu.Unlock()
	if r == nil {
		return nil
	}
	st := r.Status()
	return &st
}

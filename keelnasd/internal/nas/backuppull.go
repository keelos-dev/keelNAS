package nas

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Fetching backups (docs/08-backup-pull.md): keelNAS fetches the daily ZFS snapshots of other
// keel systems (keelMail, keelAuth, …) with their read-only pull token and keeps them in the
// encrypted dataset <pool>/keelbackup/<name>, by its own rules. The source cannot delete them.

const (
	BackupDS       = "keelbackup"
	backupSNI      = "keelbackup.invalid"
	backupKeyFile  = "backup.key" // next to state.json, on the system disk only (never in the pools' copies)
	backupStatFile = "backup-status.json"
	backupStale    = 48 * time.Hour
)

// BackupSource is one system keelNAS fetches from.
type BackupSource struct {
	Name   string `json:"name"`  // keelNAS's name for it: the dataset's last part
	Kind   string `json:"kind"`  // keelmail, keelauth, …
	Label  string `json:"label"` // what the source calls itself
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Secret string `json:"secret"` // the token's secret part (read-only on the source)
	Pin    string `json:"pin"`    // sha256 hex of the source's certificate
	Pool   string `json:"pool"`
	Hour   int    `json:"hour"` // UTC
	Paused bool   `json:"paused,omitempty"`
}

// BackupKeep: on keelNAS, the newest Daily days and the first snapshot of each of the last
// Monthly months.
type BackupKeep struct {
	Daily   int `json:"daily"`
	Monthly int `json:"monthly"`
}

var DefaultBackupKeep = BackupKeep{Daily: 30, Monthly: 12}

// DefaultBackupHour: 17:00 UTC is 02:00 in Japan (the user's office; keelNAS has no time zone).
const DefaultBackupHour = 17

func (st *State) backupKeep() BackupKeep {
	if st.BackupKeep == nil {
		return DefaultBackupKeep
	}
	return *st.BackupKeep
}

func (st *State) backupSource(name string) *BackupSource {
	for i := range st.Backups {
		if st.Backups[i].Name == name {
			return &st.Backups[i]
		}
	}
	return nil
}

// bkStatus is what the last fetch of a source did (on the system disk, not in state.json).
type bkStatus struct {
	LastAt    string `json:"last_at,omitempty"` // the last good fetch
	LastSnap  string `json:"last_snap,omitempty"`
	LastBytes int64  `json:"last_bytes,omitempty"`
	TriedAt   string `json:"tried_at,omitempty"`
	Error     string `json:"error,omitempty"`     // of the last try; "" after a good one
	LabelNow  string `json:"label_now,omitempty"` // what the source called itself at the last fetch
}

type bkRuntime struct {
	mu      sync.Mutex
	loaded  bool
	status  map[string]*bkStatus
	running map[string]bool
	moved   map[string]*atomic.Int64 // bytes of the running fetch so far
}

// ---- the token and the source's interface ----

var (
	pinRE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	secRE    = regexp.MustCompile(`^[A-Za-z0-9_-]{20,128}$`)
	bkNameRE = regexp.MustCompile(`[^a-z0-9-]+`)
)

// ParsePullToken reads kbk1.<host>:<port>.<secret>.<sha256 of the certificate>.
func ParsePullToken(tok string) (host string, port int, secret, pin string, err error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(tok), "kbk1.")
	if !ok {
		return "", 0, "", "", errors.New("not a pull token (it starts with kbk1.)")
	}
	i := strings.LastIndexByte(rest, '.')
	if i < 0 {
		return "", 0, "", "", errors.New("pull token: incomplete")
	}
	pin, rest = rest[i+1:], rest[:i]
	j := strings.LastIndexByte(rest, '.')
	if j < 0 {
		return "", 0, "", "", errors.New("pull token: incomplete")
	}
	secret, rest = rest[j+1:], rest[:j]
	h, p, err := net.SplitHostPort(rest)
	if err != nil {
		return "", 0, "", "", errors.New("pull token: no host:port")
	}
	port, _ = strconv.Atoi(p)
	if !pinRE.MatchString(pin) || !secRE.MatchString(secret) || h == "" || port < 1 || port > 65535 {
		return "", 0, "", "", errors.New("pull token: damaged")
	}
	return h, port, secret, pin, nil
}

// bkClient talks to a source: TLS server name backupSNI, the certificate must be the pinned one.
func bkClient(pin string) *http.Client {
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		TLSClientConfig: &tls.Config{ServerName: backupSNI, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12,
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("no certificate")
				}
				sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
				if hex.EncodeToString(sum[:]) != pin {
					return errors.New("the certificate is not the one in the pull token")
				}
				return nil
			}},
	}
	return &http.Client{Transport: tr}
}

type bkInfo struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Version   string `json:"version"`
	Stream    string `json:"stream"`
	Snapshots []struct {
		Name string    `json:"name"`
		Time time.Time `json:"time"`
	} `json:"snapshots"`
}

func bkRequest(ctx context.Context, src BackupSource, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "https://"+net.JoinHostPort(src.Host, strconv.Itoa(src.Port))+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+src.Secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := bkClient(src.Pin).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		if resp.StatusCode == 401 {
			return nil, errors.New("the source refused the pull token (revoked, or this address is not allowed)")
		}
		return nil, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

func bkGetInfo(ctx context.Context, src BackupSource) (*bkInfo, error) {
	resp, err := bkRequest(ctx, src, "GET", "/backup/v1/info", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var in bkInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&in); err != nil {
		return nil, fmt.Errorf("info: %v", err)
	}
	if in.Stream != "zfs" {
		return nil, fmt.Errorf("the source sends %q backups: keelNAS takes zfs only", in.Stream)
	}
	return &in, nil
}

// bkRecv runs zfs receive with the stream as its input (tests replace it).
var bkRecv = func(ctx context.Context, stream io.Reader, args ...string) error {
	cmd := exec.CommandContext(ctx, "zfs", append([]string{"receive"}, args...)...)
	cmd.Stdin = stream
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs receive: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---- the key and the datasets ----

func (s *Server) bkKeyPath() string { return filepath.Join(filepath.Dir(s.StatePath), backupKeyFile) }

// bkEnsureBase makes <pool>/keelbackup (encrypted with the key on the system disk) and loads its key.
func (s *Server) bkEnsureBase(pool string) error {
	base := pool + "/" + BackupDS
	kp := s.bkKeyPath()
	if _, err := s.Sys.ReadFile(kp); err != nil {
		if _, e := s.Sys.Run("", "zfs", "list", "-H", "-o", "name", base); e == nil {
			return fmt.Errorf("%s exists but the backup key is missing on this keelNAS: enter the saved key (Backups → key)", base)
		}
		b := make([]byte, 32)
		rand.Read(b)
		if err := s.Sys.WriteFile(kp, []byte(hex.EncodeToString(b)), 0o600); err != nil {
			return err
		}
		s.Log.Printf("backup: made the backup key %s", kp)
	}
	if _, err := s.Sys.Run("", "zfs", "list", "-H", "-o", "name", base); err != nil {
		if _, err := s.run("", "zfs", "create", "-o", "encryption=aes-256-gcm", "-o", "keyformat=hex",
			"-o", "keylocation=file://"+kp, "-o", "canmount=off", "-o", "mountpoint=none", base); err != nil {
			return fmt.Errorf("make %s: %v", base, err)
		}
		return nil
	}
	ks, _ := s.Sys.Run("", "zfs", "get", "-H", "-o", "value", "keystatus", base)
	if strings.TrimSpace(ks) != "available" {
		if _, err := s.run("", "zfs", "load-key", base); err != nil {
			return fmt.Errorf("load the backup key of %s: %v", base, err)
		}
	}
	return nil
}

type bkSnap struct {
	Name string    `json:"name"`
	Time time.Time `json:"time"`
	Used int64     `json:"used"`
}

// bkSnaps lists the snapshots of a source's copy, oldest first (nil when there is none yet).
func (s *Server) bkSnaps(ds string) []bkSnap {
	out, err := s.Sys.Run("", "zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,creation,used", "-d", "1", ds)
	if err != nil {
		return nil
	}
	var l []bkSnap
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		_, n, ok := strings.Cut(f[0], "@")
		if !ok {
			continue
		}
		c, _ := strconv.ParseInt(f[1], 10, 64)
		u, _ := strconv.ParseInt(f[2], 10, 64)
		l = append(l, bkSnap{n, time.Unix(c, 0).UTC(), u})
	}
	sort.SliceStable(l, func(i, j int) bool { return l[i].Time.Before(l[j].Time) })
	return l
}

// BackupPrunePlan: what to delete from a copy's snapshots (oldest first). The newest stays
// (the next fetch builds on it); hourly ones go; of the rest the newest k.Daily stay and the
// first one of each of the newest k.Monthly months.
func BackupPrunePlan(l []bkSnap, k BackupKeep) []string {
	if len(l) == 0 {
		return nil
	}
	keep := map[string]bool{l[len(l)-1].Name: true}
	var days []bkSnap
	for _, x := range l {
		if !strings.HasPrefix(x.Name, "keel-h-") {
			days = append(days, x)
		}
	}
	for i := len(days) - 1; i >= 0 && i >= len(days)-k.Daily; i-- {
		keep[days[i].Name] = true
	}
	months := map[string]bool{}
	var firsts []string
	for _, x := range days {
		m := x.Time.Format("2006-01")
		if !months[m] {
			months[m] = true
			firsts = append(firsts, x.Name)
		}
	}
	for i := len(firsts) - 1; i >= 0 && i >= len(firsts)-k.Monthly; i-- {
		keep[firsts[i]] = true
	}
	var out []string
	for _, x := range l {
		if !keep[x.Name] {
			out = append(out, x.Name)
		}
	}
	return out
}

// ---- fetching ----

func (s *Server) bkLoad() {
	if s.bk.loaded {
		return
	}
	s.bk.loaded, s.bk.status, s.bk.running, s.bk.moved = true, map[string]*bkStatus{}, map[string]bool{}, map[string]*atomic.Int64{}
	if b, err := s.Sys.ReadFile(filepath.Join(filepath.Dir(s.StatePath), backupStatFile)); err == nil {
		json.Unmarshal(b, &s.bk.status)
	}
}

// bkSet changes a source's status and writes the status file.
func (s *Server) bkSet(name string, f func(*bkStatus)) {
	s.bk.mu.Lock()
	defer s.bk.mu.Unlock()
	s.bkLoad()
	st := s.bk.status[name]
	if st == nil {
		st = &bkStatus{}
		s.bk.status[name] = st
	}
	f(st)
	b, _ := json.Marshal(s.bk.status)
	s.Sys.WriteFile(filepath.Join(filepath.Dir(s.StatePath), backupStatFile), b, 0o600)
}

func (s *Server) bkGet(name string) (bkStatus, bool) {
	s.bk.mu.Lock()
	defer s.bk.mu.Unlock()
	s.bkLoad()
	st := s.bk.status[name]
	if st == nil {
		return bkStatus{}, s.bk.running[name]
	}
	return *st, s.bk.running[name]
}

// countingReader counts what passed.
type countingReader struct {
	r io.Reader
	n int64
	m *atomic.Int64 // the page's progress (may be nil)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.m != nil {
		c.m.Store(c.n)
	}
	return n, err
}

// bkFetch fetches the newest snapshot of a source (full the first time, then incremental).
func (s *Server) bkFetch(ctx context.Context, src BackupSource, keep BackupKeep) (string, int64, error) {
	s.bk.mu.Lock()
	moved := s.bk.moved[src.Name]
	s.bk.mu.Unlock()
	if err := s.bkEnsureBase(src.Pool); err != nil {
		return "", 0, err
	}
	in, err := bkGetInfo(ctx, src)
	if err != nil {
		return "", 0, err
	}
	if in.Name != "" && in.Name != src.Label {
		s.bkSet(src.Name, func(b *bkStatus) { b.LabelNow = in.Name })
	}
	if len(in.Snapshots) == 0 {
		return "", 0, errors.New("the source has no daily snapshot yet")
	}
	to := in.Snapshots[len(in.Snapshots)-1].Name
	ds := src.Pool + "/" + BackupDS + "/" + src.Name
	have := s.bkSnaps(ds)
	from := ""
	if len(have) > 0 {
		if have[len(have)-1].Name == to {
			return to, 0, nil // nothing new
		}
		offered := map[string]bool{}
		for _, x := range in.Snapshots {
			offered[x.Name] = true
		}
		for i := len(have) - 1; i >= 0 && from == ""; i-- {
			if offered[have[i].Name] {
				from = have[i].Name
			}
		}
		if from == "" {
			return "", 0, fmt.Errorf("the source no longer has any snapshot this copy has (newest here %s): fetch again in full (delete the copy or rename it)", have[len(have)-1].Name)
		}
	}
	q := "/backup/v1/send?to=" + to
	if from != "" {
		q = "/backup/v1/send?from=" + from + "&to=" + to
	}
	resp, err := bkRequest(ctx, src, "GET", q, nil)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	cr := &countingReader{r: resp.Body, m: moved}
	// -u: not mounted; -x mountpoint: the copy inherits keelbackup's "none" (a copied /var/log
	// must never be mounted over keelNAS's own); -x encryption: a replication stream carries the
	// source's encryption=off, which would make the copy unencrypted under the encrypted
	// keelbackup (seen on real ZFS, 2026-11-17); readonly.
	args := []string{"-u", "-x", "mountpoint", "-x", "encryption", "-o", "readonly=on", ds}
	if err := bkRecv(ctx, cr, args...); err != nil {
		return "", cr.n, err
	}
	// tell the source (its overview shows the last fetch)
	if r, err := bkRequest(ctx, src, "POST", "/backup/v1/done", strings.NewReader(`{"to":"`+to+`"}`)); err == nil {
		r.Body.Close()
	} else {
		s.Log.Printf("backup %s: done not taken: %v", src.Name, err)
	}
	for _, n := range BackupPrunePlan(s.bkSnaps(ds), keep) {
		if _, err := s.run("", "zfs", "destroy", "-r", ds+"@"+n); err != nil {
			s.Log.Printf("backup %s: prune %s: %v", src.Name, n, err)
		}
	}
	return to, cr.n, nil
}

// bkStart fetches one source in the background (once at a time per source).
func (s *Server) bkStart(name string, wait bool) error {
	st := s.State()
	src := st.backupSource(name)
	if src == nil {
		return notFound("backup source %s", name)
	}
	srcCopy, keep := *src, st.backupKeep()
	s.bk.mu.Lock()
	s.bkLoad()
	if s.bk.running[name] {
		s.bk.mu.Unlock()
		return conflict("a fetch of %s is running", name)
	}
	s.bk.running[name] = true
	s.bk.moved[name] = &atomic.Int64{}
	s.bk.mu.Unlock()
	run := func() {
		start := s.now()
		snap, n, err := s.bkFetch(context.Background(), srcCopy, keep)
		s.bk.mu.Lock()
		delete(s.bk.running, name)
		delete(s.bk.moved, name)
		s.bk.mu.Unlock()
		s.bkSet(name, func(b *bkStatus) {
			b.TriedAt = start.Format(time.RFC3339)
			if err != nil {
				b.Error = err.Error()
				return
			}
			b.Error, b.LastAt, b.LastSnap, b.LastBytes = "", s.now().Format(time.RFC3339), snap, n
		})
		if err != nil {
			s.Log.Printf("backup %s: %v", name, err)
		} else {
			s.Log.Printf("backup %s: %s (%d bytes)", name, snap, n)
		}
	}
	if wait {
		run()
	} else {
		go run()
	}
	return nil
}

// backupTick (every minute from Tick): at its hour each source is fetched; after a failed try
// it is tried again every hour until one works.
func (s *Server) backupTick(t time.Time) {
	if t.Minute() != 0 {
		return
	}
	for _, src := range s.State().Backups {
		if src.Paused {
			continue
		}
		b, running := s.bkGet(src.Name)
		due := t.Hour() == src.Hour
		if !due && b.Error != "" { // retry hourly while the last one failed
			if last, err := time.Parse(time.RFC3339, b.LastAt); err != nil || t.Sub(last) > 20*time.Hour {
				due = true
			}
		}
		if due && !running {
			s.bkStart(src.Name, s.syncJobs)
		}
	}
}

// backupAlerts: a failed fetch (warning), nothing fetched for two days (error).
func (s *Server) backupAlerts() []Alert {
	var out []Alert
	now := s.now()
	for _, src := range s.State().Backups {
		if src.Paused {
			continue
		}
		b, _ := s.bkGet(src.Name)
		last, err := time.Parse(time.RFC3339, b.LastAt)
		switch {
		case b.LastAt != "" && err == nil && now.Sub(last) > backupStale:
			out = append(out, Alert{Level: "error", Code: "backup_stale", Object: src.Name,
				Text: fmt.Sprintf("backup %s: nothing fetched since %s%s", src.Name, b.LastAt, errSuffix(b.Error))})
		case b.Error != "":
			out = append(out, Alert{Level: "warning", Code: "backup_failed", Object: src.Name,
				Text: fmt.Sprintf("backup %s: the last fetch failed: %s", src.Name, b.Error)})
		}
	}
	return out
}

func errSuffix(e string) string {
	if e == "" {
		return ""
	}
	return " (" + e + ")"
}

// ---- API ----

type backupOut struct {
	BackupSource
	Secret    string   `json:"secret,omitempty"` // never shown
	Dataset   string   `json:"dataset"`
	Used      int64    `json:"used"`
	Running   bool     `json:"running"`
	Moved     int64    `json:"moved,omitempty"` // bytes of the running fetch so far
	Snapshots []bkSnap `json:"snapshots"`
	bkStatus
}

// GET /v1/backups
func (s *Server) getBackups(r *http.Request) (any, error) {
	st := s.State()
	out := []backupOut{}
	for _, src := range st.Backups {
		ds := src.Pool + "/" + BackupDS + "/" + src.Name
		b, running := s.bkGet(src.Name)
		o := backupOut{BackupSource: src, Dataset: ds, Running: running, bkStatus: b, Snapshots: s.bkSnaps(ds)}
		s.bk.mu.Lock()
		if m := s.bk.moved[src.Name]; m != nil {
			o.Moved = m.Load()
		}
		s.bk.mu.Unlock()
		if o.Snapshots == nil {
			o.Snapshots = []bkSnap{}
		}
		if u, err := s.Sys.Run("", "zfs", "list", "-H", "-p", "-o", "used", ds); err == nil {
			o.Used, _ = strconv.ParseInt(strings.TrimSpace(u), 10, 64)
		}
		out = append(out, o)
	}
	_, kerr := s.Sys.ReadFile(s.bkKeyPath())
	return map[string]any{"sources": out, "keep": st.backupKeep(), "key_set": kerr == nil, "default_hour": DefaultBackupHour}, nil
}

// POST /v1/backups {"token", "pool", "hour"}: checks the token against the source, then adds it.
func (s *Server) postBackup(r *http.Request) (any, error) {
	var req struct {
		Token string `json:"token"`
		Pool  string `json:"pool"`
		Hour  *int   `json:"hour"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	host, port, sec, pin, err := ParsePullToken(req.Token)
	if err != nil {
		return nil, bad("%v", err)
	}
	st := s.State()
	if req.Pool == "" && len(st.Pools) > 0 {
		req.Pool = st.Pools[0].Name
	}
	if !slices.ContainsFunc(st.Pools, func(p Pool) bool { return p.Name == req.Pool }) {
		return nil, bad("pool: no storage %q", req.Pool)
	}
	hour := DefaultBackupHour
	if req.Hour != nil {
		hour = *req.Hour
	}
	if hour < 0 || hour > 23 {
		return nil, bad("hour: 0 to 23 (UTC)")
	}
	src := BackupSource{Host: host, Port: port, Secret: sec, Pin: pin, Pool: req.Pool, Hour: hour}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	in, err := bkGetInfo(ctx, src)
	if err != nil {
		return nil, &apiError{http.StatusBadGateway, "the source does not answer as it should: " + err.Error()}
	}
	src.Kind, src.Label = in.Kind, in.Name
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.st.Clone()
	for _, b := range ns.Backups {
		if b.Host == host && b.Port == port {
			return nil, conflict("%s:%d is fetched already (as %s): give it the new token there", host, port, b.Name)
		}
	}
	base := bkNameRE.ReplaceAllString(strings.ToLower(in.Kind+"-"+strings.Split(host, ".")[0]), "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "source"
	}
	name := base
	for i := 2; ns.backupSource(name) != nil; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	src.Name = name
	ns.Backups = append(ns.Backups, src)
	if _, err := s.commit(ns); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "kind": src.Kind, "label": src.Label}, nil
}

// PUT /v1/backups/{name} {"hour", "paused", "token"}
func (s *Server) putBackup(r *http.Request) (any, error) {
	var req struct {
		Hour   *int    `json:"hour"`
		Paused *bool   `json:"paused"`
		Token  *string `json:"token"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.st.Clone()
	src := ns.backupSource(r.PathValue("name"))
	if src == nil {
		return nil, notFound("backup source %s", r.PathValue("name"))
	}
	if req.Hour != nil {
		if *req.Hour < 0 || *req.Hour > 23 {
			return nil, bad("hour: 0 to 23 (UTC)")
		}
		src.Hour = *req.Hour
	}
	if req.Paused != nil {
		src.Paused = *req.Paused
	}
	if req.Token != nil {
		host, port, sec, pin, err := ParsePullToken(*req.Token)
		if err != nil {
			return nil, bad("%v", err)
		}
		src.Host, src.Port, src.Secret, src.Pin = host, port, sec, pin
	}
	if _, err := s.commit(ns); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

// DELETE /v1/backups/{name}?data=1: stop fetching; with data=1 the copy goes too.
func (s *Server) deleteBackup(r *http.Request) (any, error) {
	name := r.PathValue("name")
	if _, running := s.bkGet(name); running {
		return nil, conflict("a fetch of %s is running", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.st.Clone()
	src := ns.backupSource(name)
	if src == nil {
		return nil, notFound("backup source %s", name)
	}
	ds := src.Pool + "/" + BackupDS + "/" + src.Name
	ns.Backups = slices.DeleteFunc(ns.Backups, func(b BackupSource) bool { return b.Name == name })
	if r.URL.Query().Get("data") == "1" {
		if _, err := s.Sys.Run("", "zfs", "list", "-H", "-o", "name", ds); err == nil {
			if _, err := s.run("", "zfs", "destroy", "-r", ds); err != nil {
				return nil, fmt.Errorf("destroy %s: %v", ds, err)
			}
		}
	}
	if _, err := s.commit(ns); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

// POST /v1/backups/{name}/fetch: now.
func (s *Server) postBackupFetch(r *http.Request) (any, error) {
	return map[string]any{}, s.bkStart(r.PathValue("name"), s.syncJobs)
}

// PUT /v1/backups/keep {"daily", "monthly"}
func (s *Server) putBackupKeep(r *http.Request) (any, error) {
	var k BackupKeep
	if err := decode(r, &k); err != nil {
		return nil, err
	}
	if k.Daily < 1 || k.Daily > 1000 || k.Monthly < 0 || k.Monthly > 1000 {
		return nil, bad("daily 1 to 1000, monthly 0 to 1000")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.st.Clone()
	ns.BackupKeep = &k
	if _, err := s.commit(ns); err != nil {
		return nil, err
	}
	return k, nil
}

// GET /v1/backups/key: the key, to keep somewhere safe (the copies cannot be read without it
// once this keelNAS's system disk is gone). POST /v1/backups/key {"key"}: put a saved key back.
func (s *Server) getBackupKey(r *http.Request) (any, error) {
	b, err := s.Sys.ReadFile(s.bkKeyPath())
	if err != nil {
		return nil, notFound("no backup key yet (it is made with the first fetch)")
	}
	return map[string]any{"key": strings.TrimSpace(string(b))}, nil
}

func (s *Server) postBackupKey(r *http.Request) (any, error) {
	var req struct {
		Key string `json:"key"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	k := strings.ToLower(strings.TrimSpace(req.Key))
	if !pinRE.MatchString(k) {
		return nil, bad("key: 64 hex characters")
	}
	if _, err := s.Sys.ReadFile(s.bkKeyPath()); err == nil {
		return nil, conflict("this keelNAS has a backup key already")
	}
	if err := s.Sys.WriteFile(s.bkKeyPath(), []byte(k), 0o600); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

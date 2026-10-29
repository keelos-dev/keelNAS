package nas

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// How fast a pool's check runs (ported 2026-10-23 from keelOS keel/internal/vm/scrubperf.go,
// user there 2026-10-04: "scrub能体现出来pool性能么？比如加一个速度图"): while a scrub or
// resilver runs, keelnasd samples it every few seconds (scan_stats' issued bytes → rate, and
// zpool iostat's read rate of each disk over the same seconds, so a slow disk of a mirror shows);
// when it ends, a small summary of the run goes into scrubs.json next to state.json, so runs can
// be compared over time (a pool that gets slower can be a disk going bad). Between checks the
// watch looks once a minute (one zpool status). Differences from keel: every pool of the NAS,
// not one; the numbers come from zpool status -j's scan_stats (exact bytes) instead of the
// rounded text.

// DevRate is a disk's read rate in bytes/s.
type DevRate struct {
	Name string `json:"name"` // vtbd1, da0 (without /dev/)
	Read int64  `json:"read"`
}

// ParseIostat reads zpool iostat -vpHPy POOL N 1: the pool's read rate and each device's. The
// devices of log, cache and spare classes are left out: a check does not read them. (keel's.)
func ParseIostat(out, pool string) (poolRead int64, devs []DevRate) {
	skip := false
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(l), "\t")
		if len(f) < 7 {
			continue
		}
		read, _ := strconv.ParseInt(f[5], 10, 64)
		switch name := f[0]; {
		case name == pool:
			poolRead = read
			skip = false
		case name == "logs" || name == "cache" || name == "spares" || name == "spare":
			skip = true
		case name == "special" || name == "dedup":
			skip = false
		case strings.HasPrefix(name, "/dev/") && !skip:
			devs = append(devs, DevRate{Name: strings.TrimPrefix(name, "/dev/"), Read: read})
		}
	}
	return poolRead, devs
}

// ScrubSample is one moment of a running check: Rate (issued bytes/s over the last half
// minute) and Read (all reads of the pool's disks, SMB's too) in bytes/s.
type ScrubSample struct {
	T      int64  `json:"t"` // Unix seconds
	Issued int64  `json:"issued"`
	Rate   *int64 `json:"rate,omitempty"`
	Read   *int64 `json:"read,omitempty"`
}

// DevStat is a disk's read rate now and on average over the check.
type DevStat struct {
	Name string `json:"name"`
	Now  int64  `json:"now"`
	Avg  int64  `json:"avg"`
}

// ScrubLive is a running check as keelnasd sees it.
type ScrubLive struct {
	Pool     string        `json:"pool"`
	Scan     ScanView      `json:"scan"`
	Interval int           `json:"interval"` // seconds between samples (grows when old ones are thinned)
	Samples  []ScrubSample `json:"samples"`
	Devs     []DevStat     `json:"devs"`
	Peak     int64         `json:"peak"`
	devSum   map[string]float64
	devSecs  float64
}

// ScrubRun is one past check (scrubs.json).
type ScrubRun struct {
	Pool     string    `json:"pool"`
	Kind     string    `json:"kind"`
	Start    string    `json:"start"`
	End      string    `json:"end"`
	Seconds  int64     `json:"seconds"`
	Bytes    int64     `json:"bytes,omitempty"`
	Avg      int64     `json:"avg,omitempty"` // Bytes / Seconds
	Peak     int64     `json:"peak,omitempty"`
	Devs     []DevStat `json:"devs,omitempty"` // average read rate of each disk (Now unused)
	Repaired int64     `json:"repaired"`
	Errors   int64     `json:"errors"`
	Canceled bool      `json:"canceled,omitempty"`
	// Superseded: a newer scan (a resilver after a replace, a scrub started at once) took its
	// place before its end was seen; zpool keeps only the last scan, so End, Seconds and Bytes
	// are as last seen and the result is not known (QEMU 2026-10-23: a 4 s scrub and a 40 s
	// resilver were lost this way before)
	Superseded bool `json:"superseded,omitempty"`
}

const (
	scrubRunsKept   = 40 // per pool
	scrubSamplesMax = 1440
	scrubRateWindow = 30 // seconds the rate is taken over
)

// ScrubWatch samples the pools while a check runs. The zero value works; Loop runs it.
type ScrubWatch struct {
	s        *Server
	Interval int // seconds of each zpool iostat sample (default 5)
	mu       sync.Mutex
	live     map[string]*ScrubLive
	runs     []ScrubRun
	loaded   bool
	kick     chan struct{}
}

func (w *ScrubWatch) interval() int {
	if w.Interval <= 0 {
		return 5
	}
	return w.Interval
}

func (w *ScrubWatch) kickChan() chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kick == nil {
		w.kick = make(chan struct{}, 1)
	}
	return w.kick
}

// Kick makes the watch look now (a check or resilver was just started).
func (w *ScrubWatch) Kick() {
	select {
	case w.kickChan() <- struct{}{}:
	default:
	}
}

// Loop runs forever: a sample every Interval seconds while a check runs, a look every minute
// between checks and right after Kick.
func (w *ScrubWatch) Loop() {
	kick := w.kickChan()
	wait := 15 * time.Second
	for {
		select {
		case <-kick:
		case <-time.After(wait):
		}
		for w.Step() {
		}
		wait = time.Minute
	}
}

// scans: each data pool's scan now.
func (w *ScrubWatch) scans() map[string]ScanView {
	m := map[string]ScanView{}
	pools, err := w.s.dataPools(w.s.State())
	if err != nil {
		return m
	}
	for _, p := range pools {
		if p.Health != "MISSING" {
			m[p.Name] = ScanFromStats(p.Scan, w.s.now())
		}
	}
	return m
}

// Step looks once; true: a check runs and was sampled (look again right away).
func (w *ScrubWatch) Step() bool {
	w.s.releaseReplaced()
	before := w.scans()
	any := false
	for pool, v := range before {
		if v.State != "running" {
			w.ended(pool, v)
			continue
		}
		iv := w.interval()
		out, err := w.s.Sys.Run("", "zpool", "iostat", "-vpHPy", pool, strconv.Itoa(iv), "1") // takes iv seconds
		if err != nil && w.s.Now == nil {
			time.Sleep(time.Duration(iv) * time.Second)
		}
		read, devs := ParseIostat(out, pool)
		after, ok := w.scans()[pool]
		if !ok {
			continue
		}
		if after.Kind != v.Kind || after.Start != v.Start { // v ended unseen, another one began
			w.mu.Lock()
			w.supersede(pool, w.live[pool], v, w.s.now())
			w.mu.Unlock()
		}
		if after.State != "running" {
			w.ended(pool, after)
			continue
		}
		w.add(pool, after, w.s.now(), iv, read, devs, err == nil)
		any = true
	}
	return any
}

// sameRun: the live record is of this scan (same kind and start).
func sameRun(l *ScrubLive, v ScanView) bool {
	return l != nil && l.Scan.Kind == v.Kind && l.Scan.Start == v.Start
}

// add puts one sample into the pool's running check (a new one when the start changed).
func (w *ScrubWatch) add(pool string, v ScanView, now time.Time, iv int, read int64, devs []DevRate, haveIO bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.live == nil {
		w.live = map[string]*ScrubLive{}
	}
	l := w.live[pool]
	if l != nil && !sameRun(l, v) && len(l.Samples) > 0 {
		w.supersede(pool, l, l.Scan, time.Unix(l.Samples[len(l.Samples)-1].T, 0))
	}
	if !sameRun(l, v) {
		l = &ScrubLive{Pool: pool, Interval: iv, Samples: []ScrubSample{}, Devs: []DevStat{}, devSum: map[string]float64{}}
		w.live[pool] = l
	}
	l.Scan = v
	smp := ScrubSample{T: now.Unix(), Issued: v.Issued}
	for _, p := range l.Samples { // the rate over the last half minute (or what there is of it)
		if dt := smp.T - p.T; dt > 0 && dt <= scrubRateWindow+int64(iv)/2 {
			r := max(0, (smp.Issued-p.Issued)/dt)
			smp.Rate = &r
			l.Peak = max(l.Peak, r)
			break
		}
	}
	if haveIO {
		r := read
		smp.Read = &r
		cur := map[string]int64{}
		for _, d := range devs {
			l.devSum[d.Name] += float64(d.Read) * float64(iv)
			cur[d.Name] = d.Read
		}
		l.devSecs += float64(iv)
		l.Devs = l.Devs[:0]
		for name, sum := range l.devSum {
			l.Devs = append(l.Devs, DevStat{Name: name, Now: cur[name], Avg: int64(sum / l.devSecs)})
		}
		sort.Slice(l.Devs, func(i, j int) bool { return l.Devs[i].Name < l.Devs[j].Name })
	}
	l.Samples = append(l.Samples, smp)
	if len(l.Samples) > scrubSamplesMax { // a long check: every other sample goes
		k := 0
		for i := range l.Samples {
			if i%2 == 0 || i == len(l.Samples)-1 {
				l.Samples[k] = l.Samples[i]
				k++
			}
		}
		l.Samples = l.Samples[:k]
		l.Interval *= 2
	}
}

// ended: the pool has no running check: the one watched (or a finished one not seen) goes into
// scrubs.json once.
func (w *ScrubWatch) ended(pool string, v ScanView) {
	w.mu.Lock()
	defer w.mu.Unlock()
	l := w.live[pool]
	if v.State == "paused" {
		if l != nil {
			l.Scan = v
		}
		return
	}
	delete(w.live, pool)
	if v.State != "finished" && v.State != "canceled" {
		return
	}
	w.load()
	for _, r := range w.runs {
		if r.Pool == pool && r.Kind == v.Kind && r.End == v.End {
			return // recorded already
		}
	}
	r := ScrubRun{Pool: pool, Kind: v.Kind, Start: v.Start, End: v.End, Seconds: v.Seconds,
		Repaired: v.Repaired, Errors: v.Errors, Canceled: v.State == "canceled"}
	if sameRun(l, v) {
		r.Peak = l.Peak
		for _, d := range l.Devs {
			r.Devs = append(r.Devs, DevStat{Name: d.Name, Avg: d.Avg})
		}
	} else if r.Canceled {
		return // nothing known about it but its end
	}
	r.Bytes = v.Issued
	if r.Bytes > 0 && r.Seconds > 0 {
		r.Avg = r.Bytes / r.Seconds
	}
	w.runs = append(w.runs, r)
	sort.SliceStable(w.runs, func(i, j int) bool { return w.runs[i].End < w.runs[j].End })
	// keep the last scrubRunsKept of each pool
	n := map[string]int{}
	for i := len(w.runs) - 1; i >= 0; i-- {
		n[w.runs[i].Pool]++
		if n[w.runs[i].Pool] > scrubRunsKept {
			w.runs = append(w.runs[:i], w.runs[i+1:]...)
		}
	}
	w.save()
}

// supersede records run v, last seen at "at", that a newer scan replaced (w.mu held); l is the
// live record when it is of v.
func (w *ScrubWatch) supersede(pool string, l *ScrubLive, v ScanView, at time.Time) {
	w.load()
	for _, r := range w.runs {
		if r.Pool == pool && r.Kind == v.Kind && r.Start == v.Start {
			return
		}
	}
	r := ScrubRun{Pool: pool, Kind: v.Kind, Start: v.Start, End: at.UTC().Format(time.RFC3339), Bytes: v.Issued,
		Repaired: v.Repaired, Errors: v.Errors, Superseded: true}
	if st, err := time.Parse(time.RFC3339, v.Start); err == nil {
		r.Seconds = max(int64(at.Sub(st).Seconds()), 0)
	}
	if sameRun(l, v) {
		r.Peak = l.Peak
		for _, d := range l.Devs {
			r.Devs = append(r.Devs, DevStat{Name: d.Name, Avg: d.Avg})
		}
	}
	if r.Bytes > 0 && r.Seconds > 0 {
		r.Avg = r.Bytes / r.Seconds
	}
	w.runs = append(w.runs, r)
	sort.SliceStable(w.runs, func(i, j int) bool { return w.runs[i].End < w.runs[j].End })
	w.save()
}

func (w *ScrubWatch) file() string { return filepath.Join(filepath.Dir(w.s.StatePath), "scrubs.json") }

type scrubFile struct {
	Runs []ScrubRun `json:"runs"`
}

// load reads scrubs.json once (w.mu held).
func (w *ScrubWatch) load() {
	if w.loaded {
		return
	}
	w.loaded = true
	if b, err := w.s.Sys.ReadFile(w.file()); err == nil {
		var f scrubFile
		if json.Unmarshal(b, &f) == nil {
			w.runs = f.Runs
		}
	}
}

func (w *ScrubWatch) save() {
	b, _ := json.MarshalIndent(scrubFile{Runs: w.runs}, "", "  ")
	if err := w.s.Sys.WriteFile(w.file(), append(b, '\n'), 0o644); err != nil {
		w.s.Log.Printf("scrubs.json: %v", err)
	}
}

// ScrubPerf is GET /v1/storage/{pool}/scrub/perf: the running check (nil: none) and the past
// ones of the pool, oldest first.
type ScrubPerf struct {
	Live *ScrubLive `json:"live"`
	Runs []ScrubRun `json:"runs"`
}

// Perf: a copy of what the watch knows about pool.
func (w *ScrubWatch) Perf(pool string) ScrubPerf {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.load()
	out := ScrubPerf{Runs: []ScrubRun{}}
	for _, r := range w.runs {
		if r.Pool == pool {
			out.Runs = append(out.Runs, r)
		}
	}
	if l := w.live[pool]; l != nil {
		c := *l
		c.Samples = append([]ScrubSample(nil), l.Samples...)
		c.Devs = append([]DevStat(nil), l.Devs...)
		out.Live = &c
	}
	return out
}

func (s *Server) getScrubPerf(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	if s.State().Pool(pool) == nil {
		return nil, notFound("no storage %s", pool)
	}
	return s.scrubs.Perf(pool), nil
}

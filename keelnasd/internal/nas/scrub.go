package nas

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The pool's check (zpool scrub), per pool: now, once at a time, or regularly (docs/04 §17.1).
// Ported 2026-10-23 from keelOS (keel/internal/vm/scrub.go NextScrub and ScrubPool,
// keel/internal/api/scrub.go's routes and scheduler; user: "拷贝就行"), merged with keelNAS's own
// 30-day rule (docs/04 §9.4), which stays the default ("auto"). keelNAS's clock is UTC, so the
// hour of a plan is UTC.

const scrubEvery = 30 * 24 * time.Hour

// ScrubPlan is when keelnasd checks a pool (state.json pools[].scrub). Every: auto (the last
// check, or the pool's creation, more than 30 days ago: one at Hour:00), week (each Weekday),
// month (the first Weekday of each month), off. Once: one check at that time, whatever Every is;
// it is removed once it has started.
type ScrubPlan struct {
	Every   string     `json:"every"`
	Weekday int        `json:"weekday"` // 0 = Sunday
	Hour    int        `json:"hour"`    // UTC
	Once    *time.Time `json:"once"`
}

// ScrubDefault is the plan of a pool that has none: keelNAS's rule since 2026-10-03.
var ScrubDefault = ScrubPlan{Every: "auto", Weekday: 0, Hour: 3}

func (p Pool) ScrubPlan() ScrubPlan {
	if p.Scrub == nil {
		return ScrubDefault
	}
	return *p.Scrub
}

// Valid checks a plan from the API.
func (p ScrubPlan) Valid() error {
	switch p.Every {
	case "auto", "week", "month", "off":
	default:
		return fmt.Errorf("every: auto, week, month or off")
	}
	if p.Weekday < 0 || p.Weekday > 6 {
		return fmt.Errorf("weekday: 0 (Sunday) to 6")
	}
	if p.Hour < 0 || p.Hour > 23 {
		return fmt.Errorf("hour: 0 to 23 (UTC)")
	}
	return nil
}

// NextScrub is the first time after now that the plan's regular check (week, month; auto: its
// daily hour, ScrubDue decides) or its once falls on; zero: none. From keel's vm.NextScrub.
func NextScrub(s ScrubPlan, now time.Time) time.Time {
	now = now.UTC()
	var next time.Time
	if s.Every == "week" || s.Every == "month" || s.Every == "auto" {
		for d := 0; d < 400; d++ { // the next matching day at Hour:00 after now
			day := time.Date(now.Year(), now.Month(), now.Day()+d, s.Hour, 0, 0, 0, time.UTC)
			if !day.After(now) {
				continue
			}
			if s.Every != "auto" && int(day.Weekday()) != s.Weekday {
				continue
			}
			if s.Every == "month" && day.Day() > 7 { // only the first such weekday of the month
				continue
			}
			next = day
			break
		}
	}
	if s.Once != nil && s.Once.After(now) && (next.IsZero() || s.Once.Before(next)) {
		next = s.Once.UTC()
	}
	return next
}

// ScrubDue: the pool's last finished scrub (or, never scrubbed, its creation) is more than 30
// days before t, and no scrub or resilver is running. created is the pool's creation (Unix).
// A resilver replaces the scrub in scan_stats, so after one the pool counts as never scrubbed.
func ScrubDue(p PoolInfo, created int64, t time.Time) bool {
	if p.Scan.State == "SCANNING" {
		return false
	}
	ref := scrubRef(p, created)
	if ref <= 0 {
		return false
	}
	return t.Sub(time.Unix(ref, 0)) > scrubEvery
}

func scrubRef(p PoolInfo, created int64) int64 {
	if p.Scan.Function == "SCRUB" && p.Scan.State == "FINISHED" && p.Scan.End > 0 {
		return p.Scan.End
	}
	return created
}

// ScanView is a pool's last or running scrub or resilver as the API shows it (GET /v1/storage
// scan, GET …/scrub scan). The progress is zpool status's own arithmetic on scan_stats
// (print_scan_scrub_resilver_status): total = to_examine − skipped, pct = issued / total, rate =
// the bytes issued in this pass / its seconds without pauses, eta = (total − issued) / rate.
type ScanView struct {
	Kind     string  `json:"kind"`  // scrub, resilver, "" = none yet
	State    string  `json:"state"` // none, running, paused, finished, canceled
	Start    string  `json:"start,omitempty"`
	End      string  `json:"end,omitempty"`
	Total    int64   `json:"total"`
	Issued   int64   `json:"issued"`
	Scanned  int64   `json:"scanned"`
	Pct      float64 `json:"pct"`
	Rate     int64   `json:"rate"`     // bytes/s, running only
	ETA      int64   `json:"eta_s"`    // seconds left, running only; -1 = not known yet
	Seconds  int64   `json:"seconds"`  // how long it ran (finished, canceled) or runs
	Repaired int64   `json:"repaired"` // bytes repaired (scrub) or resilvered
	Errors   int64   `json:"errors"`   // errors it could not repair
}

// ScanFromStats computes the view at now.
func ScanFromStats(sc Scan, now time.Time) ScanView {
	v := ScanView{Kind: strings.ToLower(sc.Function), State: "none", ETA: -1}
	if sc.Function == "" || sc.Function == "NONE" {
		v.Kind = ""
		return v
	}
	ts := func(u int64) string {
		if u <= 0 {
			return ""
		}
		return time.Unix(u, 0).UTC().Format(time.RFC3339)
	}
	v.Start, v.Total, v.Issued, v.Scanned = ts(sc.Start), max(sc.ToExamine-sc.Skipped, 0), sc.Issued, sc.Examined
	v.Repaired, v.Errors = sc.Processed, sc.Errors
	if v.Total > 0 {
		v.Pct = float64(int64(min(float64(v.Issued)/float64(v.Total), 1)*10000)) / 100
	}
	switch sc.State {
	case "SCANNING":
		v.State = "running"
		if sc.Pause > 0 {
			v.State = "paused"
		}
		elapsed := now.Unix() - sc.PassStart - sc.SpentPaused
		if sc.Pause > 0 {
			elapsed -= now.Unix() - sc.Pause
		}
		if elapsed < 1 {
			elapsed = 1
		}
		v.Rate = sc.PassIssued / elapsed
		if v.Rate > 0 && v.Total >= v.Issued && v.State == "running" {
			v.ETA = (v.Total - v.Issued) / v.Rate
		}
		if sc.Start > 0 {
			v.Seconds = max(now.Unix()-sc.Start, 0)
		}
	case "FINISHED":
		v.State, v.End, v.Pct = "finished", ts(sc.End), 100
		v.Seconds = max(sc.End-sc.Start, 0)
	case "CANCELED":
		v.State, v.End = "canceled", ts(sc.End)
		v.Seconds = max(sc.End-sc.Start, 0)
	default:
		v.State = strings.ToLower(sc.State)
	}
	return v
}

// scrubPool starts (stop false) or stops a check of the pool name (keel's ScrubPool). A check
// or resilver under way is a 409 for a start, none running a 409 for a stop.
func (s *Server) scrubPool(name string, stop bool) error {
	args := []string{"scrub", name}
	if stop {
		args = []string{"scrub", "-s", name}
	}
	out, err := s.Sys.Run("", "zpool", args...)
	if err != nil {
		msg := strings.TrimSpace(out + " " + err.Error())
		if !stop && (strings.Contains(msg, "currently scrubbing") || strings.Contains(msg, "currently resilvering")) {
			return conflict("%s is being checked or resilvered already", name)
		}
		if stop && strings.Contains(msg, "no active scrub") {
			return conflict("no check of %s is running", name)
		}
		s.Log.Printf("command failed: %v", err)
		return err
	}
	s.scrubs.Kick()
	return nil
}

// scrubTick starts the checks the pools' plans ask for at t (the scheduler calls it every
// minute). forced (the test hook): every pool whose auto rule says it is due, whatever the hour.
// A host that was off at the planned time does not catch up: the next one comes at its time.
func (s *Server) scrubTick(t time.Time, forced bool, res *TickResult) {
	st := s.State()
	type cand struct {
		plan    ScrubPlan
		once    bool // the plan's once is due
		regular bool // week/month due, or auto's hour (ScrubDue decides)
	}
	due := map[string]cand{}
	for _, p := range st.Pools {
		pl := p.ScrubPlan()
		c := cand{plan: pl}
		if pl.Once != nil && !pl.Once.After(t) && pl.Once.After(t.Add(-time.Minute)) {
			c.once = true
		}
		if pl.Every != "off" {
			reg := pl
			reg.Once = nil
			if n := NextScrub(reg, t.Add(-time.Minute)); !n.IsZero() && !n.After(t) {
				c.regular = true
			}
		}
		if forced && pl.Every == "auto" {
			c.regular = true
		}
		if c.once || c.regular {
			due[p.Name] = c
		}
	}
	if len(due) == 0 {
		return
	}
	pools, err := s.dataPools(st)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return
	}
	for _, p := range pools {
		c, ok := due[p.Name]
		if !ok || p.Health == "MISSING" {
			continue
		}
		start := c.once || (c.regular && c.plan.Every != "auto")
		if c.regular && c.plan.Every == "auto" {
			out, err := s.Sys.Run("", "zfs", "get", "-H", "-p", "-o", "value", "creation", p.Name)
			created, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
			if err != nil {
				created = 0
			}
			start = start || ScrubDue(p, created, t)
		}
		if start && p.Scan.State != "SCANNING" {
			if err := s.scrubPool(p.Name, false); err != nil {
				res.Errors = append(res.Errors, err.Error())
			} else {
				s.Log.Printf("started a scrub of %s", p.Name)
				res.Scrubs = append(res.Scrubs, p.Name)
			}
		}
	}
	// a once that came due is done (started, or a scan ran anyway): the plan keeps the rest
	s.mu.Lock()
	defer s.mu.Unlock()
	nst := s.st.Clone()
	changed := false
	for i, p := range nst.Pools {
		if c, ok := due[p.Name]; ok && c.once && p.Scrub != nil {
			pl := *p.Scrub
			pl.Once = nil
			nst.Pools[i].Scrub = &pl
			changed = true
		}
	}
	if changed {
		if err := SaveState(s.Sys, s.StatePath, nst); err != nil {
			res.Errors = append(res.Errors, err.Error())
			return
		}
		s.st = nst
	}
}

// ---- API: GET/PUT /v1/storage/{pool}/scrub, POST …/scrub/start|stop, GET …/scrub/perf ----

func (s *Server) scrubStatus(pool string) (any, error) {
	st := s.State()
	p := st.Pool(pool)
	if p == nil {
		return nil, notFound("no storage %s", pool)
	}
	pools, err := s.dataPools(st)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"pool": pool, "schedule": p.ScrubPlan(), "running": false, "scan": ScanView{State: "none", ETA: -1}}
	for _, pi := range pools {
		if pi.Name != pool {
			continue
		}
		v := ScanFromStats(pi.Scan, s.now())
		out["scan"], out["running"] = v, v.State == "running" || v.State == "paused"
		pl := p.ScrubPlan()
		reg := pl
		reg.Once = nil
		var next time.Time
		switch pl.Every {
		case "auto": // the first daily hour after the 30 days are up
			o, err := s.Sys.Run("", "zfs", "get", "-H", "-p", "-o", "value", "creation", pool)
			created, _ := strconv.ParseInt(strings.TrimSpace(o), 10, 64)
			if ref := scrubRef(pi, created); err == nil && ref > 0 {
				from := time.Unix(ref, 0).Add(scrubEvery)
				if from.Before(s.now()) {
					from = s.now()
				}
				next = NextScrub(reg, from)
			}
		case "week", "month":
			next = NextScrub(reg, s.now())
		}
		if pl.Once != nil && pl.Once.After(s.now()) && (next.IsZero() || pl.Once.Before(next)) {
			next = *pl.Once
		}
		if !next.IsZero() {
			out["next"] = next.UTC().Format(time.RFC3339)
		}
	}
	return out, nil
}

func (s *Server) getScrub(r *http.Request) (any, error) {
	return s.scrubStatus(r.PathValue("pool"))
}

func (s *Server) putScrub(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	var q ScrubPlan
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	if err := q.Valid(); err != nil {
		return nil, bad("%v", err)
	}
	if q.Once != nil && !q.Once.After(s.now()) {
		return nil, bad("once: a time in the future")
	}
	s.mu.Lock()
	st := s.st.Clone()
	var p *Pool
	for i := range st.Pools {
		if st.Pools[i].Name == pool {
			p = &st.Pools[i]
		}
	}
	if p == nil {
		s.mu.Unlock()
		return nil, notFound("no storage %s", pool)
	}
	if q.Once != nil {
		u := q.Once.UTC()
		q.Once = &u
	}
	p.Scrub = &q
	err := SaveState(s.Sys, s.StatePath, st)
	if err == nil {
		s.st = st
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.scrubStatus(pool)
}

func (s *Server) postScrubAction(r *http.Request) (any, error) {
	pool, what := r.PathValue("pool"), r.PathValue("what")
	if what != "start" && what != "stop" {
		return nil, notFound("no such API: %s %s", r.Method, r.URL.Path)
	}
	if s.State().Pool(pool) == nil {
		return nil, notFound("no storage %s", pool)
	}
	if err := s.scrubPool(pool, what == "stop"); err != nil {
		return nil, err
	}
	return s.scrubStatus(pool)
}

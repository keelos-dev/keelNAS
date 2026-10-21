package nas

import (
	"net/http"
	"slices"
	"time"
)

// The scheduler: once a minute keelnasd looks at the clock (UTC) and runs what is due. No cron.
//   on the hour   snapshots (class by ClassAt) and pruning
//   03:00         scrubs that are due
//   every 24 h    SMART (the first run right after the start)

// TickResult says what a tick did (for the log and the test hook).
type TickResult struct {
	Time    string   `json:"time"`
	Class   string   `json:"class"`
	Created []string `json:"created"`
	Skipped []string `json:"skipped"`
	Pruned  []string `json:"pruned"`
	Scrubs  []string `json:"scrubs"`
	Smart   []string `json:"smart"`
	Errors  []string `json:"errors"`
}

// Jobs of a tick; a forced job runs whatever the time.
const (
	JobSnapshots = "snapshots"
	JobScrub     = "scrub"
	JobSmart     = "smart"
)

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Tick runs what is due at t (truncated to the minute). force names jobs to run regardless of
// the time; nil = the schedule decides. only, when not nil, limits the tick to those jobs.
func (s *Server) Tick(t time.Time, force, only []string) TickResult {
	t = t.UTC().Truncate(time.Minute)
	res := TickResult{Time: t.Format(time.RFC3339), Created: []string{}, Skipped: []string{},
		Pruned: []string{}, Scrubs: []string{}, Smart: []string{}, Errors: []string{}}
	want := func(job string, due bool) bool {
		if only != nil && !slices.Contains(only, job) {
			return false
		}
		return due || slices.Contains(force, job)
	}
	if want(JobSnapshots, t.Minute() == 0) {
		s.snapshotTick(t, &res)
	}
	if want(JobScrub, t.Hour() == 3 && t.Minute() == 0) {
		s.scrubTick(t, &res)
	}
	s.hmu.Lock()
	smartDue := s.lastSmart.IsZero() || t.Sub(s.lastSmart) >= 24*time.Hour
	s.hmu.Unlock()
	if want(JobSmart, smartDue) {
		s.smartTick(t, &res)
	}
	if len(res.Smart) > 0 {
		s.Log.Printf("tick %s: SMART %v", res.Time, res.Smart)
	}
	for _, e := range res.Errors {
		s.Log.Printf("tick %s: %s", res.Time, e)
	}
	if len(res.Created)+len(res.Pruned)+len(res.Scrubs) > 0 {
		s.Log.Printf("tick %s: %d snapshots (%s), %d pruned, scrubs %v", res.Time, len(res.Created), res.Class, len(res.Pruned), res.Scrubs)
	}
	return res
}

// Schedule runs Tick at every minute boundary; it does not return.
func (s *Server) Schedule() {
	for {
		now := s.now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		time.Sleep(next.Sub(now))
		s.Tick(s.now(), nil, nil)
	}
}

// postDebugTick is the test hook (Unix socket only): {"time": RFC 3339, "force": [jobs],
// "only": [jobs]} runs one tick as if the clock said that time.
func (s *Server) postDebugTick(r *http.Request) (any, error) {
	var req struct {
		Time  string   `json:"time"`
		Force []string `json:"force"`
		Only  []string `json:"only"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	t := s.now()
	if req.Time != "" {
		var err error
		if t, err = time.Parse(time.RFC3339, req.Time); err != nil {
			return nil, bad("time: %v", err)
		}
	}
	for _, j := range append(slices.Clone(req.Force), req.Only...) {
		if j != JobSnapshots && j != JobScrub && j != JobSmart {
			return nil, bad("no job %q (snapshots, scrub, smart)", j)
		}
	}
	return s.Tick(t, req.Force, req.Only), nil
}

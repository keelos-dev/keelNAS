package nas

import (
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Scrubs, SMART and alerts (docs/04 sections 9.4 and 9.5).

// smartTick asks smartctl about every disk of a data pool (on the device smartDevices finds).
func (s *Server) smartTick(t time.Time, res *TickResult) {
	disks, err := s.disks()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return
	}
	m := map[string]SmartInfo{}
	devs := s.smartDevices(disks)
	for _, d := range disks {
		if d.Pool == "" || d.System {
			continue
		}
		args, ok := smartArgs(d.Name, devs)
		if !ok {
			m[d.Name] = SmartInfo{Status: "unknown"}
			res.Smart = append(res.Smart, d.Name+":unknown")
			continue
		}
		// smartctl's exit status is a bit mask; the JSON is what counts.
		out, _ := s.Sys.Run("", "smartctl", append([]string{"-H", "-A", "-j"}, args...)...)
		m[d.Name] = ParseSmart(out)
		res.Smart = append(res.Smart, d.Name+":"+m[d.Name].Status)
	}
	s.hmu.Lock()
	s.smart = m
	s.lastSmart = t
	s.hmu.Unlock()
}

// Alert is one current problem.
type Alert struct {
	Level  string `json:"level"` // warning, error
	Code   string `json:"code"`
	Object string `json:"object"`
	Text   string `json:"text"`
	Since  string `json:"since"`
}

// AlertInput is everything alerts are computed from.
type AlertInput struct {
	Pools     []PoolInfo
	Disks     []Disk            // with Smart filled in
	ApplyErrs []string          // of the last apply
	SnapFails map[string]string // share or LUN -> error, of the last snapshot round
}

// ComputeAlerts lists the alerts for the situation now (without since).
func ComputeAlerts(in AlertInput) []Alert {
	var a []Alert
	add := func(level, code, obj, format string, args ...any) {
		a = append(a, Alert{Level: level, Code: code, Object: obj, Text: fmt.Sprintf(format, args...)})
	}
	for _, p := range in.Pools {
		if p.Health != "ONLINE" {
			add("error", "pool_degraded", p.Name, "Storage %s is %s.", p.Name, p.Health)
		}
		if p.Health == "MISSING" {
			continue
		}
		if !p.Protected {
			add("warning", "pool_no_protection", p.Name, "Storage %s is a single disk: a disk failure loses its data.", p.Name)
		}
		if p.Size > 0 && p.Used*10 >= p.Size*9 {
			add("warning", "pool_full", p.Name, "Storage %s is %d%% full.", p.Name, p.Used*100/p.Size)
		}
		if p.Scan.Function == "SCRUB" && p.Scan.State == "FINISHED" && p.Scan.Errors > 0 {
			add("error", "scrub_errors", p.Name, "The last check of storage %s found %d errors it could not repair.", p.Name, p.Scan.Errors)
		}
	}
	for _, d := range in.Disks {
		if d.Smart == "failing" {
			add("error", "disk_failing", d.Name, "Disk %s reports that it is failing (SMART).", d.Name)
		}
	}
	if len(in.ApplyErrs) > 0 {
		add("error", "apply_failed", in.ApplyErrs[0], "The last apply had %d errors; the first: %s", len(in.ApplyErrs), in.ApplyErrs[0])
	}
	names := make([]string, 0, len(in.SnapFails))
	for n := range in.SnapFails {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		add("warning", "snapshot_failed", n, "A snapshot of %s failed: %s", n, in.SnapFails[n])
	}
	return a
}

// stampAlerts sets since: when an alert (code and object) was first seen; forgets the gone ones.
func stampAlerts(a []Alert, since map[string]time.Time, now time.Time) {
	seen := map[string]bool{}
	for i := range a {
		k := a[i].Code + "\x00" + a[i].Object
		seen[k] = true
		t, ok := since[k]
		if !ok {
			t = now.UTC()
			since[k] = t
		}
		a[i].Since = t.Format(time.RFC3339)
	}
	for k := range since {
		if !seen[k] {
			delete(since, k)
		}
	}
}

// withSmart fills in the SMART results known so far.
func (s *Server) withSmart(disks []Disk) []Disk {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	for i := range disks {
		disks[i].Smart = "unknown"
		if x, ok := s.smart[disks[i].Name]; ok {
			disks[i].Smart, disks[i].Temperature = x.Status, x.Temperature
		}
	}
	return disks
}

// alerts computes the current alerts; pools may be passed in when already at hand.
func (s *Server) alerts(pools []PoolInfo) ([]Alert, error) {
	if pools == nil {
		var err error
		if pools, err = s.dataPools(s.State()); err != nil {
			return nil, err
		}
	}
	disks, err := s.disks()
	if err != nil {
		return nil, err
	}
	_, applyErrs := s.LastApply()
	s.hmu.Lock()
	fails := map[string]string{}
	for k, v := range s.snapFails {
		fails[k] = v
	}
	s.hmu.Unlock()
	a := ComputeAlerts(AlertInput{Pools: pools, Disks: s.withSmart(disks), ApplyErrs: applyErrs, SnapFails: fails})
	s.hmu.Lock()
	stampAlerts(a, s.alertSince, s.now())
	s.hmu.Unlock()
	if a == nil {
		a = []Alert{}
	}
	return a, nil
}

func (s *Server) getAlerts(r *http.Request) (any, error) {
	a, err := s.alerts(nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"alerts": a}, nil
}

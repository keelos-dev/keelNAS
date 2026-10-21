package nas

import (
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Snapshots: every dataset whose auto snapshot is on (State.AutoSnapOn: a share's dataset
// unless switched off, any other dataset when switched on) and every LUN's zvol, hourly, daily (00:00) and weekly
// (Monday 00:00), named keelnas-YYYYMMDD-HHMMSS (UTC), the class in the user property
// keelnas:class (docs/04 section 9.3).

const (
	SnapPrefix = "keelnas-"
	snapLayout = "20060102-150405"
	ClassProp  = "keelnas:class"
)

// Classes that are taken on a schedule and pruned; manual ones never are.
var scheduledClasses = []string{"weekly", "daily", "hourly"}

// SnapName is the snapshot name for a time (UTC).
func SnapName(t time.Time) string { return SnapPrefix + t.UTC().Format(snapLayout) }

// SnapTime reads the time back from a name; ok=false for a name that is not ours.
func SnapTime(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, SnapPrefix) {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(snapLayout, strings.TrimPrefix(name, SnapPrefix), time.UTC)
	return t, err == nil
}

// ClassAt is the class of the scheduled snapshot at t: the highest one that is due and not
// switched off, "" when none is (not on the hour, or every due class is 0).
func ClassAt(t time.Time, r Retention) string {
	t = t.UTC()
	if t.Minute() != 0 || t.Second() != 0 {
		return ""
	}
	midnight := t.Hour() == 0
	switch {
	case midnight && t.Weekday() == time.Monday && r.Weekly > 0:
		return "weekly"
	case midnight && r.Daily > 0:
		return "daily"
	case r.Hourly > 0:
		return "hourly"
	}
	return ""
}

func keepOf(class string, r Retention) int {
	switch class {
	case "hourly":
		return r.Hourly
	case "daily":
		return r.Daily
	case "weekly":
		return r.Weekly
	}
	return -1 // manual and anything else: keep all
}

// Snap is one of our snapshots of one dataset.
type Snap struct {
	Name  string `json:"name"`
	Time  string `json:"time"`
	Class string `json:"class"`
	Used  int64  `json:"used"`
}

// ParseSnapshots reads `zfs list -H -p -t snapshot -o name,used,keelnas:class`: dataset -> our
// snapshots, newest first. Snapshots not named keelnas-… are left out.
func ParseSnapshots(out string) map[string][]Snap {
	m := map[string][]Snap{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		ds, name, ok := strings.Cut(f[0], "@")
		if !ok {
			continue
		}
		t, ok := SnapTime(name)
		if !ok {
			continue
		}
		used, _ := strconv.ParseInt(f[1], 10, 64)
		class := f[2]
		if class == "-" || class == "" {
			class = "manual"
		}
		m[ds] = append(m[ds], Snap{Name: name, Time: t.Format(time.RFC3339), Class: class, Used: used})
	}
	for ds := range m {
		sortSnaps(m[ds])
	}
	return m
}

func sortSnaps(s []Snap) {
	sort.Slice(s, func(i, j int) bool { return s[i].Name > s[j].Name })
}

// PrunePlan lists the snapshots to destroy: per scheduled class, all but the newest `keep`.
// Manual snapshots are never in it.
func PrunePlan(snaps []Snap, r Retention) []string {
	s := slices.Clone(snaps)
	sortSnaps(s)
	count := map[string]int{}
	var del []string
	for _, x := range s {
		keep := keepOf(x.Class, r)
		if keep < 0 {
			continue
		}
		count[x.Class]++
		if count[x.Class] > keep {
			del = append(del, x.Name)
		}
	}
	return del
}

// snapTarget is a dataset that gets snapshots: a share's, another one switched on, a LUN's.
type snapTarget struct {
	Kind    string // share, dataset, lun
	Name    string
	Dataset string
}

func snapTargets(st *State) []snapTarget {
	var t []snapTarget
	seen := map[string]bool{}
	for _, sh := range st.Shares {
		if st.AutoSnapOn(sh.Dataset) && !seen[sh.Dataset] {
			seen[sh.Dataset] = true
			t = append(t, snapTarget{"share", sh.Name, sh.Dataset})
		}
	}
	for _, ds := range slices.Sorted(maps.Keys(st.AutoSnap)) {
		if st.AutoSnap[ds] && !seen[ds] {
			seen[ds] = true
			t = append(t, snapTarget{"dataset", ds, ds})
		}
	}
	for _, l := range st.LUNs {
		t = append(t, snapTarget{"lun", l.Name, LUNDataset(l)})
	}
	return t
}

func (s *Server) listSnapshots() (map[string][]Snap, error) {
	out, err := s.Sys.Run("", "zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,used,"+ClassProp)
	if err != nil {
		return nil, err
	}
	return ParseSnapshots(out), nil
}

// written: dataset -> bytes written since its latest snapshot.
func (s *Server) written(datasets []string) map[string]int64 {
	m := map[string]int64{}
	if len(datasets) == 0 {
		return m
	}
	// written counts only what is on disk: sync first, or a write of the last seconds (the
	// open transaction group) looks like no write at all.
	pools := map[string]bool{}
	for _, d := range datasets {
		p, _, _ := strings.Cut(d, "/")
		if !pools[p] {
			pools[p] = true
			s.Sys.Run("", "zpool", "sync", p)
		}
	}
	args := append([]string{"get", "-H", "-p", "-o", "name,value", "written"}, datasets...)
	// A dataset that is missing makes zfs fail, but the others are still printed.
	out, _ := s.Sys.Run("", "zfs", args...)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 2 {
			if n, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				m[f[0]] = n
			}
		}
	}
	return m
}

func (s *Server) takeSnapshot(dataset, name, class string) error {
	_, err := s.run("", "zfs", "snapshot", "-o", ClassProp+"="+class, dataset+"@"+name)
	return err
}

// snapshotTick takes the scheduled snapshot at t (if one is due) of every dataset written to
// since its last snapshot, then prunes. Failures are kept for the snapshot_failed alert.
func (s *Server) snapshotTick(t time.Time, res *TickResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	class := ClassAt(t, st.Snapshots)
	res.Class = class
	targets := snapTargets(st)
	ds := s.datasets()
	fails := map[string]string{}
	var present []string
	for _, tg := range targets {
		if _, ok := ds[tg.Dataset]; ok {
			present = append(present, tg.Dataset)
		}
	}
	snaps, err := s.listSnapshots()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return
	}
	if class != "" {
		w := s.written(present)
		name := SnapName(t)
		for _, tg := range targets {
			if _, ok := ds[tg.Dataset]; !ok {
				continue // the pool is not imported: apply_failed says so
			}
			if n, ok := w[tg.Dataset]; ok && n == 0 {
				res.Skipped = append(res.Skipped, tg.Dataset)
				continue
			}
			if slices.ContainsFunc(snaps[tg.Dataset], func(x Snap) bool { return x.Name == name }) {
				continue // a manual one of this very second
			}
			if err := s.takeSnapshot(tg.Dataset, name, class); err != nil {
				fails[tg.Name] = err.Error()
				continue
			}
			res.Created = append(res.Created, tg.Dataset+"@"+name)
			snaps[tg.Dataset] = append(snaps[tg.Dataset], Snap{Name: name, Class: class})
		}
	}
	for _, tg := range targets {
		for _, n := range PrunePlan(snaps[tg.Dataset], st.Snapshots) {
			if _, err := s.run("", "zfs", "destroy", tg.Dataset+"@"+n); err != nil {
				fails[tg.Name] = err.Error()
				continue
			}
			res.Pruned = append(res.Pruned, tg.Dataset+"@"+n)
		}
	}
	s.hmu.Lock()
	s.snapFails = fails
	s.hmu.Unlock()
}

// ---- API: /v1/shares/{name}/snapshots, /v1/luns/{name}/snapshots, /v1/settings ----

// snapDataset finds the dataset of the share or LUN in the path.
func (s *Server) snapDataset(r *http.Request) (string, error) {
	st := s.State()
	name := r.PathValue("name")
	if strings.HasPrefix(r.URL.Path, "/v1/luns/") {
		l := st.LUN(name)
		if l == nil {
			return "", notFound("no LUN %s", name)
		}
		return LUNDataset(*l), nil
	}
	sh := st.Share(name)
	if sh == nil {
		return "", notFound("no share %s", name)
	}
	return sh.Dataset, nil
}

func (s *Server) getSnapshots(r *http.Request) (any, error) {
	ds, err := s.snapDataset(r)
	if err != nil {
		return nil, err
	}
	all, err := s.listSnapshots()
	if err != nil {
		return nil, err
	}
	list := all[ds]
	if list == nil {
		list = []Snap{}
	}
	return map[string]any{"snapshots": list}, nil
}

func (s *Server) postSnapshot(r *http.Request) (any, error) {
	ds, err := s.snapDataset(r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := SnapName(s.now())
	all, err := s.listSnapshots()
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(all[ds], func(x Snap) bool { return x.Name == name }) {
		return nil, conflict("snapshot %s exists: try again in a second", name)
	}
	if err := s.takeSnapshot(ds, name, "manual"); err != nil {
		return nil, err
	}
	if all, err = s.listSnapshots(); err == nil {
		for _, x := range all[ds] {
			if x.Name == name {
				return x, nil
			}
		}
	}
	t, _ := SnapTime(name)
	return Snap{Name: name, Time: t.Format(time.RFC3339), Class: "manual"}, nil
}

func (s *Server) deleteSnapshot(r *http.Request) (any, error) {
	ds, err := s.snapDataset(r)
	if err != nil {
		return nil, err
	}
	snap := r.PathValue("snap")
	if _, ok := SnapTime(snap); !ok {
		return nil, bad("%q is not a keelNAS snapshot", snap)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.listSnapshots()
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(all[ds], func(x Snap) bool { return x.Name == snap }) {
		return nil, notFound("no snapshot %s", snap)
	}
	if _, err := s.run("", "zfs", "destroy", ds+"@"+snap); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

type Settings struct {
	Snapshots Retention `json:"snapshots"`
}

func (s *Server) getSettings(r *http.Request) (any, error) {
	return Settings{Snapshots: s.State().Snapshots}, nil
}

// putSettings: {"snapshots": {"hourly", "daily", "weekly"}}; a class left out keeps its value.
func (s *Server) putSettings(r *http.Request) (any, error) {
	var req struct {
		Snapshots *struct {
			Hourly *int `json:"hourly"`
			Daily  *int `json:"daily"`
			Weekly *int `json:"weekly"`
		} `json:"snapshots"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if p := req.Snapshots; p != nil {
		for _, x := range []struct {
			v   *int
			dst *int
			n   string
		}{{p.Hourly, &st.Snapshots.Hourly, "hourly"}, {p.Daily, &st.Snapshots.Daily, "daily"}, {p.Weekly, &st.Snapshots.Weekly, "weekly"}} {
			if x.v == nil {
				continue
			}
			if *x.v < 0 || *x.v > 1000 {
				return nil, bad("snapshots.%s must be 0 to 1000", x.n)
			}
			*x.dst = *x.v
		}
	}
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return Settings{Snapshots: st.Snapshots}, nil
}

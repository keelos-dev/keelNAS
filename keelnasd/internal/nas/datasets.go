package nas

// Datasets (docs/06): the storage page's dataset tree with each dataset's owner, making and
// destroying datasets keelNAS made ("ours", State.Datasets), a read-only file listing. The
// owner is changed by a job (jobs.go).

import (
	"errors"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"keelnas/internal/sys"
)

// ShareRef is a dataset's share as the tree shows it.
type ShareRef struct {
	Name string   `json:"name"`
	RW   []string `json:"rw"`
	RO   []string `json:"ro"`
}

// DSOwner is who owns a dataset's root directory: User is the keelNAS (or keelAuth) user with
// that uid, "" when none is.
type DSOwner struct {
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
	User string `json:"user"`
}

// DatasetInfo is one row of GET /v1/storage/{pool}/datasets.
type DatasetInfo struct {
	Name       string    `json:"name"`
	Mountpoint string    `json:"mountpoint"`
	Mounted    bool      `json:"mounted"`
	Used       int64     `json:"used"`
	Avail      int64     `json:"avail"`
	Ours       bool      `json:"ours"`
	Share      *ShareRef `json:"share"`
	Snapshots  int       `json:"snapshots"`
	// AutoSnapshot: it gets the scheduled snapshots (State.AutoSnapOn; PUT …/auto-snapshot)
	AutoSnapshot bool     `json:"auto_snapshot"`
	Owner        *DSOwner `json:"owner"`     // nil: not mounted
	Shareable    bool     `json:"shareable"` // ours, or its owner is a keelNAS user
	Job          *Job     `json:"job"`       // the owner change running on it, nil = none
}

// ParseDatasetTree reads `zfs list -H -p -r -t filesystem -o name,used,avail,mountpoint,mounted`.
func ParseDatasetTree(out string) []DatasetInfo {
	res := []DatasetInfo{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 5 || f[0] == "" {
			continue
		}
		used, _ := strconv.ParseInt(f[1], 10, 64)
		avail, _ := strconv.ParseInt(f[2], 10, 64)
		res = append(res, DatasetInfo{Name: f[0], Used: used, Avail: avail, Mountpoint: f[3], Mounted: f[4] == "yes"})
	}
	slices.SortFunc(res, func(a, b DatasetInfo) int { return strings.Compare(a.Name, b.Name) })
	return res
}

// inPool: dataset is the pool's root or below it.
func inPool(pool, dataset string) bool {
	return dataset == pool || strings.HasPrefix(dataset, pool+"/")
}

// datasetTree is every file system of a pool with what keelNAS knows about it.
func (s *Server) datasetTree(st *State, pool string) ([]DatasetInfo, error) {
	out, err := s.run("", "zfs", "list", "-H", "-p", "-r", "-t", "filesystem", "-o", "name,used,avail,mountpoint,mounted", pool)
	if err != nil {
		return nil, err
	}
	res := ParseDatasetTree(out)
	snaps := map[string]int{}
	if so, err := s.Sys.Run("", "zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name", "-r", pool); err == nil {
		for _, line := range strings.Split(so, "\n") {
			if i := strings.IndexByte(line, '@'); i > 0 {
				snaps[line[:i]]++
			}
		}
	}
	var paths []string
	for _, d := range res {
		if d.Mounted && strings.HasPrefix(d.Mountpoint, "/") {
			paths = append(paths, d.Mountpoint)
		}
	}
	owners := map[string]DSOwner{}
	if len(paths) > 0 {
		so, _ := s.Sys.Run("", "stat", append([]string{"-f", "%u:%g %N"}, paths...)...)
		for _, line := range strings.Split(so, "\n") {
			ids, path, ok := strings.Cut(line, " ")
			a, b, ok2 := strings.Cut(ids, ":")
			uid, err1 := strconv.Atoi(a)
			gid, err2 := strconv.Atoi(b)
			if ok && ok2 && err1 == nil && err2 == nil {
				owners[path] = DSOwner{UID: uid, GID: gid, User: st.UserByID(uid)}
			}
		}
	}
	for i := range res {
		d := &res[i]
		d.Ours = st.Ours(d.Name)
		d.Snapshots = snaps[d.Name]
		d.AutoSnapshot = st.AutoSnapOn(d.Name)
		if sh := st.ShareOf(d.Name); sh != nil {
			d.Share = &ShareRef{sh.Name, sh.RW, sh.RO}
		}
		if o, ok := owners[d.Mountpoint]; ok && d.Mounted {
			d.Owner = &o
			d.Shareable = d.Ours || o.User != ""
		}
		d.Job = s.runningJob(d.Name)
	}
	return res, nil
}

// findDataset is one file system of a pool in the state: 404 for an unknown pool or dataset.
func (s *Server) findDataset(st *State, pool, name string) (DatasetInfo, error) {
	if st.Pool(pool) == nil {
		return DatasetInfo{}, notFound("no storage %s", pool)
	}
	if !inPool(pool, name) {
		return DatasetInfo{}, notFound("no dataset %s in storage %s", name, pool)
	}
	tree, err := s.datasetTree(st, pool)
	if err != nil {
		return DatasetInfo{}, err
	}
	for _, d := range tree {
		if d.Name == name {
			return d, nil
		}
	}
	return DatasetInfo{}, notFound("no dataset %s in storage %s", name, pool)
}

func (s *Server) getDatasets(r *http.Request) (any, error) {
	st := s.State()
	pool := r.PathValue("pool")
	if st.Pool(pool) == nil {
		return nil, notFound("no storage %s", pool)
	}
	tree, err := s.datasetTree(st, pool)
	if err != nil {
		return nil, err
	}
	return map[string]any{"datasets": tree}, nil
}

// putAutoSnapshot switches a dataset's scheduled snapshots on or off: {"on": bool}. Setting it
// back to its default (a share's dataset on, any other off) forgets the choice. Snapshots it
// already has stay; with the schedule off none are taken or pruned any more.
func (s *Server) putAutoSnapshot(r *http.Request) (any, error) {
	pool, name := r.PathValue("pool"), r.PathValue("name")
	var req struct {
		On *bool `json:"on"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.On == nil {
		return nil, bad("on: true or false")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if _, err := s.findDataset(st, pool, name); err != nil {
		return nil, err
	}
	if st.AutoSnap == nil {
		st.AutoSnap = map[string]bool{}
	}
	delete(st.AutoSnap, name)
	if st.AutoSnapOn(name) != *req.On {
		st.AutoSnap[name] = *req.On
	}
	if len(st.AutoSnap) == 0 {
		st.AutoSnap = nil
	}
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return map[string]any{"dataset": name, "auto_snapshot": *req.On}, nil
}

// postDataset makes a dataset (not shared): {"parent", "name"}. Below <pool>/shares it is
// mounted at /shares/<name> like the shares keelNAS always made; below a parent that is not
// mounted at "/<dataset>"; otherwise it inherits the parent's mountpoint. It is ours, owned
// root:keelnas 2770 like every dataset of ours.
func (s *Server) postDataset(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	var req struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !ValidName(req.Name) {
		return nil, bad("bad dataset name %q: lower-case letters, digits, - and _, a letter first, at most 32", req.Name)
	}
	if req.Parent == "" {
		req.Parent = pool + "/shares"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if st.Pool(pool) == nil {
		return nil, notFound("no storage %s", pool)
	}
	if !inPool(pool, req.Parent) {
		return nil, bad("parent %s is not in storage %s", req.Parent, pool)
	}
	ds := s.datasets()
	if req.Parent == pool+"/shares" {
		if err := s.ensureParent(req.Parent, ds); err != nil {
			return nil, err
		}
	}
	parent, ok := ds[req.Parent]
	if !ok || parent.Type != "filesystem" {
		return nil, notFound("no dataset %s", req.Parent)
	}
	full := req.Parent + "/" + req.Name
	if _, ok := ds[full]; ok {
		return nil, conflict("dataset %s exists", full)
	}
	args := []string{"create"}
	switch {
	case req.Parent == pool+"/shares":
		mp := SharePath(req.Name)
		path := mp
		if p := st.Pool(pool); p.Altroot != "" {
			path = p.Altroot + mp
		}
		for _, d := range ds {
			if d.Mountpoint == path {
				return nil, conflict("%s is the mount point of %s already", path, d.Name)
			}
		}
		args = append(args, "-o", "mountpoint="+mp)
	case !strings.HasPrefix(parent.Mountpoint, "/"):
		args = append(args, "-o", "mountpoint=/"+full)
	}
	if _, err := s.run("", "zfs", append(args, full)...); err != nil {
		return nil, err
	}
	out, err := s.run("", "zfs", "get", "-H", "-o", "value", "mountpoint", full)
	if err != nil {
		return nil, err
	}
	path := strings.TrimSpace(out)
	if _, err := s.run("", "chown", "root:"+Group, path); err != nil {
		return nil, err
	}
	if _, err := s.run("", "chmod", "2770", path); err != nil {
		return nil, err
	}
	st.Datasets = append(st.Datasets, full)
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return s.findDataset(st, pool, full)
}

// deleteDataset destroys a dataset of ours (with its snapshots); never one keelNAS did not make
// (403), not a shared one or one with a share below (409), not one with datasets below (409).
func (s *Server) deleteDataset(r *http.Request) (any, error) {
	pool, name := r.PathValue("pool"), r.PathValue("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if _, err := s.findDataset(st, pool, name); err != nil {
		return nil, err
	}
	if !st.Ours(name) {
		return nil, forbidden("dataset %s was not made by keelNAS: keelNAS never destroys it", name)
	}
	for _, sh := range st.Shares {
		if sh.Dataset == name || strings.HasPrefix(sh.Dataset, name+"/") {
			return nil, conflict("dataset %s is shared as %s: remove the share first", sh.Dataset, sh.Name)
		}
	}
	for n := range s.datasets() {
		if strings.HasPrefix(n, name+"/") {
			return nil, conflict("dataset %s has %s below it: delete that first", name, n)
		}
	}
	if _, err := s.run("", "zfs", "destroy", "-r", name); err != nil {
		return nil, err
	}
	st.Datasets = slices.DeleteFunc(st.Datasets, func(d string) bool { return d == name })
	delete(st.AutoSnap, name)
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// maxEntries is the most entries one listing returns.
const maxEntries = 5000

// getFiles lists a directory of a dataset, read-only (docs/06 §2): never leaving the dataset's
// mountpoint, symbolic links listed but not followed.
func (s *Server) getFiles(r *http.Request) (any, error) {
	pool, name := r.PathValue("pool"), r.PathValue("name")
	rel := r.URL.Query().Get("path")
	clean, err := sys.CleanRel(rel)
	if err != nil {
		return nil, bad("bad path %q: %v", rel, err)
	}
	d, err := s.findDataset(s.State(), pool, name)
	if err != nil {
		return nil, err
	}
	if !d.Mounted || !strings.HasPrefix(d.Mountpoint, "/") {
		return nil, conflict("dataset %s is not mounted", name)
	}
	ents, trunc, err := sys.ListDir(d.Mountpoint, clean, maxEntries)
	switch {
	case errors.Is(err, sys.ErrEscape):
		return nil, bad("bad path %q: %v", rel, err)
	case errors.Is(err, sys.ErrNotDir):
		return nil, bad("%s is not a directory", rel)
	case errors.Is(err, fs.ErrNotExist):
		return nil, notFound("no directory %s in %s", rel, name)
	case err != nil:
		return nil, err
	}
	if clean == "." {
		clean = ""
	}
	return map[string]any{"dataset": name, "path": clean, "entries": ents, "truncated": trunc}, nil
}

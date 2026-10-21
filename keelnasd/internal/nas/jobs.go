package nas

// Background jobs (docs/06 §2): changing the owner of a dataset's files ("chown") can take hours
// on a big dataset, so it runs here and the API shows its progress.

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"keelnas/internal/sys"
)

// Job is one background job as the API shows it.
type Job struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"` // chown
	Dataset  string   `json:"dataset"`
	User     string   `json:"user"`
	State    string   `json:"state"` // counting, running, done, failed
	Files    int64    `json:"files"` // done so far
	Total    int64    `json:"total"` // files to do (0 while counting)
	Bytes    int64    `json:"bytes"`
	TotalB   int64    `json:"total_bytes"`
	Failed   int64    `json:"failed"` // entries that could not be changed
	Errors   []string `json:"errors"` // the first of them
	Error    string   `json:"error"`  // why the job failed
	Started  string   `json:"started"`
	Finished string   `json:"finished"`
}

type job struct {
	mu                                Job // fields below the counters, under jobs.mu
	files, total, bytes, totalB, fail atomic.Int64
}

type jobs struct {
	mu   sync.Mutex
	next int
	list []*job
}

// snapshot of a job with its counters.
func (j *job) view() Job {
	v := j.mu
	v.Files, v.Total, v.Bytes, v.TotalB, v.Failed = j.files.Load(), j.total.Load(), j.bytes.Load(), j.totalB.Load(), j.fail.Load()
	v.Errors = slices.Clone(v.Errors)
	if v.Errors == nil {
		v.Errors = []string{}
	}
	return v
}

func (s *Server) jobList() []Job {
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	res := []Job{}
	for _, j := range s.jobs.list {
		res = append(res, j.view())
	}
	return res
}

// runningJob is the unfinished job on a dataset, nil = none.
func (s *Server) runningJob(dataset string) *Job {
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	for _, j := range s.jobs.list {
		if j.mu.Dataset == dataset && (j.mu.State == "counting" || j.mu.State == "running") {
			v := j.view()
			return &v
		}
	}
	return nil
}

func (s *Server) getJobs(r *http.Request) (any, error) {
	return map[string]any{"jobs": s.jobList()}, nil
}

func (s *Server) getJob(r *http.Request) (any, error) {
	id := r.PathValue("id")
	for _, j := range s.jobList() {
		if j.ID == id {
			return j, nil
		}
	}
	return nil, notFound("no job %s", id)
}

// chownGID is the group a changed dataset gets (a variable for the tests, which cannot chown to
// another group).
var chownGID = GroupID

const (
	chownDirMode  = os.ModeSetgid | 0o770 // like ensureShare's 2770
	chownFileMode = 0o660                 // like Samba's create mask
)

// chownSkip: below a dataset's root, never changed (snapshots; keelNAS's copy of its settings).
var chownSkip = []string{".zfs", ".keelnas"}

// postOwner starts changing the owner of a dataset's files: {"user"} (a keelNAS or keelAuth
// user). Everything on that dataset (not below it in child datasets, links not followed) gets
// user:keelnas, directories 2770, files 0660, as on a dataset keelNAS makes; afterwards it can be
// shared like one. Not the root of a storage: it holds keelNAS's own files.
func (s *Server) postOwner(r *http.Request) (any, error) {
	pool, name := r.PathValue("pool"), r.PathValue("name")
	var req struct {
		User string `json:"user"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	st := s.State()
	uid, ok := st.UserID(req.User)
	if !ok {
		return nil, bad("no keelNAS user %q", req.User)
	}
	if name == pool {
		return nil, bad("%s is the root of the storage, which holds keelNAS's own files: change the owner of a dataset in it", name)
	}
	d, err := s.findDataset(st, pool, name)
	if err != nil {
		return nil, err
	}
	if !d.Mounted || len(d.Mountpoint) == 0 || d.Mountpoint[0] != '/' {
		return nil, conflict("dataset %s is not mounted", name)
	}
	s.jobs.mu.Lock()
	for _, j := range s.jobs.list {
		if j.mu.Dataset == name && (j.mu.State == "counting" || j.mu.State == "running") {
			s.jobs.mu.Unlock()
			return nil, conflict("the owner of %s is being changed already (job %s)", name, j.mu.ID)
		}
	}
	s.jobs.next++
	j := &job{mu: Job{ID: strconv.Itoa(s.jobs.next), Kind: "chown", Dataset: name, User: req.User, State: "counting",
		Errors: []string{}, Started: s.now().Format(time.RFC3339)}}
	s.jobs.list = append(s.jobs.list, j)
	if len(s.jobs.list) > 50 { // finished ones are forgotten after a while
		s.jobs.list = s.jobs.list[len(s.jobs.list)-50:]
	}
	s.jobs.mu.Unlock()
	s.Log.Printf("job %s: chown %s to %s:%s (uid %d)", j.mu.ID, name, Group, req.User, uid)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runChown(j, d.Mountpoint, uid)
	}()
	if s.syncJobs { // tests
		<-done
	}
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	return j.view(), nil
}

func (s *Server) runChown(j *job, root string, uid int) {
	finish := func(state, why string, errs []error) {
		s.jobs.mu.Lock()
		j.mu.State, j.mu.Error, j.mu.Finished = state, why, s.now().Format(time.RFC3339)
		for _, e := range errs {
			j.mu.Errors = append(j.mu.Errors, e.Error())
		}
		s.jobs.mu.Unlock()
		s.Log.Printf("job %s: %s %s (%d files, %d failed) %s", j.mu.ID, j.mu.Dataset, state, j.files.Load(), j.fail.Load(), why)
	}
	// first count, so the page can show how far it is
	_, err := sys.WalkTree(root, chownSkip, func(_ *os.Root, e sys.TreeEntry) error {
		j.total.Add(1)
		if e.Info.Mode().IsRegular() {
			j.totalB.Add(e.Info.Size())
		}
		return nil
	})
	if err != nil {
		finish("failed", err.Error(), nil)
		return
	}
	s.jobs.mu.Lock()
	j.mu.State = "running"
	s.jobs.mu.Unlock()
	errs, err := sys.WalkTree(root, chownSkip, func(r *os.Root, e sys.TreeEntry) error {
		err := sys.ChownEntry(r, e, uid, chownGID, chownDirMode, chownFileMode)
		j.files.Add(1)
		if e.Info.Mode().IsRegular() {
			j.bytes.Add(e.Info.Size())
		}
		if err != nil {
			j.fail.Add(1)
		}
		return err
	})
	switch {
	case err != nil:
		finish("failed", err.Error(), errs)
	case len(errs) > 0:
		finish("failed", fmt.Sprintf("%d entries could not be changed or read", max(j.fail.Load(), int64(len(errs)))), errs)
	default:
		finish("done", "", nil)
	}
}

package nas

// Folders of a share with access lists of their own (docs/07). A folder is a plain directory in
// the share's root; its list IS its NFSv4 ACL: nothing is kept in state.json, so what this page
// sets and what the owner or a manager sets in Windows' security tab is the same thing, and
// each side sees the other's changes. A folder whose named entries are all inherited is "like
// the share"; one with entries of its own (inheritance switched off, in Windows' words) lets in
// only who is named there.
//
// When a list changes, the files below still carry the entries they inherited before: a job
// (kind "acl") rewrites them the way Windows does, each file getting its own explicit entries
// plus what its directory hands down. What keeps people out meanwhile is the folder's own ACL,
// which is changed at once.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"keelnas/internal/sys"
)

// ---- ACL entries, as getfacl prints them: who:perms:flags:type ----

type ace struct{ who, perm, flags, kind string }

func parseACE(line string) (ace, bool) {
	f := strings.Split(strings.TrimSpace(line), ":")
	n := len(f)
	if n < 4 {
		return ace{}, false
	}
	return ace{who: strings.Join(f[:n-3], ":"), perm: f[n-3], flags: f[n-2], kind: f[n-1]}, true
}

func (a ace) String() string { return a.who + ":" + a.perm + ":" + a.flags + ":" + a.kind }
func (a ace) named() bool {
	return strings.HasPrefix(a.who, "user:") || strings.HasPrefix(a.who, "group:")
}
func (a ace) inherited() bool { return strings.Contains(a.flags, "I") }

func parseACL(lines []string) []ace {
	var acl []ace
	for _, l := range lines {
		if a, ok := parseACE(l); ok {
			acl = append(acl, a)
		}
	}
	return acl
}

func aclStrings(acl []ace) []string {
	out := make([]string, len(acl))
	for i, a := range acl {
		out[i] = a.String()
	}
	return out
}

// ownACL: the ACL does not come from the directory above: it has named entries and none of them
// is inherited, or it was emptied (no named entry and not even the owner's). Such a folder (or
// file) is somebody's decision; a rewrite leaves it and what is below it alone.
func ownACL(acl []ace) bool {
	named, owner := 0, false
	for _, a := range acl {
		if a.named() {
			if a.inherited() {
				return false
			}
			named++
		}
		if a.who == "owner@" {
			owner = true
		}
	}
	return named > 0 || !owner
}

// handedDown is what a directory with this ACL gives a new file or directory in it (ZFS with
// aclinherit=passthrough): the entries flagged for it, marked inherited.
func handedDown(parent []ace, dir bool) []ace {
	var out []ace
	for _, a := range parent {
		f, d, n := strings.Contains(a.flags, "f"), strings.Contains(a.flags, "d"), strings.Contains(a.flags, "n")
		switch {
		case dir && d && !n:
			fl := "-d----I"
			if f {
				fl = "fd----I"
			}
			out = append(out, ace{a.who, a.perm, fl, a.kind})
		case dir && d, !dir && f:
			out = append(out, ace{a.who, a.perm, "------I", a.kind})
		case dir && f && !n: // for the files further down only
			out = append(out, ace{a.who, a.perm, "f-i---I", a.kind})
		}
	}
	return out
}

// rewritten is a file's ACL after a rewrite: the entries somebody put on this file (named, not
// inherited) and then what its directory hands down.
func rewritten(old, down []ace) []ace {
	var out []ace
	for _, a := range old {
		if a.named() && !a.inherited() {
			out = append(out, a)
		}
	}
	return append(out, down...)
}

// getACLs reads the ACLs of paths (entry lines each), one getfacl for all of them.
func (s *Server) getACLs(paths []string) [][]string {
	res := make([][]string, 0, len(paths))
	for len(paths) > 0 {
		n := min(len(paths), 200)
		chunk := paths[:n]
		paths = paths[n:]
		out, _ := s.Sys.Run("", "getfacl", chunk...)
		var blocks [][]string
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			switch {
			case strings.HasPrefix(l, "# file:"):
				blocks = append(blocks, []string{})
			case l == "" || strings.HasPrefix(l, "#") || len(blocks) == 0:
			default:
				blocks[len(blocks)-1] = append(blocks[len(blocks)-1], l)
			}
		}
		if len(blocks) != len(chunk) { // a file went away meanwhile, or a name with a line break
			blocks = blocks[:0]
			for _, p := range chunk {
				out, _ := s.Sys.Run("", "getfacl", "-q", p)
				blocks = append(blocks, strings.Fields(out))
			}
		}
		res = append(res, blocks...)
	}
	return res
}

// setACL gives paths, which all have had entries now, the ACL acl, in one step per file: the new
// entries go in front and the old ones are taken out behind them (setfacl -m would merge two
// entries of one name, and -b first would leave a file with only its mode bits for a moment).
func (s *Server) setACL(paths []string, had int, acl []string) error {
	if len(acl) == 0 {
		acl = []string{"everyone@:" + aclNone + ":-------:allow"}
	}
	args := []string{"-a", "0", strings.Join(acl, ",")}
	for range had {
		args = append(args, "-x", strconv.Itoa(len(acl)))
	}
	_, err := s.run("", "setfacl", append(args, paths...)...)
	return err
}

// ---- the folders of a share ----

// Folder is a first-level folder of a share as the API shows it.
type Folder struct {
	Name string `json:"name"`
	// Inherit: like the share (its named entries are inherited). RW and RO are then the names
	// somebody added on top, usually none.
	Inherit bool     `json:"inherit"`
	RW      []string `json:"rw"`
	RO      []string `json:"ro"`
	// Other: it has entries this page does not show as read-write or read-only (set in
	// Windows: special rights, deny entries, entries for the folder only); ACL has them all.
	Other bool     `json:"other"`
	ACL   []string `json:"acl"`
}

// shareName is an ACL principal as a share names it: user:alice -> alice, group:office -> @office.
func shareName(who string) string {
	if g, ok := strings.CutPrefix(who, "group:"); ok {
		return "@" + g
	}
	return strings.TrimPrefix(who, "user:")
}

func folderView(name string, lines []string, sh Share) Folder {
	acl := parseACL(lines)
	f := Folder{Name: name, Inherit: !ownACL(acl), RW: []string{}, RO: []string{}, ACL: aclStrings(acl)}
	for _, a := range acl {
		if !a.named() || a.inherited() {
			if !a.named() && a.kind != "allow" {
				f.Other = true
			}
			continue
		}
		n := shareName(a.who)
		has := func(c string) bool { return strings.Contains(a.perm, c) }
		whole := a.kind == "allow" && strings.Contains(a.flags, "f") && strings.Contains(a.flags, "d") && !strings.ContainsAny(a.flags, "in")
		switch {
		case whole && has("C") && slices.Contains(sh.Managers, n): // a manager: in every folder
		case whole && has("r") && has("w") && has("x") && !has("C"):
			f.RW = append(f.RW, n)
		case whole && has("r") && has("x") && !has("w") && !has("p") && !has("C"):
			f.RO = append(f.RO, n)
		default:
			f.Other = true
		}
	}
	slices.Sort(f.RW)
	slices.Sort(f.RO)
	return f
}

// folderDirs: the directories in a share's root (not links, not .zfs or keelNAS's own).
func folderDirs(root string) []string {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() && !slices.Contains(chownSkip, e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names
}

func (s *Server) folders(sh Share, root string) []Folder {
	names := folderDirs(root)
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = filepath.Join(root, n)
	}
	res := []Folder{}
	for i, lines := range s.getACLs(paths) {
		res = append(res, folderView(names[i], lines, sh))
	}
	return res
}

// folderGuests: the names in the lists of the share's folders that are not in the share's own
// lists. They must pass the share's root (ShareACL) and Samba's "valid users" to reach their folder.
func (s *Server) folderGuests(st *State, sh Share, root string) []string {
	var out []string
	for _, f := range s.folders(sh, root) {
		for _, n := range append(slices.Clone(f.RW), f.RO...) {
			if aclWho(st, n) != "" && !slices.Contains(sh.RW, n) && !slices.Contains(sh.RO, n) && !slices.Contains(sh.Managers, n) {
				out = append(out, n)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// validFolderName: what Windows takes as a folder name (Japanese is fine).
func validFolderName(n string) bool {
	if n == "" || len(n) > 255 || n == "." || n == ".." || strings.ContainsAny(n, "\\/:*?\"<>|") ||
		strings.HasSuffix(n, " ") || strings.HasSuffix(n, ".") || strings.HasPrefix(n, " ") || slices.Contains(chownSkip, n) {
		return false
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// shareRootOf: a share and where it is mounted.
func (s *Server) shareRootOf(st *State, name string) (*Share, string, error) {
	sh := st.Share(name)
	if sh == nil {
		return nil, "", notFound("no share %s", name)
	}
	p := s.datasets()[sh.Dataset].Mountpoint
	if !strings.HasPrefix(p, "/") {
		return nil, "", conflict("share %s: dataset %s is not mounted", name, sh.Dataset)
	}
	return sh, p, nil
}

func (s *Server) getFolders(r *http.Request) (any, error) {
	st := s.State()
	sh, root, err := s.shareRootOf(st, r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"folders": s.folders(*sh, root), "job": s.runningJob(sh.Dataset)}, nil
}

type folderReq struct {
	Name    string   `json:"name"`
	Inherit bool     `json:"inherit"`
	RW      []string `json:"rw"`
	RO      []string `json:"ro"`
}

// checkFolderAccess: like checkAccess, but an empty list is fine when the share has managers.
func checkFolderAccess(st *State, sh *Share, req *folderReq) error {
	req.RW, req.RO = dedup(req.RW), dedup(req.RO)
	if req.Inherit {
		if len(req.RW)+len(req.RO) > 0 {
			return bad("give a list, or inherit, not both")
		}
		return nil
	}
	if len(req.RW)+len(req.RO) == 0 {
		if len(sh.Managers) == 0 {
			return bad("a folder needs somebody who may enter it")
		}
		return nil
	}
	return checkAccess(st, req.RW, req.RO)
}

// setFolder puts the list on a folder at once (so that who lost access is out now), lets the
// names that are new to the share pass its root, and starts the rewrite of what is below.
// Called with s.mu held.
func (s *Server) setFolder(st *State, sh *Share, root, name string, req folderReq) (*Job, error) {
	path := filepath.Join(root, name)
	if j := s.runningJob(sh.Dataset); j != nil {
		return nil, conflict("the files of %s are being changed (job %s): try again when that is done", sh.Dataset, j.ID)
	}
	var want []string
	if req.Inherit {
		want = aclStrings(handedDown(parseACL(s.getACLs([]string{root})[0]), true))
	} else {
		want = FolderACL(st, *sh, req.RW, req.RO)
	}
	have := s.getACLs([]string{path})[0]
	if !slices.Equal(have, want) {
		if err := s.setACL([]string{path}, len(have), want); err != nil {
			return nil, err
		}
	}
	s.applyLocked() // the share's root and smb4.conf, for names only this folder has
	j, err := s.startACL(sh.Dataset, name, []aclTask{{dir: path}})
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// postFolder makes a folder in a share's root: {"name"} like the share, or with {"rw","ro"}.
func (s *Server) postFolder(r *http.Request) (any, error) {
	var req folderReq
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !validFolderName(req.Name) {
		return nil, bad("bad folder name %q: not \\ / : * ? \" < > |, no space or dot at the end", req.Name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	sh, root, err := s.shareRootOf(st, r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	own := len(req.RW)+len(req.RO) > 0
	req.Inherit = !own
	if err := checkFolderAccess(st, sh, &req); err != nil {
		return nil, err
	}
	path := filepath.Join(root, req.Name)
	if err := os.Mkdir(path, 0o770); err != nil {
		if os.IsExist(err) {
			return nil, conflict("%s exists in %s", req.Name, sh.Name)
		}
		return nil, err
	}
	s.Log.Printf("share %s: folder %q made", sh.Name, req.Name)
	if own {
		if _, err := s.setFolder(st, sh, root, req.Name, req); err != nil {
			return nil, err
		}
	}
	return folderView(req.Name, s.getACLs([]string{path})[0], *sh), nil
}

// putFolder sets a folder's list: {"rw","ro"}, or {"inherit": true} = like the share again.
func (s *Server) putFolder(r *http.Request) (any, error) {
	var req folderReq
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	name := r.PathValue("folder")
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	sh, root, err := s.shareRootOf(st, r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	if !slices.Contains(folderDirs(root), name) {
		return nil, notFound("no folder %q in %s", name, sh.Name)
	}
	if err := checkFolderAccess(st, sh, &req); err != nil {
		return nil, err
	}
	j, err := s.setFolder(st, sh, root, name, req)
	if err != nil {
		return nil, err
	}
	s.Log.Printf("share %s: folder %q: inherit %v, rw %v, ro %v (job %s)", sh.Name, name, req.Inherit, req.RW, req.RO, j.ID)
	return map[string]any{"folder": folderView(name, s.getACLs([]string{filepath.Join(root, name)})[0], *sh), "job": j}, nil
}

// deleteFolder removes an empty folder.
func (s *Server) deleteFolder(r *http.Request) (any, error) {
	name := r.PathValue("folder")
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, root, err := s.shareRootOf(s.st, r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	if !slices.Contains(folderDirs(root), name) {
		return nil, notFound("no folder %q in %s", name, sh.Name)
	}
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		return nil, conflict("%s is not empty: only an empty folder is deleted here", name)
	}
	s.Log.Printf("share %s: folder %q deleted", sh.Name, name)
	return map[string]any{}, nil
}

// postShareACL rewrites the inherited entries of everything in the share ("apply the
// permissions again": after files were moved between folders, which keeps their old entries).
func (s *Server) postShareACL(r *http.Request) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, root, err := s.shareRootOf(s.st, r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	return s.startACL(sh.Dataset, "", []aclTask{{dir: root, skipTop: chownSkip}})
}

// managersChanged puts the share's new managers into the folders with a list of their own
// (their ACLs name the managers; the other folders inherit them from the root) and returns the
// folders it changed, for the rewrite below them.
func (s *Server) managersChanged(st *State, sh Share, old []string, root string) []aclTask {
	var tasks []aclTask
	for _, f := range s.folders(sh, root) {
		if f.Inherit {
			continue
		}
		path := filepath.Join(root, f.Name)
		have := s.getACLs([]string{path})[0]
		var want []string
		for _, n := range sorted(sh.Managers) {
			if w := aclWho(st, n); w != "" {
				want = append(want, w+":"+aclFull+":fd-----:allow")
			}
		}
		for _, a := range parseACL(have) {
			n := shareName(a.who)
			if a.named() && (slices.Contains(sh.Managers, n) || slices.Contains(old, n) && strings.Contains(a.perm, "C")) {
				continue
			}
			want = append(want, a.String())
		}
		if slices.Equal(have, want) {
			continue
		}
		if err := s.setACL([]string{path}, len(have), want); err != nil {
			s.Log.Printf("share %s: folder %q: %v", sh.Name, f.Name, err)
			continue
		}
		tasks = append(tasks, aclTask{dir: path})
	}
	return tasks
}

// ---- the rewrite job ----

// aclTask: rewrite everything below dir from dir's ACL as it is now.
type aclTask struct {
	dir     string
	skipTop []string
}

func (s *Server) startACL(dataset, what string, tasks []aclTask) (Job, error) {
	s.jobs.mu.Lock()
	for _, j := range s.jobs.list {
		if j.mu.Dataset == dataset && (j.mu.State == "counting" || j.mu.State == "running") {
			s.jobs.mu.Unlock()
			return Job{}, conflict("the files of %s are being changed already (job %s)", dataset, j.mu.ID)
		}
	}
	s.jobs.next++
	j := &job{mu: Job{ID: strconv.Itoa(s.jobs.next), Kind: "acl", Dataset: dataset, Path: what, State: "counting",
		Errors: []string{}, Started: s.now().Format(time.RFC3339)}}
	s.jobs.list = append(s.jobs.list, j)
	if len(s.jobs.list) > 50 {
		s.jobs.list = s.jobs.list[len(s.jobs.list)-50:]
	}
	s.jobs.mu.Unlock()
	s.Log.Printf("job %s: permissions of %s %q", j.mu.ID, dataset, what)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runACL(j, tasks)
	}()
	if s.syncJobs { // tests
		<-done
	}
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	return j.view(), nil
}

func (s *Server) runACL(j *job, tasks []aclTask) {
	finish := func(state, why string, errs []string) {
		s.jobs.mu.Lock()
		j.mu.State, j.mu.Error, j.mu.Finished = state, why, s.now().Format(time.RFC3339)
		j.mu.Errors = append(j.mu.Errors, errs...)
		s.jobs.mu.Unlock()
		s.Log.Printf("job %s: %s %s (%d entries, %d failed) %s", j.mu.ID, j.mu.Dataset, state, j.files.Load(), j.fail.Load(), why)
	}
	for _, t := range tasks { // count, so the page can show how far it is
		sys.WalkTree(t.dir, t.skipTop, func(_ *os.Root, e sys.TreeEntry) error {
			j.total.Add(1)
			return nil
		})
	}
	s.jobs.mu.Lock()
	j.mu.State = "running"
	s.jobs.mu.Unlock()
	var errs []string
	for _, t := range tasks {
		fi, err := os.Lstat(t.dir)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		s.rewriteDir(j, t.dir, parseACL(s.getACLs([]string{t.dir})[0]), devOf(fi), t.skipTop, &errs)
	}
	if len(errs) > 0 {
		finish("failed", fmt.Sprintf("%d entries could not be changed or read", max(j.fail.Load(), int64(len(errs)))), errs)
		return
	}
	j.total.Store(j.files.Load()) // the count was an upper bound: links and folders left alone are in it
	finish("done", "", nil)
}

func devOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}

// rewriteDir gives everything in dir the ACL it would have inherited from dir (acl), keeping
// each entry's own explicit names, and goes on below. Links are not touched, other datasets
// not entered, and what has an ACL of its own (ownACL) is left with all below it.
func (s *Server) rewriteDir(j *job, dir string, acl []ace, dev uint64, skip []string, errs *[]string) {
	note := func(err error) {
		j.fail.Add(1)
		if len(*errs) < 20 {
			*errs = append(*errs, err.Error())
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		note(err)
		return
	}
	var paths []string
	var isDir []bool
	for _, e := range ents {
		if e.Type()&os.ModeSymlink != 0 || slices.Contains(skip, e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if fi, err := os.Lstat(p); err != nil || devOf(fi) != dev {
				continue // another dataset mounted here
			}
		} else if !e.Type().IsRegular() {
			continue
		}
		paths = append(paths, p)
		isDir = append(isDir, e.IsDir())
	}
	downD, downF := handedDown(acl, true), handedDown(acl, false)
	type sub struct {
		path string
		acl  []ace
	}
	var subs []sub
	type group struct {
		had   int
		acl   []string
		paths []string
	}
	groups := map[string]*group{}
	var order []string
	for i, lines := range s.getACLs(paths) {
		j.files.Add(1)
		old := parseACL(lines)
		if len(old) == 0 { // gone meanwhile
			continue
		}
		if ownACL(old) {
			continue
		}
		down := downF
		if isDir[i] {
			down = downD
		}
		want := rewritten(old, down)
		ws := aclStrings(want)
		if isDir[i] {
			subs = append(subs, sub{paths[i], want})
		}
		if slices.Equal(lines, ws) {
			continue
		}
		k := strconv.Itoa(len(lines)) + "|" + strings.Join(ws, ",")
		g := groups[k]
		if g == nil {
			g = &group{had: len(lines), acl: ws}
			groups[k] = g
			order = append(order, k)
		}
		g.paths = append(g.paths, paths[i])
	}
	for _, k := range order {
		g := groups[k]
		for len(g.paths) > 0 {
			n := min(len(g.paths), 200)
			if err := s.setACL(g.paths[:n], g.had, g.acl); err != nil {
				note(err)
			}
			g.paths = g.paths[n:]
		}
	}
	for _, d := range subs {
		s.rewriteDir(j, d.path, d.acl, dev, nil, errs)
	}
}

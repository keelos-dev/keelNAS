package nas

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"keelnas/internal/sys"
)

// Server holds the state and does everything with it. One change at a time (mu).
type Server struct {
	Sys       sys.Sys
	StatePath string
	Version   string
	Log       *log.Logger
	// Heads reads the first bytes of disks (seed disk); nil = no seed lookup.
	Heads sys.HeadReader

	mu        sync.Mutex
	st        *State
	lastTime  time.Time
	lastErrs  []string
	lastMu    sync.Mutex
	startTime time.Time

	// Now is the clock (UTC); nil = time.Now. Tests set it.
	Now func() time.Time

	serial        int64  // of the last copy written to the pools
	pendingPasswd []byte // restored Samba passwords not imported yet

	jobs     jobs // background jobs (jobs.go)
	syncJobs bool // tests: a job is finished when its POST answers

	hwMu sync.Mutex // the hardware page's cached read (hardware.go)
	hw   *Hardware
	hwAt time.Time

	hmu          sync.Mutex // the fields below
	restoredFrom string
	smart        map[string]SmartInfo
	lastSmart    time.Time
	snapFails    map[string]string
	alertSince   map[string]time.Time
	seed         *Seed      // the seed found at the start, nil = none
	seedDisk     string     // its disk, hidden from the API
	reg          *Registrar // registration with keel, nil = no seed
}

// NewServer loads (or creates) state.json.
func NewServer(s sys.Sys, statePath, version string, lg *log.Logger) (*Server, error) {
	if lg == nil {
		lg = log.New(os.Stderr, "", log.LstdFlags)
	}
	st, created, err := LoadState(s, statePath)
	if err != nil {
		return nil, err
	}
	srv := &Server{Sys: s, StatePath: statePath, Version: version, Log: lg, st: st, startTime: time.Now(),
		smart: map[string]SmartInfo{}, snapFails: map[string]string{}, alertSince: map[string]time.Time{}}
	if hr, ok := s.(sys.HeadReader); ok {
		srv.Heads = hr
	}
	if created {
		_, rerr := s.ReadFile(statePath)
		if err := SaveState(s, statePath, st); err != nil {
			return nil, err
		}
		if rerr == nil {
			lg.Printf("rewrote %s (older form, or it had no key)", statePath)
		} else {
			lg.Printf("created %s with a new key", statePath)
		}
	}
	return srv, nil
}

// State is a copy of the current state.
func (s *Server) State() *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Clone()
}

func (s *Server) run(stdin, name string, args ...string) (string, error) {
	out, err := s.Sys.Run(stdin, name, args...)
	if err != nil {
		s.Log.Printf("command failed: %v", err)
	}
	return out, err
}

func (s *Server) hostname() string {
	out, err := s.Sys.Run("", "sysctl", "-n", "kern.hostname")
	if err != nil || strings.TrimSpace(out) == "" {
		return "keelnas"
	}
	h := strings.TrimSpace(out)
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return strings.ToLower(h)
}

// Apply turns the current state into the system. Errors are logged, collected and returned;
// every step goes on after a failure.
func (s *Server) Apply() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked()
}

func (s *Server) applyLocked() []string {
	st := s.st
	var errs []string
	fail := func(format string, a ...any) {
		m := fmt.Sprintf(format, a...)
		s.Log.Printf("apply: %s", m)
		errs = append(errs, m)
	}

	// 1. Pools: import by guid what is not imported yet.
	imported := s.importedPools()
	for _, p := range st.Pools {
		if _, ok := imported[p.GUID]; ok {
			continue
		}
		if err := s.importPool(p); err != nil {
			fail("pool %s: %v", p.Name, err)
		} else {
			s.Log.Printf("imported pool %s (guid %s)", p.Name, p.GUID)
		}
	}
	imported = s.importedPools()
	poolUp := map[string]bool{}
	for _, name := range imported {
		poolUp[name] = true
	}

	// 2. Group and users.
	if _, err := s.Sys.Run("", "pw", "groupshow", Group); err != nil {
		if _, err := s.run("", "pw", "groupadd", "-n", Group, "-g", strconv.Itoa(GroupID)); err != nil {
			fail("group %s: %v", Group, err)
		}
	}
	pw, err := s.pwUsers()
	if err != nil {
		fail("users: %v", err)
	} else {
		want := map[string]bool{}
		for _, u := range st.Users {
			want[u.Name] = true
			if err := s.ensureUser(u, pw); err != nil {
				fail("user %s: %v", u.Name, err)
			}
		}
		if st.Directory != nil {
			for _, u := range st.Directory.Users {
				if u.Problem == "" {
					want[u.Name] = true
				}
			}
		}
		// keelAuth's users and groups (directory.go)
		s.applyDirectory(st, pw, fail)
		// A keelNAS user (our group, our uid range) that is no longer in the state goes.
		for _, u := range pw {
			if u.GID == GroupID && u.UID >= FirstUID && !want[u.Name] {
				if _, err := s.run("", "pw", "userdel", "-n", u.Name); err != nil {
					fail("remove user %s: %v", u.Name, err)
				}
			}
		}
	}

	// 3. Datasets and zvols; where each share is.
	ds, dsErr := s.listDatasets()
	if dsErr != nil {
		fail("datasets: %v", dsErr)
	}
	places := map[string]string{}
	for _, sh := range st.Shares {
		if pool := PoolOf(sh.Dataset); !poolUp[pool] {
			fail("share %s: pool %s is not imported", sh.Name, pool)
			continue
		}
		path, err := s.ensureShare(st, sh, ds)
		if err != nil {
			fail("share %s: %v", sh.Name, err)
			continue
		}
		places[sh.Name] = path
	}
	for _, l := range st.LUNs {
		if !poolUp[l.Pool] {
			fail("lun %s: pool %s is not imported", l.Name, l.Pool)
			continue
		}
		if err := s.ensureLUN(l, ds); err != nil {
			fail("lun %s: %v", l.Name, err)
		}
	}

	// 4. Configuration files.
	if _, err := s.Sys.Run("", "install", "-d", "-m", "0700", SambaDir); err != nil {
		fail("%s: %v", SambaDir, err)
	}
	smbChanged := false
	if dsErr == nil { // without the datasets every share would look unmounted: keep the old file
		if smbChanged, err = s.writeIfChanged(SmbConf, SmbConfig(st, places), 0o644); err != nil {
			fail("%s: %v", SmbConf, err)
		}
	}
	ctlChanged, err := s.writeIfChanged(CtlConf, CtlConfig(st, s.hostname()), 0o600)
	if err != nil {
		fail("%s: %v", CtlConf, err)
	}
	// Restored passwords go in once the users and smb4.conf exist.
	if err := s.importPasswords(); err != nil {
		fail("restore the Samba passwords: %v", err)
	}

	// 5. Services: start what is not running, reload what has a new configuration.
	if err := s.samba(smbChanged); err != nil {
		fail("smbd: %v", err)
	}
	if err := s.ctld(ctlChanged); err != nil {
		fail("ctld: %v", err)
	}

	// 6. The copy of the state in every data pool.
	s.copyToPools(poolUp)

	s.lastMu.Lock()
	s.lastTime = time.Now().UTC()
	s.lastErrs = append([]string{}, errs...)
	s.lastMu.Unlock()
	if errs == nil {
		errs = []string{}
	}
	return errs
}

// importedPools: guid -> name.
func (s *Server) importedPools() map[string]string {
	m := map[string]string{}
	out, err := s.Sys.Run("", "zpool", "list", "-H", "-p", "-o", "guid,name")
	if err != nil {
		return m
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			m[f[0]] = f[1]
		}
	}
	return m
}

func (s *Server) importPool(p Pool) error {
	// Quietly first: after a new boot environment or a restore the pool was last used by
	// another host id, the import fails, and that is expected; then take it anyway.
	args := []string{p.GUID, p.Name}
	if p.Altroot != "" {
		// a pool that was imported from the disks (foreign.go): its datasets below its altroot
		args = append([]string{"-R", p.Altroot}, args...)
	}
	if _, err := s.Sys.Run("", "zpool", append([]string{"import"}, args...)...); err == nil {
		return nil
	}
	_, err := s.run("", "zpool", append([]string{"import", "-f"}, args...)...)
	if err == nil {
		s.Log.Printf("pool %s was last used by another host id: imported with -f", p.Name)
	}
	return err
}

func (s *Server) pwUsers() (map[string]PwUser, error) {
	out, err := s.Sys.Run("", "pw", "usershow", "-a")
	if err != nil {
		return nil, err
	}
	return ParsePwUsers(out), nil
}

func (s *Server) ensureUser(u User, pw map[string]PwUser) error {
	have, ok := pw[u.Name]
	if !ok {
		_, err := s.run("", "pw", "useradd", "-n", u.Name, "-u", strconv.Itoa(u.UID), "-g", Group,
			"-c", "keelNAS user", "-d", "/nonexistent", "-s", "/usr/sbin/nologin", "-w", "no")
		return err
	}
	if have.UID != u.UID {
		return fmt.Errorf("the system has %s with uid %d, the state says %d", u.Name, have.UID, u.UID)
	}
	if have.GID != GroupID {
		_, err := s.run("", "pw", "usermod", "-n", u.Name, "-g", Group)
		return err
	}
	return nil
}

func (s *Server) datasets() map[string]Dataset {
	ds, _ := s.listDatasets()
	return ds
}

func (s *Server) listDatasets() (map[string]Dataset, error) {
	out, err := s.Sys.Run("", "zfs", "list", "-H", "-p", "-t", "filesystem,volume", "-o", "name,type,used,mountpoint")
	if err != nil {
		return map[string]Dataset{}, err
	}
	return ParseDatasets(out), nil
}

// ensureParent makes <pool>/shares or <pool>/luns: not mounted, only a parent.
func (s *Server) ensureParent(name string, ds map[string]Dataset) error {
	if _, ok := ds[name]; ok {
		return nil
	}
	_, err := s.run("", "zfs", "create", "-o", "canmount=off", "-o", "mountpoint=none", name)
	if err == nil {
		ds[name] = Dataset{Name: name, Type: "filesystem"}
	}
	return err
}

// ensureShare checks a share's dataset and returns where it is mounted. A dataset of ours is
// made again if it is <pool>/shares/<x> and missing, and kept root:keelnas 2770; a dataset
// keelNAS did not make is never changed.
func (s *Server) ensureShare(st *State, sh Share, ds map[string]Dataset) (string, error) {
	name := sh.Dataset
	d, ok := ds[name]
	if !st.Ours(name) {
		switch {
		case !ok:
			return "", fmt.Errorf("dataset %s is missing", name)
		case !strings.HasPrefix(d.Mountpoint, "/"):
			return "", fmt.Errorf("dataset %s is not mounted (mountpoint %s)", name, d.Mountpoint)
		}
		return d.Mountpoint, nil
	}
	path := d.Mountpoint
	if !ok {
		pool := PoolOf(name)
		parent, last := name[:strings.LastIndexByte(name, '/')], name[strings.LastIndexByte(name, '/')+1:]
		if parent != pool+"/shares" {
			return "", fmt.Errorf("dataset %s is missing", name)
		}
		if err := s.ensureParent(parent, ds); err != nil {
			return "", err
		}
		// zfs takes the mountpoint without the pool's altroot and lists it with it
		path = SharePath(last)
		if p := st.Pool(pool); p != nil && p.Altroot != "" {
			path = p.Altroot + path
		}
		if _, err := s.run("", "zfs", "create", "-o", "mountpoint="+SharePath(last), name); err != nil {
			return "", err
		}
		ds[name] = Dataset{Name: name, Type: "filesystem", Mountpoint: path}
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("dataset %s is not mounted (mountpoint %s)", name, path)
	}
	// Only when they differ: a chown or chmod changes the directory's ctime, and that is a write
	// that would make every hourly snapshot look necessary. Any owner in our group is fine (a
	// dataset's owner can be changed to a user, jobs.go).
	out, _ := s.Sys.Run("", "stat", "-f", "%Su:%Sg %Mp%Lp", path)
	if strings.HasSuffix(strings.TrimSpace(out), ":"+Group+" 2770") {
		return path, nil
	}
	if _, err := s.run("", "chown", "root:"+Group, path); err != nil {
		return "", err
	}
	_, err := s.run("", "chmod", "2770", path)
	return path, err
}

func (s *Server) ensureLUN(l LUN, ds map[string]Dataset) error {
	if err := s.ensureParent(l.Pool+"/luns", ds); err != nil {
		return err
	}
	name := LUNDataset(l)
	if _, ok := ds[name]; ok {
		return nil
	}
	_, err := s.run("", "zfs", "create", "-V", strconv.Itoa(l.SizeGiB)+"G", "-o", "volmode=dev", name)
	if err == nil {
		ds[name] = Dataset{Name: name, Type: "volume"}
	}
	return err
}

// writeIfChanged writes a generated file and reports whether its content changed.
func (s *Server) writeIfChanged(path, content string, perm os.FileMode) (bool, error) {
	old, err := s.Sys.ReadFile(path)
	if err == nil && string(old) == content {
		return false, nil
	}
	if err := s.Sys.WriteFile(path, []byte(content), perm); err != nil {
		return false, err
	}
	s.Log.Printf("wrote %s", path)
	return true, nil
}

func (s *Server) running(proc string) bool {
	_, err := s.Sys.Run("", "pgrep", "-x", proc)
	return err == nil
}

// samba runs smbd directly: the port's rc script started with onestart also starts nmbd and
// winbindd, which a standalone server with NetBIOS off does not need.
func (s *Server) samba(changed bool) error {
	if s.running("smbd") {
		if changed {
			_, err := s.run("", "smbcontrol", "smbd", "reload-config")
			return err
		}
		return nil
	}
	// What the port's rc script prepares: the pid directory and fdescfs under it.
	if _, err := s.run("", "install", "-d", "-m", "0755", "/var/run/samba4/fd"); err != nil {
		return err
	}
	if out, _ := s.Sys.Run("", "mount", "-p", "-t", "fdescfs"); !strings.Contains(out, "/var/run/samba4/fd") {
		if _, err := s.run("", "mount", "-t", "fdescfs", "-o", "nodup", "fdesc", "/var/run/samba4/fd"); err != nil {
			return err
		}
	}
	_, err := s.run("", "/usr/local/sbin/smbd", "--daemon", "--configfile="+SmbConf)
	if err == nil {
		s.Log.Printf("started smbd")
	}
	return err
}

func (s *Server) ctld(changed bool) error {
	if s.running("ctld") {
		if changed {
			_, err := s.run("", "service", "ctld", "onereload")
			return err
		}
		return nil
	}
	_, err := s.run("", "service", "ctld", "onestart")
	if err == nil {
		s.Log.Printf("started ctld")
	}
	return err
}

// commit saves a new state and applies it. Called with mu held.
func (s *Server) commit(st *State) ([]string, error) {
	if err := SaveState(s.Sys, s.StatePath, st); err != nil {
		return nil, err
	}
	s.st = st
	return s.applyLocked(), nil
}

// LastApply is the time and errors of the last apply.
func (s *Server) LastApply() (time.Time, []string) {
	s.lastMu.Lock()
	defer s.lastMu.Unlock()
	return s.lastTime, append([]string{}, s.lastErrs...)
}

var errNotFound = errors.New("not found")

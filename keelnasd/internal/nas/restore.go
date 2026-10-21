package nas

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Restoring the settings from the copies in the data pools (docs/04 section 9.2).

const (
	copyDir    = ".keelnas"
	probeRoot  = "/var/run/keelnas-probe"
	restorePDB = SambaDir + "/restore.smbpasswd"
)

// PoolCopy is <pool>/.keelnas/state.json: the state plus a serial and the time it was written.
type PoolCopy struct {
	State
	Serial int64  `json:"serial"`
	Time   string `json:"time"`
}

// FoundCopy is a copy found in a pool that can be imported.
type FoundCopy struct {
	Pool      string
	Copy      PoolCopy
	Smbpasswd []byte
}

// BestCopy is the copy with the highest serial (ties: the later time, then the pool name).
func BestCopy(found []FoundCopy) *FoundCopy {
	var best *FoundCopy
	for i := range found {
		f := &found[i]
		switch {
		case best == nil,
			f.Copy.Serial > best.Copy.Serial,
			f.Copy.Serial == best.Copy.Serial && f.Copy.Time > best.Copy.Time,
			f.Copy.Serial == best.Copy.Serial && f.Copy.Time == best.Copy.Time && f.Pool < best.Pool:
			best = f
		}
	}
	return best
}

// poolMountpoint is where a pool's root dataset is mounted ("" when it is not).
func (s *Server) poolMountpoint(pool string) string {
	out, err := s.Sys.Run("", "zfs", "get", "-H", "-o", "value", "mountpoint", pool)
	mp := strings.TrimSpace(out)
	if err != nil || !strings.HasPrefix(mp, "/") {
		return ""
	}
	return mp
}

// copyToPools writes the state (with the next serial) and the Samba passwords into every
// imported data pool. Called with mu held.
func (s *Server) copyToPools(up map[string]bool) {
	if s.serial == 0 {
		// After a start: go on from the highest serial in the pools.
		for _, p := range s.st.Pools {
			if !up[p.Name] {
				continue
			}
			if mp := s.poolMountpoint(p.Name); mp != "" {
				if b, err := s.Sys.ReadFile(mp + "/" + copyDir + "/state.json"); err == nil {
					var c PoolCopy
					if json.Unmarshal(b, &c) == nil && c.Serial > s.serial {
						s.serial = c.Serial
					}
				}
			}
		}
	}
	s.serial++
	c := PoolCopy{State: *s.st, Serial: s.serial, Time: s.now().Format(time.RFC3339)}
	b, _ := json.MarshalIndent(c, "", "  ")
	b = append(b, '\n')
	pw, pwErr := s.Sys.Run("", "pdbedit", "-s", SmbConf, "-L", "-w")
	if pwErr != nil {
		s.Log.Printf("pdbedit -L -w: %v (the copies keep their old passwords)", pwErr)
	}
	for _, p := range s.st.Pools {
		if !up[p.Name] {
			continue
		}
		mp := s.poolMountpoint(p.Name)
		if mp == "" {
			continue
		}
		if err := s.Sys.WriteFile(mp+"/"+copyDir+"/state.json", b, 0o600); err != nil {
			s.Log.Printf("copy of the state to %s: %v", p.Name, err)
		}
		if pwErr == nil && s.pendingPasswd == nil {
			if err := s.Sys.WriteFile(mp+"/"+copyDir+"/smbpasswd", []byte(pw), 0o600); err != nil {
				s.Log.Printf("copy of the passwords to %s: %v", p.Name, err)
			}
		}
	}
}

// refreshCopies writes the copies again (a password changed). Called with mu held.
func (s *Server) refreshCopies() {
	up := map[string]bool{}
	for _, n := range s.importedPools() {
		up[n] = true
	}
	s.copyToPools(up)
}

// findCopies imports every importable pool read-only under probeRoot, reads its copy, and
// exports it again. Pools without a copy are left as they were.
func (s *Server) findCopies() []FoundCopy {
	out, _ := s.Sys.Run("", "zpool", "import")
	var found []FoundCopy
	for _, p := range ParseImport(out) {
		if p.GUID == "" {
			continue
		}
		args := []string{"import", "-o", "readonly=on", "-N", "-R", probeRoot, p.GUID}
		if _, err := s.Sys.Run("", "zpool", args...); err != nil {
			// Last used by another host id: a read-only import with -f writes nothing.
			args = append([]string{"import", "-f"}, args[1:]...)
			if _, err := s.run("", "zpool", args...); err != nil {
				s.Log.Printf("restore: cannot look into pool %s (%s)", p.Name, p.GUID)
				continue
			}
		}
		var f *FoundCopy
		if _, err := s.run("", "zfs", "mount", p.Name); err == nil {
			if mp := s.poolMountpoint(p.Name); mp != "" {
				if b, err := s.Sys.ReadFile(mp + "/" + copyDir + "/state.json"); err == nil {
					var c PoolCopy
					c.State.Snapshots = DefaultRetention
					if err := json.Unmarshal(b, &c); err != nil {
						s.Log.Printf("restore: %s: %v", p.Name, err)
					} else {
						c.State.normalize()
						f = &FoundCopy{Pool: p.Name, Copy: c}
						f.Smbpasswd, _ = s.Sys.ReadFile(mp + "/" + copyDir + "/smbpasswd")
					}
				}
			}
		}
		if _, err := s.run("", "zpool", "export", p.Name); err != nil {
			s.run("", "zpool", "export", "-f", p.Name)
		}
		if f != nil {
			s.Log.Printf("restore: pool %s has a copy of the settings (serial %d, %s)", p.Name, f.Copy.Serial, f.Copy.Time)
			found = append(found, *f)
		} else {
			s.Log.Printf("restore: pool %s has no copy of the settings: left alone", p.Name)
		}
	}
	return found
}

// errNotFresh: restore only replaces a state that has nothing in it.
var errNotFresh = conflict("the settings are not fresh: restore only works on a new keelNAS")

// Restore takes the newest copy of the settings from the pools that can be imported, if the
// state is fresh: the state (key and listen too), the pools it lists, the Samba passwords.
// It returns the pool the copy came from ("" = none found) and the errors of the apply.
func (s *Server) Restore() (string, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.st.Fresh() {
		return "", nil, errNotFresh
	}
	best := BestCopy(s.findCopies())
	if best == nil {
		return "", nil, nil
	}
	st := best.Copy.State.Clone()
	if err := SaveState(s.Sys, s.StatePath, st); err != nil {
		return "", nil, err
	}
	s.st = st
	s.serial = best.Copy.Serial
	s.pendingPasswd = best.Smbpasswd
	s.hmu.Lock()
	s.restoredFrom = best.Pool
	s.hmu.Unlock()
	s.Log.Printf("restored the settings from pool %s (serial %d, written %s)", best.Pool, best.Copy.Serial, best.Copy.Time)
	return best.Pool, s.applyLocked(), nil
}

// importPasswords puts restored Samba passwords back (in apply, after the users and smb4.conf
// exist, before the copies are written again). Called with mu held.
func (s *Server) importPasswords() error {
	if len(s.pendingPasswd) == 0 {
		return nil
	}
	pw := s.pendingPasswd
	if err := s.Sys.WriteFile(restorePDB, pw, 0o600); err != nil {
		return err
	}
	defer s.Sys.Run("", "rm", "-f", restorePDB)
	if _, err := s.run("", "pdbedit", "-s", SmbConf, "-i", "smbpasswd:"+restorePDB); err != nil {
		return err
	}
	s.pendingPasswd = nil
	s.Log.Printf("restored the Samba passwords")
	return nil
}

// RestoredFrom is the pool the settings were restored from since the start ("" = not restored).
func (s *Server) RestoredFrom() string {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	return s.restoredFrom
}

func (s *Server) postRestore(r *http.Request) (any, error) {
	pool, errs, err := s.Restore()
	if err != nil {
		return nil, err
	}
	if pool == "" {
		return nil, notFound("no pool that can be imported has a copy of the settings")
	}
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{"ok": len(errs) == 0, "restored_from": pool, "errors": errs}, nil
}

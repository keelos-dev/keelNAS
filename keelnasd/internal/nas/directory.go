package nas

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Accounts from keelAuth (docs/04 section 12, keelOS docs/54 section 3.3): keel reads the users,
// groups and NT hashes from keelAuth and pushes them here (PUT /v1/directory); keelNAS makes them
// system users and Samba accounts next to its own local users. The NT hashes are not kept in
// state.json: they go straight into Samba's password database (pdbedit), whose copy in the
// pools already exists (restore.go).

const (
	// SyncIDMin: uids and gids from keelAuth are at least this (Kanidm gives large numbers);
	// keelNAS's own users start at 2001, so the two never meet.
	SyncIDMin = 65536
	syncIDMax = 1<<31 - 1
	syncPDB   = SambaDir + "/sync.smbpasswd"
)

// Directory is the connection to keelAuth and what the last sync brought (no NT hashes).
type Directory struct {
	Source   string     `json:"source"` // keel's id of the keelAuth instance (auth1)
	Name     string     `json:"name"`   // what the page calls it
	Domain   string     `json:"domain"`
	Revision int64      `json:"revision"`
	SyncedAt string     `json:"synced_at"`
	Users    []DirUser  `json:"users"`
	Groups   []DirGroup `json:"groups"`
}

// DirUser is a user from keelAuth. Problem says why it is not made on the NAS ("" = it is):
// bad_name, bad_uid, local_user (a local user has the name), system_account.
type DirUser struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	UID         int      `json:"uid"`
	Enabled     bool     `json:"enabled"`
	HasPassword bool     `json:"has_password"` // false: keelAuth has no NT hash (D5 upstream users)
	Groups      []string `json:"groups"`
	Problem     string   `json:"problem,omitempty"`
}

// DirGroup is a group from keelAuth; a share names it as "@name". Problem: bad_name, bad_gid,
// system_group (the system has a group of that name).
type DirGroup struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"` // only users that are made on the NAS
	Problem string   `json:"problem,omitempty"`
}

func (d *Directory) clone() *Directory {
	if d == nil {
		return nil
	}
	c := *d
	c.Users = make([]DirUser, len(d.Users))
	for i, u := range d.Users {
		u.Groups = slices.Clone(u.Groups)
		c.Users[i] = u
	}
	c.Groups = make([]DirGroup, len(d.Groups))
	for i, g := range d.Groups {
		g.Members = slices.Clone(g.Members)
		c.Groups[i] = g
	}
	return &c
}

// DirUser is the keelAuth user name that is made on the NAS (nil: none).
func (s *State) DirUser(name string) *DirUser {
	if s.Directory == nil {
		return nil
	}
	for i := range s.Directory.Users {
		if u := &s.Directory.Users[i]; u.Name == name && u.Problem == "" {
			return u
		}
	}
	return nil
}

// DirGroup is the keelAuth group name that is made on the NAS (nil: none).
func (s *State) DirGroup(name string) *DirGroup {
	if s.Directory == nil {
		return nil
	}
	for i := range s.Directory.Groups {
		if g := &s.Directory.Groups[i]; g.Name == name && g.Problem == "" {
			return g
		}
	}
	return nil
}

// Principal: n may be named in a share's lists: a local user, a keelAuth user, "@group".
func (s *State) Principal(n string) bool {
	if g, ok := strings.CutPrefix(n, "@"); ok {
		return s.DirGroup(g) != nil
	}
	return s.User(n) != nil || s.DirUser(n) != nil
}

// dropPrincipals takes the names in gone out of every share's lists.
func (s *State) dropPrincipals(gone map[string]bool) {
	for i := range s.Shares {
		s.Shares[i].RW = slices.DeleteFunc(s.Shares[i].RW, func(n string) bool { return gone[n] })
		s.Shares[i].RO = slices.DeleteFunc(s.Shares[i].RO, func(n string) bool { return gone[n] })
	}
}

// ---- system groups ----

type PwGroup struct {
	Name    string
	GID     int
	Members []string
}

// ParsePwGroups reads `pw groupshow -a`: name:*:gid:member,member.
func ParsePwGroups(out string) map[string]PwGroup {
	m := map[string]PwGroup{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 3 {
			continue
		}
		gid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		g := PwGroup{Name: f[0], GID: gid, Members: []string{}}
		if len(f) > 3 && f[3] != "" {
			g.Members = strings.Split(f[3], ",")
		}
		m[f[0]] = g
	}
	return m
}

func (s *Server) pwGroups() (map[string]PwGroup, error) {
	out, err := s.Sys.Run("", "pw", "groupshow", "-a")
	if err != nil {
		return nil, err
	}
	return ParsePwGroups(out), nil
}

// applyDirectory makes the keelAuth users' and groups' system accounts (apply, after the local
// users). A user keelAuth made again under the same name has a new uid: it is changed.
// Groups of keelAuth that are gone go too (gid SyncIDMin and up, not in the state).
func (s *Server) applyDirectory(st *State, pw map[string]PwUser, fail func(string, ...any)) {
	if st.Directory != nil {
		for _, u := range st.Directory.Users {
			if u.Problem != "" {
				continue
			}
			if have, ok := pw[u.Name]; ok && have.GID == GroupID && have.UID != u.UID {
				if _, err := s.run("", "pw", "usermod", "-n", u.Name, "-u", strconv.Itoa(u.UID)); err != nil {
					fail("user %s: %v", u.Name, err)
				}
				continue
			}
			if err := s.ensureUser(User{Name: u.Name, UID: u.UID}, pw); err != nil {
				fail("user %s: %v", u.Name, err)
			}
		}
	}
	groups, err := s.pwGroups()
	if err != nil {
		fail("groups: %v", err)
		return
	}
	want := map[string]bool{}
	if st.Directory != nil {
		for _, g := range st.Directory.Groups {
			if g.Problem != "" {
				continue
			}
			want[g.Name] = true
			members := sorted(g.Members)
			have, ok := groups[g.Name]
			switch {
			case !ok:
				args := []string{"groupadd", "-n", g.Name, "-g", strconv.Itoa(g.GID)}
				if len(members) > 0 {
					args = append(args, "-M", strings.Join(members, ","))
				}
				if _, err := s.run("", "pw", args...); err != nil {
					fail("group %s: %v", g.Name, err)
				}
				continue
			case have.GID != g.GID:
				if _, err := s.run("", "pw", "groupmod", "-n", g.Name, "-g", strconv.Itoa(g.GID)); err != nil {
					fail("group %s: %v", g.Name, err)
					continue
				}
			}
			if slices.Equal(sorted(have.Members), members) {
				continue
			}
			var err error
			if len(members) == 0 {
				_, err = s.run("", "pw", "groupmod", "-n", g.Name, "-d", strings.Join(have.Members, ","))
			} else {
				_, err = s.run("", "pw", "groupmod", "-n", g.Name, "-M", strings.Join(members, ","))
			}
			if err != nil {
				fail("group %s: %v", g.Name, err)
			}
		}
	}
	for _, g := range groups {
		if g.GID >= SyncIDMin && g.GID <= syncIDMax && !want[g.Name] {
			if _, err := s.run("", "pw", "groupdel", "-n", g.Name); err != nil {
				fail("remove group %s: %v", g.Name, err)
			}
		}
	}
}

// ---- Samba's password database ----

// pdbEntry is one line of `pdbedit -L -w` (smbpasswd format).
type pdbEntry struct {
	NT       string // upper-case hex, "" = none
	Disabled bool
}

// ParsePdb reads `pdbedit -L -w`: name:uid:LM:NT:[flags]:LCT-…:
func ParsePdb(out string) map[string]pdbEntry {
	m := map[string]pdbEntry{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 5 || f[0] == "" {
			continue
		}
		e := pdbEntry{NT: strings.ToUpper(f[3]), Disabled: strings.Contains(strings.Trim(f[4], "[]"), "D")}
		if len(e.NT) != 32 || strings.HasPrefix(e.NT, "NO PASSWORD") {
			e.NT = ""
		}
		m[f[0]] = e
	}
	return m
}

// pdbLine is a smbpasswd line for pdbedit -i: no LM hash, the NT hash, [U] or [DU].
func pdbLine(name string, uid int, nt string, disabled bool, lct int64) string {
	flags := "[U          ]"
	if disabled {
		flags = "[DU         ]"
	}
	return fmt.Sprintf("%s:%d:%s:%s:%s:LCT-%08X:\n", name, uid, strings.Repeat("X", 32), nt, flags, lct)
}

func randomNT() string {
	b := make([]byte, 16)
	rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

// syncSamba brings Samba's accounts of the keelAuth users to what keelAuth says: the NT hash,
// disabled when keelAuth disabled them or has no hash; users in gone lose their account. Only
// accounts that differ are written (one pdbedit -i of a temporary file: the hashes never show on
// a command line). Called with mu held, after apply made the system users.
func (s *Server) syncSamba(users []DirUser, hashes map[string]string, gone []string) error {
	out, err := s.Sys.Run("", "pdbedit", "-s", SmbConf, "-L", "-w")
	if err != nil {
		return err
	}
	cur := ParsePdb(out)
	var b strings.Builder
	n := 0
	for _, u := range users {
		if u.Problem != "" {
			continue
		}
		nt := strings.ToUpper(hashes[u.Name])
		disabled := !u.Enabled || nt == ""
		c, ok := cur[u.Name]
		if nt == "" {
			// no hash in keelAuth: keep whatever is there, disabled; a new account gets a hash nobody knows
			nt = c.NT
			if nt == "" {
				nt = randomNT()
			}
		}
		if ok && c.NT == nt && c.Disabled == disabled {
			continue
		}
		b.WriteString(pdbLine(u.Name, u.UID, nt, disabled, s.now().Unix()))
		n++
	}
	var errs []string
	if n > 0 {
		if err := s.Sys.WriteFile(syncPDB, []byte(b.String()), 0o600); err != nil {
			return err
		}
		_, err := s.run("", "pdbedit", "-s", SmbConf, "-i", "smbpasswd:"+syncPDB)
		s.Sys.Run("", "rm", "-f", syncPDB)
		if err != nil {
			errs = append(errs, err.Error())
		} else {
			s.Log.Printf("directory: %d Samba account(s) written", n)
		}
	}
	for _, name := range gone {
		if _, ok := cur[name]; ok {
			if _, err := s.run("", "pdbedit", "-s", SmbConf, "-x", "-u", name); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if errs != nil {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// ---- API ----

// SyncRequest is PUT /v1/directory: what keel read from keelAuth's GET /v1/sync/accounts
// (keelAuth docs/02 section 3.5) plus which keelAuth it is.
type SyncRequest struct {
	Source   string `json:"source"`
	Name     string `json:"name"`
	Domain   string `json:"domain"`
	Revision int64  `json:"revision"`
	Users    []struct {
		Name        string   `json:"name"`
		DisplayName string   `json:"display_name"`
		UID         int      `json:"uid"`
		Enabled     bool     `json:"enabled"`
		NTHash      string   `json:"nt_hash"`
		SetAt       string   `json:"password_set_at"`
		MustChange  bool     `json:"must_change"`
		Groups      []string `json:"groups"`
	} `json:"users"`
	Groups []struct {
		Name    string   `json:"name"`
		GID     int      `json:"gid"`
		Members []string `json:"members"`
	} `json:"groups"`
}

var hexRE = func(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// DirectoryInfo is GET /v1/directory.
type DirectoryInfo struct {
	Connected bool `json:"connected"`
	*Directory
}

func dirInfo(d *Directory) DirectoryInfo {
	if d == nil {
		return DirectoryInfo{}
	}
	return DirectoryInfo{Connected: true, Directory: d}
}

func (s *Server) getDirectory(r *http.Request) (any, error) {
	return dirInfo(s.State().Directory), nil
}

// putDirectory takes a sync: the users, groups and NT hashes of keelAuth as they are now.
func (s *Server) putDirectory(r *http.Request) (any, error) {
	var req SyncRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Source == "" {
		return nil, bad("source: which keelAuth this is")
	}
	hashes := map[string]string{}
	for _, u := range req.Users {
		if u.NTHash != "" && !hexRE(u.NTHash) {
			return nil, bad("user %s: nt_hash is not 32 hexadecimal characters", u.Name)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	pw, err := s.pwUsers()
	if err != nil {
		return nil, err
	}
	groups, err := s.pwGroups()
	if err != nil {
		return nil, err
	}
	d := &Directory{Source: req.Source, Name: req.Name, Domain: req.Domain, Revision: req.Revision,
		SyncedAt: s.now().Format("2006-01-02T15:04:05Z07:00"), Users: []DirUser{}, Groups: []DirGroup{}}
	uids := map[int]int{}
	for _, u := range req.Users {
		uids[u.UID]++
	}
	made := map[string]bool{}
	for _, u := range req.Users {
		du := DirUser{Name: u.Name, DisplayName: u.DisplayName, UID: u.UID, Enabled: u.Enabled,
			HasPassword: u.NTHash != "", Groups: sorted(u.Groups)}
		if du.Groups == nil {
			du.Groups = []string{}
		}
		have, inPw := pw[u.Name]
		switch {
		case !ValidName(u.Name):
			du.Problem = "bad_name"
		case u.UID < SyncIDMin || u.UID > syncIDMax || uids[u.UID] > 1:
			du.Problem = "bad_uid"
		case st.User(u.Name) != nil:
			du.Problem = "local_user"
		case inPw && (have.GID != GroupID || have.UID < SyncIDMin):
			du.Problem = "system_account"
		case made[u.Name]:
			du.Problem = "bad_name" // twice in one sync
		}
		if du.Problem == "" {
			made[u.Name] = true
			hashes[u.Name] = strings.ToLower(u.NTHash)
		}
		d.Users = append(d.Users, du)
	}
	for _, g := range req.Groups {
		dg := DirGroup{Name: g.Name, GID: g.GID, Members: []string{}}
		have, inPw := groups[g.Name]
		switch {
		case !ValidName(g.Name) || g.Name == Group:
			dg.Problem = "bad_name"
		case g.GID < SyncIDMin || g.GID > syncIDMax:
			dg.Problem = "bad_gid"
		case inPw && have.GID < SyncIDMin:
			dg.Problem = "system_group"
		}
		for _, m := range g.Members {
			if made[m] {
				dg.Members = append(dg.Members, m)
			}
		}
		dg.Members = sorted(dg.Members)
		d.Groups = append(d.Groups, dg)
	}
	// what was made before and is not any more leaves the shares and Samba
	gone, goneUsers := map[string]bool{}, []string{}
	if old := st.Directory; old != nil {
		for _, u := range old.Users {
			if u.Problem == "" && !made[u.Name] {
				gone[u.Name] = true
				goneUsers = append(goneUsers, u.Name)
			}
		}
		for _, g := range old.Groups {
			if g.Problem != "" {
				continue
			}
			if !slices.ContainsFunc(d.Groups, func(x DirGroup) bool { return x.Name == g.Name && x.Problem == "" }) {
				gone["@"+g.Name] = true
			}
		}
	}
	st.dropPrincipals(gone)
	st.Directory = d
	errs, err := s.commit(st)
	if err != nil {
		return nil, err
	}
	if err := s.syncSamba(d.Users, hashes, goneUsers); err != nil {
		errs = append(errs, "samba: "+err.Error())
	}
	s.refreshCopies()
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{"ok": len(errs) == 0, "errors": errs, "revision": d.Revision, "users": len(made), "removed": len(goneUsers)}, nil
}

// deleteDirectory disconnects from keelAuth. ?users=keep: the keelAuth users that can log in
// (enabled, with a password) become local users with their password, and a share's groups are
// replaced by their members; the others are removed. ?users=delete: all of them are removed.
func (s *Server) deleteDirectory(r *http.Request) (any, error) {
	mode := r.URL.Query().Get("users")
	if mode != "keep" && mode != "delete" {
		return nil, bad("users=keep or users=delete: what becomes of the users from keelAuth")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	d := st.Directory
	if d == nil {
		return nil, notFound("not connected to keelAuth")
	}
	kept, removed := []string{}, []string{}
	keep := map[string]bool{}
	for _, u := range d.Users {
		if u.Problem != "" {
			continue
		}
		if mode == "keep" && u.Enabled && u.HasPassword {
			keep[u.Name] = true
			kept = append(kept, u.Name)
			st.Users = append(st.Users, User{Name: u.Name, UID: u.UID})
		} else {
			removed = append(removed, u.Name)
		}
	}
	// groups in shares: their kept members instead (read-write wins over read-only)
	for i := range st.Shares {
		sh := &st.Shares[i]
		expand := func(list []string) []string {
			var out []string
			for _, n := range list {
				if g, ok := strings.CutPrefix(n, "@"); ok {
					if dg := st.DirGroup(g); dg != nil {
						for _, m := range dg.Members {
							if keep[m] {
								out = append(out, m)
							}
						}
					}
					continue
				}
				if st.DirUser(n) != nil && !keep[n] {
					continue
				}
				out = append(out, n)
			}
			return dedup(out)
		}
		sh.RW = expand(sh.RW)
		sh.RO = slices.DeleteFunc(expand(sh.RO), func(n string) bool { return slices.Contains(sh.RW, n) })
	}
	st.Directory = nil
	errs, err := s.commit(st)
	if err != nil {
		return nil, err
	}
	for _, n := range removed {
		s.Sys.Run("", "pdbedit", "-s", SmbConf, "-x", "-u", n)
	}
	s.refreshCopies()
	s.Log.Printf("directory: disconnected from %s (%s): %d user(s) kept as local, %d removed", d.Source, mode, len(kept), len(removed))
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{"ok": len(errs) == 0, "errors": errs, "kept": kept, "removed": removed}, nil
}

// Package nas is keelnasd itself: the settings (state.json), turning them into the system
// (apply) and the HTTP API. All system access goes through sys.Sys.
package nas

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"keelnas/internal/sys"
)

const (
	StatePath  = "/var/db/keelnas/state.json"
	SambaDir   = "/var/db/keelnas/samba"
	SmbConf    = "/usr/local/etc/smb4.conf"
	CtlConf    = "/etc/ctl.conf"
	SharesRoot = "/shares"
	Group      = "keelnas"
	GroupID    = 2000
	FirstUID   = 2001
)

type Pool struct {
	Name string `json:"name"`
	GUID string `json:"guid"`
	// Altroot: a pool that was on the disks already and was imported (foreign.go) is imported
	// with this altroot every time, so its own mountpoints stay below it; "" for the pools
	// keelNAS made.
	Altroot string `json:"altroot,omitempty"`
	// Scrub: when keelnasd checks the pool (scrub.go); nil = the default (ScrubDefault)
	Scrub *ScrubPlan `json:"scrub,omitempty"`
}

type User struct {
	Name string `json:"name"`
	UID  int    `json:"uid"`
}

// Share is an SMB share of one ZFS dataset (docs/06).
type Share struct {
	Name    string   `json:"name"`
	Dataset string   `json:"dataset"`
	RW      []string `json:"rw"`
	RO      []string `json:"ro"`
	// Owner is only read: keelnasd 251c814 had Samba's force user here (docs/06 §6, dropped
	// 2026-10-08); normalize drops it.
	Owner string `json:"owner,omitempty"`
	// Pool is only read: state.json before docs/06 had {"name","pool"} = <pool>/shares/<name>;
	// normalize turns it into Dataset (and that dataset into one of ours) and drops it.
	Pool string `json:"pool,omitempty"`
}

// PoolOf is the pool a dataset is in.
func PoolOf(dataset string) string {
	p, _, _ := strings.Cut(dataset, "/")
	return p
}

type LUN struct {
	Name    string `json:"name"`
	Pool    string `json:"pool"`
	SizeGiB int    `json:"size_gib"`
}

// Retention is how many scheduled snapshots of each class are kept; 0 = that class is not taken.
type Retention struct {
	Hourly int `json:"hourly"`
	Daily  int `json:"daily"`
	Weekly int `json:"weekly"`
}

// DefaultRetention: 24 hourly, 7 daily, 4 weekly (docs/04 section 9.3).
var DefaultRetention = Retention{Hourly: 24, Daily: 7, Weekly: 4}

// State is the one copy of the settings; everything else is generated from it.
type State struct {
	Version int     `json:"version"`
	Key     string  `json:"key"`
	Listen  string  `json:"listen"`
	Pools   []Pool  `json:"pools"`
	Users   []User  `json:"users"`
	Shares  []Share `json:"shares"`
	// Datasets: the datasets keelNAS made ("ours"): only these can be destroyed (docs/06 §2)
	Datasets  []string  `json:"datasets"`
	LUNs      []LUN     `json:"luns"`
	Snapshots Retention `json:"snapshots"`
	// AutoSnap: datasets whose scheduled snapshots were switched on or off by hand (the
	// storage page); one not in it follows AutoSnapOn's default
	AutoSnap map[string]bool `json:"auto_snapshot,omitempty"`
	Network   *Network  `json:"network,omitempty"` // the LAN NIC (network.go); nil = DHCP
	// Directory: connected to keelAuth, its users and groups (directory.go); nil = not connected
	Directory *Directory `json:"directory,omitempty"`
}

// Fresh: nothing set up yet (no pools, users, shares or LUNs). Only then may settings be restored.
func (s *State) Fresh() bool {
	return len(s.Pools) == 0 && len(s.Users) == 0 && len(s.Shares) == 0 && len(s.LUNs) == 0 && s.Directory == nil
}

// ParseState reads state.json's text; a missing "snapshots" (or a missing class in it) is the default.
func ParseState(b []byte) (*State, error) {
	st, _, err := parseState(b)
	return st, err
}

// parseState is ParseState that also reports whether the text was of an older form.
func parseState(b []byte) (*State, bool, error) {
	st := &State{Snapshots: DefaultRetention}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, false, err
	}
	return st, st.normalize(), nil
}

// normalize fills empty lists and rewrites shares of the old form; it reports whether it
// rewrote anything (then state.json is written again).
func (s *State) normalize() bool {
	changed := false
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Pools == nil {
		s.Pools = []Pool{}
	}
	if s.Users == nil {
		s.Users = []User{}
	}
	if s.Shares == nil {
		s.Shares = []Share{}
	}
	if s.LUNs == nil {
		s.LUNs = []LUN{}
	}
	if s.Datasets == nil {
		s.Datasets = []string{}
	}
	for i := range s.Shares {
		if sh := &s.Shares[i]; sh.Pool != "" {
			if sh.Dataset == "" {
				sh.Dataset = sh.Pool + "/shares/" + sh.Name
				if !slices.Contains(s.Datasets, sh.Dataset) {
					s.Datasets = append(s.Datasets, sh.Dataset)
				}
			}
			sh.Pool = ""
			changed = true
		}
		if s.Shares[i].Owner != "" {
			s.Shares[i].Owner = ""
			changed = true
		}
		if s.Shares[i].RW == nil {
			s.Shares[i].RW = []string{}
		}
		if s.Shares[i].RO == nil {
			s.Shares[i].RO = []string{}
		}
	}
	return changed
}

// Clone is a deep copy, so a change can be prepared and thrown away.
func (s *State) Clone() *State {
	c := *s
	c.Pools = slices.Clone(s.Pools)
	c.Users = slices.Clone(s.Users)
	c.LUNs = slices.Clone(s.LUNs)
	c.Datasets = slices.Clone(s.Datasets)
	c.AutoSnap = maps.Clone(s.AutoSnap)
	c.Shares = make([]Share, len(s.Shares))
	for i, sh := range s.Shares {
		sh.RW = slices.Clone(sh.RW)
		sh.RO = slices.Clone(sh.RO)
		c.Shares[i] = sh
	}
	if s.Network != nil {
		n := *s.Network
		n.DNS = slices.Clone(s.Network.DNS)
		c.Network = &n
	}
	c.Directory = s.Directory.clone()
	c.normalize()
	return &c
}

// AutoSnapOn: the dataset gets scheduled snapshots: as set on the storage page, else when it
// is a share's dataset (before the setting existed every share got them, and still does).
func (s *State) AutoSnapOn(dataset string) bool {
	if v, ok := s.AutoSnap[dataset]; ok {
		return v
	}
	return s.ShareOf(dataset) != nil
}

func (s *State) Pool(name string) *Pool {
	for i := range s.Pools {
		if s.Pools[i].Name == name {
			return &s.Pools[i]
		}
	}
	return nil
}

func (s *State) User(name string) *User {
	for i := range s.Users {
		if s.Users[i].Name == name {
			return &s.Users[i]
		}
	}
	return nil
}

func (s *State) Share(name string) *Share {
	for i := range s.Shares {
		if s.Shares[i].Name == name {
			return &s.Shares[i]
		}
	}
	return nil
}

func (s *State) LUN(name string) *LUN {
	for i := range s.LUNs {
		if s.LUNs[i].Name == name {
			return &s.LUNs[i]
		}
	}
	return nil
}

// NextUID is one above the highest uid ever given (uids are not reused) and not taken in taken.
// Users kept from keelAuth (uid SyncIDMin and up) do not count.
func (s *State) NextUID(taken map[int]bool) int {
	uid := FirstUID
	for _, u := range s.Users {
		if u.UID >= uid && u.UID < SyncIDMin {
			uid = u.UID + 1
		}
	}
	for taken[uid] {
		uid++
	}
	return uid
}

// NextPoolName gives tank, tank2, tank3 … not in state and not in imported (which also holds
// the names of pools that are on the disks but not imported: a new pool never takes their name).
func (s *State) NextPoolName(imported map[string]bool) string {
	for i := 1; ; i++ {
		n := "tank"
		if i > 1 {
			n = fmt.Sprintf("tank%d", i)
		}
		if s.Pool(n) == nil && !imported[n] {
			return n
		}
	}
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidName: lower-case letters, digits, - and _, a letter first, at most 32.
func ValidName(n string) bool { return nameRE.MatchString(n) }

// reservedShares are section names Samba treats specially.
var reservedShares = []string{"global", "homes", "printers", "print"}

// LoadState reads state.json; a missing file is an empty state with a new key.
// created reports that the file did not exist (or had no key) and must be written.
func LoadState(s sys.Sys, path string) (st *State, created bool, err error) {
	b, err := s.ReadFile(path)
	st = &State{Snapshots: DefaultRetention}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		created = true
	case err != nil:
		return nil, false, err
	default:
		var old bool
		if st, old, err = parseState(b); err != nil {
			return nil, false, fmt.Errorf("%s: %v", path, err)
		}
		created = old
	}
	if st.Key == "" {
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return nil, false, err
		}
		st.Key = hex.EncodeToString(k)
		created = true
	}
	if st.normalize() {
		created = true
	}
	return st, created, nil
}

// Marshal is state.json's text.
func (s *State) Marshal() []byte {
	b, _ := json.MarshalIndent(s, "", "  ")
	return append(b, '\n')
}

// SaveState writes state.json atomically (root only: it holds the key).
func SaveState(s sys.Sys, path string, st *State) error {
	return s.WriteFile(path, st.Marshal(), 0o600)
}

// UserID is the uid of a keelNAS user or a keelAuth user keelNAS made.
func (s *State) UserID(name string) (int, bool) {
	if u := s.User(name); u != nil {
		return u.UID, true
	}
	if u := s.DirUser(name); u != nil {
		return u.UID, true
	}
	return 0, false
}

// UserByID is the keelNAS (or keelAuth) user with this uid, "" = none.
func (s *State) UserByID(uid int) string {
	for _, u := range s.Users {
		if u.UID == uid {
			return u.Name
		}
	}
	if s.Directory != nil {
		for _, u := range s.Directory.Users {
			if u.UID == uid && u.Problem == "" {
				return u.Name
			}
		}
	}
	return ""
}

// Ours: keelNAS made this dataset.
func (s *State) Ours(dataset string) bool { return slices.Contains(s.Datasets, dataset) }

// ShareOf is the share of a dataset, nil = not shared.
func (s *State) ShareOf(dataset string) *Share {
	for i := range s.Shares {
		if s.Shares[i].Dataset == dataset {
			return &s.Shares[i]
		}
	}
	return nil
}

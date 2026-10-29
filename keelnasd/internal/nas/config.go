package nas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

const generatedHeader = "# Written by keelnasd from /var/db/keelnas/state.json. Changes here are overwritten.\n"

// SharePath is where a share's dataset is mounted.
func SharePath(name string) string { return SharesRoot + "/" + name }

// LegacyDataset is the dataset the old form of a new share {"name","pool"} makes: <pool>/shares/<name>.
func LegacyDataset(pool, name string) string { return pool + "/shares/" + name }

func LUNDataset(l LUN) string { return l.Pool + "/luns/" + l.Name }

// IQN of a LUN's target.
func IQN(host, lun string) string { return "iqn.2026-10.dev.keelos:" + host + ":" + lun }

// SmbConfig is smb4.conf for the state (docs/04 section 5); paths: share name -> where its
// dataset is mounted ("" or missing = not mounted: the share is off).
func SmbConfig(st *State, paths map[string]string) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	// multicast dns register = no: Samba is built with mDNSResponder (the port wants a zeroconf
	// choice for vfs_fruit, docs/05 section 11) but mdnsd does not run; without it smbd logs a
	// failed connect to /var/run/mdnsd at every start. macOS discovery is keelnasd's (not done).
	b.WriteString(`[global]
  server role = standalone server
  server string = keelNAS
  map to guest = Never
  restrict anonymous = 2
  server min protocol = SMB2
  disable netbios = yes
  multicast dns register = no
  smb ports = 445
  passdb backend = tdbsam:` + SambaDir + `/passdb.tdb
  private dir = ` + SambaDir + `
  load printers = no
  printcap name = /dev/null
`)
	shares := slices.Clone(st.Shares)
	slices.SortFunc(shares, func(a, b Share) int { return strings.Compare(a.Name, b.Name) })
	for _, sh := range shares {
		valid := smbNames(union(sh.RW, sh.RO))
		path := paths[sh.Name]
		fmt.Fprintf(&b, "[%s]\n", sh.Name)
		if path == "" {
			// its dataset is not there or not mounted (apply says why)
			fmt.Fprintf(&b, "  # dataset %s is not mounted\n  path = /nonexistent\n  available = no\n", sh.Dataset)
			continue
		}
		fmt.Fprintf(&b, "  path = %s\n", path)
		if len(valid) == 0 {
			// nobody left (its users were removed): an empty "valid users" would let every user in
			b.WriteString("  available = no\n")
		} else {
			fmt.Fprintf(&b, "  valid users = %s\n", strings.Join(valid, " "))
		}
		// Who may write is the dataset's NFSv4 ACL (ShareACL, docs/06 section 8): the share's
		// root gives the read-write users modify rights and the read-only users read rights,
		// inherited by every file; the owner of a file can then grant or take away that one
		// file in Windows' security tab (vfs_zfsacl). So the share is writable for all of them.
		b.WriteString("  read only = no\n")
		b.WriteString("  vfs objects = shadow_copy2 zfsacl\n")
		b.WriteString("  nfs4:mode = special\n")
		b.WriteString("  nfs4:acedup = merge\n")
		// Windows' "previous versions" from the share's snapshots (docs/04 section 9.3).
		b.WriteString("  shadow:snapdir = .zfs/snapshot\n")
		b.WriteString("  shadow:format = " + SnapPrefix + "%Y%m%d-%H%M%S\n")
		b.WriteString("  shadow:sort = desc\n")
		b.WriteString("  shadow:localtime = no\n")
	}
	return b.String()
}

// CtlConfig is ctl.conf: one portal group, one target per LUN, no authentication yet.
func CtlConfig(st *State, host string) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString(`portal-group pg0 {
	discovery-auth-group no-authentication
	listen 0.0.0.0
}
`)
	luns := slices.Clone(st.LUNs)
	slices.SortFunc(luns, func(a, b LUN) int { return strings.Compare(a.Name, b.Name) })
	for _, l := range luns {
		fmt.Fprintf(&b, `
target %s {
	auth-group no-authentication
	portal-group pg0
	lun 0 {
		path /dev/zvol/%s
		serial "%s"
		device-id "keelNAS %s"
	}
}
`, IQN(host, l.Name), LUNDataset(l), lunSerial(l), l.Name)
	}
	return b.String()
}

// lunSerial is stable across reloads and reboots, so an initiator sees the same disk (CTL allows 16 bytes).
func lunSerial(l LUN) string {
	h := sha256.Sum256([]byte(l.Pool + "/" + l.Name))
	return "KN" + strings.ToUpper(hex.EncodeToString(h[:7]))
}

// smbNames writes a keelAuth group "@office" as Samba's "+office" (a Unix group, no NIS lookup).
func smbNames(a []string) []string {
	c := make([]string, len(a))
	for i, n := range a {
		if g, ok := strings.CutPrefix(n, "@"); ok {
			n = "+" + g
		}
		c[i] = n
	}
	return c
}

// ShareACL is the NFSv4 ACL of a share's root directory, in the form setfacl -m takes and
// getfacl -q prints (so apply can see whether it is set): read-write users and groups get
// modify_set (everything but write_acl and write_owner), read-only ones read_set and execute
// (traversal), both inherited by files and directories; the owner of a file keeps full control
// of it (owner@), and the group and everyone else get nothing. A name that is not on the NAS
// (a keelAuth user or group with a problem) is left out.
func ShareACL(st *State, sh Share) []string {
	const (
		modify = "rwxpDdaARWc--s"
		read   = "r-x---a-R-c--s"
		full   = "rwxpDdaARWcCos"
		none   = "--------------"
	)
	var acl []string
	add := func(name, perm string) {
		kind := "user"
		if g, ok := strings.CutPrefix(name, "@"); ok {
			if !st.GroupMade(g) {
				return
			}
			kind, name = "group", g
		} else if !st.UserMade(name) {
			return
		}
		acl = append(acl, kind+":"+name+":"+perm+":fd-----:allow")
	}
	for _, n := range sorted(sh.RW) {
		add(n, modify)
	}
	for _, n := range sorted(sh.RO) {
		if !slices.Contains(sh.RW, n) {
			add(n, read)
		}
	}
	return append(acl, "owner@:"+full+":fd-----:allow", "group@:"+none+":fd-----:allow", "everyone@:"+none+":fd-----:allow")
}

// UserMade: the user exists on the NAS (local, or from keelAuth and made).
func (st *State) UserMade(name string) bool {
	for _, u := range st.Users {
		if u.Name == name {
			return true
		}
	}
	if st.Directory != nil {
		for _, u := range st.Directory.Users {
			if u.Name == name {
				return u.Problem == ""
			}
		}
	}
	return false
}

// GroupMade: the keelAuth group exists on the NAS.
func (st *State) GroupMade(name string) bool {
	if st.Directory != nil {
		for _, g := range st.Directory.Groups {
			if g.Name == name {
				return g.Problem == ""
			}
		}
	}
	return false
}

func sorted(a []string) []string {
	c := slices.Clone(a)
	slices.Sort(c)
	return c
}

func union(a, b []string) []string {
	c := append(slices.Clone(a), b...)
	slices.Sort(c)
	return slices.Compact(c)
}

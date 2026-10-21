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
	b.WriteString(`[global]
  server role = standalone server
  server string = keelNAS
  map to guest = Never
  restrict anonymous = 2
  server min protocol = SMB2
  disable netbios = yes
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
		if len(sh.RW) > 0 {
			fmt.Fprintf(&b, "  write list = %s\n", strings.Join(smbNames(sorted(sh.RW)), " "))
		}
		b.WriteString("  read only = yes\n")
		fmt.Fprintf(&b, "  force group = %s\n", Group)
		b.WriteString("  create mask = 0660\n")
		b.WriteString("  directory mask = 2770\n")
		// Windows' "previous versions" from the share's snapshots (docs/04 section 9.3).
		b.WriteString("  vfs objects = shadow_copy2\n")
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

package nas

import (
	"errors"
	"net/http"
	"slices"
	"strings"
)

// Disks that hold something keelNAS did not put there, and pools that are on the disks but not
// imported (docs/04 §4.1). keelNAS writes only on empty disks: a disk with another pool's ZFS
// label, a partition table, a file system or any data in its first MiB is never offered for
// storage and is refused by every path that writes a disk. A pool that is not imported can be
// taken over as it is ("import"), with its data.

// headCheck is how much of a disk's start must be zeros for it to count as empty.
const headCheck = 1 << 20

// poolsRoot: an imported pool's datasets are mounted below poolsRoot/<pool> (zpool import -R),
// so that their own mountpoints (/usr/home, /var/…) never cover keelNAS's file systems.
const poolsRoot = "/pools"

// importableDevices: the importable pools, and for each disk the pool it belongs to.
func (s *Server) importable(names []string, labels map[string]string) ([]Importable, map[string]Importable) {
	out, _ := s.Sys.Run("", "zpool", "import")
	pools := ParseImport(out)
	byDisk := map[string]Importable{}
	for _, p := range pools {
		for _, d := range p.Devices {
			if n := DiskOf(d.Path, labels, names); n != "" {
				byDisk[n] = p
			}
		}
	}
	return pools, byDisk
}

// checkedDisks is disks() with what is on the disks that are in no imported pool (pool labels
// of other pools, partitions, file systems, data), and Free set.
func (s *Server) checkedDisks() ([]Disk, error) {
	disks, err := s.disks()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(disks))
	for i, d := range disks {
		names[i] = d.Name
	}
	if go_, err := s.Sys.Run("", "geom", "disk", "list"); err == nil {
		names = names[:0]
		for _, d := range ParseGeomDisks(go_) {
			names = append(names, d.Name)
		}
	}
	lo, _ := s.Sys.Run("", "glabel", "status", "-s")
	labels := ParseGlabel(lo)
	_, byDisk := s.importable(names, labels)
	gp, gerr := s.Sys.Run("", "gpart", "show", "-p")
	tables := ParseGpartShow(gp)
	for i := range disks {
		d := &disks[i]
		if d.Pool != "" || d.System {
			continue
		}
		if p, ok := byDisk[d.Name]; ok {
			d.Pool, d.PoolID, d.PoolState = p.Name, p.GUID, "not_imported"
			continue
		}
		// the disk itself and its partitions: a ZFS label on any of them
		var scheme string
		devs := []string{d.Name}
		for _, t := range tables {
			if DiskOf(t.Provider, nil, []string{d.Name}) == d.Name {
				if t.Provider == d.Name {
					scheme = t.Scheme
				}
				devs = append(devs, t.Parts...)
			}
		}
		unknown := ""
		if gerr != nil && strings.TrimSpace(gp) != "" {
			unknown = "partitions"
		}
		for _, dev := range devs {
			out, _ := s.Sys.Run("", "zdb", "-l", "/dev/"+dev)
			l, ok := ParseZdbLabel(out)
			if !ok {
				unknown = "zfs label"
				continue
			}
			if l.Found {
				d.Pool, d.PoolID, d.PoolState = l.Name, l.GUID, "not_imported"
				if d.Pool == "" {
					d.Pool = "(zfs)" // a cache or spare device of some pool
				}
				if l.State == "2" {
					d.PoolState = "destroyed"
				}
				break
			}
		}
		switch {
		case d.Pool != "":
		case scheme != "":
			d.Content = "partitions " + scheme
		case unknown != "":
			d.Content = "unknown"
		default:
			out, err := s.Sys.Run("", "fstyp", "/dev/"+d.Name)
			switch {
			case err == nil && strings.TrimSpace(out) != "":
				d.Content = "filesystem " + strings.TrimSpace(out)
			case err != nil && strings.Contains(out, "not recognized"):
				if s.Heads != nil {
					b, err := s.Heads.ReadHead(d.Name, headCheck)
					switch {
					case err != nil:
						d.Content = "unknown"
					case slices.ContainsFunc(b, func(c byte) bool { return c != 0 }):
						d.Content = "data"
					}
				}
			default:
				d.Content = "unknown"
			}
		}
	}
	for i := range disks {
		disks[i].Free = disks[i].Pool == "" && !disks[i].System && disks[i].Content == ""
	}
	return disks, nil
}

// diskBusy explains why a disk cannot take storage ("" = it is free).
func diskBusy(d Disk) string {
	switch {
	case d.System:
		return "disk " + d.Name + " holds the keelNAS system"
	case d.PoolState == "imported" || (d.Pool != "" && d.PoolState == ""):
		return "disk " + d.Name + " is in use (pool " + d.Pool + ")"
	case d.PoolState == "destroyed":
		return "disk " + d.Name + " holds a destroyed ZFS pool " + d.Pool + " (id " + d.PoolID + "): its data can still be recovered, so keelNAS does not write on it"
	case d.Pool != "":
		return "disk " + d.Name + " belongs to ZFS pool " + d.Pool + " (id " + d.PoolID + "), which is not imported: keelNAS never writes on another pool's disks; import the pool to use its data"
	case d.Content == "unknown":
		return "cannot tell whether disk " + d.Name + " is empty: keelNAS only writes on disks it checked"
	case d.Content != "":
		return "disk " + d.Name + " is not empty (" + d.Content + "): keelNAS only uses empty disks"
	}
	return ""
}

// ---- importing a pool that is on the disks ----

type ImportableDisk struct {
	Name   string `json:"name"`   // the disk ("" = not found: a missing device)
	Device string `json:"device"` // as zpool import names it: gpt/slot01, vtbd1, …
	State  string `json:"state"`
	Size   int64  `json:"size"`
}

type ImportablePool struct {
	Name      string           `json:"name"`
	ID        string           `json:"id"`
	State     string           `json:"state"`
	Status    string           `json:"status"`
	Action    string           `json:"action"`
	Layout    string           `json:"layout"`
	OtherHost bool             `json:"other_host"` // last used by another system: import needs force
	LastHost  string           `json:"last_host"`  // that system's host name, from the label
	NameTaken bool             `json:"name_taken"` // a pool of that name is imported: give a new name
	Size      int64            `json:"size"`       // the member disks' sizes added up (raw)
	Disks     []ImportableDisk `json:"disks"`
}

func (s *Server) importablePools() ([]ImportablePool, error) {
	out, err := s.Sys.Run("", "geom", "disk", "list")
	if err != nil {
		return nil, err
	}
	geom := ParseGeomDisks(out)
	names := make([]string, len(geom))
	for i, d := range geom {
		names[i] = d.Name
	}
	lo, _ := s.Sys.Run("", "glabel", "status", "-s")
	labels := ParseGlabel(lo)
	pools, _ := s.importable(names, labels)
	taken := map[string]bool{}
	for _, n := range s.importedPools() {
		taken[n] = true
	}
	res := []ImportablePool{}
	for _, p := range pools {
		ip := ImportablePool{Name: p.Name, ID: p.GUID, State: p.State, Status: p.Status, Action: p.Action,
			Layout: p.Layout, OtherHost: p.OtherHost(), NameTaken: taken[p.Name], Disks: []ImportableDisk{}}
		for _, dv := range p.Devices {
			x := ImportableDisk{Name: DiskOf(dv.Path, labels, names), Device: dv.Path, State: dv.State}
			for _, g := range geom {
				if g.Name == x.Name {
					x.Size = g.Size
				}
			}
			ip.Size += x.Size
			if ip.LastHost == "" && x.Name != "" {
				lo, _ := s.Sys.Run("", "zdb", "-l", "/dev/"+strings.TrimPrefix(dv.Path, "/dev/"))
				if l, ok := ParseZdbLabel(lo); ok && l.Found {
					ip.LastHost = l.Hostname
				}
			}
			ip.Disks = append(ip.Disks, x)
		}
		res = append(res, ip)
	}
	return res, nil
}

func (s *Server) getImportable(r *http.Request) (any, error) {
	s.mu.Lock() // not while an import runs
	defer s.mu.Unlock()
	p, err := s.importablePools()
	if err != nil {
		return nil, err
	}
	return map[string]any{"pools": p}, nil
}

// postImport takes over a pool that is on the disks: {name: new name (optional), force: true
// when it was last used by another system}. Its datasets are mounted below /pools/<name>
// (altroot, kept in the state and used again at every start), the pool itself is not changed
// (no upgrade, no property set), except the name when a new one is given.
func (s *Server) postImport(r *http.Request) (any, error) {
	id := r.PathValue("id")
	var req struct {
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pools, err := s.importablePools()
	if err != nil {
		return nil, err
	}
	var p *ImportablePool
	for i := range pools {
		if pools[i].ID == id {
			p = &pools[i]
		}
	}
	if p == nil {
		return nil, notFound("no pool with id %s that can be imported", id)
	}
	st := s.st.Clone()
	for _, x := range st.Pools {
		if x.GUID == id {
			return nil, conflict("pool %s is in the settings already: it is imported at the next apply", x.Name)
		}
	}
	name := p.Name
	if req.Name != "" {
		if !ValidName(req.Name) {
			return nil, bad("name: lower-case letters, digits, - and _, a letter first, at most 32")
		}
		name = req.Name
	}
	imported := s.importedPools()
	for _, n := range imported {
		if n == name {
			return nil, conflict("a pool named %s is imported already: import this one under another name", name)
		}
	}
	if st.Pool(name) != nil {
		return nil, conflict("a storage named %s is in the settings: import this one under another name", name)
	}
	if p.OtherHost && !req.Force {
		host := p.LastHost
		if host == "" {
			host = "another system"
		}
		return nil, conflict("pool %s was last used by %s: import it only when that system no longer uses these disks, and confirm with force", p.Name, host)
	}
	altroot := poolsRoot + "/" + name
	args := []string{"import"}
	if p.OtherHost {
		args = append(args, "-f")
	}
	args = append(args, "-R", altroot, id)
	if name != p.Name {
		args = append(args, name)
	}
	_, ierr := s.run("", "zpool", args...)
	if _, ok := s.importedPools()[id]; !ok {
		if ierr == nil {
			ierr = errors.New("zpool import said nothing, but the pool is not imported")
		}
		return nil, ierr
	}
	warn := ""
	if ierr != nil {
		// imported, but a dataset could not be mounted (an encrypted one without its key, …)
		warn = ierr.Error()
	}
	s.Log.Printf("imported pool %s (id %s) below %s", name, id, altroot)
	st.Pools = append(st.Pools, Pool{Name: name, GUID: id, Altroot: altroot})
	errs, err := s.commit(st)
	if err != nil {
		return nil, err
	}
	item, err := s.storageItem(st, name)
	if err != nil {
		return nil, err
	}
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{"storage": item, "altroot": altroot, "warning": warn, "errors": errs}, nil
}

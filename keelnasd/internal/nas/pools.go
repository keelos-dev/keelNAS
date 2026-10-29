package nas

import (
	"net/http"
	"slices"
	"strings"
)

// Export, expand, delete a storage; clear a stray ZFS label (docs/04 §17.3). Ported 2026-10-23
// from keelOS: keel/internal/api POST /v1/storage/export, /pool/expand, /pool/delete,
// /labelclear and keel/internal/install's ExportPool, ExpandPool, DestroyPool, freeDevice;
// keel's confirm rule kept (the pool's name), keel's guests become keelNAS's shares and LUNs.

// poolGrowth: what the pool gains when its devices are brought to their full size: zpool's
// expandsize, or (keel's mirrorGrowth: expandsize says "-" until the vdev is reopened) what a
// single disk or mirror would gain at its smallest working device's size, when that is at least
// 2 GiB and 2 % (less is labels and metaslab rounding).
func poolGrowth(p PoolInfo) int64 {
	var sum int64
	for _, t := range p.Tops {
		if (t.Type != "disk" && t.Type != "mirror") || t.Size <= 0 {
			continue
		}
		var least int64 = -1
		for _, m := range p.Members {
			if m.Top == t.Name && m.State == "ONLINE" && m.Size > 0 && (least < 0 || m.Size < least) {
				least = m.Size
			}
		}
		if g := least - t.Size; least > 0 && g >= 2<<30 && g >= t.Size/50 {
			sum += g
		}
	}
	return max(sum, p.ExpandSize)
}

// usersOf: the shares and LUNs on the pool (they keep it from being exported or deleted).
func usersOf(st *State, pool string) []string {
	var u []string
	for _, sh := range st.Shares {
		if PoolOf(sh.Dataset) == pool {
			u = append(u, "share "+sh.Name)
		}
	}
	for _, l := range st.LUNs {
		if l.Pool == pool {
			u = append(u, "LUN "+l.Name)
		}
	}
	return u
}

// postExport: POST /v1/storage/{pool}/export {"confirm": the pool's name}: the pool leaves
// keelNAS (zpool export) with its data; GET /v1/importable lists it again.
func (s *Server) postExport(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Confirm != pool {
		return nil, bad("confirm must be the storage's name (%q): it leaves keelNAS until it is imported again", pool)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if _, err := s.poolNow(st, pool); err != nil {
		return nil, err
	}
	if u := usersOf(st, pool); len(u) > 0 {
		return nil, conflict("storage %s still has %s: remove them first (their data stays)", pool, strings.Join(u, ", "))
	}
	if _, err := s.run("", "zpool", "export", pool); err != nil {
		return nil, err
	}
	s.Log.Printf("exported pool %s", pool)
	st.Pools = slices.DeleteFunc(st.Pools, func(p Pool) bool { return p.Name == pool })
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "pool": pool}, nil
}

// postExpand: POST /v1/storage/{pool}/expand: zpool online -e on each working disk (keel's
// ExpandPool; autoexpand is left as it is).
func (s *Server) postExpand(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	p, err := s.poolNow(st, pool)
	if err != nil {
		return nil, err
	}
	var errs []string
	n := 0
	for _, m := range p.Members {
		if m.State != "ONLINE" || m.Missing {
			continue
		}
		n++
		if _, err := s.run("", "zpool", "online", "-e", pool, m.Path); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if n == 0 {
		return nil, conflict("storage %s has no working disk to expand", pool)
	}
	if len(errs) == n {
		return nil, conflict("%s", strings.Join(errs, "; "))
	}
	x, err := s.storageItem(st, pool)
	if err != nil {
		return nil, err
	}
	return map[string]any{"storage": x, "was": p.Size, "errors": append([]string{}, errs...)}, nil
}

// deleteStorage: DELETE /v1/storage/{pool}?confirm=<name>: zpool destroy, its disks' labels
// cleared (free again); a storage that is missing is only forgotten. Refused (409) while shares
// or LUNs are on it.
func (s *Server) deleteStorage(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	if r.URL.Query().Get("confirm") != pool {
		return nil, bad("confirm must be the storage's name (%q): everything on it is lost", pool)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	p, err := s.poolNow(st, pool)
	missing := err != nil && p.Health == "MISSING"
	if err != nil && !missing {
		return nil, err
	}
	if u := usersOf(st, pool); len(u) > 0 {
		return nil, conflict("storage %s still has %s: remove them first", pool, strings.Join(u, ", "))
	}
	var disks []string
	if !missing {
		disks = s.memberDisks(p)
		if _, err := s.run("", "zpool", "destroy", pool); err != nil {
			return nil, err
		}
		s.Log.Printf("destroyed pool %s", pool)
		for _, d := range disks {
			s.clearDisk(d)
		}
	}
	st.Pools = slices.DeleteFunc(st.Pools, func(p Pool) bool { return p.Name == pool })
	st.Datasets = slices.DeleteFunc(st.Datasets, func(d string) bool { return PoolOf(d) == pool })
	for k := range st.AutoSnap {
		if PoolOf(k) == pool {
			delete(st.AutoSnap, k)
		}
	}
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	freed := []string{}
	for _, d := range disks {
		if d != "" {
			freed = append(freed, d)
		}
	}
	return map[string]any{"ok": true, "pool": pool, "forgotten": missing, "disks": freed}, nil
}

// postLabelclear: POST /v1/disks/{name}/labelclear {"confirm"}: clears the ZFS labels on a
// disk (and its partitions) that is in no imported pool: a destroyed pool's leftover, or a pool
// that is not imported (its data on this disk is gone). confirm: the pool's name as the label
// gives it (GET /v1/disks pool), the disk's name for a label without one. §14's "a separate
// operation, confirmed with the pool's name".
func (s *Server) postLabelclear(r *http.Request) (any, error) {
	name := r.PathValue("name")
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == s.SeedDisk() {
		return nil, conflict("disk %s is the seed disk from keelOS", name)
	}
	disks, err := s.checkedDisks()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(disks, func(d Disk) bool { return d.Name == name })
	if i < 0 {
		return nil, notFound("no disk %s", name)
	}
	d := disks[i]
	switch {
	case d.System:
		return nil, conflict("disk %s holds the system", name)
	case d.PoolState == "imported":
		return nil, conflict("disk %s is in storage %s: detach it or delete the storage", name, d.Pool)
	case d.PoolState != "not_imported" && d.PoolState != "destroyed":
		return nil, conflict("disk %s has no ZFS label", name)
	}
	want := d.Pool
	if want == "" || want == "(zfs)" {
		want = name
	}
	if req.Confirm != want {
		return nil, bad("confirm must be %q: clearing the labels destroys what pool %s held on this disk", want, d.Pool)
	}
	devs := []string{name}
	if gp, err := s.Sys.Run("", "gpart", "show", "-p"); err == nil {
		for _, t := range ParseGpartShow(gp) {
			if t.Provider == name {
				devs = append(devs, t.Parts...)
			}
		}
	}
	cleared := []string{}
	for _, dev := range devs {
		out, _ := s.Sys.Run("", "zdb", "-l", "/dev/"+dev)
		if l, ok := ParseZdbLabel(out); ok && !l.Found {
			continue
		}
		if _, err := s.run("", "zpool", "labelclear", "-f", "/dev/"+dev); err != nil {
			return nil, err
		}
		cleared = append(cleared, dev)
		if dev == name { // a whole-disk label: the disk is free afterwards (zeroHead)
			s.zeroHead(name)
		}
	}
	s.Log.Printf("cleared the ZFS labels of pool %s on %v", d.Pool, cleared)
	return map[string]any{"ok": true, "disk": name, "cleared": cleared}, nil
}

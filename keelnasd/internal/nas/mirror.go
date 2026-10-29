package nas

import (
	"fmt"
	"net/http"
	"strings"
)

// Mirror disks (docs/04 §17.2), ported 2026-10-23 from keelOS: keel/internal/vm/storage.go's
// rules (Detachable: never a mirror's last device, nor its last working one; AttachTarget: a
// single disk or a mirror, not raidz) and keel/internal/api's POST /v1/storage/pool/attach and
// /detach; replace is new (keel has none): zpool replace of a missing or failed member. keelNAS
// uses whole disks, so keel's partitioning and boot copies are left out. Every new disk passes
// freeDisk first: keelNAS never writes on another pool's disks (409), and no command takes -f.

// poolNow: the pool as zpool sees it now; 404 when it is not a storage, 409 when it is missing.
func (s *Server) poolNow(st *State, name string) (PoolInfo, error) {
	if st.Pool(name) == nil {
		return PoolInfo{}, notFound("no storage %s", name)
	}
	pools, err := s.dataPools(st)
	if err != nil {
		return PoolInfo{}, err
	}
	for _, p := range pools {
		if p.Name == name {
			if p.Health == "MISSING" {
				return p, conflict("storage %s is not imported", name)
			}
			return p, nil
		}
	}
	return PoolInfo{}, notFound("no storage %s", name)
}

// memberDisks: each member's disk name ("" for a missing one), as GET /v1/storage shows it.
func (s *Server) memberDisks(p PoolInfo) []string {
	lo, _ := s.Sys.Run("", "glabel", "status", "-s")
	labels := ParseGlabel(lo)
	var names []string
	if out, err := s.Sys.Run("", "geom", "disk", "list"); err == nil {
		for _, d := range ParseGeomDisks(out) {
			names = append(names, d.Name)
		}
	}
	res := make([]string, len(p.Members))
	for i, m := range p.Members {
		if m.Missing {
			continue
		}
		if res[i] = DiskOf(m.Path, labels, names); res[i] == "" {
			res[i] = strings.TrimPrefix(m.Path, "/dev/")
		}
	}
	return res
}

// findMember: the member named by its disk name or its guid.
func (s *Server) findMember(p PoolInfo, name string) (int, error) {
	disks := s.memberDisks(p)
	for i, m := range p.Members {
		if name != "" && (m.GUID == name || disks[i] == name) {
			return i, nil
		}
	}
	return -1, notFound("storage %s has no disk %s", p.Name, name)
}

func topOf(p PoolInfo, name string) Top {
	for _, t := range p.Tops {
		if t.Name == name {
			return t
		}
	}
	return Top{}
}

// working: a device that holds the data (keel's working()).
func working(m Member) bool {
	switch m.State {
	case "UNAVAIL", "REMOVED", "FAULTED", "OFFLINE":
		return false
	}
	return !m.Missing
}

// Detachable: member i can leave the pool with zpool detach (keel's Pool.Detachable).
func Detachable(p PoolInfo, i int, name string) error {
	m := p.Members[i]
	if t := topOf(p, m.Top); t.Type != "mirror" {
		return conflict("%s is not part of a mirror: it cannot be detached", name)
	}
	n, ok := 0, 0
	for _, x := range p.Members {
		if x.Top == m.Top {
			n++
			if working(x) {
				ok++
			}
		}
	}
	if n < 2 {
		return conflict("%s is the mirror's last device", name)
	}
	if working(m) && ok < 2 { // zpool would say "no valid replicas"
		return conflict("%s is the mirror's last working disk: detach the missing or failed ones, or attach another disk first", name)
	}
	return nil
}

// diskSize: a disk's size from geom (0: not known).
func (s *Server) diskSize(name string) int64 {
	out, err := s.Sys.Run("", "geom", "disk", "list")
	if err != nil {
		return 0
	}
	for _, d := range ParseGeomDisks(out) {
		if d.Name == name {
			return d.Size
		}
	}
	return 0
}

// bigEnough: a new disk for vdev top must be at least as big as its smallest working device
// (zpool would refuse a smaller one: "device is too small"; keel's AttachNeed is finer, with
// partitions; keelNAS's whole disks compare as they are).
func (s *Server) bigEnough(p PoolInfo, top, disk string, skip int) error {
	var need int64
	for i, m := range p.Members {
		if m.Top == top && i != skip && working(m) && m.Size > 0 && (need == 0 || m.Size < need) {
			need = m.Size
		}
	}
	if skip >= 0 && need == 0 {
		need = p.Members[skip].Size
	}
	if have := s.diskSize(disk); need > 0 && have > 0 && have < need {
		return conflict("disk %s (%s) is smaller than the disks of %s (%s): a mirror member cannot be smaller", disk, gibText(have), p.Name, gibText(need))
	}
	return nil
}

func gibText(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

// clearDisk removes what ZFS left on a disk that left a pool (keel's freeDevice): a detached
// or destroyed pool's label would make it "not free" (§14).
func (s *Server) clearDisk(disk string) {
	if disk == "" {
		return
	}
	if _, err := s.Sys.Run("", "zpool", "labelclear", "-f", "/dev/"+disk); err != nil {
		s.Log.Printf("labelclear %s: %v", disk, err)
	}
	s.zeroHead(disk)
}

// zeroHead zeroes the first 4 MiB of a disk keelNAS has let go: labelclear (and a detach) leave
// the uberblocks of the front labels, and checkedDisks would call the disk "data", not free
// (QEMU 2026-10-23). 4 MiB: ZFS's two front labels and its boot block, more than headCheck.
func (s *Server) zeroHead(disk string) {
	if _, err := s.Sys.Run("", "dd", "if=/dev/zero", "of=/dev/"+disk, "bs=1m", "count=4"); err != nil {
		s.Log.Printf("zeroing the start of %s: %v", disk, err)
	}
}

// postAttach: POST /v1/storage/{pool}/attach {"disk", "to"}: disk becomes a mirror of to (a
// member disk or a top-level vdev; default: the pool's only single disk or mirror).
func (s *Server) postAttach(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	var req struct {
		Disk string `json:"disk"`
		To   string `json:"to"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Disk == "" {
		return nil, bad("give the disk to attach")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	p, err := s.poolNow(st, pool)
	if err != nil {
		return nil, err
	}
	top := ""
	switch {
	case req.To == "":
		for _, t := range p.Tops {
			if t.Type == "disk" || t.Type == "mirror" {
				if top != "" {
					return nil, bad("storage %s has more than one disk or mirror: say which (to)", pool)
				}
				top = t.Name
			}
		}
	case topOf(p, req.To).Name != "":
		top = req.To
	default:
		i, err := s.findMember(p, req.To)
		if err != nil {
			return nil, err
		}
		top = p.Members[i].Top
	}
	if t := topOf(p, top); t.Type != "disk" && t.Type != "mirror" {
		return nil, conflict("a disk can be attached to a mirror or to a single disk only")
	}
	disks := s.memberDisks(p)
	target := ""
	for i, m := range p.Members {
		if m.Top == top && working(m) && disks[i] != "" {
			target = disks[i]
			break
		}
	}
	if target == "" {
		return nil, conflict("%s has no working disk to attach to", top)
	}
	if err := s.freeDisk(req.Disk); err != nil {
		return nil, err
	}
	if err := s.bigEnough(p, top, req.Disk, -1); err != nil {
		return nil, err
	}
	// no -f: zpool refuses a disk that belongs to another pool (freeDisk checked it too)
	if _, err := s.run("", "zpool", "attach", pool, target, req.Disk); err != nil {
		return nil, err
	}
	s.scrubs.Kick()
	s.applyLocked()
	return s.storageItem(st, pool)
}

// postDetach: POST /v1/storage/{pool}/detach {"disk"}: a disk (name, or the guid of a missing
// one) leaves its mirror; a disk that is there gets its label cleared and is free again.
func (s *Server) postDetach(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	var req struct {
		Disk string `json:"disk"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	p, err := s.poolNow(st, pool)
	if err != nil {
		return nil, err
	}
	i, err := s.findMember(p, req.Disk)
	if err != nil {
		return nil, err
	}
	if err := Detachable(p, i, req.Disk); err != nil {
		return nil, err
	}
	disk := s.memberDisks(p)[i]
	if _, err := s.run("", "zpool", "detach", pool, p.Members[i].GUID); err != nil {
		return nil, err
	}
	s.clearDisk(disk)
	s.applyLocked()
	return s.storageItem(st, pool)
}

// postReplace: POST /v1/storage/{pool}/replace {"old", "disk"}: disk takes the place of old (a
// member disk, or the guid of a missing one) and is resilvered; when it is done ZFS lets the old
// one go.
func (s *Server) postReplace(r *http.Request) (any, error) {
	pool := r.PathValue("pool")
	var req struct {
		Old  string `json:"old"`
		Disk string `json:"disk"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Old == "" || req.Disk == "" {
		return nil, bad("give old (the disk or guid to replace) and disk (the new one)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	p, err := s.poolNow(st, pool)
	if err != nil {
		return nil, err
	}
	i, err := s.findMember(p, req.Old)
	if err != nil {
		return nil, err
	}
	if err := s.freeDisk(req.Disk); err != nil {
		return nil, err
	}
	if err := s.bigEnough(p, p.Members[i].Top, req.Disk, i); err != nil {
		return nil, err
	}
	old := s.memberDisks(p)[i]
	if _, err := s.run("", "zpool", "replace", pool, p.Members[i].GUID, req.Disk); err != nil {
		return nil, err
	}
	if old != "" {
		s.hmu.Lock()
		s.replaced = append(s.replaced, replacedDisk{Pool: pool, PoolGUID: p.GUID, Member: p.Members[i].GUID, Disk: old})
		s.hmu.Unlock()
	}
	s.scrubs.Kick()
	s.applyLocked()
	return s.storageItem(st, pool)
}

// replacedDisk is a disk that zpool replace is taking out of a pool. When the resilver is done
// ZFS lets it go but leaves its label (QEMU 2026-10-23: the old disk then shows as "pool tank,
// not imported" with the imported pool's id), so the watch clears it (releaseReplaced). Kept in
// memory only: after a restart such a disk needs POST /v1/disks/{name}/labelclear.
type replacedDisk struct {
	Pool, PoolGUID, Member, Disk string
}

// releaseReplaced clears the label of each replaced disk once it is no longer in its pool, is
// in no imported pool, and its label is still that pool's.
func (s *Server) releaseReplaced() {
	s.hmu.Lock()
	todo := append([]replacedDisk(nil), s.replaced...)
	s.hmu.Unlock()
	if len(todo) == 0 {
		return
	}
	pools, err := s.dataPools(s.State())
	if err != nil {
		return
	}
	disks, err := s.allDisks()
	if err != nil {
		return
	}
	var keep []replacedDisk
	for _, r := range todo {
		gone, found := true, false
		for _, p := range pools {
			if p.GUID != r.PoolGUID || p.Health == "MISSING" {
				continue
			}
			found = true
			for _, m := range p.Members {
				if m.GUID == r.Member {
					gone = false
				}
			}
		}
		if !found || !gone { // still resilvering (or the pool is away): look again later
			keep = append(keep, r)
			continue
		}
		inPool := false
		for _, d := range disks {
			if d.Name == r.Disk && d.Pool != "" {
				inPool = true
			}
		}
		out, _ := s.Sys.Run("", "zdb", "-l", "/dev/"+r.Disk)
		if l, ok := ParseZdbLabel(out); !inPool && ok && l.Found && l.GUID == r.PoolGUID {
			s.clearDisk(r.Disk)
			s.Log.Printf("%s left storage %s (replaced): its label is cleared", r.Disk, r.Pool)
		}
	}
	s.hmu.Lock()
	s.replaced = keep
	s.hmu.Unlock()
}

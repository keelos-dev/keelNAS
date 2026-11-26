package nas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Keel's volumes (keelOS docs/90 §10.3): a guest's disk of the host keelNAS runs on, as an
// iSCSI LUN over keel's data link (seed "data", network.go). One zvol and one target per disk
// (one TCP connection per iSCSI session: a target per disk spreads the disks over sessions and
// a broken session takes only its disk, docs/90 §10.4). They are not keelNAS's LUNs: the LUN
// list and its page do not show them, and only keel (with the instance key) makes or deletes
// them. The portal listens only on the data link's address; a target lets in only the
// initiator name keel gave when it made the volume.

// KeelVol is one of keel's volumes in state.json.
type KeelVol struct {
	Name      string `json:"name"` // keel's: <guest uuid>-<disk id>
	Pool      string `json:"pool"`
	SizeGiB   int    `json:"size_gib"`
	Initiator string `json:"initiator"` // the host's iSCSI initiator name
}

// KeelVolInfo is what the API says about one.
type KeelVolInfo struct {
	KeelVol
	Dataset string `json:"dataset"`
	File    string `json:"file"`
	IQN     string `json:"iqn"`
	Serial  string `json:"serial"` // the LUN's serial: the host finds the disk as /dev/diskid/DISK-<serial>
	Portal  string `json:"portal"` // the data link's address ("" = no data link: not exported)
}

var (
	keelVolRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	initiatorRE = regexp.MustCompile(`^iqn\.[0-9]{4}-[0-9]{2}\.[a-z0-9.-]+(:[A-Za-z0-9.:_-]+)?$`)
)

// KeelVolDataset: <pool>/keel/<name>, a file system holding the disk as one sparse file
// (disk.raw). Not a zvol yet: when this was written new zvols on .240 got no /dev/zvol node,
// so CTL could not open them (keelOS docs/90 §10.7). That was the geom → dev switch failing
// (apply.go ZvolDefaultDev, fixed 2026-11-25), so a zvol is possible again; the move back is
// keelOS docs/90 §10.9. CTL's block back end takes a file as well.
func KeelVolDataset(v KeelVol) string { return v.Pool + "/keel/" + v.Name }

// keelVolMount: the volume's mountpoint as zfs takes it (without the pool's altroot).
func keelVolMount(name string) string { return "/keelvol/" + name }

// keelVolFile: the disk file's path, with the pool's altroot.
func keelVolFile(st *State, v KeelVol) string {
	root := ""
	if p := st.Pool(v.Pool); p != nil {
		root = p.Altroot
	}
	return root + keelVolMount(v.Name) + "/disk.raw"
}

// KeelVolIQN: the target's name; no host name in it (a renamed keelNAS keeps its targets).
func KeelVolIQN(name string) string { return "iqn.2026-11.dev.keelos:keel:" + name }

// KeelVolSerial: fixed by the name (CTL takes 16 bytes).
func KeelVolSerial(name string) string {
	h := sha256.Sum256([]byte("keel/" + name))
	return "KK" + strings.ToUpper(hex.EncodeToString(h[:7]))
}

// dataAddr: the data link's address without its prefix ("" = none).
func (s *Server) dataAddr() string {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	if s.seed == nil || s.seed.Data == nil {
		return ""
	}
	a, _, _ := strings.Cut(s.seed.Data.Address, "/")
	return a
}

// KeelVol finds one by name.
func (s *State) KeelVol(name string) *KeelVol {
	for i := range s.KeelVols {
		if s.KeelVols[i].Name == name {
			return &s.KeelVols[i]
		}
	}
	return nil
}

func (s *Server) keelVolInfo(st *State, v KeelVol) KeelVolInfo {
	return KeelVolInfo{KeelVol: v, Dataset: KeelVolDataset(v), File: keelVolFile(st, v), IQN: KeelVolIQN(v.Name), Serial: KeelVolSerial(v.Name), Portal: s.dataAddr()}
}

// ctlKeel is ctl.conf's part for keel's volumes: their portal group on the data link and a
// target per volume that only its initiator may log in to. "" without a data link.
func ctlKeel(st *State, vols []KeelVol, addr string) string {
	if addr == "" || len(vols) == 0 {
		return ""
	}
	vols = slices.Clone(vols)
	slices.SortFunc(vols, func(a, b KeelVol) int { return strings.Compare(a.Name, b.Name) })
	var b strings.Builder
	fmt.Fprintf(&b, `
portal-group pgkeel {
	discovery-auth-group no-access
	listen %s
}
`, addr)
	groups := map[string]string{} // initiator -> auth group
	var inits []string
	for _, v := range vols {
		if _, ok := groups[v.Initiator]; !ok {
			groups[v.Initiator] = "agkeel" + strconv.Itoa(len(groups))
			inits = append(inits, v.Initiator)
		}
	}
	for _, in := range inits {
		fmt.Fprintf(&b, `
auth-group %s {
	auth-type none
	initiator-name "%s"
}
`, groups[in], in)
	}
	for _, v := range vols {
		fmt.Fprintf(&b, `
target %s {
	auth-group %s
	portal-group pgkeel
	lun 0 {
		path %s
		serial "%s"
		device-id "keel %s"
	}
}
`, KeelVolIQN(v.Name), groups[v.Initiator], keelVolFile(st, v), KeelVolSerial(v.Name), v.Name)
	}
	return b.String()
}

// ensureKeelVol makes the volume's file system and its sparse disk file when they are missing.
// An existing file is never truncated (its size is the disk's).
func (s *Server) ensureKeelVol(st *State, v KeelVol, ds map[string]Dataset) error {
	if err := s.ensureParent(v.Pool+"/keel", ds); err != nil {
		return err
	}
	name := KeelVolDataset(v)
	if d, ok := ds[name]; ok && d.Type != "filesystem" {
		return fmt.Errorf("%s is a %s, not keel's volume", name, d.Type)
	} else if !ok {
		// 64K records: a guest's disk is written in small blocks, unlike a share's files
		if _, err := s.run("", "zfs", "create", "-o", "mountpoint="+keelVolMount(v.Name), "-o", "recordsize=64K", name); err != nil {
			return err
		}
		ds[name] = Dataset{Name: name, Type: "filesystem", Mountpoint: keelVolMount(v.Name)}
	}
	f := keelVolFile(st, v)
	if _, err := s.Sys.Run("", "test", "-e", f); err == nil {
		return nil
	}
	_, err := s.run("", "truncate", "-s", strconv.Itoa(v.SizeGiB)+"G", f)
	return err
}

func (s *Server) getKeelVols(r *http.Request) (any, error) {
	st := s.State()
	out := []KeelVolInfo{}
	for _, v := range st.KeelVols {
		out = append(out, s.keelVolInfo(st, v))
	}
	return map[string]any{"volumes": out, "portal": s.dataAddr()}, nil
}

// postKeelVol: {name, size_gib, pool ("" = the only pool), initiator}. A volume that exists
// with the same size and initiator is answered again (keel retries after a lost reply).
func (s *Server) postKeelVol(r *http.Request) (any, error) {
	var req KeelVol
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !keelVolRE.MatchString(req.Name) {
		return nil, bad("bad volume name %q: lower-case letters, digits and -, at most 63", req.Name)
	}
	if req.SizeGiB < 1 {
		return nil, bad("size_gib must be at least 1")
	}
	if !initiatorRE.MatchString(req.Initiator) {
		return nil, bad("bad initiator name %q", req.Initiator)
	}
	if s.dataAddr() == "" {
		return nil, conflict("this keelNAS has no data link from keel (seed \"data\"): keel's volumes are not exported without it")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if v := st.KeelVol(req.Name); v != nil {
		if v.SizeGiB == req.SizeGiB && v.Initiator == req.Initiator && (req.Pool == "" || req.Pool == v.Pool) {
			return s.keelVolInfo(st, *v), nil
		}
		return nil, conflict("volume %s exists", req.Name)
	}
	if req.Pool == "" && len(st.Pools) == 1 {
		req.Pool = st.Pools[0].Name
	}
	if st.Pool(req.Pool) == nil {
		return nil, bad("no storage %q (pool: one of this keelNAS's)", req.Pool)
	}
	ds := s.datasets()
	if _, ok := ds[KeelVolDataset(req)]; ok {
		s.Log.Printf("keel volume %s: reusing the existing %s", req.Name, KeelVolDataset(req))
	}
	if err := s.ensureKeelVol(st, req, ds); err != nil {
		return nil, err
	}
	st.KeelVols = append(st.KeelVols, req)
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return s.keelVolInfo(st, req), nil
}

// deleteKeelVol: out of ctl.conf; ?purge=1 also destroys the zvol (keel deleting the disk).
func (s *Server) deleteKeelVol(r *http.Request) (any, error) {
	name := r.PathValue("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	v := st.KeelVol(name)
	if v == nil {
		return nil, notFound("no keel volume %s", name)
	}
	old := *v
	st.KeelVols = slices.DeleteFunc(st.KeelVols, func(x KeelVol) bool { return x.Name == name })
	if _, err := s.commit(st); err != nil { // ctld lets go of the zvol on the reload
		return nil, err
	}
	if purge(r) {
		if _, err := s.run("", "zfs", "destroy", "-r", KeelVolDataset(old)); err != nil {
			return nil, err
		}
	}
	return map[string]any{"deleted": name, "purged": purge(r)}, nil
}

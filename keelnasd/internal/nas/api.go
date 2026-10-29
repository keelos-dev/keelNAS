package nas

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"keelnas/internal/sys"
)

// apiError is an answer other than 200: {"error": "…"} with its status code.
type apiError struct {
	Code int
	Msg  string
}

func (e *apiError) Error() string { return e.Msg }

func bad(format string, a ...any) error {
	return &apiError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}
func notFound(format string, a ...any) error {
	return &apiError{http.StatusNotFound, fmt.Sprintf(format, a...)}
}
func forbidden(format string, a ...any) error {
	return &apiError{http.StatusForbidden, fmt.Sprintf(format, a...)}
}
func conflict(format string, a ...any) error {
	return &apiError{http.StatusConflict, fmt.Sprintf(format, a...)}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	b, _ := json.Marshal(v)
	w.Write(append(b, '\n'))
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.Code, map[string]string{"error": ae.Msg})
		return
	}
	// A failed system command: 500 with the command's output (it is in the error text).
	var ce *sys.CmdError
	if errors.As(err, &ce) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": ce.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return bad("cannot read the request: %v", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return bad("the request has no JSON body")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return bad("bad JSON: %v", err)
	}
	return nil
}

type handler func(r *http.Request) (any, error)

func (s *Server) wrap(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := h(r)
		if err != nil {
			if r.Method != http.MethodGet {
				s.Log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
			}
			writeErr(w, err)
			return
		}
		if r.Method != http.MethodGet {
			s.Log.Printf("%s %s: ok", r.Method, r.URL.Path)
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// Handler is the API. The Unix socket serves it as is; the TCP listener wraps it in RequireKey.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/status", s.wrap(s.getStatus))
	m.HandleFunc("GET /v1/disks", s.wrap(s.getDisks))
	m.HandleFunc("GET /v1/page", s.wrap(s.getPage))
	m.HandleFunc("GET /v1/hardware", s.wrap(s.getHardware))
	m.HandleFunc("GET /v1/hardware/disks/{name}", s.wrap(s.getHardwareDisk))
	m.HandleFunc("GET /v1/storage", s.wrap(s.getStorage))
	m.HandleFunc("POST /v1/storage", s.wrap(s.postStorage))
	m.HandleFunc("POST /v1/storage/{name}/protect", s.wrap(s.postProtect))
	m.HandleFunc("GET /v1/storage/{pool}/scrub", s.wrap(s.getScrub))
	m.HandleFunc("PUT /v1/storage/{pool}/scrub", s.wrap(s.putScrub))
	m.HandleFunc("GET /v1/storage/{pool}/scrub/perf", s.wrap(s.getScrubPerf))
	m.HandleFunc("POST /v1/storage/{pool}/scrub/{what}", s.wrap(s.postScrubAction))
	m.HandleFunc("POST /v1/storage/{pool}/attach", s.wrap(s.postAttach))
	m.HandleFunc("POST /v1/storage/{pool}/detach", s.wrap(s.postDetach))
	m.HandleFunc("POST /v1/storage/{pool}/replace", s.wrap(s.postReplace))
	m.HandleFunc("POST /v1/storage/{pool}/export", s.wrap(s.postExport))
	m.HandleFunc("POST /v1/storage/{pool}/expand", s.wrap(s.postExpand))
	m.HandleFunc("DELETE /v1/storage/{pool}", s.wrap(s.deleteStorage))
	m.HandleFunc("POST /v1/disks/{name}/labelclear", s.wrap(s.postLabelclear))
	m.HandleFunc("GET /v1/storage/{pool}/datasets", s.wrap(s.getDatasets))
	m.HandleFunc("POST /v1/storage/{pool}/datasets", s.wrap(s.postDataset))
	// {name} is the whole dataset name with its slashes escaped (tank%2Fhome)
	m.HandleFunc("DELETE /v1/storage/{pool}/datasets/{name}", s.wrap(s.deleteDataset))
	m.HandleFunc("GET /v1/storage/{pool}/datasets/{name}/files", s.wrap(s.getFiles))
	m.HandleFunc("POST /v1/storage/{pool}/datasets/{name}/owner", s.wrap(s.postOwner))
	m.HandleFunc("PUT /v1/storage/{pool}/datasets/{name}/auto-snapshot", s.wrap(s.putAutoSnapshot))
	m.HandleFunc("GET /v1/jobs", s.wrap(s.getJobs))
	m.HandleFunc("GET /v1/jobs/{id}", s.wrap(s.getJob))
	m.HandleFunc("GET /v1/importable", s.wrap(s.getImportable))
	m.HandleFunc("POST /v1/importable/{id}/import", s.wrap(s.postImport))
	m.HandleFunc("GET /v1/users", s.wrap(s.getUsers))
	m.HandleFunc("POST /v1/users", s.wrap(s.postUser))
	m.HandleFunc("PUT /v1/users/{name}", s.wrap(s.putUser))
	m.HandleFunc("DELETE /v1/users/{name}", s.wrap(s.deleteUser))
	m.HandleFunc("GET /v1/shares", s.wrap(s.getShares))
	m.HandleFunc("POST /v1/shares", s.wrap(s.postShare))
	m.HandleFunc("PUT /v1/shares/{name}", s.wrap(s.putShare))
	m.HandleFunc("DELETE /v1/shares/{name}", s.wrap(s.deleteShare))
	m.HandleFunc("GET /v1/luns", s.wrap(s.getLUNs))
	m.HandleFunc("POST /v1/luns", s.wrap(s.postLUN))
	m.HandleFunc("DELETE /v1/luns/{name}", s.wrap(s.deleteLUN))
	m.HandleFunc("POST /v1/apply", s.wrap(s.postApply))
	m.HandleFunc("GET /v1/shares/{name}/snapshots", s.wrap(s.getSnapshots))
	m.HandleFunc("POST /v1/shares/{name}/snapshots", s.wrap(s.postSnapshot))
	m.HandleFunc("DELETE /v1/shares/{name}/snapshots/{snap}", s.wrap(s.deleteSnapshot))
	m.HandleFunc("GET /v1/luns/{name}/snapshots", s.wrap(s.getSnapshots))
	m.HandleFunc("POST /v1/luns/{name}/snapshots", s.wrap(s.postSnapshot))
	m.HandleFunc("DELETE /v1/luns/{name}/snapshots/{snap}", s.wrap(s.deleteSnapshot))
	m.HandleFunc("GET /v1/settings", s.wrap(s.getSettings))
	m.HandleFunc("PUT /v1/settings", s.wrap(s.putSettings))
	m.HandleFunc("GET /v1/alerts", s.wrap(s.getAlerts))
	m.HandleFunc("GET /v1/network", s.wrap(s.getNetwork))
	m.HandleFunc("PUT /v1/network", s.wrap(s.putNetwork))
	m.HandleFunc("POST /v1/restore", s.wrap(s.postRestore))
	m.HandleFunc("GET /v1/state/export", s.wrap(s.getStateExport))
	m.HandleFunc("POST /v1/state/import", s.wrap(s.postStateImport))
	m.HandleFunc("GET /v1/directory", s.wrap(s.getDirectory))
	m.HandleFunc("PUT /v1/directory", s.wrap(s.putDirectory))
	m.HandleFunc("DELETE /v1/directory", s.wrap(s.deleteDirectory))
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, notFound("no such API: %s %s", r.Method, r.URL.Path))
	})
	return m
}

// LocalHandler is the API plus the test hook, for the Unix socket only (root on the NAS itself).
func (s *Server) LocalHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/debug/tick", s.wrap(s.postDebugTick))
	m.Handle("/", s.Handler())
	return m
}

// RequireKey lets a request through only with "Authorization: Bearer <key>".
func RequireKey(key string, h http.Handler) http.Handler {
	want := []byte("Bearer " + key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if key == "" || subtle.ConstantTimeCompare(got, want) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "a valid key is required"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ---- status, disks, storage ----

type Memory struct {
	Total int64 `json:"total"`
	Free  int64 `json:"free"`
	ARC   int64 `json:"arc"`
}

type PoolSummary struct {
	Name      string `json:"name"`
	Health    string `json:"health"`
	Size      int64  `json:"size"`
	Used      int64  `json:"used"`
	Free      int64  `json:"free"`
	Protected bool   `json:"protected"`
}

type LastApplyInfo struct {
	Time   string   `json:"time"`
	Errors []string `json:"errors"`
}

type Status struct {
	Version      string          `json:"version"`
	Hostname     string          `json:"hostname"`
	UptimeS      int64           `json:"uptime_s"`
	Memory       Memory          `json:"memory"`
	Pools        []PoolSummary   `json:"pools"`
	Services     map[string]bool `json:"services"`
	LastApply    LastApplyInfo   `json:"last_apply"`
	Addresses    []string        `json:"addresses"`
	Alerts       int             `json:"alerts"`
	RestoredFrom string          `json:"restored_from"`
	Keel         *KeelStatus     `json:"keel,omitempty"` // only with a seed disk
}

func (s *Server) sysctlInt(name string) int64 {
	out, err := s.Sys.Run("", "sysctl", "-n", name)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return n
}

// dataPools: the pools in the state, as the system sees them now.
func (s *Server) dataPools(st *State) ([]PoolInfo, error) {
	so, err := s.Sys.Run("", "zpool", "status", "-j", "-p", "--json-int")
	if err != nil {
		return nil, err
	}
	lo, err := s.Sys.Run("", "zpool", "list", "-j", "-p", "--json-int")
	if err != nil {
		return nil, err
	}
	all, err := ParsePools(so, lo)
	if err != nil {
		return nil, err
	}
	var out []PoolInfo
	for _, p := range st.Pools {
		found := false
		for _, pi := range all {
			if pi.GUID == p.GUID {
				out = append(out, pi)
				found = true
				break
			}
		}
		if !found {
			out = append(out, PoolInfo{Name: p.Name, GUID: p.GUID, Health: "MISSING"})
		}
	}
	return out, nil
}

// versionString is the image's version with keelnasd's, or keelnasd's alone.
func (s *Server) versionString() string {
	if b, err := s.Sys.ReadFile("/etc/keelnas-version"); err == nil {
		return strings.TrimSpace(string(b)) + " (keelnasd " + s.Version + ")"
	}
	return s.Version
}

func (s *Server) getStatus(r *http.Request) (any, error) {
	st := s.State()
	stat := Status{Version: s.versionString(), Hostname: s.hostname(), Pools: []PoolSummary{}}
	if out, err := s.Sys.Run("", "sysctl", "-n", "kern.boottime"); err == nil {
		if bt := ParseBoottime(out); bt > 0 {
			stat.UptimeS = time.Now().Unix() - bt
		}
	}
	page := s.sysctlInt("hw.pagesize")
	stat.Memory = Memory{
		Total: s.sysctlInt("hw.physmem"),
		Free:  s.sysctlInt("vm.stats.vm.v_free_count") * page,
		ARC:   s.sysctlInt("kstat.zfs.misc.arcstats.size"),
	}
	pools, err := s.dataPools(st)
	if err != nil {
		return nil, err
	}
	for _, p := range pools {
		stat.Pools = append(stat.Pools, PoolSummary{p.Name, p.Health, p.Size, p.Used, p.Free, p.Protected})
	}
	stat.Services = map[string]bool{"smbd": s.running("smbd"), "ctld": s.running("ctld")}
	t, errs := s.LastApply()
	stat.LastApply = LastApplyInfo{Errors: errs}
	if !t.IsZero() {
		stat.LastApply.Time = t.Format(time.RFC3339)
	}
	stat.Addresses = []string{}
	if out, err := s.Sys.Run("", "ifconfig", "-a", "inet"); err == nil {
		stat.Addresses = ParseAddresses(out)
	}
	if a, err := s.alerts(pools); err == nil {
		stat.Alerts = len(a)
	}
	stat.RestoredFrom = s.RestoredFrom()
	stat.Keel = s.keelStatus()
	return stat, nil
}

// disks: every disk but the seed disk, with the pool it is in ("" = free) and whether it
// holds the system.
func (s *Server) disks() ([]Disk, error) {
	disks, err := s.allDisks()
	if err != nil {
		return nil, err
	}
	if sd := s.SeedDisk(); sd != "" {
		disks = slices.DeleteFunc(disks, func(d Disk) bool { return d.Name == sd })
	}
	return disks, nil
}

// allDisks is disks with the seed disk.
func (s *Server) allDisks() ([]Disk, error) {
	out, err := s.Sys.Run("", "geom", "disk", "list")
	if err != nil {
		return nil, err
	}
	disks := slices.DeleteFunc(ParseGeomDisks(out), func(d Disk) bool { return !RealDisk(d) })
	names := make([]string, len(disks))
	for i, d := range disks {
		names[i] = d.Name
	}
	lo, _ := s.Sys.Run("", "glabel", "status", "-s")
	labels := ParseGlabel(lo)
	so, err := s.Sys.Run("", "zpool", "status", "-j", "-p", "--json-int")
	if err != nil {
		return nil, err
	}
	pools, err := ParsePools(so, "")
	if err != nil {
		return nil, err
	}
	sysPool := ""
	if bo, err := s.Sys.Run("", "zpool", "list", "-H", "-o", "name,bootfs"); err == nil {
		for _, line := range strings.Split(bo, "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[1] != "-" {
				sysPool = f[0]
			}
		}
	}
	for _, p := range pools {
		for _, m := range p.Members {
			if m.Missing { // its old path may be another disk's name now
				continue
			}
			d := DiskOf(m.Path, labels, names)
			for i := range disks {
				if disks[i].Name == d {
					disks[i].Pool, disks[i].PoolID, disks[i].PoolState = p.Name, p.GUID, "imported"
					disks[i].System = p.Name == sysPool
				}
			}
		}
	}
	if disks == nil {
		disks = []Disk{}
	}
	return s.withSmart(disks), nil
}

// getDisks: every disk with what is on it (checkedDisks): free ones can take storage.
func (s *Server) getDisks(r *http.Request) (any, error) {
	d, err := s.checkedDisks()
	if err != nil {
		return nil, err
	}
	return map[string]any{"disks": d}, nil
}

type StorageDisk struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// GUID: the device's guid (detach and replace take it, or the name); Vdev: its top-level
	// vdev (mirror-0, or its own name for a single disk); Missing: not there since the pool was
	// imported (Name is then the guid, Was the disk it was); Size: the device's size (phys_space)
	GUID    string `json:"guid"`
	Vdev    string `json:"vdev"`
	Missing bool   `json:"missing,omitempty"`
	Was     string `json:"was,omitempty"`
	Size    int64  `json:"size"`
}

// StorageVdev is a top-level vdev: type disk, mirror, raidz1…; size its allocatable space.
type StorageVdev struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type Storage struct {
	PoolSummary
	Disks []StorageDisk `json:"disks"`
	// Scan: the last or running scrub or resilver with its progress (scrub.go ScanView)
	Scan  ScanView      `json:"scan"`
	Vdevs []StorageVdev `json:"vdevs"`
	// Expand: bytes the pool gains with POST …/expand (its disks grew, a bigger disk replaced a
	// smaller one); 0: nothing to gain (pools.go poolGrowth)
	Expand int64 `json:"expand"`
}

func (s *Server) storage(st *State) ([]Storage, error) {
	pools, err := s.dataPools(st)
	if err != nil {
		return nil, err
	}
	lo, _ := s.Sys.Run("", "glabel", "status", "-s")
	labels := ParseGlabel(lo)
	var names []string
	if go_, err := s.Sys.Run("", "geom", "disk", "list"); err == nil {
		for _, d := range ParseGeomDisks(go_) {
			names = append(names, d.Name)
		}
	}
	res := []Storage{}
	for _, p := range pools {
		x := Storage{PoolSummary: PoolSummary{p.Name, p.Health, p.Size, p.Used, p.Free, p.Protected}, Disks: []StorageDisk{},
			Scan: ScanFromStats(p.Scan, s.now()), Vdevs: []StorageVdev{}, Expand: poolGrowth(p)}
		for _, t := range p.Tops {
			x.Vdevs = append(x.Vdevs, StorageVdev{t.Name, t.Type, t.Size})
		}
		for _, m := range p.Members {
			n := DiskOf(m.Path, labels, names)
			if n == "" {
				n = strings.TrimPrefix(m.Path, "/dev/")
			}
			d := StorageDisk{Name: n, State: m.State, GUID: m.GUID, Vdev: m.Top, Size: m.Size}
			if m.Missing {
				d.Name, d.Missing, d.Was = m.GUID, true, strings.TrimPrefix(m.Path, "/dev/")
			}
			x.Disks = append(x.Disks, d)
		}
		res = append(res, x)
	}
	return res, nil
}

func (s *Server) getStorage(r *http.Request) (any, error) {
	x, err := s.storage(s.State())
	if err != nil {
		return nil, err
	}
	return map[string]any{"storage": x}, nil
}

func (s *Server) storageItem(st *State, name string) (any, error) {
	x, err := s.storage(st)
	if err != nil {
		return nil, err
	}
	for _, p := range x {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, notFound("no storage %s", name)
}

// freeDisk checks that a disk exists and is empty: in no pool (imported or not), no partition
// table, no file system, zeros at its start (checkedDisks). Every path that writes a disk asks
// it first, with mu held.
func (s *Server) freeDisk(name string) error {
	if name != "" && name == s.SeedDisk() {
		return conflict("disk %s is the seed disk from keelOS, not a disk for storage", name)
	}
	disks, err := s.checkedDisks()
	if err != nil {
		return err
	}
	for _, d := range disks {
		if d.Name == name {
			if why := diskBusy(d); why != "" || !d.Free {
				if why == "" {
					why = "disk " + name + " is not free"
				}
				return conflict("%s", why)
			}
			return nil
		}
	}
	return notFound("no disk %s", name)
}

func (s *Server) postStorage(r *http.Request) (any, error) {
	var req struct {
		Disks   []string `json:"disks"`
		Protect bool     `json:"protect"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	switch {
	case len(req.Disks) == 1 && !req.Protect:
	case len(req.Disks) == 2 && req.Protect:
	case len(req.Disks) == 2:
		return nil, bad("two disks are a mirror: set protect")
	case len(req.Disks) == 1:
		return nil, bad("protect needs two disks")
	default:
		return nil, bad("give one disk, or two disks with protect")
	}
	if len(req.Disks) == 2 && req.Disks[0] == req.Disks[1] {
		return nil, bad("the two disks are the same")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range req.Disks {
		if err := s.freeDisk(d); err != nil {
			return nil, err
		}
	}
	imported := map[string]bool{}
	for _, n := range s.importedPools() {
		imported[n] = true
	}
	io_, _ := s.Sys.Run("", "zpool", "import")
	for _, p := range ParseImport(io_) {
		imported[p.Name] = true // a pool on the disks that waits to be imported keeps its name
	}
	st := s.st.Clone()
	name := st.NextPoolName(imported)
	// No -f: zpool itself refuses a disk that belongs to another pool, a second guard after
	// freeDisk. The one thing -f is allowed to override is a mirror of two disks of different
	// sizes, and only on disks freeDisk has just found empty.
	args := []string{"create", "-o", "ashift=12", "-o", "autotrim=on",
		"-O", "compression=lz4", "-O", "atime=off", "-O", "xattr=sa", name}
	if req.Protect {
		args = append(args, "mirror")
	}
	args = append(args, req.Disks...)
	if out, err := s.Sys.Run("", "zpool", args...); err != nil {
		if !req.Protect || !strings.Contains(out, "different sizes") {
			s.Log.Printf("command failed: %v", err)
			return nil, err
		}
		args = append([]string{"create", "-f"}, args[1:]...)
		if _, err := s.run("", "zpool", args...); err != nil {
			return nil, err
		}
	}
	out, err := s.run("", "zpool", "get", "-H", "-p", "-o", "value", "guid", name)
	if err != nil {
		return nil, err
	}
	st.Pools = append(st.Pools, Pool{Name: name, GUID: strings.TrimSpace(out)})
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return s.storageItem(st, name)
}

func (s *Server) postProtect(r *http.Request) (any, error) {
	name := r.PathValue("name")
	var req struct {
		Disk string `json:"disk"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Disk == "" {
		return nil, bad("give the disk to add")
	}
	s.mu.Lock()
	st := s.st.Clone()
	if st.Pool(name) == nil {
		s.mu.Unlock()
		return nil, notFound("no storage %s", name)
	}
	x, err := s.storage(st)
	if err == nil {
		err = s.freeDisk(req.Disk)
	}
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	var cur Storage
	for _, p := range x {
		if p.Name == name {
			cur = p
		}
	}
	if cur.Protected || len(cur.Disks) != 1 {
		s.mu.Unlock()
		return nil, conflict("storage %s is not a single disk", name)
	}
	// no -f: zpool refuses a disk that belongs to another pool (freeDisk checked it too)
	_, err = s.run("", "zpool", "attach", name, cur.Disks[0].Name, req.Disk)
	if err == nil {
		s.applyLocked()
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.storageItem(st, name)
}

// ---- users ----

// getUsers: the local users; with keelAuth connected also its users and groups (directory.go).
func (s *Server) getUsers(r *http.Request) (any, error) {
	st := s.State()
	res := map[string]any{"users": st.Users, "synced": []DirUser{}, "groups": []DirGroup{}, "directory": nil}
	if d := st.Directory; d != nil {
		res["synced"], res["groups"] = d.Users, d.Groups
		res["directory"] = map[string]any{"source": d.Source, "name": d.Name, "domain": d.Domain, "revision": d.Revision, "synced_at": d.SyncedAt}
	}
	return res, nil
}

// fromKeelAuth: name is a keelAuth user; it is changed there, not here.
func fromKeelAuth(st *State, name string) error {
	if st.DirUser(name) != nil {
		return conflict("user %s comes from keelAuth: change or remove it there", name)
	}
	return nil
}

func checkPassword(p string) error {
	if p == "" {
		return bad("the password is empty")
	}
	if strings.ContainsAny(p, "\n\r\x00") {
		return bad("the password has a line break")
	}
	return nil
}

func (s *Server) setPassword(name, password string) error {
	_, err := s.Sys.Run(password+"\n"+password+"\n", "smbpasswd", "-c", SmbConf, "-s", "-a", name)
	if err != nil {
		// Do not log stdin; the error has only the command and its output.
		s.Log.Printf("smbpasswd %s failed: %v", name, err)
	}
	return err
}

func (s *Server) postUser(r *http.Request) (any, error) {
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !ValidName(req.Name) {
		return nil, bad("bad user name %q: lower-case letters, digits, - and _, a letter first, at most 32", req.Name)
	}
	if err := checkPassword(req.Password); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if st.User(req.Name) != nil {
		return nil, conflict("user %s exists", req.Name)
	}
	if err := fromKeelAuth(st, req.Name); err != nil {
		return nil, err
	}
	pw, err := s.pwUsers()
	if err != nil {
		return nil, err
	}
	if _, ok := pw[req.Name]; ok {
		return nil, conflict("%s is a system account", req.Name)
	}
	taken := map[int]bool{}
	for _, u := range pw {
		taken[u.UID] = true
	}
	u := User{Name: req.Name, UID: st.NextUID(taken)}
	st.Users = append(st.Users, u)
	// The system user first (smbpasswd needs it), then the password, then the state.
	if err := s.ensureUser(u, pw); err != nil {
		return nil, err
	}
	if err := s.setPassword(u.Name, req.Password); err != nil {
		s.Sys.Run("", "pw", "userdel", "-n", u.Name)
		return nil, err
	}
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Server) putUser(r *http.Request) (any, error) {
	name := r.PathValue("name")
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := checkPassword(req.Password); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fromKeelAuth(s.st, name); err != nil {
		return nil, err
	}
	u := s.st.User(name)
	if u == nil {
		return nil, notFound("no user %s", name)
	}
	if err := s.setPassword(name, req.Password); err != nil {
		return nil, err
	}
	s.refreshCopies()
	return *u, nil
}

func (s *Server) deleteUser(r *http.Request) (any, error) {
	name := r.PathValue("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if err := fromKeelAuth(st, name); err != nil {
		return nil, err
	}
	if st.User(name) == nil {
		return nil, notFound("no user %s", name)
	}
	st.Users = slices.DeleteFunc(st.Users, func(u User) bool { return u.Name == name })
	for i := range st.Shares {
		st.Shares[i].RW = slices.DeleteFunc(st.Shares[i].RW, func(n string) bool { return n == name })
		st.Shares[i].RO = slices.DeleteFunc(st.Shares[i].RO, func(n string) bool { return n == name })
	}
	s.Sys.Run("", "pdbedit", "-s", SmbConf, "-x", "-u", name)
	// apply removes the system user: it is in our group and range and no longer in the state.
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// ---- shares ----

// ShareInfo is a share as the API shows it: where it is mounted, the pool, whether keelNAS made
// its dataset.
type ShareInfo struct {
	Share
	Pool string `json:"pool"`
	Path string `json:"path"`
	Used int64  `json:"used"`
	Ours bool   `json:"ours"`
}

func (s *Server) shareInfos(st *State) []ShareInfo {
	ds := s.datasets()
	res := []ShareInfo{}
	for _, sh := range st.Shares {
		d := ds[sh.Dataset]
		path := d.Mountpoint
		if !strings.HasPrefix(path, "/") {
			path = ""
		}
		res = append(res, ShareInfo{Share: sh, Pool: PoolOf(sh.Dataset), Path: path, Used: d.Used, Ours: st.Ours(sh.Dataset)})
	}
	return res
}

func (s *Server) getShares(r *http.Request) (any, error) {
	return map[string]any{"shares": s.shareInfos(s.State())}, nil
}

func (s *Server) shareItem(name string) ShareInfo {
	for _, x := range s.shareInfos(s.st) {
		if x.Name == name {
			return x
		}
	}
	return ShareInfo{}
}

// checkAccess: users exist, nobody is in both lists, somebody can get in.
func checkAccess(st *State, rw, ro []string) error {
	if len(rw) == 0 && len(ro) == 0 {
		return bad("a share needs at least one user (no guest shares)")
	}
	for _, n := range append(slices.Clone(rw), ro...) {
		if !st.Principal(n) {
			if strings.HasPrefix(n, "@") {
				return bad("no group %s from keelAuth", n[1:])
			}
			return bad("no user %s", n)
		}
	}
	for _, n := range rw {
		if slices.Contains(ro, n) {
			return bad("user %s is both read-write and read-only", n)
		}
	}
	return nil
}

func dedup(a []string) []string {
	c := slices.Clone(a)
	if c == nil {
		return []string{}
	}
	slices.Sort(c)
	return slices.Compact(c)
}

// postShare shares a dataset: {"dataset", "name", "rw", "ro"} (docs/06 §3). The old
// form {"name", "pool"} makes <pool>/shares/<name> (ours) and shares it; an existing one of that
// name is taken back with its data.
func (s *Server) postShare(r *http.Request) (any, error) {
	var req struct {
		Name    string   `json:"name"`
		Pool    string   `json:"pool"`
		Dataset string   `json:"dataset"`
		RW      []string `json:"rw"`
		RO      []string `json:"ro"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Dataset != "" && req.Pool != "" {
		return nil, bad("give the dataset, or (the old form) the pool, not both")
	}
	if req.Dataset != "" && req.Name == "" {
		req.Name = req.Dataset[strings.LastIndexByte(req.Dataset, '/')+1:]
	}
	if !ValidName(req.Name) || slices.Contains(reservedShares, req.Name) {
		return nil, bad("bad share name %q: lower-case letters, digits, - and _, a letter first, at most 32", req.Name)
	}
	req.RW, req.RO = dedup(req.RW), dedup(req.RO)
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if st.Share(req.Name) != nil {
		return nil, conflict("share %s exists", req.Name)
	}
	ds := s.datasets()
	if req.Dataset == "" {
		if req.Pool == "" && len(st.Pools) == 1 {
			req.Pool = st.Pools[0].Name
		}
		if st.Pool(req.Pool) == nil {
			return nil, bad("no storage %q", req.Pool)
		}
		req.Dataset = LegacyDataset(req.Pool, req.Name)
		if _, ok := ds[req.Dataset]; !ok && !st.Ours(req.Dataset) {
			st.Datasets = append(st.Datasets, req.Dataset) // made below by ensureShare
		}
	} else if st.Pool(PoolOf(req.Dataset)) == nil {
		return nil, notFound("no storage %s", PoolOf(req.Dataset))
	}
	if sh := st.ShareOf(req.Dataset); sh != nil {
		return nil, conflict("dataset %s is shared as %s already", req.Dataset, sh.Name)
	}
	if err := checkAccess(st, req.RW, req.RO); err != nil {
		return nil, err
	}
	sh := Share{Name: req.Name, Dataset: req.Dataset, RW: req.RW, RO: req.RO}
	if st.Ours(sh.Dataset) {
		if _, err := s.ensureShare(st, sh, ds); err != nil {
			return nil, err
		}
	} else {
		// a dataset keelNAS did not make: only once its files belong to a keelNAS user (the owner
		// change, jobs.go), so that the users of the share are who they are on its files
		d, err := s.findDataset(st, PoolOf(sh.Dataset), sh.Dataset)
		if err != nil {
			return nil, err
		}
		switch {
		case !d.Mounted || !strings.HasPrefix(d.Mountpoint, "/") || d.Owner == nil:
			return nil, conflict("dataset %s is not mounted: it cannot be shared", sh.Dataset)
		case d.Job != nil:
			return nil, conflict("the owner of %s is being changed (job %s): share it when that is done", sh.Dataset, d.Job.ID)
		case !d.Shareable:
			who := "uid " + strconv.Itoa(d.Owner.UID)
			if d.Owner.UID == 0 {
				who = "root"
			}
			return nil, conflict("the files of %s belong to %s, not to a keelNAS user: change the owner of the dataset first", sh.Dataset, who)
		}
	}
	st.Shares = append(st.Shares, sh)
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return s.shareItem(req.Name), nil
}

func (s *Server) putShare(r *http.Request) (any, error) {
	name := r.PathValue("name")
	var req struct {
		RW []string `json:"rw"`
		RO []string `json:"ro"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	sh := st.Share(name)
	if sh == nil {
		return nil, notFound("no share %s", name)
	}
	rw, ro := dedup(req.RW), dedup(req.RO)
	if err := checkAccess(st, rw, ro); err != nil {
		return nil, err
	}
	sh.RW, sh.RO = rw, ro
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	return s.shareItem(name), nil
}

func purge(r *http.Request) bool {
	v := r.URL.Query().Get("purge")
	return v == "1" || v == "true" || v == "yes"
}

// deleteShare removes a share; its data stays. ?purge=1 destroys the dataset too, only one
// keelNAS made (403 for any other, and nothing is changed).
func (s *Server) deleteShare(r *http.Request) (any, error) {
	name := r.PathValue("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	sh := st.Share(name)
	if sh == nil {
		return nil, notFound("no share %s", name)
	}
	old := *sh
	var below []string
	if purge(r) {
		if !st.Ours(old.Dataset) {
			return nil, forbidden("dataset %s was not made by keelNAS: keelNAS never destroys it; remove the share without purge", old.Dataset)
		}
		for n := range s.datasets() {
			if !strings.HasPrefix(n, old.Dataset+"/") {
				continue
			}
			if !st.Ours(n) {
				return nil, forbidden("dataset %s has %s below it, which keelNAS did not make: not destroyed", old.Dataset, n)
			}
			if x := st.ShareOf(n); x != nil {
				return nil, conflict("dataset %s below it is shared as %s", n, x.Name)
			}
			below = append(below, n)
		}
	}
	st.Shares = slices.DeleteFunc(st.Shares, func(x Share) bool { return x.Name == name })
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	if purge(r) {
		if _, err := s.run("", "zfs", "destroy", "-r", old.Dataset); err != nil {
			return nil, err
		}
		st = s.st.Clone()
		st.Datasets = slices.DeleteFunc(st.Datasets, func(d string) bool { return d == old.Dataset || slices.Contains(below, d) })
		if _, err := s.commit(st); err != nil {
			return nil, err
		}
	}
	return map[string]bool{"ok": true}, nil
}

// ---- LUNs ----

type LUNInfo struct {
	LUN
	IQN string `json:"iqn"`
}

func (s *Server) lunInfos(st *State) []LUNInfo {
	h := s.hostname()
	res := []LUNInfo{}
	for _, l := range st.LUNs {
		res = append(res, LUNInfo{LUN: l, IQN: IQN(h, l.Name)})
	}
	return res
}

func (s *Server) getLUNs(r *http.Request) (any, error) {
	return map[string]any{"luns": s.lunInfos(s.State())}, nil
}

func (s *Server) postLUN(r *http.Request) (any, error) {
	var req LUN
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !ValidName(req.Name) {
		return nil, bad("bad LUN name %q: lower-case letters, digits, - and _, a letter first, at most 32", req.Name)
	}
	if req.SizeGiB < 1 {
		return nil, bad("size_gib must be at least 1")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	if st.LUN(req.Name) != nil {
		return nil, conflict("LUN %s exists", req.Name)
	}
	if req.Pool == "" && len(st.Pools) == 1 {
		req.Pool = st.Pools[0].Name
	}
	if st.Pool(req.Pool) == nil {
		return nil, bad("no storage %q", req.Pool)
	}
	ds := s.datasets()
	if _, ok := ds[LUNDataset(req)]; ok {
		// A LUN deleted without purge left its zvol: taking it back keeps the data.
		s.Log.Printf("LUN %s: reusing the existing zvol %s", req.Name, LUNDataset(req))
	}
	if err := s.ensureLUN(req, ds); err != nil {
		return nil, err
	}
	st.LUNs = append(st.LUNs, req)
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	for _, x := range s.lunInfos(st) {
		if x.Name == req.Name {
			return x, nil
		}
	}
	return nil, errNotFound
}

func (s *Server) deleteLUN(r *http.Request) (any, error) {
	name := r.PathValue("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st.Clone()
	l := st.LUN(name)
	if l == nil {
		return nil, notFound("no LUN %s", name)
	}
	old := *l
	st.LUNs = slices.DeleteFunc(st.LUNs, func(x LUN) bool { return x.Name == name })
	// ctld lets go of the zvol on the reload in apply; only then can it be destroyed.
	if _, err := s.commit(st); err != nil {
		return nil, err
	}
	if purge(r) {
		if _, err := s.run("", "zfs", "destroy", LUNDataset(old)); err != nil {
			return nil, err
		}
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) postApply(r *http.Request) (any, error) {
	errs := s.Apply()
	return map[string]any{"ok": len(errs) == 0, "errors": errs}, nil
}

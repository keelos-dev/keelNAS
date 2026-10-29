package nas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Disk is one disk from `geom disk list`.
type Disk struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Model  string `json:"model"`
	Serial string `json:"serial"`
	// Pool: the pool whose ZFS label is on the disk (or on a partition of it), imported or not;
	// "" = none. PoolState says which: imported, not_imported (another pool, docs/04 §4.1),
	// destroyed. keelNAS never writes on a disk with a pool.
	Pool      string `json:"pool"`
	PoolID    string `json:"pool_id,omitempty"`
	PoolState string `json:"pool_state,omitempty"`
	// Content: what else is on a disk without a pool ("" = nothing found): "partitions GPT",
	// "filesystem ufs", "data" (its first MiB is not zeros), "unknown" (could not be checked).
	Content     string `json:"content,omitempty"`
	Free        bool   `json:"free"` // no pool, not the system, nothing on it: storage may use it
	System      bool   `json:"system"`
	Smart       string `json:"smart"`       // ok, failing, unknown
	Temperature int    `json:"temperature"` // degrees Celsius, 0 = not known
	// SSD, Sector: from geom (rotationrate 0; Sectorsize), for the hardware page
	SSD    bool `json:"ssd,omitempty"`
	Sector int  `json:"sector,omitempty"`
}

// RealDisk: not an optical drive and not empty (QEMU's cd0 has size 0).
func RealDisk(d Disk) bool {
	return d.Size > 0 && !strings.HasPrefix(d.Name, "cd")
}

// ParseGeomDisks reads `geom disk list`.
func ParseGeomDisks(out string) []Disk {
	var disks []Disk
	var cur *Disk
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "Geom name: "):
			disks = append(disks, Disk{Name: strings.TrimPrefix(t, "Geom name: ")})
			cur = &disks[len(disks)-1]
		case cur == nil:
		case strings.HasPrefix(t, "Mediasize: "):
			f := strings.Fields(strings.TrimPrefix(t, "Mediasize: "))
			if len(f) > 0 {
				cur.Size, _ = strconv.ParseInt(f[0], 10, 64)
			}
		case strings.HasPrefix(t, "descr: "):
			cur.Model = strings.TrimSpace(strings.TrimPrefix(t, "descr: "))
		case strings.HasPrefix(t, "ident: "):
			cur.Serial = strings.TrimSpace(strings.TrimPrefix(t, "ident: "))
		case strings.HasPrefix(t, "Sectorsize: "):
			cur.Sector, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(t, "Sectorsize: ")))
		case strings.HasPrefix(t, "rotationrate: "):
			cur.SSD = strings.TrimSpace(strings.TrimPrefix(t, "rotationrate: ")) == "0"
		}
	}
	return disks
}

// ParseGlabel reads `glabel status -s`: label -> provider (gpt/knsys -> vtbd0p3).
func ParseGlabel(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 {
			m[f[0]] = f[2]
		}
	}
	return m
}

// DiskOf maps a vdev path (/dev/vtbd1, /dev/gpt/knsys, vtbd0p3, /dev/diskid/…) to its disk name.
func DiskOf(path string, labels map[string]string, disks []string) string {
	p := strings.TrimPrefix(path, "/dev/")
	if prov, ok := labels[p]; ok {
		p = prov
	}
	best := ""
	for _, d := range disks {
		if p == d {
			return d
		}
		if strings.HasPrefix(p, d) && len(d) > len(best) {
			rest := p[len(d):]
			if len(rest) > 1 && (rest[0] == 'p' || rest[0] == 's') && rest[1] >= '0' && rest[1] <= '9' {
				best = d
			}
		}
	}
	return best
}

// PoolInfo is a pool from `zpool status -j` and `zpool list -j`.
type PoolInfo struct {
	Name      string
	GUID      string
	Health    string
	Size      int64
	Used      int64
	Free      int64
	Protected bool
	Members   []Member // leaf vdevs
	Tops      []Top    // top-level vdevs (a disk, mirror-0, raidz1-0…), by name
	Scan      Scan
	// ExpandSize: zpool list's expandsize, what zpool online -e would add (0: none known)
	ExpandSize int64
}

// Top is a top-level vdev: Size is its allocatable space (total_space; the metaslabs).
type Top struct {
	Name string
	Type string // disk, mirror, raidz1, …
	Size int64
}

// Scan is the pool's last (or current) scrub or resilver, from scan_stats.
type Scan struct {
	Function string // SCRUB, RESILVER, "" = none
	State    string // SCANNING, FINISHED, CANCELED
	End      int64  // Unix time the scan ended
	Errors   int64  // errors it could not repair
	// the rest of scan_stats, for the progress (scrub.go ScanFromStats): byte counts, and the
	// current pass (pass_start, the time it was paused, issued_bytes_per_scan)
	Start, ToExamine, Examined, Skipped, Processed, Issued int64
	PassStart, Pause, SpentPaused, PassIssued              int64
}

// Member is a leaf vdev. A device that was not there when the pool was imported has its guid as
// its name (Missing) and the path it had (Path).
type Member struct {
	Path    string
	State   string
	GUID    string
	Top     string // its top-level vdev (its own name for a single disk)
	Size    int64  // phys_space: the device's size
	Missing bool
}

type zvdev struct {
	Name     string           `json:"name"`
	Type     string           `json:"vdev_type"`
	Path     string           `json:"path"`
	State    string           `json:"state"`
	Class    string           `json:"class"`
	GUID     json.Number      `json:"guid"`
	Total    json.Number      `json:"total_space"`
	Phys     json.Number      `json:"phys_space"`
	Children map[string]zvdev `json:"vdevs"`
}

type zpoolStatus struct {
	Pools map[string]struct {
		Name  string                     `json:"name"`
		State string                     `json:"state"`
		GUID  json.Number                `json:"pool_guid"`
		Vdevs map[string]zvdev           `json:"vdevs"`
		Scan  map[string]json.RawMessage `json:"scan_stats"`
	} `json:"pools"`
}

type zprop struct {
	Value json.RawMessage `json:"value"`
}

type zpoolList struct {
	Pools map[string]struct {
		Name       string           `json:"name"`
		Properties map[string]zprop `json:"properties"`
	} `json:"pools"`
}

func decodeNum(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}

func propInt(p map[string]zprop, k string) int64 {
	raw := p[k].Value
	var n json.Number
	if decodeNum(raw, &n) == nil {
		i, _ := n.Int64()
		return i
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		i, _ := strconv.ParseInt(s, 10, 64)
		return i
	}
	return 0
}

// ParsePools combines `zpool status -j -p --json-int` and `zpool list -j -p --json-int`.
func ParsePools(statusOut, listOut string) ([]PoolInfo, error) {
	var st zpoolStatus
	if err := decodeNum([]byte(statusOut), &st); err != nil {
		return nil, fmt.Errorf("zpool status: %v", err)
	}
	var ls zpoolList
	if strings.TrimSpace(listOut) != "" {
		if err := decodeNum([]byte(listOut), &ls); err != nil {
			return nil, fmt.Errorf("zpool list: %v", err)
		}
	}
	var pools []PoolInfo
	for name, p := range st.Pools {
		pi := PoolInfo{Name: name, GUID: p.GUID.String(), Health: p.State}
		if root, ok := p.Vdevs[name]; ok {
			walkVdevs(root, &pi, 0, "")
		}
		if sc := p.Scan; sc != nil {
			str := func(k string) string { var v string; json.Unmarshal(sc[k], &v); return v }
			num := func(k string) int64 { var n json.Number; decodeNum(sc[k], &n); i, _ := n.Int64(); return i }
			x := &pi.Scan
			x.Function, x.State = str("function"), str("state")
			x.End, x.Errors, x.Start = num("end_time"), num("errors"), num("start_time")
			x.ToExamine, x.Examined, x.Skipped = num("to_examine"), num("examined"), num("skipped")
			x.Processed, x.Issued, x.PassStart = num("processed"), num("issued"), num("pass_start")
			x.Pause, x.SpentPaused, x.PassIssued = num("scrub_pause"), num("scrub_spent_paused"), num("issued_bytes_per_scan")
		}
		if l, ok := ls.Pools[name]; ok {
			pi.Size = propInt(l.Properties, "size")
			pi.Used = propInt(l.Properties, "allocated")
			pi.Free = propInt(l.Properties, "free")
			pi.ExpandSize = propInt(l.Properties, "expandsize")
		}
		pools = append(pools, pi)
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })
	return pools, nil
}

func walkVdevs(v zvdev, pi *PoolInfo, depth int, top string) {
	names := make([]string, 0, len(v.Children))
	for k := range v.Children {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		c := v.Children[k]
		t := top
		if depth == 0 {
			t = c.Name
			if t == "" {
				t = k
			}
			typ := c.Type
			if typ == "raidz" || typ == "draid" {
				typ = strings.SplitN(k, "-", 2)[0] // raidz2-0: raidz2
			}
			size, _ := c.Total.Int64()
			pi.Tops = append(pi.Tops, Top{Name: t, Type: typ, Size: size})
		}
		switch {
		case c.Type == "mirror" || strings.HasPrefix(c.Type, "raidz") || c.Type == "draid":
			pi.Protected = true
			walkVdevs(c, pi, depth+1, t)
		case len(c.Children) > 0:
			walkVdevs(c, pi, depth+1, t)
		default:
			path := c.Path
			if path == "" {
				path = c.Name
			}
			size, _ := c.Phys.Int64()
			pi.Members = append(pi.Members, Member{Path: path, State: c.State, GUID: c.GUID.String(), Top: t, Size: size,
				Missing: isGUIDName(c.Name) && c.State != "ONLINE"})
		}
	}
}

// isGUIDName: zpool names a device by its guid when it was not there at import ("was /dev/…").
func isGUIDName(n string) bool {
	if n == "" {
		return false
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Dataset is a line of `zfs list -H -p -o name,type,used,mountpoint`.
type Dataset struct {
	Name       string
	Type       string
	Used       int64
	Mountpoint string
}

func ParseDatasets(out string) map[string]Dataset {
	m := map[string]Dataset{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		used, _ := strconv.ParseInt(f[2], 10, 64)
		m[f[0]] = Dataset{Name: f[0], Type: f[1], Used: used, Mountpoint: f[3]}
	}
	return m
}

// PwUser is a line of `pw usershow -a` (master.passwd format).
type PwUser struct {
	Name string
	UID  int
	GID  int
}

func ParsePwUsers(out string) map[string]PwUser {
	m := map[string]PwUser{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 4 {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			continue
		}
		m[f[0]] = PwUser{Name: f[0], UID: uid, GID: gid}
	}
	return m
}

// ParseBoottime reads `sysctl -n kern.boottime`: "{ sec = 1790934600, usec = 312793 } …".
func ParseBoottime(out string) int64 {
	i := strings.Index(out, "sec = ")
	if i < 0 {
		return 0
	}
	s := out[i+6:]
	j := strings.IndexAny(s, ", }")
	if j > 0 {
		s = s[:j]
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// ParseAddresses reads `ifconfig -a inet`: the IPv4 addresses of every interface but the loopback.
func ParseAddresses(out string) []string {
	addrs := []string{}
	iface := ""
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			if i := strings.IndexByte(line, ':'); i > 0 {
				iface = line[:i]
			}
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "inet" && !strings.HasPrefix(iface, "lo") && !internalNIC(iface) {
			addrs = append(addrs, f[1])
		}
	}
	return addrs
}

// internalNIC is the NIC towards keelOS. keelOS has no internal NIC yet (docs/01 section 8.0);
// until it has one and we know how to recognise it, no interface is internal.
func internalNIC(string) bool { return false }

// Importable is a pool `zpool import` lists: one that is on the disks but not imported.
type Importable struct {
	Name    string
	GUID    string
	State   string // ONLINE, DEGRADED, UNAVAIL, …
	Status  string // "" when there is nothing to say
	Action  string
	Layout  string // the first top-level vdev's kind: raidz2, mirror, … ("disk" = no redundancy)
	Devices []Member
}

// OtherHost: the pool was last used by another system (another host id): importing it needs
// -f, and that is only right when that system no longer uses the disks.
func (p Importable) OtherHost() bool {
	return strings.Contains(p.Status, "another system") || strings.Contains(p.Action, "'-f'")
}

var (
	importKeyRE  = regexp.MustCompile(`^\s*(pool|id|state|status|action|see|config|comment|errata|cause):\s?(.*)$`)
	vdevGroupRE  = regexp.MustCompile(`^(mirror|raidz[123]?|draid[0-9]*(:[0-9a-z:]*)?|replacing|spare|indirect)-[0-9]+$`)
	vdevStateRE  = regexp.MustCompile(`^(ONLINE|DEGRADED|FAULTED|OFFLINE|UNAVAIL|REMOVED|AVAIL|INUSE|SPLIT)$`)
	vdevSections = map[string]bool{"logs": true, "cache": true, "spares": true, "special": true, "dedup": true}
)

// ParseImport reads `zpool import` (no arguments): each pool's name, id, state, status and
// action, and the leaf devices of its config ("gpt/slot01", "vtbd1", "da0p1", a guid for a
// missing one) with their state.
func ParseImport(out string) []Importable {
	var res []Importable
	var cur *Importable
	key := "" // the key whose text continues on the next lines
	inConfig := false
	for _, line := range strings.Split(out, "\n") {
		if m := importKeyRE.FindStringSubmatch(line); m != nil {
			k, v := m[1], strings.TrimSpace(m[2])
			if k == "pool" {
				res = append(res, Importable{Name: v})
				cur = &res[len(res)-1]
				key, inConfig = "", false
				continue
			}
			if cur == nil {
				continue
			}
			key, inConfig = k, k == "config"
			switch k {
			case "id":
				cur.GUID = v
			case "state":
				cur.State = v
			case "status":
				cur.Status = v
			case "action":
				cur.Action = v
			}
			continue
		}
		if cur == nil {
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" {
			key = ""
			continue
		}
		if !inConfig {
			switch key { // a continuation line
			case "status":
				cur.Status += " " + t
			case "action":
				cur.Action += " " + t
			}
			continue
		}
		f := strings.Fields(t)
		if len(f) < 2 || !vdevStateRE.MatchString(f[1]) || f[0] == cur.Name || vdevSections[f[0]] {
			continue
		}
		if vdevGroupRE.MatchString(f[0]) {
			if cur.Layout == "" {
				cur.Layout = f[0][:strings.LastIndexByte(f[0], '-')]
			}
			continue
		}
		if cur.Layout == "" {
			cur.Layout = "disk"
		}
		cur.Devices = append(cur.Devices, Member{Path: f[0], State: f[1]})
	}
	return res
}

// ZLabel is what `zdb -l DEV` found: a ZFS label (Found) with its pool, or none.
type ZLabel struct {
	Found    bool
	Name     string // "" for a cache or spare device's label
	GUID     string
	State    string // 0 active, 1 exported, 2 destroyed, 3 spare, 4 cache
	Hostname string
}

// ParseZdbLabel reads `zdb -l DEV`. ok is false when the output is neither a label nor the
// "failed to unpack label" lines of a device without one: then nothing is known about it.
func ParseZdbLabel(out string) (l ZLabel, ok bool) {
	if strings.Contains(out, "LABEL ") && strings.Contains(out, "version:") {
		l.Found = true
		for _, line := range strings.Split(out, "\n") {
			t := strings.TrimSpace(line)
			k, v, has := strings.Cut(t, ": ")
			if !has || strings.HasPrefix(line, "        ") { // vdev_tree's children
				continue
			}
			v = strings.Trim(v, "'")
			switch {
			case k == "name" && l.Name == "":
				l.Name = v
			case k == "pool_guid" && l.GUID == "":
				l.GUID = v
			case k == "state" && l.State == "":
				l.State = v
			case k == "hostname" && l.Hostname == "":
				l.Hostname = v
			}
		}
		return l, true
	}
	if strings.Contains(out, "failed to unpack label") || strings.Contains(out, "failed to read label") {
		return ZLabel{}, true
	}
	return ZLabel{}, false
}

// GpartTable is a partition table from `gpart show -p`: the provider it is on, its scheme and
// its partitions.
type GpartTable struct {
	Provider string
	Scheme   string
	Parts    []string
}

// ParseGpartShow reads `gpart show -p` (every partitioned provider).
func ParseGpartShow(out string) []GpartTable {
	var res []GpartTable
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) >= 5 && f[0] == "=>":
			res = append(res, GpartTable{Provider: f[3], Scheme: f[4]})
		case len(res) > 0 && len(f) >= 4 && f[2] != "-":
			t := &res[len(res)-1]
			t.Parts = append(t.Parts, f[2])
		}
	}
	return res
}

// SmartInfo is what smartctl says about a disk (-H -A -j, or -i -H -A -j with -n standby for
// the hardware page): the two summary fields the daily check keeps, and the details the
// hardware page shows (the same fields as keelOS's hardware page, keelOS hwinfo.Disk).
type SmartInfo struct {
	Status      string `json:"smart"`       // ok, failing, unknown
	Temperature int    `json:"temperature"` // degrees Celsius, 0 = not known
	// Standby: the disk was asleep and left so (-n standby); nothing below was read this time
	Standby  bool   `json:"standby,omitempty"`
	Protocol string `json:"protocol,omitempty"` // ATA, NVMe, SCSI
	Firmware string `json:"firmware,omitempty"`
	// Health: "passed", "failed", or "" (no SMART)
	Health string `json:"health,omitempty"`
	Hours  *int64 `json:"hours,omitempty"` // power-on hours
	Wear   *int   `json:"wear,omitempty"`  // NVMe percentage used (100: rated life reached)
	// Reallocated, Pending: ATA sectors remapped and waiting (attributes 5, 197); MediaErrors: NVMe
	Reallocated *int64 `json:"reallocated,omitempty"`
	Pending     *int64 `json:"pending,omitempty"`
	MediaErrors *int64 `json:"media_errors,omitempty"`
	// NVMe only: available spare in percent, unsafe shutdowns
	Spare           *int   `json:"spare,omitempty"`
	UnsafeShutdowns *int64 `json:"unsafe_shutdowns,omitempty"`
}

// ParseSmart reads `smartctl -j … DEV`. smartctl's exit status is a bit mask and is often
// not 0 for a readable answer, so only the JSON counts: no smart_status = unknown (a virtual
// disk, or smartctl could not talk to it); bit 3 of exit_status = the disk says it is failing.
// With -n standby, a sleeping disk answers with bit 1 and no SMART data: Standby.
func ParseSmart(out string) SmartInfo {
	var j struct {
		Smartctl struct {
			ExitStatus int `json:"exit_status"`
			Messages   []struct {
				String string `json:"string"`
			} `json:"messages"`
		} `json:"smartctl"`
		Firmware string `json:"firmware_version"`
		Device   struct {
			Protocol string `json:"protocol"`
		} `json:"device"`
		SmartStatus *struct {
			Passed *bool `json:"passed"`
		} `json:"smart_status"`
		Temperature struct {
			Current int `json:"current"`
		} `json:"temperature"`
		PowerOn *struct {
			Hours int64 `json:"hours"`
		} `json:"power_on_time"`
		NVMe *struct {
			PercentageUsed  int   `json:"percentage_used"`
			MediaErrors     int64 `json:"media_errors"`
			AvailableSpare  int   `json:"available_spare"`
			UnsafeShutdowns int64 `json:"unsafe_shutdowns"`
		} `json:"nvme_smart_health_information_log"`
		ATA *struct {
			Table []struct {
				ID  int `json:"id"`
				Raw struct {
					Value int64 `json:"value"`
				} `json:"raw"`
			} `json:"table"`
		} `json:"ata_smart_attributes"`
	}
	if json.Unmarshal([]byte(out), &j) != nil {
		return SmartInfo{Status: "unknown"}
	}
	info := SmartInfo{Status: "unknown", Temperature: j.Temperature.Current, Protocol: j.Device.Protocol, Firmware: j.Firmware}
	switch {
	case j.SmartStatus == nil || j.SmartStatus.Passed == nil:
		// "Device is in STANDBY mode, exit(2)" (or SLEEP): -n standby left it asleep. Bit 1 of the
		// exit status alone is not that: it is also "could not open / no SMART" (bhyve's NVMe).
		for _, m := range j.Smartctl.Messages {
			if strings.Contains(m.String, "STANDBY mode") || strings.Contains(m.String, "SLEEP mode") {
				info.Standby = true
			}
		}
	case !*j.SmartStatus.Passed || j.Smartctl.ExitStatus&8 != 0:
		info.Status, info.Health = "failing", "failed"
	default:
		info.Status, info.Health = "ok", "passed"
	}
	if j.PowerOn != nil {
		h := j.PowerOn.Hours
		info.Hours = &h
	}
	if j.NVMe != nil {
		w, e, sp, us := j.NVMe.PercentageUsed, j.NVMe.MediaErrors, j.NVMe.AvailableSpare, j.NVMe.UnsafeShutdowns
		info.Wear, info.MediaErrors, info.Spare, info.UnsafeShutdowns = &w, &e, &sp, &us
	}
	if j.ATA != nil {
		for _, a := range j.ATA.Table {
			v := a.Raw.Value & 0xffffffff // the low 32 bits; vendors put other things above
			switch a.ID {
			case 5:
				info.Reallocated = &v
			case 197:
				info.Pending = &v
			}
		}
	}
	return info
}

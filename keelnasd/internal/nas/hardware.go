package nas

import (
	"net/http"
	"sort"
	"sync"
	"time"
)

// The hardware page (docs/04 section 15): every disk with what SMART says about it now, the
// same fields as keelOS's own hardware page, and one disk in full (smartctl -x) for the dialog
// behind the health mark. The daily check (health.go) keeps looking only at the pool disks and
// drives the alerts; this reads all of them when asked, at most every hwEvery.

const hwEvery = 30 * time.Second

// HWDisk is one disk on the hardware page: geom's identity, where it belongs, and SMART.
type HWDisk struct {
	Name   string `json:"name"`
	Model  string `json:"model,omitempty"`
	Serial string `json:"serial,omitempty"`
	Size   int64  `json:"size"`
	Sector int    `json:"sector,omitempty"`
	SSD    bool   `json:"ssd"`
	// Pool: the imported pool the disk is in ("" = none); System: it holds keelNAS itself
	Pool   string `json:"pool,omitempty"`
	System bool   `json:"system,omitempty"`
	// Controller: what the disk hangs on (controllers.go); none for a virtual (virtio) disk
	Controller *Controller `json:"controller,omitempty"`
	SmartInfo
}

// Hardware is GET /v1/hardware.
type Hardware struct {
	Disks  []HWDisk `json:"disks"`
	ReadAt string   `json:"read_at"` // RFC 3339, when SMART was last read
}

// hardware reads every disk's SMART (smartctl -n standby: a sleeping disk is left asleep and
// keeps the daily check's values, marked standby), up to four disks at a time, and keeps the
// answer for hwEvery.
func (s *Server) hardware() (Hardware, error) {
	s.hwMu.Lock()
	defer s.hwMu.Unlock()
	now := s.now()
	if s.hw != nil && now.Sub(s.hwAt) < hwEvery {
		return *s.hw, nil
	}
	disks, err := s.disks()
	if err != nil {
		return Hardware{}, err
	}
	hw := Hardware{Disks: make([]HWDisk, len(disks)), ReadAt: now.UTC().Format(time.RFC3339)}
	ctrls := s.controllers()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, d := range disks {
		hw.Disks[i] = HWDisk{Name: d.Name, Model: d.Model, Serial: d.Serial, Size: d.Size, Sector: d.Sector, SSD: d.SSD, Pool: d.Pool, System: d.System}
		if c, ok := ctrls[d.Name]; ok {
			hw.Disks[i].Controller = &c
		}
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, _ := s.Sys.Run("", "smartctl", "-j", "-n", "standby", "-i", "-H", "-A", "/dev/"+name)
			hw.Disks[i].SmartInfo = ParseSmart(out)
		}(i, d.Name)
	}
	wg.Wait()
	// a sleeping disk: the daily check's values, so the page still has something to show
	s.hmu.Lock()
	for i := range hw.Disks {
		if x, ok := s.smart[hw.Disks[i].Name]; ok && hw.Disks[i].Standby {
			x.Standby = true
			hw.Disks[i].SmartInfo = x
		}
	}
	s.hmu.Unlock()
	sort.Slice(hw.Disks, func(i, j int) bool { return diskLess(hw.Disks[i].Name, hw.Disks[j].Name) })
	s.hw, s.hwAt = &hw, now
	return hw, nil
}

// diskLess orders da2 before da10 (the name's letters, then its number).
func diskLess(a, b string) bool {
	pa, na := splitDiskName(a)
	pb, nb := splitDiskName(b)
	if pa != pb {
		return pa < pb
	}
	if na != nb {
		return na < nb
	}
	return a < b
}

func splitDiskName(n string) (string, int) {
	i := len(n)
	for i > 0 && n[i-1] >= '0' && n[i-1] <= '9' {
		i--
	}
	num := 0
	for _, c := range n[i:] {
		num = num*10 + int(c-'0')
	}
	return n[:i], num
}

func (s *Server) getHardware(r *http.Request) (any, error) {
	return s.hardware()
}

// HWDiskDetail is GET /v1/hardware/disks/{name}: what the disk says about itself in full.
type HWDiskDetail struct {
	Name   string `json:"name"`
	Device string `json:"device"`
	Smart  string `json:"smart"` // the text of smartctl -x
}

func (s *Server) getHardwareDisk(r *http.Request) (any, error) {
	name := r.PathValue("name")
	disks, err := s.disks()
	if err != nil {
		return nil, err
	}
	found := false
	for _, d := range disks {
		found = found || d.Name == name
	}
	if !found {
		return nil, notFound("no disk %s", name)
	}
	// smartctl's exit status has bits for "the disk has logged errors": its text counts all the same
	out, _ := s.Sys.Run("", "smartctl", "-x", "/dev/"+name)
	return HWDiskDetail{Name: name, Device: "/dev/" + name, Smart: out}, nil
}

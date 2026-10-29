package nas

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Which device smartctl reads for a disk (docs/04 section 15), the way keelOS's host page does it
// (keel/internal/hwinfo/disks.go smartDevice): smartctl 7.5 on FreeBSD has no nda support and
// treats /dev/ndaN as a SCSI disk ("INQUIRY failed"); `smartctl --scan` lists the NVMe
// controller /dev/nvmeN (-d nvme) instead. So: scan, ask every listed device its serial number,
// and use the device whose serial is the disk's (geom's ident), with its -d type, for every
// smartctl call on that disk. smartd is never run (user 2026-10-23).

// smartDev is smartctl's device and -d type for one disk.
type smartDev struct{ Dev, Type string }

// smartDevs caches the scan (serial -> device) for hwEvery: the hardware page and the daily
// check share it.
type smartDevs struct {
	mu  sync.Mutex
	m   map[string]smartDev
	at  time.Time
	set bool
}

// smartDevices: disk name -> smartctl's device for the disks whose serial a scanned device has.
func (s *Server) smartDevices(disks []Disk) map[string]smartDev {
	s.sdev.mu.Lock()
	defer s.sdev.mu.Unlock()
	now := s.now()
	if !s.sdev.set || now.Sub(s.sdev.at) >= hwEvery {
		s.sdev.m, s.sdev.at, s.sdev.set = s.scanSmart(), now, true
	}
	res := map[string]smartDev{}
	for _, d := range disks {
		if x, ok := s.sdev.m[d.Serial]; ok && d.Serial != "" {
			res[d.Name] = x
		}
	}
	return res
}

func (s *Server) scanSmart() map[string]smartDev {
	m := map[string]smartDev{}
	out, _ := s.Sys.Run("", "smartctl", "--scan", "-j")
	var sc struct {
		Devices []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"devices"`
	}
	if json.Unmarshal([]byte(out), &sc) != nil {
		return m
	}
	for _, d := range sc.Devices {
		// --scan also lists /dev/ndaN as "scsi": asking it only gives INQUIRY failed, its controller
		// /dev/nvmeN is in the list too
		if strings.HasPrefix(d.Name, "/dev/ses") || strings.HasPrefix(d.Name, "/dev/pass") || strings.HasPrefix(d.Name, "/dev/nda") || d.Type == "" {
			continue
		}
		// the exit status is a bit mask; the JSON is what counts
		j, _ := s.Sys.Run("", "smartctl", "-j", "-i", "-d", d.Type, d.Name)
		var id struct {
			Serial string `json:"serial_number"`
		}
		if json.Unmarshal([]byte(j), &id) == nil && id.Serial != "" {
			if _, dup := m[id.Serial]; !dup {
				m[id.Serial] = smartDev{d.Name, d.Type}
			}
		}
	}
	return m
}

// smartArgs: the device arguments for a smartctl call on the disk; ok = false: do not call
// smartctl at all (an NVMe namespace the scan did not find: /dev/ndaN only gives INQUIRY failed).
// Any other disk the scan did not find (virtio) is asked by its own name, as before.
func smartArgs(name string, devs map[string]smartDev) ([]string, bool) {
	if x, ok := devs[name]; ok {
		return []string{"-d", x.Type, x.Dev}, true
	}
	if strings.HasPrefix(name, "nda") {
		return nil, false
	}
	return []string{"/dev/" + name}, true
}

// nvmeCtrl: the controller (nvme0) of a smartctl device /dev/nvme0 ("" for any other).
func nvmeCtrl(dev string) string {
	if c, ok := strings.CutPrefix(dev, "/dev/nvme"); ok && c != "" && !strings.ContainsAny(c, "ns/") {
		return "nvme" + c
	}
	return ""
}

package nas

import (
	"regexp"
	"strings"
)

// Which controller each disk hangs on (docs/04 section 15.1), for the hardware page's groups:
// CAM says which bus a disk is on and which driver instance owns the bus (camcontrol devlist
// -v); the instance's sysctl node has the chip's name and its PCI address. A virtio disk is not
// in CAM: it has no controller here and the page lists it apart.

// Controller is a disk controller of the NAS.
type Controller struct {
	Name string `json:"name"`           // mpr0, ahci0, nvme0
	Desc string `json:"desc,omitempty"` // the driver's name for the chip
	PCI  string `json:"pci,omitempty"`  // pci0:0:6:0
}

var (
	camBusRE  = regexp.MustCompile(`^scbus(-?\d+) on (\S+) bus \d+`)
	camDevRE  = regexp.MustCompile(`\(([^)]*)\)\s*$`)
	devUnitRE = regexp.MustCompile(`^([a-z_]+?)(\d+)$`)
	dbsfRE    = regexp.MustCompile(`dbsf=(\S+)`)
)

// ParseCamDevlist reads `camcontrol devlist -v`: disk (da3, ada0, nda1) -> the bus's owner (mpr0,
// ahcich2, nvme0).
func ParseCamDevlist(out string) map[string]string {
	m := map[string]string{}
	sim := ""
	for _, line := range strings.Split(out, "\n") {
		if b := camBusRE.FindStringSubmatch(line); b != nil {
			sim = b[2]
			continue
		}
		d := camDevRE.FindStringSubmatch(line)
		if d == nil || sim == "" || sim == "xpt0" {
			continue
		}
		for _, p := range strings.Split(d[1], ",") {
			if p = strings.TrimSpace(p); p != "" && !strings.HasPrefix(p, "pass") && !strings.HasPrefix(p, "ses") {
				m[p] = sim
			}
		}
	}
	return m
}

func (s *Server) devSysctl(dev, leaf string) string {
	u := devUnitRE.FindStringSubmatch(dev)
	if u == nil {
		return ""
	}
	out, err := s.Sys.Run("", "sysctl", "-n", "dev."+u[1]+"."+u[2]+".%"+leaf)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// controllers: disk -> its controller. An AHCI channel (ahcich2) is shown as its controller (ahci0).
func (s *Server) controllers() map[string]Controller {
	out, err := s.Sys.Run("", "camcontrol", "devlist", "-v")
	if err != nil {
		return nil
	}
	bySim := map[string]Controller{}
	m := map[string]Controller{}
	for disk, sim := range ParseCamDevlist(out) {
		c, ok := bySim[sim]
		if !ok {
			name := sim
			if strings.HasPrefix(sim, "ahcich") || strings.HasPrefix(sim, "ata") {
				if p := s.devSysctl(sim, "parent"); devUnitRE.MatchString(p) {
					name = p
				}
			}
			c = Controller{Name: name, Desc: s.devSysctl(name, "desc")}
			if l := dbsfRE.FindStringSubmatch(s.devSysctl(name, "location")); l != nil {
				c.PCI = l[1]
			}
			bySim[sim] = c
		}
		m[disk] = c
	}
	return m
}

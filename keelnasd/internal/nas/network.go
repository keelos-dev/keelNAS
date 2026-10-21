package nas

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The network (docs/04 section 11; user 2026-10-03: "需要加一个网络配置的tab", "因为我们走内部网络
// 管理keelNAS, 它网络不对我们也应该可以设定得到"). keelNAS has two NICs: the LAN one, set here
// (DHCP or a fixed address), and the internal one, which only connects keelNAS with its keelOS
// host: keel manages keelNAS over it, so a wrong LAN setting never locks keel out. The internal
// NIC is found by the MAC the seed names and gets the seed's address; nothing here changes it.
// The settings live in state.json (the system disk's /etc is replaced with every version).

// resolvMark starts the resolv.conf keelnasd writes for a fixed address.
const resolvMark = "# keelnasd (state.json network)\n"

// Network is state.json's "network"; nil = DHCP.
type Network struct {
	Mode    string   `json:"mode"`              // "dhcp" or "static"
	Address string   `json:"address,omitempty"` // static: 198.51.100.20/24
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
}

// Iface is one network interface as ifconfig shows it.
type Iface struct {
	Name     string   `json:"name"`
	MAC      string   `json:"mac"`
	Up       bool     `json:"up"`
	Link     bool     `json:"link"`
	Address  []string `json:"addresses"` // IPv4 with prefix length
	Internal bool     `json:"internal,omitempty"`
}

var (
	ifHeadRE = regexp.MustCompile(`^([a-z][a-z0-9_.]*): flags=[0-9a-f]+<([^>]*)>`)
	macRE    = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)
)

// parseIfconfig reads `ifconfig -a`: the Ethernet interfaces (those with an "ether" line).
func parseIfconfig(out string) []Iface {
	var all []Iface
	var cur *Iface
	for _, l := range strings.Split(out, "\n") {
		if m := ifHeadRE.FindStringSubmatch(l); m != nil {
			all = append(all, Iface{Name: m[1], Up: strings.Contains(","+m[2]+",", ",UP,"), Address: []string{}})
			cur = &all[len(all)-1]
			continue
		}
		if cur == nil {
			continue
		}
		f := strings.Fields(l)
		switch {
		case len(f) >= 2 && f[0] == "ether":
			cur.MAC = strings.ToLower(f[1])
		case len(f) >= 4 && f[0] == "inet" && f[2] == "netmask":
			ones := 0
			var mask uint64
			fmt.Sscanf(strings.TrimPrefix(f[3], "0x"), "%x", &mask)
			for b := mask; b != 0; b &= b - 1 {
				ones++
			}
			cur.Address = append(cur.Address, fmt.Sprintf("%s/%d", f[1], ones))
		case len(f) >= 2 && f[0] == "status:":
			cur.Link = f[1] == "active"
		}
	}
	var out2 []Iface
	for _, i := range all {
		if i.MAC != "" {
			out2 = append(out2, i)
		}
	}
	return out2
}

func (s *Server) ifaces() []Iface {
	out, err := s.Sys.Run("", "ifconfig", "-a")
	if err != nil {
		return nil
	}
	is := parseIfconfig(out)
	in := s.internalMAC()
	for k := range is {
		is[k].Internal = in != "" && is[k].MAC == in
	}
	return is
}

// internalMAC is the internal NIC's MAC from the seed ("" = no seed or an older seed).
func (s *Server) internalMAC() string {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	if s.seed == nil || s.seed.Internal == nil {
		return ""
	}
	return s.seed.Internal.MAC
}

// lan is the LAN interface: the first Ethernet interface that is not the internal one.
func lan(is []Iface) *Iface {
	for k := range is {
		if !is[k].Internal {
			return &is[k]
		}
	}
	return nil
}

// ApplyInternal sets the internal NIC's address from the seed; no DHCP there.
func (s *Server) ApplyInternal() {
	s.hmu.Lock()
	sd := s.seed
	s.hmu.Unlock()
	if sd == nil || sd.Internal == nil {
		return
	}
	for _, i := range s.ifaces() {
		if !i.Internal {
			continue
		}
		s.Sys.Run("", "service", "dhclient", "stop", i.Name)
		if _, err := s.Sys.Run("", "ifconfig", i.Name, "inet", sd.Internal.Address, "up"); err != nil {
			s.Log.Printf("internal NIC %s: %v", i.Name, err)
			return
		}
		s.Log.Printf("internal NIC %s: %s", i.Name, sd.Internal.Address)
		return
	}
	s.Log.Printf("internal NIC: no interface with MAC %s", sd.Internal.MAC)
}

// ApplyNetwork sets the LAN interface from state.json's "network".
func (s *Server) ApplyNetwork() error {
	n := s.State().Network
	i := lan(s.ifaces())
	if i == nil {
		return fmt.Errorf("no LAN interface")
	}
	if n == nil || n.Mode == "dhcp" {
		if out, _ := s.Sys.Run("", "service", "dhclient", "status", i.Name); strings.Contains(out, "is running") {
			return nil
		}
		for _, a := range i.Address { // a fixed address before
			s.Sys.Run("", "ifconfig", i.Name, "inet", strings.Split(a, "/")[0], "-alias")
		}
		// a resolv.conf keelnasd wrote for the fixed address: dhclient does not replace it
		if b, err := s.Sys.ReadFile("/etc/resolv.conf"); err == nil && strings.HasPrefix(string(b), resolvMark) {
			s.Sys.WriteFile("/etc/resolv.conf", nil, 0o644)
		}
		_, err := s.Sys.Run("", "service", "dhclient", "start", i.Name)
		s.Log.Printf("network %s: DHCP", i.Name)
		return err
	}
	s.Sys.Run("", "service", "dhclient", "stop", i.Name)
	for _, a := range i.Address {
		if a != n.Address {
			s.Sys.Run("", "ifconfig", i.Name, "inet", strings.Split(a, "/")[0], "-alias")
		}
	}
	if _, err := s.Sys.Run("", "ifconfig", i.Name, "inet", n.Address, "up"); err != nil {
		return err
	}
	s.Sys.Run("", "route", "-n", "delete", "default")
	if n.Gateway != "" {
		if _, err := s.Sys.Run("", "route", "-n", "add", "default", n.Gateway); err != nil {
			return err
		}
	}
	if len(n.DNS) > 0 {
		var b strings.Builder
		b.WriteString(resolvMark)
		for _, d := range n.DNS {
			b.WriteString("nameserver " + d + "\n")
		}
		if err := s.Sys.WriteFile("/etc/resolv.conf", []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	s.Log.Printf("network %s: %s gateway %s dns %v", i.Name, n.Address, n.Gateway, n.DNS)
	return nil
}

// ValidNetwork checks a Network.
func ValidNetwork(n *Network) error {
	switch n.Mode {
	case "dhcp":
		if n.Address != "" || n.Gateway != "" || len(n.DNS) > 0 {
			return fmt.Errorf("dhcp takes no address, gateway or dns")
		}
		return nil
	case "static":
	default:
		return fmt.Errorf("mode: dhcp or static")
	}
	ip, ipn, err := net.ParseCIDR(n.Address)
	if err != nil || ip.To4() == nil {
		return fmt.Errorf("address: an IPv4 address with its prefix length, like 198.51.100.20/24")
	}
	if ones, _ := ipn.Mask.Size(); ones < 8 || ones > 30 || ip.Equal(ipn.IP) || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
		return fmt.Errorf("address %s cannot be a host's address", n.Address)
	}
	if n.Gateway != "" {
		g := net.ParseIP(n.Gateway)
		if g == nil || g.To4() == nil || !ipn.Contains(g) || g.Equal(ip) {
			return fmt.Errorf("gateway %s: an IPv4 address in %s other than the address", n.Gateway, ipn)
		}
	}
	if len(n.DNS) > 3 {
		return fmt.Errorf("dns: at most 3 servers")
	}
	for _, d := range n.DNS {
		if x := net.ParseIP(d); x == nil || x.To4() == nil {
			return fmt.Errorf("dns %q: an IPv4 address", d)
		}
	}
	return nil
}

// NetworkView is GET /v1/network.
type NetworkView struct {
	Settings Network  `json:"settings"`
	LAN      *Iface   `json:"lan"`
	Internal *Iface   `json:"internal"`
	Gateway  string   `json:"gateway"` // the default route now
	DNS      []string `json:"dns"`     // resolv.conf now
	Hostname string   `json:"hostname"`
}

func (s *Server) getNetwork(r *http.Request) (any, error) {
	v := NetworkView{Settings: Network{Mode: "dhcp"}, DNS: []string{}, Hostname: s.hostname()}
	if n := s.State().Network; n != nil {
		v.Settings = *n
	}
	is := s.ifaces()
	v.LAN = lan(is)
	for k := range is {
		if is[k].Internal {
			v.Internal = &is[k]
		}
	}
	if out, err := s.Sys.Run("", "route", "-n", "get", "default"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[0] == "gateway:" {
				v.Gateway = f[1]
			}
		}
	}
	if b, err := s.Sys.ReadFile("/etc/resolv.conf"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[0] == "nameserver" {
				v.DNS = append(v.DNS, f[1])
			}
		}
	}
	return v, nil
}

// putNetwork saves the LAN settings and applies them a moment after the answer went out (the
// request may have come over the LAN; keel's comes over the internal NIC).
func (s *Server) putNetwork(r *http.Request) (any, error) {
	var n Network
	if err := decode(r, &n); err != nil {
		return nil, err
	}
	if err := ValidNetwork(&n); err != nil {
		return nil, bad("%v", err)
	}
	s.mu.Lock()
	st := s.st.Clone()
	if n.Mode == "dhcp" {
		st.Network = nil
	} else {
		st.Network = &n
	}
	if err := SaveState(s.Sys, s.StatePath, st); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.st = st
	s.mu.Unlock()
	go func() {
		time.Sleep(time.Second)
		if err := s.ApplyNetwork(); err != nil {
			s.Log.Printf("network: %v", err)
		}
	}()
	return map[string]any{"ok": true, "settings": n}, nil
}

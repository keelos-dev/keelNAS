package nas

import (
	"io"
	"log"
	"slices"
	"strings"
	"testing"

	"keelnas/internal/sys"
)

const ifconfigOut = `vtnet0: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST,LOWER_UP> metric 0 mtu 1500
	options=4c079b<RXCSUM,TXCSUM>
	ether 58:9c:fc:00:11:22
	inet 192.0.2.127 netmask 0xffffff00 broadcast 192.0.2.255
	media: Ethernet autoselect (10Gbase-T <full-duplex>)
	status: active
	nd6 options=29<PERFORMNUD,IFDISABLED,AUTO_LINKLOCAL>
vtnet1: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST,LOWER_UP> metric 0 mtu 1500
	ether 58:9C:FC:0A:BB:CC
	inet 169.254.77.2 netmask 0xffffff00 broadcast 169.254.77.255
	status: active
lo0: flags=1008049<UP,LOOPBACK,RUNNING,MULTICAST,LOWER_UP> metric 0 mtu 16384
	inet 127.0.0.1 netmask 0xff000000
`

func TestParseIfconfig(t *testing.T) {
	is := parseIfconfig(ifconfigOut)
	if len(is) != 2 || is[0].Name != "vtnet0" || is[0].MAC != "58:9c:fc:00:11:22" || !is[0].Link || !is[0].Up ||
		!slices.Equal(is[0].Address, []string{"192.0.2.127/24"}) || is[1].MAC != "58:9c:fc:0a:bb:cc" {
		t.Fatalf("%+v", is)
	}
}

func TestValidNetwork(t *testing.T) {
	for _, c := range []struct {
		n  Network
		ok bool
	}{
		{Network{Mode: "dhcp"}, true},
		{Network{Mode: "dhcp", Address: "10.0.0.2/24"}, false},
		{Network{Mode: "static", Address: "198.51.100.20/24", Gateway: "198.51.100.1", DNS: []string{"1.1.1.1"}}, true},
		{Network{Mode: "static", Address: "198.51.100.20/24"}, true},
		{Network{Mode: "static", Address: "198.51.100.20"}, false},
		{Network{Mode: "static", Address: "198.51.100.0/24"}, false},
		{Network{Mode: "static", Address: "169.254.77.5/24"}, false},
		{Network{Mode: "static", Address: "198.51.100.20/24", Gateway: "203.0.113.1"}, false},
		{Network{Mode: "static", Address: "198.51.100.20/24", DNS: []string{"x"}}, false},
		{Network{Mode: "auto"}, false},
	} {
		if err := ValidNetwork(&c.n); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.n, err)
		}
	}
}

func TestApplyNetwork(t *testing.T) {
	f := sys.NewFake()
	dhcpRunning := true
	f.Handle = func(line, _ string) (string, error, bool) {
		switch {
		case line == "ifconfig -a":
			return ifconfigOut, nil, true
		case strings.HasPrefix(line, "service dhclient status"):
			if dhcpRunning {
				return "dhclient is running as pid 1", nil, true
			}
			return "dhclient is not running", nil, true
		}
		return "", nil, false
	}
	s, err := NewServer(f, StatePath, "test", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	s.seed = &Seed{Internal: &SeedInternal{MAC: "58:9c:fc:0a:bb:cc", Address: "169.254.77.2/24"}}

	// the internal NIC: its address, no DHCP; the LAN NIC is untouched
	f.Calls = nil
	s.ApplyInternal()
	if !f.Ran("ifconfig vtnet1 inet 169.254.77.2/24 up") || !f.Ran("service dhclient stop vtnet1") || f.Ran("ifconfig vtnet0") {
		t.Fatalf("internal: %v", f.Calls)
	}

	// static on the LAN NIC (vtnet0, not the internal one)
	s.st.Network = &Network{Mode: "static", Address: "192.0.2.50/24", Gateway: "192.0.2.254", DNS: []string{"192.0.2.254"}}
	f.Calls = nil
	if err := s.ApplyNetwork(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"service dhclient stop vtnet0", "ifconfig vtnet0 inet 192.0.2.127 -alias", "ifconfig vtnet0 inet 192.0.2.50/24 up", "route -n add default 192.0.2.254"} {
		if !f.Ran(want) {
			t.Errorf("static: no %q in %v", want, f.Calls)
		}
	}
	if f.Ran("ifconfig vtnet1") {
		t.Errorf("static touched the internal NIC: %v", f.Calls)
	}
	if !strings.Contains(string(f.Files["/etc/resolv.conf"]), "nameserver 192.0.2.254") {
		t.Errorf("resolv.conf: %q", f.Files["/etc/resolv.conf"])
	}

	// back to DHCP while dhclient does not run: the old address goes, dhclient starts
	s.st.Network = nil
	dhcpRunning = false
	f.Calls = nil
	s.ApplyNetwork()
	if !f.Ran("service dhclient start vtnet0") {
		t.Errorf("dhcp: %v", f.Calls)
	}
	if len(f.Files["/etc/resolv.conf"]) != 0 {
		t.Errorf("dhcp kept keelnasd's resolv.conf: %q", f.Files["/etc/resolv.conf"])
	}
	// DHCP that runs already: nothing
	dhcpRunning = true
	f.Calls = nil
	s.ApplyNetwork()
	if f.Ran("service dhclient start") || f.Ran("ifconfig vtnet0") {
		t.Errorf("dhcp running: %v", f.Calls)
	}
}

package nas

import (
	"strings"
	"testing"
)

func TestCtlKeel(t *testing.T) {
	vols := []KeelVol{
		{Name: "d639674b-4539-47b6-bd59-b16f5cfea861-disk1", Pool: "tank", SizeGiB: 40, Initiator: "iqn.2026-11.dev.keelos:host-1a2b3c4d"},
		{Name: "aaaa-disk0", Pool: "tank", SizeGiB: 20, Initiator: "iqn.2026-11.dev.keelos:host-1a2b3c4d"},
	}
	st := &State{Pools: []Pool{{Name: "tank", Altroot: "/pools/tank"}}}
	if got := ctlKeel(st, vols, ""); got != "" {
		t.Fatalf("no data link: %q", got)
	}
	if got := ctlKeel(st, nil, "169.254.78.2"); got != "" {
		t.Fatalf("no volumes: %q", got)
	}
	got := ctlKeel(st, vols, "169.254.78.2")
	for _, want := range []string{
		"portal-group pgkeel {\n\tdiscovery-auth-group no-access\n\tlisten 169.254.78.2\n}",
		"auth-group agkeel0 {\n\tauth-type none\n\tinitiator-name \"iqn.2026-11.dev.keelos:host-1a2b3c4d\"\n}",
		"target iqn.2026-11.dev.keelos:keel:aaaa-disk0 {\n\tauth-group agkeel0\n\tportal-group pgkeel",
		"path /pools/tank/keelvol/d639674b-4539-47b6-bd59-b16f5cfea861-disk1/disk.raw",
		`serial "` + KeelVolSerial("aaaa-disk0") + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if strings.Count(got, "auth-group agkeel") != 3 || strings.Index(got, "aaaa-disk0 {") > strings.Index(got, "d639674b") {
		t.Errorf("one auth group per initiator, targets sorted:\n%s", got)
	}
	if s := KeelVolSerial("aaaa-disk0"); len(s) != 16 || s != KeelVolSerial("aaaa-disk0") || s == KeelVolSerial("aaaa-disk1") {
		t.Errorf("serial %q", s)
	}
	for n, ok := range map[string]bool{"d639674b-4539-47b6-bd59-b16f5cfea861-disk1": true, "Upper": false, "-x": false, "a/b": false, strings.Repeat("a", 64): false} {
		if keelVolRE.MatchString(n) != ok {
			t.Errorf("name %q: want %v", n, ok)
		}
	}
	for n, ok := range map[string]bool{"iqn.2026-11.dev.keelos:host-1a2b3c4d": true, "iqn.1994-09.org.freebsd:qnas": true, "foo": false, `iqn.2026-11.x:"a"`: false} {
		if initiatorRE.MatchString(n) != ok {
			t.Errorf("initiator %q: want %v", n, ok)
		}
	}
}

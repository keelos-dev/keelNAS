package nas

import (
	"strings"
	"testing"

	"keelnas/internal/sys"
)

// smartctl's device for each disk comes from --scan and the serial numbers (like keelOS's host
// page): nda0 is read as /dev/nvme0 -d nvme, ada-style disks as --scan names them, and no smartctl
// call ever gets /dev/nda*. The detail has nvmecontrol's log page 2 for NVMe only.
func TestSmartDevicesNVMe(t *testing.T) {
	s, w, f, ts := newTestServer(t)
	nvme := readTestdata(t, "smart-nvme.json")
	ata := readTestdata(t, "smart-ata-ok.json")
	lp := "SMART/Health Information Log\n============================\nCritical Warning State:         0x00\nTemperature:                    317 K, 43.85 C, 110.93 F\n"
	// geom: vtbd0 stays (system, virtio, not in the scan), vtbd1 becomes nda0, plus ada0
	geom := strings.ReplaceAll(readTestdata(t, "geom-disk-list.txt"), "vtbd1", "nda0")
	geom = strings.Replace(geom, "Geom name: cd0", "Geom name: ada0\nProviders:\n1. Name: ada0\n   Mediasize: 4000787030016 (3.6T)\n   Sectorsize: 512\n   Mode: r0w0e0\n   descr: WDC WD40EFRX\n   ident: WD-ATA0001\n   rotationrate: 5400\n\nGeom name: cd0", 1)
	w.extra = func(line string, a []string) (string, error, bool) {
		switch {
		case line == "geom disk list":
			return geom, nil, true
		case strings.HasPrefix(line, "zpool status -j"):
			return strings.ReplaceAll(w.status, "vtbd1", "nda0"), nil, true
		case line == "smartctl --scan -j":
			return `{"devices":[{"name":"/dev/ses0","type":"scsi"},{"name":"/dev/ada0","type":"atacam"},{"name":"/dev/nda0","type":"scsi"},{"name":"/dev/nvme0","type":"nvme"},{"name":"/dev/pass2","type":"scsi"}]}`, nil, true
		case line == "smartctl -j -i -d nvme /dev/nvme0":
			return `{"serial_number":"BHYVE-FCD9-0EEB-9081"}`, sys.ErrFail, true
		case line == "smartctl -j -i -d atacam /dev/ada0":
			return `{"serial_number":"WD-ATA0001"}`, nil, true
		case strings.HasPrefix(line, "smartctl") && strings.HasSuffix(line, "-d nvme /dev/nvme0"):
			if strings.Contains(line, " -x ") {
				return "smartctl 7.5\nSMART overall-health self-assessment test result: PASSED\n", nil, true
			}
			return nvme, nil, true
		case strings.HasPrefix(line, "smartctl") && strings.HasSuffix(line, "-d atacam /dev/ada0"):
			if strings.Contains(line, " -x ") {
				return "smartctl 7.5 ATA\n", nil, true
			}
			return ata, nil, true
		case line == "nvmecontrol logpage -p 2 nvme0":
			return lp, nil, true
		case strings.HasPrefix(line, "smartctl"):
			return `{"smartctl":{"exit_status":2}}`, sys.ErrFail, true
		}
		return "", nil, false
	}
	if c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["nda0"]}`); c != 200 {
		t.Fatalf("storage: %d %v", c, m)
	}
	_, m := do(t, ts, "GET", "/v1/hardware", "")
	by := map[string]map[string]any{}
	for _, d := range m["disks"].([]any) {
		by[d.(map[string]any)["name"].(string)] = d.(map[string]any)
	}
	if d := by["nda0"]; d == nil || d["smart"] != "ok" || d["temperature"] != float64(44) || d["protocol"] != "NVMe" || d["wear"] != float64(3) || d["spare"] != float64(100) || d["unsafe_shutdowns"] != float64(12) {
		t.Errorf("nda0: %v", d)
	}
	if d := by["ada0"]; d == nil || d["health"] != "passed" || d["temperature"] != float64(37) {
		t.Errorf("ada0: %v", d)
	}
	for _, want := range []string{"smartctl -j -n standby -i -H -A -d nvme /dev/nvme0", "smartctl -j -n standby -i -H -A -d atacam /dev/ada0", "smartctl -j -n standby -i -H -A /dev/vtbd0"} {
		if !f.Ran(want) {
			t.Errorf("not run: %s", want)
		}
	}
	// the detail: smartctl -x on the device; the log page for NVMe only
	code, d := do(t, ts, "GET", "/v1/hardware/disks/nda0", "")
	if code != 200 || d["device"] != "/dev/nvme0" || !strings.Contains(d["smart"].(string), "PASSED") || d["nvme_log"] != lp {
		t.Errorf("detail nda0: %d %v", code, d)
	}
	_, d = do(t, ts, "GET", "/v1/hardware/disks/ada0", "")
	if d["device"] != "/dev/ada0" || d["nvme_log"] != nil || d["smart"] != "smartctl 7.5 ATA\n" {
		t.Errorf("detail ada0: %v", d)
	}
	// the daily check on the pool disk uses the same device
	res := s.Tick(utc("2026-10-23T11:07:00Z"), nil, nil)
	if strings.Join(res.Smart, " ") != "nda0:ok" || !f.Ran("smartctl -H -A -j -d nvme /dev/nvme0") {
		t.Errorf("tick: %v %v", res.Smart, f.Calls)
	}
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "smartctl") && strings.Contains(c, "/dev/nda") {
			t.Errorf("smartctl on nda: %s", c)
		}
	}
}

// An nda the scan does not find: unknown, and smartctl is not asked about /dev/nda0.
func TestSmartArgs(t *testing.T) {
	if a, ok := smartArgs("nda0", nil); ok || a != nil {
		t.Errorf("nda0: %v %v", a, ok)
	}
	if a, ok := smartArgs("vtbd1", nil); !ok || strings.Join(a, " ") != "/dev/vtbd1" {
		t.Errorf("vtbd1: %v", a)
	}
	if a, _ := smartArgs("nda0", map[string]smartDev{"nda0": {"/dev/nvme1", "nvme"}}); strings.Join(a, " ") != "-d nvme /dev/nvme1" {
		t.Errorf("mapped: %v", a)
	}
	for in, want := range map[string]string{"/dev/nvme0": "nvme0", "/dev/nvme12": "nvme12", "/dev/ada0": "", "/dev/nvme0ns1": "", "/dev/nvme": ""} {
		if got := nvmeCtrl(in); got != want {
			t.Errorf("nvmeCtrl(%s) = %q", in, got)
		}
	}
}

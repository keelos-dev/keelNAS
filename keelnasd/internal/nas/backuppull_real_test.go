package nas

import (
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"keelnas/internal/sys"
)

// TestRealFetch runs a fetch against a real source with real ZFS (FreeBSD only; set
// KEELNAS_REAL_TOKEN to a pull token and KEELNAS_REAL_POOL to a scratch pool). It fetches
// twice (the second time with KEELNAS_REAL_AGAIN=1 after a new snapshot on the source).
func TestRealFetch(t *testing.T) {
	tok, pool := os.Getenv("KEELNAS_REAL_TOKEN"), os.Getenv("KEELNAS_REAL_POOL")
	if tok == "" || pool == "" {
		t.Skip("KEELNAS_REAL_TOKEN / KEELNAS_REAL_POOL not set")
	}
	host, port, sec, pin, err := ParsePullToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if h := os.Getenv("KEELNAS_REAL_HOST"); h != "" { // the test VM reaches its own keelMail by another name
		host = h
	}
	dir := os.Getenv("KEELNAS_REAL_DIR") // the key stays between runs
	if dir == "" {
		dir = t.TempDir()
	}
	s := &Server{Sys: sys.Real{}, StatePath: dir + "/state.json", Log: log.New(os.Stderr, "", log.LstdFlags)}
	src := BackupSource{Name: "real", Host: host, Port: port, Secret: sec, Pin: pin, Pool: pool}
	snap, n, err := s.bkFetch(context.Background(), src, BackupKeep{Daily: 30, Monthly: 12})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fetched %s, %d bytes", snap, n)
	out, _ := s.Sys.Run("", "zfs", "list", "-H", "-t", "all", "-o", "name,encryption,readonly,mountpoint,mounted", "-r", pool+"/"+BackupDS)
	t.Logf("\n%s", out)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if f[1] != "aes-256-gcm" || (!strings.Contains(f[0], "@") && (f[3] != "none" || f[4] != "no")) {
			t.Errorf("not encrypted or mountable: %s", line)
		}
	}
	// the archive inside the copy (keelMail docs/09): mounted read-only and searched
	if os.Getenv("KEELNAS_REAL_ARCHIVE") != "" {
		src.Kind = "keelmail"
		now := time.Now().UTC()
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		res, total, missing, err := s.archSearch(src, ArchQuery{From: day.AddDate(0, 0, -1), To: day, Body: os.Getenv("KEELNAS_REAL_ARCHIVE")})
		t.Logf("archive: %d found, missing %v, err %v", total, missing, err)
		for _, e := range res {
			_, text, raw, err := s.archOpen(src, e.Snap, e.ID)
			t.Logf("  %s %s %q from %s: text %q, eml %d bytes, err %v", e.T.Format(time.RFC3339), e.Dir, e.Subject, e.Snap, text, len(raw), err)
		}
		if err != nil || total == 0 {
			t.Errorf("nothing found in the copy")
		}
		if out, _ := s.Sys.Run("", "mount", "-p"); strings.Contains(out, archMountRoot) {
			t.Errorf("left mounted: %s", out)
		}
	}
}

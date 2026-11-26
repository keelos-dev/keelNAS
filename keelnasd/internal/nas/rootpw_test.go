package nas

import (
	"io"
	"log"
	"regexp"
	"strings"
	"testing"

	"keelnas/internal/sys"
)

// the same vector is in keelOS keel/internal/web/nasrootpw_test.go
const rootPwVectorKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRootPassword(t *testing.T) {
	p := RootPassword(rootPwVectorKey)
	if !regexp.MustCompile(`^[2-9a-hjkmnp-z]{4}(-[2-9a-hjkmnp-z]{4}){3}$`).MatchString(p) {
		t.Fatalf("shape %q", p)
	}
	if p != RootPassword(rootPwVectorKey) || p == RootPassword(strings.Repeat("f", 64)) {
		t.Fatalf("not fixed by the key: %q", p)
	}
	if p != "5hca-u6k5-932y-bfje" {
		t.Fatalf("vector %q", p)
	}
	f := sys.NewFake()
	s, err := NewServer(f, StatePath, "test", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	s.ApplyRootPassword() // no seed: nothing
	if len(f.Calls) != 0 {
		t.Fatalf("without a seed: %v", f.Calls)
	}
	s.seed = &Seed{Key: rootPwVectorKey}
	s.ApplyRootPassword()
	if !f.Ran("pw usermod root -h 0") {
		t.Fatalf("calls %v", f.Calls)
	}
}

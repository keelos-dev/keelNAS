package nas

import "testing"

// A user synced from keelAuth may have inner dots (first.last); local users and shares keep the
// old rule.
func TestValidSyncName(t *testing.T) {
	for n, want := range map[string]bool{"manho.sen": true, "a.b.c": true, "anna": true, ".a": false, "a.": false, "a..b": false, "A.b": false} {
		if ValidSyncName(n) != want {
			t.Errorf("ValidSyncName(%q) = %v", n, !want)
		}
	}
	if ValidName("a.b") {
		t.Error("a local name took a dot")
	}
}

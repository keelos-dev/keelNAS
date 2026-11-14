package nas

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"keelnas/internal/sys"
)

// aclStore answers getfacl and setfacl -a 0 … -x N … from memory. A path nobody set has the
// ACL of a file the owner change left behind (0660: the group may write).
type aclStore struct {
	mu  sync.Mutex
	acl map[string][]string
}

var oldFileACL = []string{"owner@:rw-p--aARWcCos:-------:allow", "group@:rw-p--a-R-c--s:-------:allow", "everyone@:------a-R-c--s:-------:allow"}

func (a *aclStore) get(p string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := a.acl[p]; ok {
		return slices.Clone(v)
	}
	return slices.Clone(oldFileACL)
}

func (a *aclStore) handle(line string) (string, bool) {
	f := strings.Fields(line)
	switch f[0] {
	case "getfacl":
		var b strings.Builder
		for _, p := range f[1:] {
			if p == "-q" {
				continue
			}
			if !slices.Contains(f, "-q") {
				b.WriteString("# file: " + p + "\n# owner: root\n# group: keelnas\n")
			}
			for _, e := range a.get(p) {
				b.WriteString("   " + e + "\n")
			}
			b.WriteString("\n")
		}
		return b.String(), true
	case "setfacl":
		if len(f) < 4 || f[1] != "-a" || f[2] != "0" {
			return "", false
		}
		add := strings.Split(f[3], ",")
		drop := 0
		i := 4
		for ; i+1 < len(f) && f[i] == "-x"; i += 2 {
			if n, _ := strconv.Atoi(f[i+1]); n != len(add) {
				panic("setfacl -x " + f[i+1] + " with " + strconv.Itoa(len(add)) + " new entries")
			}
			drop++
		}
		for _, p := range f[i:] {
			if had := len(a.get(p)); had != drop {
				panic("setfacl: " + p + " has " + strconv.Itoa(had) + " entries, " + strconv.Itoa(drop) + " taken out")
			}
			a.mu.Lock()
			a.acl[p] = slices.Clone(add)
			a.mu.Unlock()
		}
		return "", true
	}
	return "", false
}

func TestACLRules(t *testing.T) {
	root := parseACL([]string{"user:a:r-x---a-R-c--s:-------:allow", "user:a:rwxpDdaARWc--s:fdi----:allow", "group:g:r-x---a-R-c--s:fd-----:allow", "owner@:rwxpDdaARWcCos:fd-----:allow"})
	if got := aclStrings(handedDown(root, true)); !slices.Equal(got, []string{"user:a:rwxpDdaARWc--s:fd----I:allow", "group:g:r-x---a-R-c--s:fd----I:allow", "owner@:rwxpDdaARWcCos:fd----I:allow"}) {
		t.Errorf("a directory inherits %q", got)
	}
	if got := aclStrings(handedDown(root, false)); !slices.Equal(got, []string{"user:a:rwxpDdaARWc--s:------I:allow", "group:g:r-x---a-R-c--s:------I:allow", "owner@:rwxpDdaARWcCos:------I:allow"}) {
		t.Errorf("a file inherits %q", got)
	}
	for acl, own := range map[string]bool{
		"user:a:rwxpDdaARWc--s:fd----I:allow owner@:rwxpDdaARWcCos:fd----I:allow":                                 false, // inherited
		"user:b:rwxpDdaARWc--s:fd-----:allow user:a:rwxpDdaARWc--s:fd----I:allow":                                 false, // inherited, and one more name
		"group:g:rwxpDdaARWc---:fd-----:allow owner@:rwxpDdaARWcCo-:fd-----:allow everyone@:------:fd----I:allow": true,  // Windows: inheritance off
		"everyone@:--------------:fd----I:allow":                                                                  true,  // Windows: all removed
		"owner@:rw-p--aARWcCos:-------:allow group@:rw-p--a-R-c--s:-------:allow everyone@:------a-R-c--s::allow": false, // an old file
	} {
		if got := ownACL(parseACL(strings.Fields(acl))); got != own {
			t.Errorf("ownACL(%s) = %v", acl, got)
		}
	}
	for _, n := range []string{"経理", "Scan", "a b", "2026.11"} {
		if !validFolderName(n) {
			t.Errorf("folder name %q refused", n)
		}
	}
	for _, n := range []string{"", ".", "..", "a/b", "a\\b", "a:b", "x.", "x ", " x", ".zfs", ".keelnas", "a\nb", "a*"} {
		if validFolderName(n) {
			t.Errorf("folder name %q taken", n)
		}
	}
}

func TestFolders(t *testing.T) {
	s, w, call, conf, home := datasetServer(t)
	fk := s.Sys.(*sys.Fake)
	s.syncJobs = true
	store := &aclStore{acl: map[string][]string{}}
	old := fk.Handle
	fk.Handle = func(line, stdin string) (string, error, bool) {
		if out, ok := store.handle(line); ok {
			return out, nil, true
		}
		return old(line, stdin)
	}
	call("POST", "/v1/users", `{"name":"carol","password":"c"}`)
	call("POST", "/v1/users", `{"name":"scan","password":"s"}`)
	w.mu.Lock()
	w.owners[home] = "2001:2000"
	w.mu.Unlock()
	if c, m := call("POST", "/v1/shares", `{"dataset":"tank/home","name":"cst","rw":["alice","bob"]}`); c != 200 {
		t.Fatalf("share: %d %v", c, m)
	}
	const (
		mod  = ":rwxpDdaARWc--s:"
		modi = ":rwxpD-aARWc--s:" // without "delete this one": folders with their own list, a root for managers
		read = ":r-x---a-R-c--s:"
		full = ":rwxpDdaARWcCos:"
		none = ":--------------:"
	)
	tail := func(fl string) []string {
		return []string{"owner@" + full + fl + ":allow", "group@" + none + fl + ":allow", "everyone@" + none + fl + ":allow"}
	}
	is := func(what, path string, want ...string) {
		t.Helper()
		if got := store.get(path); !slices.Equal(got, want) {
			t.Errorf("%s: ACL of %s:\n got  %q\n want %q", what, strings.TrimPrefix(path, home), got, want)
		}
	}
	is("share", home, append([]string{"user:alice" + mod + "fd-----:allow", "user:bob" + mod + "fd-----:allow"}, tail("fd-----")...)...)
	if !strings.Contains(conf(), "  hide unreadable = yes\n") {
		t.Errorf("smb4.conf does not hide what cannot be read:\n%s", conf())
	}

	// a folder like the share, and one of its own: 経理 for alice only
	for body, want := range map[string]int{`{"name":"共通"}`: 200, `{"name":"経理","rw":["alice"]}`: 200, `{"name":"経理"}`: 409,
		`{"name":"a/b"}`: 400, `{"name":".zfs"}`: 400, `{"name":"x","rw":["nobody"]}`: 400, `{"name":"y","rw":["alice"],"ro":["alice"]}`: 400} {
		if c, m := call("POST", "/v1/shares/cst/folders", body); c != want {
			t.Errorf("POST folders %s: %d (want %d) %v", body, c, want, m)
		}
	}
	if c, _ := call("POST", "/v1/shares/nope/folders", `{"name":"x"}`); c != 404 {
		t.Errorf("unknown share: %d", c)
	}
	keiri := filepath.Join(home, "経理")
	is("own list", keiri, append([]string{"user:alice" + modi + "fd-----:allow"}, tail("fd-----")...)...)
	folder := func(name string) map[string]any {
		t.Helper()
		_, m := call("GET", "/v1/shares/cst/folders", "")
		for _, x := range m["folders"].([]any) {
			if f := x.(map[string]any); f["name"] == name {
				return f
			}
		}
		t.Fatalf("no folder %s in %v", name, m)
		return nil
	}
	if f := folder("経理"); f["inherit"] != false || len(f["rw"].([]any)) != 1 || f["rw"].([]any)[0] != "alice" || len(f["ro"].([]any)) != 0 || f["other"] != false {
		t.Errorf("経理: %v", f)
	}
	if f := folder("共通"); f["inherit"] != true || len(f["rw"].([]any)) != 0 {
		t.Errorf("共通: %v", f)
	}

	// 営業 exists with files: an old one (the group may write), one somebody gave carol in
	// Windows, a sub-folder, and a sub-folder with inheritance switched off in Windows
	eigyo := filepath.Join(home, "営業")
	for _, d := range []string{"sub", "private"} {
		os.MkdirAll(filepath.Join(eigyo, d), 0o770)
	}
	for _, f := range []string{"old.txt", "carol.txt", "sub/deep.txt", "private/p.txt"} {
		os.WriteFile(filepath.Join(eigyo, f), []byte("x"), 0o660)
	}
	os.Symlink("/etc/passwd", filepath.Join(eigyo, "link"))
	store.acl[filepath.Join(eigyo, "carol.txt")] = []string{"user:carol" + read + "-------:allow", "user:alice" + mod + "------I:allow", "user:bob" + mod + "------I:allow", "owner@" + full + "------I:allow"}
	priv := []string{"user:carol" + mod + "fd-----:allow", "owner@" + full + "fd-----:allow"}
	store.acl[filepath.Join(eigyo, "private")] = priv
	store.acl[filepath.Join(eigyo, "private/p.txt")] = []string{"user:carol" + mod + "------I:allow"}
	c, m := call("PUT", "/v1/shares/cst/folders/営業", `{"rw":["bob"],"ro":["alice"]}`)
	if c != 200 || m["job"].(map[string]any)["state"] != "done" || m["job"].(map[string]any)["kind"] != "acl" || m["job"].(map[string]any)["path"] != "営業" {
		t.Fatalf("set 営業: %d %v", c, m)
	}
	if j := m["job"].(map[string]any); j["files"] != float64(5) || j["total"] != float64(5) || j["failed"] != float64(0) {
		t.Errorf("job counts: %v", j) // old.txt, carol.txt, sub, private (looked at, left alone), sub/deep.txt
	}
	down := func(fl string) []string {
		return append([]string{"user:bob" + modi + fl + ":allow", "user:alice" + read + fl + ":allow"}, tail(fl)...)
	}
	is("folder", eigyo, down("fd-----")...)
	is("old file: the group's write is gone", filepath.Join(eigyo, "old.txt"), down("------I")...)
	is("explicit entry kept", filepath.Join(eigyo, "carol.txt"), append([]string{"user:carol" + read + "-------:allow"}, down("------I")...)...)
	is("sub-folder", filepath.Join(eigyo, "sub"), down("fd----I")...)
	is("file below", filepath.Join(eigyo, "sub/deep.txt"), down("------I")...)
	is("inheritance off: left alone", filepath.Join(eigyo, "private"), priv...)
	is("and what is below it", filepath.Join(eigyo, "private/p.txt"), "user:carol"+mod+"------I:allow")
	if _, ok := store.acl[filepath.Join(eigyo, "link")]; ok {
		t.Error("a link was changed")
	}

	// a name that is only in a folder's list (the scanner's account) passes the root and Samba
	if c, m := call("PUT", "/v1/shares/cst/folders/共通", `{"rw":["scan"],"ro":["alice","bob"]}`); c != 200 {
		t.Fatalf("共通: %d %v", c, m)
	}
	is("root with a guest", home, append([]string{"user:alice" + mod + "fd-----:allow", "user:bob" + mod + "fd-----:allow", "user:scan" + read + "-------:allow"}, tail("fd-----")...)...)
	if !strings.Contains(conf(), "  valid users = alice bob scan\n") {
		t.Errorf("valid users:\n%s", conf())
	}

	// like the share again: the folder and its files inherit from the root
	if c, m := call("PUT", "/v1/shares/cst/folders/共通", `{"inherit":true}`); c != 200 || m["folder"].(map[string]any)["inherit"] != true {
		t.Fatalf("共通 back: %d %v", c, m)
	}
	is("like the share", filepath.Join(home, "共通"), append([]string{"user:alice" + mod + "fd----I:allow", "user:bob" + mod + "fd----I:allow"}, tail("fd----I")...)...)
	is("root without the guest", home, append([]string{"user:alice" + mod + "fd-----:allow", "user:bob" + mod + "fd-----:allow"}, tail("fd-----")...)...)
	for body, want := range map[string]int{`{"inherit":true,"rw":["alice"]}`: 400, `{}`: 400, `{"rw":["nobody"]}`: 400} {
		if c, m := call("PUT", "/v1/shares/cst/folders/共通", body); c != want {
			t.Errorf("PUT 共通 %s: %d (want %d) %v", body, c, want, m)
		}
	}
	if c, _ := call("PUT", "/v1/shares/cst/folders/nope", `{"rw":["alice"]}`); c != 404 {
		t.Errorf("unknown folder: %d", c)
	}

	// managers: full control everywhere, in the folders with their own list too; only they
	// make things in the root
	os.WriteFile(filepath.Join(home, "rootfile.txt"), []byte("x"), 0o660)
	if c, m := call("PUT", "/v1/shares/cst", `{"rw":["alice","bob","carol"],"managers":["carol"],"root_managers":true}`); c != 200 || m["managers"].([]any)[0] != "carol" || len(m["rw"].([]any)) != 2 || m["root_managers"] != true {
		t.Fatalf("managers: %d %v", c, m)
	}
	is("root with managers", home, append([]string{"user:carol" + full + "fd-----:allow", "user:alice" + read + "-------:allow", "user:alice" + modi + "fdi----:allow",
		"user:bob" + read + "-------:allow", "user:bob" + modi + "fdi----:allow"}, tail("fd-----")...)...)
	is("a file in the root", filepath.Join(home, "rootfile.txt"), append([]string{"user:carol" + full + "------I:allow", "user:alice" + modi + "------I:allow", "user:bob" + modi + "------I:allow"}, tail("------I")...)...)
	is("own list takes the manager", eigyo, append([]string{"user:carol" + full + "fd-----:allow"}, down("fd-----")...)...)
	is("and its files", filepath.Join(eigyo, "old.txt"), append([]string{"user:carol" + full + "------I:allow"}, down("------I")...)...)
	if f := folder("営業"); len(f["rw"].([]any)) != 1 || f["other"] != false || f["inherit"] != false {
		t.Errorf("営業 with a manager: %v", f) // the manager is not shown as one of the list
	}
	// a folder for the managers only
	if c, m := call("PUT", "/v1/shares/cst/folders/経理", `{}`); c != 200 {
		t.Errorf("managers only: %d %v", c, m)
	}
	is("managers only", keiri, append([]string{"user:carol" + full + "fd-----:allow"}, tail("fd-----")...)...)
	// the manager goes: out of the folders too
	if c, m := call("PUT", "/v1/shares/cst", `{"rw":["alice","bob"],"managers":[]}`); c != 200 || m["managers"] != nil {
		t.Fatalf("no managers: %d %v", c, m)
	}
	is("own list without the manager", eigyo, down("fd-----")...)
	is("its files", filepath.Join(eigyo, "old.txt"), down("------I")...)

	// what Windows set and this page cannot show as a list
	store.acl[keiri] = []string{"user:bob:r-x---a-R-c--s:-d-----:allow", "user:alice:rwxpDdaARWcCos:fd-----:allow", "user:carol" + mod + "fd-----:deny"}
	if f := folder("経理"); f["other"] != true || f["inherit"] != false || len(f["acl"].([]any)) != 3 {
		t.Errorf("special entries: %v", f)
	}

	// only an empty folder is deleted
	if c, _ := call("DELETE", "/v1/shares/cst/folders/営業", ""); c != 409 {
		t.Errorf("delete a full folder: %d", c)
	}
	if c, _ := call("DELETE", "/v1/shares/cst/folders/共通", ""); c != 200 {
		t.Errorf("delete an empty folder: %d", c)
	}
	if _, err := os.Stat(filepath.Join(home, "共通")); err == nil {
		t.Error("共通 is still there")
	}
	// apply the permissions again
	store.acl[filepath.Join(home, "rootfile.txt")] = slices.Clone(oldFileACL)
	if c, m := call("POST", "/v1/shares/cst/acl", ""); c != 200 || m["state"] != "done" {
		t.Fatalf("again: %d %v", c, m)
	}
	is("again", filepath.Join(home, "rootfile.txt"), append([]string{"user:alice" + modi + "------I:allow", "user:bob" + modi + "------I:allow"}, tail("------I")...)...)
}

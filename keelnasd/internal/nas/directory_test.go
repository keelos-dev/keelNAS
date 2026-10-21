package nas

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"keelnas/internal/sys"
)

// dirWorld adds system groups and Samba's password database to the test world.
type dirWorld struct {
	groups  map[string]PwGroup
	pdb     map[string]pdbEntry
	pdbUID  map[string]int
	imports []string // contents of every pdbedit -i
}

func newDirServer(t *testing.T) (*Server, *world, *sys.Fake, *dirWorld, func(method, path, body string) (int, map[string]any)) {
	s, w, f, ts := newTestServer(t)
	d := &dirWorld{groups: map[string]PwGroup{"keelnas": {"keelnas", 2000, nil}, "staff": {"staff", 20, nil}},
		pdb: map[string]pdbEntry{}, pdbUID: map[string]int{}}
	w.extra = func(line string, a []string) (string, error, bool) {
		switch {
		case line == "pw groupshow -a":
			var b strings.Builder
			for _, g := range d.groups {
				fmt.Fprintf(&b, "%s:*:%d:%s\n", g.Name, g.GID, strings.Join(g.Members, ","))
			}
			return b.String(), nil, true
		case strings.HasPrefix(line, "pw groupadd -n "):
			gid, _ := strconv.Atoi(a[5])
			g := PwGroup{Name: a[3], GID: gid}
			if len(a) > 7 && a[6] == "-M" {
				g.Members = strings.Split(a[7], ",")
			}
			d.groups[a[3]] = g
			return "", nil, true
		case strings.HasPrefix(line, "pw groupmod -n "):
			g := d.groups[a[3]]
			switch a[4] {
			case "-M":
				g.Members = strings.Split(a[5], ",")
			case "-d":
				g.Members = nil
			case "-g":
				g.GID, _ = strconv.Atoi(a[5])
			}
			d.groups[a[3]] = g
			return "", nil, true
		case strings.HasPrefix(line, "pw groupdel -n "):
			delete(d.groups, a[3])
			return "", nil, true
		case strings.HasPrefix(line, "pw usermod -n ") && a[4] == "-u":
			uid, _ := strconv.Atoi(a[5])
			u := w.users[a[3]]
			u.UID = uid
			w.users[a[3]] = u
			return "", nil, true
		case line == "pdbedit -s "+SmbConf+" -L -w":
			var b strings.Builder
			for n, e := range d.pdb {
				fl := "[U          ]"
				if e.Disabled {
					fl = "[DU         ]"
				}
				fmt.Fprintf(&b, "%s:%d:XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX:%s:%s:LCT-00000000:\n", n, d.pdbUID[n], e.NT, fl)
			}
			return b.String(), nil, true
		case line == "pdbedit -s "+SmbConf+" -i smbpasswd:"+syncPDB:
			body := string(f.Files[syncPDB])
			d.imports = append(d.imports, body)
			for n, e := range ParsePdb(body) {
				if _, ok := w.users[n]; !ok {
					return "no such user " + n, sys.ErrFail, true
				}
				d.pdb[n] = e
			}
			return "", nil, true
		case strings.HasPrefix(line, "pdbedit -s "+SmbConf+" -x -u "):
			delete(d.pdb, a[len(a)-1])
			return "", nil, true
		}
		return "", nil, false
	}
	call := func(method, path, body string) (int, map[string]any) { return do(t, ts, method, path, body) }
	return s, w, f, d, call
}

const hashA = "8846f7eaee8fb117ad06bdd830b7586c"
const hashA2 = "0123456789abcdef0123456789abcdef"

// syncBody is a sync as keel sends it (keelAuth docs/02 section 3.5 plus source and name).
func syncBody(rev int, users, groups string) string {
	return fmt.Sprintf(`{"source":"auth1","name":"Office","domain":"auth.example.lan","revision":%d,"users":[%s],"groups":[%s]}`, rev, users, groups)
}

func user(name string, uid int, enabled bool, nt string, groups ...string) string {
	g, _ := strconvQuote(groups)
	return fmt.Sprintf(`{"name":%q,"display_name":"%s","uid":%d,"enabled":%v,"nt_hash":%q,"password_set_at":"","must_change":false,"groups":%s}`,
		name, strings.ToUpper(name[:1])+name[1:], uid, enabled, nt, g)
}

func group(name string, gid int, members ...string) string {
	m, _ := strconvQuote(members)
	return fmt.Sprintf(`{"name":%q,"gid":%d,"members":%s}`, name, gid, m)
}

func strconvQuote(a []string) (string, error) {
	q := make([]string, len(a))
	for i, s := range a {
		q[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(q, ",") + "]", nil
}

func TestDirectorySync(t *testing.T) {
	s, w, f, d, call := newDirServer(t)
	if c, m := call("GET", "/v1/directory", ""); c != 200 || m["connected"] != false {
		t.Fatalf("not connected: %d %v", c, m)
	}
	// storage and a local user first
	if c, m := call("POST", "/v1/storage", `{"disks":["vtbd1"]}`); c != 200 {
		t.Fatalf("storage %d %v", c, m)
	}
	if c, m := call("POST", "/v1/users", `{"name":"eve","password":"pw"}`); c != 200 {
		t.Fatalf("local user %d %v", c, m)
	}
	if c, m := call("PUT", "/v1/directory", `{"source":"auth1","users":[{"name":"x","uid":70000,"enabled":true,"nt_hash":"zz","groups":[]}],"groups":[]}`); c != 400 {
		t.Errorf("bad hash: %d %v", c, m)
	}
	if c, _ := call("PUT", "/v1/directory", `{"users":[],"groups":[]}`); c != 400 {
		t.Errorf("no source: %d", c)
	}
	body := syncBody(5, strings.Join([]string{
		user("alice", 70001, true, hashA, "office"),
		user("bob", 70002, true, "", "office"),   // no NT hash (an upstream user, D5)
		user("carol", 70003, false, hashA2),      // disabled
		user("dave", 100, true, hashA, "office"), // uid in the system's range
		user("eve", 70005, true, hashA),          // a local user has the name
		user("keel", 70006, true, hashA),         // a system account
		user("Bad.Name", 70007, true, hashA),
	}, ","), strings.Join([]string{
		group("office", 80000, "alice", "bob", "dave"),
		group("wheel", 0, "alice"),
		group("staff", 80001, "alice"),
	}, ","))
	c, m := call("PUT", "/v1/directory", body)
	if c != 200 || m["ok"] != true || m["users"] != float64(3) {
		t.Fatalf("sync: %d %v", c, m)
	}
	// system users with keelAuth's uids, in keelNAS's group
	for n, uid := range map[string]int{"alice": 70001, "bob": 70002, "carol": 70003} {
		if u := w.users[n]; u.UID != uid || u.GID != GroupID {
			t.Errorf("system user %s: %+v", n, u)
		}
	}
	if _, ok := w.users["dave"]; ok {
		t.Error("dave (bad uid) was made")
	}
	if g := d.groups["office"]; g.GID != 80000 || !slices.Equal(g.Members, []string{"alice", "bob"}) {
		t.Errorf("group office: %+v", g)
	}
	if g := d.groups["staff"]; g.GID != 20 {
		t.Errorf("the system's staff was changed: %+v", g)
	}
	// Samba: alice with her hash, bob disabled with a hash nobody knows, carol disabled
	if e := d.pdb["alice"]; e.NT != strings.ToUpper(hashA) || e.Disabled {
		t.Errorf("alice in samba: %+v", e)
	}
	if e := d.pdb["bob"]; !e.Disabled || len(e.NT) != 32 {
		t.Errorf("bob in samba: %+v", e)
	}
	if e := d.pdb["carol"]; !e.Disabled || e.NT != strings.ToUpper(hashA2) {
		t.Errorf("carol in samba: %+v", e)
	}
	for _, c := range f.Calls {
		if strings.Contains(strings.ToUpper(c), strings.ToUpper(hashA)) {
			t.Errorf("a hash on a command line: %s", c)
		}
	}
	if !f.Ran("rm -f " + syncPDB) {
		t.Error("the temporary smbpasswd file stays")
	}
	// what the page sees
	_, m = call("GET", "/v1/users", "")
	if us := m["users"].([]any); len(us) != 1 {
		t.Errorf("local users: %v", us)
	}
	problems := map[string]string{}
	for _, x := range m["synced"].([]any) {
		u := x.(map[string]any)
		p, _ := u["problem"].(string)
		problems[u["name"].(string)] = p
		if _, ok := u["nt_hash"]; ok {
			t.Error("a hash in GET /v1/users")
		}
	}
	want := map[string]string{"alice": "", "bob": "", "carol": "", "dave": "bad_uid", "eve": "local_user", "keel": "system_account", "Bad.Name": "bad_name"}
	for n, p := range want {
		if problems[n] != p {
			t.Errorf("problem of %s = %q, want %q", n, problems[n], p)
		}
	}
	gp := map[string]string{}
	for _, x := range m["groups"].([]any) {
		g := x.(map[string]any)
		p, _ := g["problem"].(string)
		gp[g["name"].(string)] = p
	}
	if gp["office"] != "" || gp["wheel"] != "bad_gid" || gp["staff"] != "system_group" {
		t.Errorf("group problems %v", gp)
	}
	if dir := m["directory"].(map[string]any); dir["source"] != "auth1" || dir["revision"] != float64(5) {
		t.Errorf("directory %v", dir)
	}
	// shares take keelAuth's users and groups
	if c, m := call("POST", "/v1/shares", `{"name":"docs","rw":["alice","@office"],"ro":["eve"]}`); c != 200 {
		t.Fatalf("share with synced users: %d %v", c, m)
	}
	if c, _ := call("POST", "/v1/shares", `{"name":"x","rw":["@wheel"]}`); c != 400 {
		t.Errorf("a group with a problem: %d", c)
	}
	if c, _ := call("POST", "/v1/shares", `{"name":"y","rw":["dave"]}`); c != 400 {
		t.Errorf("a user with a problem: %d", c)
	}
	conf := string(f.Files[SmbConf])
	if !strings.Contains(conf, "valid users = +office alice eve\n") || !strings.Contains(conf, "write list = +office alice\n") {
		t.Errorf("smb4.conf:\n%s", conf)
	}
	// keelAuth's users are not changed here
	if c, _ := call("PUT", "/v1/users/alice", `{"password":"x"}`); c != 409 {
		t.Errorf("password of a synced user: %d", c)
	}
	if c, _ := call("DELETE", "/v1/users/alice", ""); c != 409 {
		t.Errorf("delete a synced user: %d", c)
	}
	if c, _ := call("POST", "/v1/users", `{"name":"alice","password":"x"}`); c != 409 {
		t.Errorf("local user with a synced name: %d", c)
	}

	// the same again: nothing written to Samba
	n := len(d.imports)
	if c, m := call("PUT", "/v1/directory", body); c != 200 || m["ok"] != true {
		t.Fatalf("again: %d %v", c, m)
	}
	if len(d.imports) != n {
		t.Errorf("an unchanged sync wrote %q", d.imports[n:])
	}
	// alice's password changes, carol is enabled: only those two lines
	body2 := strings.Replace(body, user("alice", 70001, true, hashA, "office"), user("alice", 70001, true, hashA2, "office"), 1)
	body2 = strings.Replace(body2, user("carol", 70003, false, hashA2), user("carol", 70003, true, hashA2), 1)
	call("PUT", "/v1/directory", body2)
	if len(d.imports) != n+1 || strings.Count(d.imports[n], "\n") != 2 {
		t.Fatalf("imports after a change: %q", d.imports[n:])
	}
	if e := d.pdb["alice"]; e.NT != strings.ToUpper(hashA2) || e.Disabled {
		t.Errorf("alice's new hash: %+v", e)
	}
	if e := d.pdb["carol"]; e.Disabled {
		t.Errorf("carol enabled: %+v", e)
	}
	// alice disabled in keelAuth
	body3 := strings.Replace(body2, user("alice", 70001, true, hashA2, "office"), user("alice", 70001, false, hashA2, "office"), 1)
	call("PUT", "/v1/directory", body3)
	if e := d.pdb["alice"]; !e.Disabled || e.NT != strings.ToUpper(hashA2) {
		t.Errorf("alice disabled: %+v", e)
	}
	if st := s.State(); !slices.Contains(st.Share("docs").RW, "alice") {
		t.Error("a disabled user left the share")
	}
	// bob and the group office are deleted in keelAuth
	body4 := syncBody(9, strings.Join([]string{user("alice", 70001, true, hashA2), user("carol", 70003, true, hashA2)}, ","), "")
	if c, m := call("PUT", "/v1/directory", body4); c != 200 || m["removed"] != float64(1) {
		t.Fatalf("bob gone: %d %v", c, m)
	}
	if _, ok := w.users["bob"]; ok {
		t.Error("bob's system user stays")
	}
	if _, ok := d.pdb["bob"]; ok {
		t.Error("bob's Samba account stays")
	}
	if _, ok := d.groups["office"]; ok {
		t.Error("group office stays")
	}
	if sh := s.State().Share("docs"); !slices.Equal(sh.RW, []string{"alice"}) {
		t.Errorf("share docs after the group went: %+v", sh)
	}
	// a user made again in keelAuth gets a new uid: the system user follows
	body5 := syncBody(10, strings.Join([]string{user("alice", 70011, true, hashA2), user("carol", 70003, true, hashA2)}, ","), "")
	call("PUT", "/v1/directory", body5)
	if u := w.users["alice"]; u.UID != 70011 {
		t.Errorf("alice's new uid: %+v", u)
	}
	// the copy in the pools has the directory, without hashes
	st := s.State()
	if st.Directory == nil || st.Directory.Revision != 10 || st.Fresh() {
		t.Errorf("state %+v", st.Directory)
	}
	if strings.Contains(string(st.Marshal()), hashA2) {
		t.Error("an NT hash in state.json")
	}
}

func TestDirectoryDisconnect(t *testing.T) {
	for _, mode := range []string{"keep", "delete"} {
		t.Run(mode, func(t *testing.T) {
			s, w, _, d, call := newDirServer(t)
			call("POST", "/v1/storage", `{"disks":["vtbd1"]}`)
			call("POST", "/v1/users", `{"name":"eve","password":"pw"}`)
			call("PUT", "/v1/directory", syncBody(1, strings.Join([]string{
				user("alice", 70001, true, hashA, "office"),
				user("bob", 70002, false, hashA, "office"),
				user("carol", 70003, true, "", "office"),
			}, ","), group("office", 80000, "alice", "bob", "carol")))
			if c, m := call("POST", "/v1/shares", `{"name":"docs","rw":["@office"],"ro":["alice","eve"]}`); c != 200 {
				t.Fatalf("share %d %v", c, m)
			}
			if c, _ := call("DELETE", "/v1/directory", ""); c != 400 {
				t.Errorf("no mode: %d", c)
			}
			c, m := call("DELETE", "/v1/directory?users="+mode, "")
			if c != 200 || m["ok"] != true {
				t.Fatalf("disconnect: %d %v", c, m)
			}
			st := s.State()
			if st.Directory != nil {
				t.Error("still connected")
			}
			if _, ok := d.groups["office"]; ok {
				t.Error("group office stays")
			}
			for _, n := range []string{"bob", "carol"} {
				if _, ok := w.users[n]; ok {
					t.Errorf("%s stays", n)
				}
				if _, ok := d.pdb[n]; ok {
					t.Errorf("%s stays in Samba", n)
				}
			}
			sh := st.Share("docs")
			if mode == "keep" {
				// alice (enabled, with a password) is local now and keeps read-write through the group
				if u := st.User("alice"); u == nil || u.UID != 70001 {
					t.Errorf("alice not local: %+v", st.Users)
				}
				if _, ok := d.pdb["alice"]; !ok {
					t.Error("alice's password is gone")
				}
				if !slices.Equal(sh.RW, []string{"alice"}) || !slices.Equal(sh.RO, []string{"eve"}) {
					t.Errorf("share %+v", sh)
				}
				if !slices.Equal(m["kept"].([]any), []any{"alice"}) {
					t.Errorf("kept %v", m["kept"])
				}
				// a new local user still gets a uid from 2001 up
				if c, m := call("POST", "/v1/users", `{"name":"frank","password":"pw"}`); c != 200 || m["uid"] != float64(2002) {
					t.Errorf("next local uid: %v", m)
				}
			} else {
				if st.User("alice") != nil || w.users["alice"].Name != "" {
					t.Error("alice stays")
				}
				if len(sh.RW) != 0 || !slices.Equal(sh.RO, []string{"eve"}) {
					t.Errorf("share %+v", sh)
				}
			}
			if c, _ := call("DELETE", "/v1/directory?users="+mode, ""); c != 404 {
				t.Errorf("second disconnect: %d", c)
			}
		})
	}
}

func TestDirectoryHelpers(t *testing.T) {
	st := &State{Directory: &Directory{Users: []DirUser{{Name: "a", Groups: []string{"g"}}}, Groups: []DirGroup{{Name: "g", Members: []string{"a"}}}}}
	c := st.Clone()
	c.Directory.Users[0].Groups[0] = "x"
	c.Directory.Groups[0].Members[0] = "x"
	if st.Directory.Users[0].Groups[0] != "g" || st.Directory.Groups[0].Members[0] != "a" {
		t.Error("Clone shares the directory's slices")
	}
	if (&State{}).Clone().Directory != nil {
		t.Error("clone of no directory")
	}
	if st.Fresh() {
		t.Error("a connected state is not fresh")
	}
	g := ParsePwGroups("wheel:*:0:root\nkeelnas:*:2000:\noffice:*:80000:alice,bob\n")
	if len(g) != 3 || g["office"].GID != 80000 || !slices.Equal(g["office"].Members, []string{"alice", "bob"}) || len(g["keelnas"].Members) != 0 {
		t.Errorf("groups %+v", g)
	}
	p := ParsePdb("alice:70001:XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX:8846F7EAEE8FB117AD06BDD830B7586C:[U          ]:LCT-6AC0BFA9:\n" +
		"bob:70002:XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX:NO PASSWORDXXXXXXXXXXXXXXXXXXXXX:[DU         ]:LCT-00000000:\n")
	if p["alice"].NT != "8846F7EAEE8FB117AD06BDD830B7586C" || p["alice"].Disabled || p["bob"].NT != "" || !p["bob"].Disabled {
		t.Errorf("pdb %+v", p)
	}
	if l := pdbLine("a", 70001, "AB", true, 0x10); l != "a:70001:XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX:AB:[DU         ]:LCT-00000010:\n" {
		t.Errorf("line %q", l)
	}
	// a share nobody may use any more is switched off, not opened to everybody
	conf := SmbConfig(&State{Shares: []Share{{Name: "empty", Dataset: "tank/shares/empty", RW: []string{}, RO: []string{}}}},
		map[string]string{"empty": "/shares/empty"})
	if !strings.Contains(conf, "[empty]\n  path = /shares/empty\n  available = no\n") || strings.Contains(conf, "valid users = \n") {
		t.Errorf("empty share:\n%s", conf)
	}
}

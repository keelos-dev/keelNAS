package nas

import (
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"keelnas/internal/sys"
)

var auditNow = time.Date(2026, 11, 18, 0, 40, 30, 0, time.UTC)

const auditRoot = "/shares/docs"

func line(ts, msg string) string { return ts + " keelnas smbd_audit[4242]: " + msg }

func TestParseAuditLine(t *testing.T) {
	roots := map[string]string{"docs": auditRoot}
	cases := []struct {
		in   string
		want *AuditRecord // nil: dropped
	}{
		// reading a file's data
		{line("Nov 18 00:31:08", "alice|192.0.2.5|docs|create_file|ok|0x120089|file|open|/shares/docs/人事/評価.xlsx"),
			&AuditRecord{Time: "2026-11-18T00:31:08Z", User: "alice", IP: "192.0.2.5", Share: "docs", Act: "read", Path: "人事/評価.xlsx"}},
		// only the attributes (Explorer's listing): dropped
		{line("Nov 18 00:31:08", "alice|192.0.2.5|docs|create_file|ok|0x80|file|open|/shares/docs/a.txt"), nil},
		// writing; a relative path works too
		{line("Nov 18 00:31:09", "bob|192.0.2.6|docs|create_file|ok|0x12019f|file|open|a/b.docx"),
			&AuditRecord{Time: "2026-11-18T00:31:09Z", User: "bob", IP: "192.0.2.6", Share: "docs", Act: "write", Path: "a/b.docx"}},
		{line("Nov 18 00:31:10", "bob|192.0.2.6|docs|create_file|ok|0x12019f|file|create|/shares/docs/new.txt"),
			&AuditRecord{Time: "2026-11-18T00:31:10Z", User: "bob", IP: "192.0.2.6", Share: "docs", Act: "create", Path: "new.txt"}},
		{line("Nov 18 00:31:10", "bob|192.0.2.6|docs|create_file|ok|0x80|file|overwrite_if|/shares/docs/x.txt"),
			&AuditRecord{Time: "2026-11-18T00:31:10Z", User: "bob", IP: "192.0.2.6", Share: "docs", Act: "write", Path: "x.txt"}},
		// generic read
		{line("Nov 18 00:31:11", "bob|192.0.2.6|docs|create_file|ok|0x80000000|file|open|/shares/docs/g.pdf"),
			&AuditRecord{Time: "2026-11-18T00:31:11Z", User: "bob", IP: "192.0.2.6", Share: "docs", Act: "read", Path: "g.pdf"}},
		// entering a folder, noise, a stream: dropped
		{line("Nov 18 00:31:12", "alice|192.0.2.5|docs|create_file|ok|0x100081|dir|open|/shares/docs/人事"), nil},
		{line("Nov 18 00:31:12", "alice|192.0.2.5|docs|create_file|ok|0x120089|file|open|/shares/docs/x/desktop.ini"), nil},
		{line("Nov 18 00:31:12", "alice|192.0.2.5|docs|create_file|ok|0x120089|file|open|/shares/docs/x/Thumbs.db"), nil},
		{line("Nov 18 00:31:12", "alice|192.0.2.5|docs|create_file|ok|0x120089|file|open|/shares/docs/f.exe:Zone.Identifier"), nil},
		// a missing file (Explorer looks for many): dropped; a refusal: kept
		{line("Nov 18 00:31:13", "alice|192.0.2.5|docs|create_file|fail (No such file or directory)|0x120089|file|open|/shares/docs/nope"), nil},
		{line("Nov 18 00:31:13", "carol|192.0.2.7|docs|create_file|fail (Permission denied)|0x100081|dir|open|/shares/docs/人事"),
			&AuditRecord{Time: "2026-11-18T00:31:13Z", User: "carol", IP: "192.0.2.7", Share: "docs", Act: "denied", Path: "人事", Detail: "open: Permission denied"}},
		// rename, delete, mkdir
		{line("Nov 18 00:31:14", "bob|192.0.2.6|docs|renameat|ok|/shares/docs/a.txt|/shares/docs/old/a.txt"),
			&AuditRecord{Time: "2026-11-18T00:31:14Z", User: "bob", IP: "192.0.2.6", Share: "docs", Act: "rename", Path: "a.txt", To: "old/a.txt"}},
		{line("Nov 18 00:31:15", "bob|192.0.2.6|docs|unlinkat|ok|/shares/docs/old/a.txt"),
			&AuditRecord{Time: "2026-11-18T00:31:15Z", User: "bob", IP: "192.0.2.6", Share: "docs", Act: "delete", Path: "old/a.txt"}},
		{line("Nov 18 00:31:16", "bob|192.0.2.6|docs|mkdirat|ok|/shares/docs/新しいフォルダー"),
			&AuditRecord{Time: "2026-11-18T00:31:16Z", User: "bob", IP: "192.0.2.6", Share: "docs", Act: "mkdir", Path: "新しいフォルダー"}},
		{line("Nov 18 00:31:17", "carol|192.0.2.7|docs|unlinkat|fail (Permission denied)|/shares/docs/b.txt"),
			&AuditRecord{Time: "2026-11-18T00:31:17Z", User: "carol", IP: "192.0.2.7", Share: "docs", Act: "denied", Path: "b.txt", Detail: "delete: Permission denied"}},
		// connecting to the share
		{line("Nov 18 00:31:18", "alice|192.0.2.5|docs|connect|ok|docs"),
			&AuditRecord{Time: "2026-11-18T00:31:18Z", User: "alice", IP: "192.0.2.5", Share: "docs", Act: "connect"}},
		// RFC 3339 time stamps (syslogd -O rfc5424-ish file output)
		{"2026-11-18T00:31:19.123456+00:00 keelnas smbd_audit[1]: alice|192.0.2.5|docs|unlinkat|ok|/shares/docs/z",
			&AuditRecord{Time: "2026-11-18T00:31:19Z", User: "alice", IP: "192.0.2.5", Share: "docs", Act: "delete", Path: "z"}},
		// December's lines read on the 1st of January belong to the year before
		// (checked separately below), other programs' lines and junk: dropped
		{line("Nov 18 00:31:20", "kernel noise"), nil},
		{"Nov 18 00:31:20 keelnas sshd[1]: Accepted publickey", nil},
		{"garbage", nil},
	}
	for _, c := range cases {
		got, ok := parseAuditLine(c.in, auditNow, roots)
		switch {
		case c.want == nil && ok:
			t.Errorf("%q: kept %+v", c.in, got)
		case c.want != nil && !ok:
			t.Errorf("%q: dropped", c.in)
		case c.want != nil && got != *c.want:
			t.Errorf("%q:\n got  %+v\n want %+v", c.in, got, *c.want)
		}
	}
	jan := time.Date(2027, 1, 1, 0, 0, 30, 0, time.UTC)
	r, ok := parseAuditLine(line("Dec 31 23:59:58", "a|192.0.2.1|docs|unlinkat|ok|x"), jan, roots)
	if !ok || r.Time != "2026-12-31T23:59:58Z" {
		t.Errorf("year change: %v %+v", ok, r)
	}
}

func TestAuditDedup(t *testing.T) {
	rec := func(ts, act, p string) AuditRecord {
		return AuditRecord{Time: ts, User: "alice", IP: "192.0.2.5", Share: "docs", Act: act, Path: p}
	}
	seen := map[string]time.Time{}
	got := auditDedup([]AuditRecord{
		rec("2026-11-18T00:31:00Z", "read", "a"),
		rec("2026-11-18T00:31:05Z", "read", "a"), // the same look
		rec("2026-11-18T00:31:06Z", "write", "a"),
		rec("2026-11-18T00:31:07Z", "read", "b"),
	}, seen)
	if len(got) != 3 {
		t.Errorf("first batch: %+v", got)
	}
	// the next minute's batch: a within a minute of the last is still a repeat, later is not
	got = auditDedup([]AuditRecord{
		rec("2026-11-18T00:31:50Z", "read", "a"),
		rec("2026-11-18T00:32:10Z", "read", "a"),
	}, seen)
	if len(got) != 1 || got[0].Time != "2026-11-18T00:32:10Z" {
		t.Errorf("second batch: %+v", got)
	}
}

func TestSmbConfigAudit(t *testing.T) {
	st := &State{Shares: []Share{
		{Name: "docs", Dataset: "tank/shares/docs", RW: []string{"alice"}, Audit: true},
		{Name: "plain", Dataset: "tank/shares/plain", RW: []string{"alice"}},
	}}
	got := SmbConfig(st, map[string]string{"docs": "/shares/docs", "plain": "/shares/plain"}, nil)
	for _, want := range []string{
		"[docs]\n  path = /shares/docs\n  valid users = alice\n  read only = no\n  hide unreadable = yes\n  vfs objects = full_audit shadow_copy2 zfsacl\n  full_audit:prefix = %u|%I|%S\n",
		"  full_audit:success = connect create_file renameat unlinkat mkdirat\n",
		"  full_audit:facility = LOCAL5\n  full_audit:priority = INFO\n",
		"[plain]\n  path = /shares/plain\n  valid users = alice\n  read only = no\n  hide unreadable = yes\n  vfs objects = shadow_copy2 zfsacl\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("smb4.conf lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "full_audit:prefix") != 1 {
		t.Errorf("full_audit outside the audited share:\n%s", got)
	}
}

// auditServer: a server whose pool tank is mounted on a temporary directory and whose
// syslogd file is another one.
func auditServer(t *testing.T) (*Server, *sys.Fake, string) {
	t.Helper()
	dir := t.TempDir()
	pool := filepath.Join(dir, "tank")
	os.MkdirAll(pool, 0o755)
	old := auditLog
	auditLog = filepath.Join(dir, "keelnas-audit.log")
	t.Cleanup(func() { auditLog = old })
	f := sys.NewFake()
	f.Handle = func(cmd, _ string) (string, error, bool) {
		if cmd == "zfs get -H -o value mountpoint tank" {
			return pool + "\n", nil, true
		}
		return "", nil, false
	}
	now := auditNow
	s := &Server{Sys: f, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now },
		st: &State{Shares: []Share{
			{Name: "docs", Dataset: "tank/shares/docs", RW: []string{"alice"}, Audit: true},
			{Name: "plain", Dataset: "tank/shares/plain", RW: []string{"alice"}},
		}}}
	return s, f, pool
}

func TestAuditTickAndQuery(t *testing.T) {
	s, f, pool := auditServer(t)
	old := time.Local
	time.Local = time.UTC // syslogd's clock (keelNAS has no time zone of its own)
	t.Cleanup(func() { time.Local = old })

	// The first tick sets syslogd up: its file, the empty log, a reload.
	s.auditTick(auditNow)
	if b, _ := f.ReadFile(auditSyslogConf); !strings.Contains(string(b), "local5.info\t"+auditLog) {
		t.Errorf("syslog.d: %q", b)
	}
	if _, err := os.Stat(auditLog); err != nil {
		t.Errorf("the log file is not made: %v", err)
	}
	if !f.Ran("service syslogd reload") {
		t.Errorf("no reload: %v", f.Calls)
	}
	f.Reset()
	s.auditTick(auditNow)
	if f.Ran("service syslogd reload") {
		t.Error("a reload with nothing to do")
	}

	lines := []string{
		line("Nov 17 23:59:59", "alice|192.0.2.5|docs|create_file|ok|0x120089|file|open|/shares/docs/昨日.txt"),
		line("Nov 18 00:31:08", "alice|192.0.2.5|docs|create_file|ok|0x120089|file|open|/shares/docs/人事/評価.xlsx"),
		line("Nov 18 00:31:09", "alice|192.0.2.5|docs|create_file|ok|0x120089|file|open|/shares/docs/人事/評価.xlsx"),
		line("Nov 18 00:31:10", "bob|192.0.2.6|docs|unlinkat|ok|/shares/docs/old.txt"),
		line("Nov 18 00:31:11", "alice|192.0.2.5|docs|create_file|ok|0x80|file|open|/shares/docs/a.txt"),
		line("Nov 18 00:31:12", "alice|192.0.2.5|gone|unlinkat|ok|/shares/gone/x"),
	}
	os.WriteFile(auditLog, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	// this tick sets the lines aside (syslogd reopens), the next one stores them
	s.auditTick(auditNow)
	if _, err := os.Stat(auditLog + ".work"); err != nil || !f.Ran("service syslogd reload") {
		t.Fatalf("not set aside: %v %v", err, f.Calls)
	}
	if b, _ := os.ReadFile(auditLog); len(b) != 0 {
		t.Errorf("the new log is not empty: %q", b)
	}
	s.auditTick(auditNow.Add(time.Minute))
	if _, err := os.Stat(auditLog + ".work"); err == nil {
		t.Error("the set-aside lines stay")
	}
	dir := filepath.Join(pool, ".keelnas", "audit", "docs")
	b, err := os.ReadFile(filepath.Join(dir, "2026-11-18.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n != 2 { // the repeat and the attribute look dropped
		t.Errorf("2026-11-18: %d records:\n%s", n, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-11-17.jsonl")); err != nil {
		t.Errorf("yesterday's line not in its day: %v", err)
	}

	// The query: newest first, filters, limit.
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	get := func(q string) (int, AuditQuery) {
		resp, err := ts.Client().Get(ts.URL + "/v1/audit?" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var a AuditQuery
		json.NewDecoder(resp.Body).Decode(&a)
		return resp.StatusCode, a
	}
	c, a := get("share=docs")
	if c != 200 || len(a.Records) != 3 || a.Records[0].Act != "delete" || a.Records[2].Path != "昨日.txt" || a.From != "2026-11-12" {
		t.Errorf("all: %d %+v", c, a)
	}
	if _, a = get("share=docs&user=bob"); len(a.Records) != 1 || a.Records[0].User != "bob" {
		t.Errorf("user: %+v", a)
	}
	if _, a = get("share=docs&q=" + "%E4%BA%BA%E4%BA%8B"); len(a.Records) != 1 || a.Records[0].Path != "人事/評価.xlsx" { // 人事
		t.Errorf("q: %+v", a)
	}
	if _, a = get("share=docs&act=read&from=2026-11-18&to=2026-11-18"); len(a.Records) != 1 {
		t.Errorf("act and one day: %+v", a)
	}
	if _, a = get("share=docs&limit=2"); len(a.Records) != 2 || !a.Truncated {
		t.Errorf("limit: %+v", a)
	}
	if _, a = get("share=plain"); len(a.Records) != 0 {
		t.Errorf("plain: %+v", a)
	}
	for _, q := range []string{"share=nope", "share=docs&from=2026-11-19&to=2026-11-18", "share=docs&from=2024-01-01", "share=docs&limit=0", "share=docs&to=x"} {
		if c, _ := get(q); c == 200 {
			t.Errorf("%s: 200", q)
		}
	}

	// Pruning: a day file older than auditKeepDays goes, newer ones stay.
	os.WriteFile(filepath.Join(dir, "2025-11-01.jsonl"), []byte("{}\n"), 0o600)
	s.auditTick(time.Date(2026, 11, 19, 4, 10, 0, 0, time.UTC))
	if _, err := os.Stat(filepath.Join(dir, "2025-11-01.jsonl")); err == nil {
		t.Error("an old day file stays")
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-11-17.jsonl")); err != nil {
		t.Error("a recent day file is gone")
	}
}

func TestAuditSyslogOnlyWhenOn(t *testing.T) {
	s, f, _ := auditServer(t)
	s.st.Shares[0].Audit = false
	s.auditTick(auditNow)
	if _, err := f.ReadFile(auditSyslogConf); err == nil || f.Ran("service syslogd reload") {
		t.Errorf("syslogd touched without an audited share: %v", f.Calls)
	}
}

func TestAPIShareAudit(t *testing.T) {
	old := auditLog
	auditLog = filepath.Join(t.TempDir(), "keelnas-audit.log")
	t.Cleanup(func() { auditLog = old })
	s, _, f, ts := newTestServer(t)
	if c, m := do(t, ts, "POST", "/v1/storage", `{"disks":["vtbd1"]}`); c != 200 {
		t.Fatalf("storage: %d %v", c, m)
	}
	do(t, ts, "POST", "/v1/users", `{"name":"alice","password":"s3cret"}`)
	if c, m := do(t, ts, "POST", "/v1/shares", `{"name":"docs","pool":"tank","rw":["alice"],"audit":true}`); c != 200 || m["audit"] != true {
		t.Fatalf("new share with audit: %d %v", c, m)
	}
	if !s.State().Share("docs").Audit {
		t.Error("audit not in the state")
	}
	if b, _ := f.ReadFile(SmbConf); !strings.Contains(string(b), "vfs objects = full_audit") {
		t.Errorf("smb4.conf:\n%s", b)
	}
	if b, _ := f.ReadFile(auditSyslogConf); len(b) == 0 {
		t.Error("syslogd not set up by apply")
	}
	// a PUT without "audit" keeps it, with false turns it off
	if c, m := do(t, ts, "PUT", "/v1/shares/docs", `{"rw":["alice"],"ro":[]}`); c != 200 || m["audit"] != true {
		t.Errorf("PUT without audit: %d %v", c, m)
	}
	if c, m := do(t, ts, "PUT", "/v1/shares/docs", `{"rw":["alice"],"ro":[],"audit":false}`); c != 200 || m["audit"] == true {
		t.Errorf("PUT audit off: %d %v", c, m)
	}
	if b, _ := f.ReadFile(SmbConf); strings.Contains(string(b), "full_audit") {
		t.Errorf("smb4.conf still audits:\n%s", b)
	}
}

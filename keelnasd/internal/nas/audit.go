package nas

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Access records (docs/04 section 18): for a share with "audit" on, Samba's vfs_full_audit
// writes every open, rename, delete and new folder to syslog (facility LOCAL5, priority INFO);
// syslogd puts them in auditLog; once a minute keelnasd takes that file, keeps what says who
// did what to which file (the opens that only look at a file's attributes, Explorer's
// desktop.ini and thumbnails and the same thing again within a minute are dropped) and appends
// it to <pool>/.keelnas/audit/<share>/<UTC date>.jsonl on the share's own pool: the records
// outlive a system disk swap, and no share shows them. Kept auditKeepDays days.

const (
	auditSyslogConf = "/usr/local/etc/syslog.d/keelnas-audit.conf"
	auditKeepDays   = 365
	auditMaxDays    = 366  // one query looks at most this many days
	auditMaxRecords = 5000 // and answers at most this many records
)

// auditLog is where syslogd writes the lines (a variable for the tests).
var auditLog = "/var/log/keelnas-audit.log"

// auditSyslogText: local5.info only, into auditLog. "*.notice" (/var/log/messages) does not
// take INFO, so the lines go nowhere else.
const auditSyslogText = "# Written by keelnasd: Samba's access records (vfs_full_audit), docs/04 section 18.\nlocal5.info\t" + "%s\n"

// auditSmb is what a share with audit on adds to its section of smb4.conf. full_audit comes
// first so that it sees every call. create_file carries the access mask (docs/04 18.1):
// reading the data and only looking at the attributes are told apart there.
const auditSmb = `  full_audit:prefix = %u|%I|%S
  full_audit:success = connect create_file renameat unlinkat mkdirat
  full_audit:failure = connect create_file renameat unlinkat mkdirat
  full_audit:facility = LOCAL5
  full_audit:priority = INFO
`

// AuditRecord is one access as it is kept and shown.
type AuditRecord struct {
	Time  string `json:"time"` // RFC 3339, UTC
	User  string `json:"user"`
	IP    string `json:"ip"`
	Share string `json:"share"`
	// Act: connect, read, write, create, rename, delete, mkdir, or denied (a refused open,
	// rename, delete or new folder; Detail says which and why)
	Act    string `json:"act"`
	Path   string `json:"path,omitempty"` // in the share, "/" separated, no leading slash
	To     string `json:"to,omitempty"`   // rename: the new path
	Detail string `json:"detail,omitempty"`
}

// The access mask bits that say the data is read or written (MS-SMB2 2.2.13.1).
const (
	maskReadData   = 0x00000001
	maskWriteData  = 0x00000002
	maskAppendData = 0x00000004
	maskMaxAllowed = 0x02000000
	maskGenericAll = 0x10000000
	maskGenWrite   = 0x40000000
	maskGenRead    = 0x80000000
)

// parseSyslogTime reads the start of a syslogd line: RFC 3164 ("Nov 18 09:31:08 ", no year:
// the latest such time that is not after now) or RFC 3339. It returns the time and the rest.
func parseSyslogTime(line string, now time.Time) (time.Time, string, bool) {
	if len(line) > 20 && line[0] >= '0' && line[0] <= '9' {
		sp := strings.IndexByte(line, ' ')
		if sp > 0 {
			if t, err := time.Parse(time.RFC3339Nano, line[:sp]); err == nil {
				return t.UTC(), line[sp+1:], true
			}
		}
		return time.Time{}, "", false
	}
	if len(line) < 16 || line[15] != ' ' {
		return time.Time{}, "", false
	}
	loc := now.Location()
	t, err := time.ParseInLocation("Jan _2 15:04:05 2006", line[:15]+" "+strconv.Itoa(now.Year()), loc)
	if err != nil {
		return time.Time{}, "", false
	}
	if t.After(now.Add(24 * time.Hour)) { // December's lines read in January
		t = t.AddDate(-1, 0, 0)
	}
	return t.UTC(), line[16:], true
}

// auditNoise: names Windows opens by itself while a folder is shown.
func auditNoise(p string) bool {
	base := strings.ToLower(p[strings.LastIndexByte(p, '/')+1:])
	switch base {
	case "desktop.ini", "thumbs.db", ".ds_store", "autorun.inf", "folder.jpg", "folder.gif":
		return true
	}
	return strings.HasPrefix(base, "._") || strings.Contains(base, ":") // AppleDouble, a stream (Zone.Identifier)
}

// relPath turns what full_audit logs (an absolute path, or one relative to the share's root)
// into the path in the share.
func relPath(p, root string) string {
	if root != "" && (p == root || strings.HasPrefix(p, root+"/")) {
		p = p[len(root):]
	}
	p = strings.Trim(p, "/")
	if p == "." {
		return ""
	}
	return strings.TrimPrefix(p, "./")
}

// denied: a failure that is a refusal (not "no such file", which Explorer causes all the time).
func denied(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "permission denied") || strings.Contains(s, "access denied") ||
		strings.Contains(s, "operation not permitted") || strings.Contains(s, "access_denied")
}

// parseAuditLine turns one syslogd line into a record; ok=false for lines that are not
// full_audit's or that say nothing worth keeping. roots: share name -> its path.
func parseAuditLine(line string, now time.Time, roots map[string]string) (AuditRecord, bool) {
	t, rest, ok := parseSyslogTime(line, now)
	if !ok {
		return AuditRecord{}, false
	}
	i := strings.Index(rest, "smbd_audit")
	if i < 0 {
		return AuditRecord{}, false
	}
	j := strings.Index(rest[i:], ": ")
	if j < 0 {
		return AuditRecord{}, false
	}
	msg := strings.TrimRight(rest[i+j+2:], "\r\n ")
	f := strings.Split(msg, "|")
	// user|ip|share|op|ok or fail (why)|args...
	if len(f) < 5 {
		return AuditRecord{}, false
	}
	r := AuditRecord{Time: t.Format(time.RFC3339), User: f[0], IP: f[1], Share: f[2]}
	op, status, args := f[3], f[4], f[5:]
	okCall := status == "ok"
	if !okCall && !denied(status) {
		return AuditRecord{}, false
	}
	root := roots[r.Share]
	refuse := func(what, p string) (AuditRecord, bool) {
		r.Act, r.Path, r.Detail = "denied", relPath(p, root), what+": "+strings.TrimSuffix(strings.TrimPrefix(status, "fail ("), ")")
		return r, true
	}
	switch op {
	case "connect":
		if !okCall {
			r.Act, r.Detail = "denied", "connect: "+strings.TrimSuffix(strings.TrimPrefix(status, "fail ("), ")")
			return r, true
		}
		r.Act = "connect"
		return r, true
	case "create_file":
		// 0xMASK|file or dir|disposition|path
		if len(args) < 4 {
			return AuditRecord{}, false
		}
		mask, err := strconv.ParseUint(strings.TrimPrefix(args[0], "0x"), 16, 32)
		if err != nil {
			return AuditRecord{}, false
		}
		p := strings.Join(args[3:], "|")
		if auditNoise(p) {
			return AuditRecord{}, false
		}
		if !okCall {
			return refuse("open", p)
		}
		if args[1] == "dir" {
			return AuditRecord{}, false // entering a folder; a new one is mkdirat
		}
		write := mask&(maskWriteData|maskAppendData|maskGenWrite|maskGenericAll) != 0
		read := mask&(maskReadData|maskGenRead|maskGenericAll|maskMaxAllowed) != 0
		switch disp := args[2]; {
		case disp == "create":
			r.Act = "create"
		case write || disp == "overwrite" || disp == "overwrite_if" || disp == "supersede":
			r.Act = "write"
		case read:
			r.Act = "read"
		default:
			return AuditRecord{}, false // attributes, security, a delete-on-close open (unlinkat follows)
		}
		r.Path = relPath(p, root)
		return r, true
	case "renameat":
		if len(args) < 2 {
			return AuditRecord{}, false
		}
		if !okCall {
			return refuse("rename", args[0])
		}
		r.Act, r.Path, r.To = "rename", relPath(args[0], root), relPath(args[1], root)
		return r, true
	case "unlinkat":
		if len(args) < 1 {
			return AuditRecord{}, false
		}
		if !okCall {
			return refuse("delete", args[0])
		}
		r.Act, r.Path = "delete", relPath(args[0], root)
		return r, !auditNoise(r.Path)
	case "mkdirat":
		if len(args) < 1 {
			return AuditRecord{}, false
		}
		if !okCall {
			return refuse("mkdir", args[0])
		}
		r.Act, r.Path = "mkdir", relPath(args[0], root)
		return r, true
	}
	return AuditRecord{}, false
}

// auditDedup drops a record that repeats one of the last minute (same user, address, share,
// act, paths): Office and Explorer open a file several times for one look. seen is kept
// between batches and pruned here.
func auditDedup(recs []AuditRecord, seen map[string]time.Time) []AuditRecord {
	out := recs[:0]
	var last time.Time
	for _, r := range recs {
		t, _ := time.Parse(time.RFC3339, r.Time)
		if t.After(last) {
			last = t
		}
		k := strings.Join([]string{r.User, r.IP, r.Share, r.Act, r.Path, r.To}, "\x00")
		if p, ok := seen[k]; ok && t.Sub(p) < time.Minute && t.Sub(p) >= 0 {
			continue
		}
		seen[k] = t
		out = append(out, r)
	}
	for k, t := range seen {
		if last.Sub(t) >= time.Minute {
			delete(seen, k)
		}
	}
	return out
}

// auditDir is where a share's records are kept on its pool ("" when the pool is not mounted).
func (s *Server) auditDir(sh Share) string {
	mp := s.poolMountpoint(PoolOf(sh.Dataset))
	if mp == "" {
		return ""
	}
	return filepath.Join(mp, copyDir, "audit", sh.Name)
}

// auditOn: some share has audit on.
func auditOn(st *State) bool {
	return slices.ContainsFunc(st.Shares, func(sh Share) bool { return sh.Audit })
}

// ensureAuditSyslog makes syslogd write LOCAL5 to auditLog (once; it reloads syslogd when it
// changed something). Called from apply and the tick.
func (s *Server) ensureAuditSyslog(st *State) error {
	if !auditOn(st) {
		return nil
	}
	want := []byte(fmt.Sprintf(auditSyslogText, auditLog))
	changed := false
	if have, err := s.Sys.ReadFile(auditSyslogConf); err != nil || !bytes.Equal(have, want) {
		if err := s.Sys.WriteFile(auditSyslogConf, want, 0o644); err != nil {
			return err
		}
		changed = true
	}
	// syslogd opens the file without creating it
	if _, err := os.Stat(auditLog); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(auditLog, nil, 0o600); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		if _, err := s.Sys.Run("", "service", "syslogd", "reload"); err != nil {
			return err
		}
		s.Log.Printf("audit: syslogd writes the access records to %s", auditLog)
	}
	return nil
}

// auditTick (every minute): the lines of the minute before are read (syslogd has surely
// reopened the file by then), then this minute's file is set aside for the next tick.
func (s *Server) auditTick(t time.Time) {
	s.mu.Lock()
	st := s.st.Clone()
	s.mu.Unlock()
	if err := s.ensureAuditSyslog(st); err != nil {
		s.Log.Printf("audit: %v", err)
	}
	work := auditLog + ".work"
	if b, err := os.ReadFile(work); err == nil {
		if err := s.auditStore(st, b); err != nil {
			s.Log.Printf("audit: %v", err)
		}
		os.Remove(work)
	}
	if fi, err := os.Stat(auditLog); err == nil && fi.Size() > 0 {
		if err := os.Rename(auditLog, work); err == nil {
			os.WriteFile(auditLog, nil, 0o600)
			if _, err := s.Sys.Run("", "service", "syslogd", "reload"); err != nil {
				s.Log.Printf("audit: %v", err)
			}
		}
	}
	if t.Hour() == 4 && t.Minute() == 10 {
		s.auditPrune(st, t)
	}
}

// auditStore parses syslogd's lines and appends the records to each share's day files.
func (s *Server) auditStore(st *State, b []byte) error {
	roots := map[string]string{}
	byName := map[string]Share{}
	ds := s.datasets()
	for _, sh := range st.Shares {
		roots[sh.Name] = SharePath(sh.Name)
		if mp := ds[sh.Dataset].Mountpoint; strings.HasPrefix(mp, "/") {
			roots[sh.Name] = mp
		}
		byName[sh.Name] = sh
	}
	now := s.now()
	var recs []AuditRecord
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if r, ok := parseAuditLine(sc.Text(), now.In(time.Local), roots); ok {
			recs = append(recs, r)
		}
	}
	if s.auditSeen == nil {
		s.auditSeen = map[string]time.Time{}
	}
	recs = auditDedup(recs, s.auditSeen)
	// share -> day -> lines
	out := map[string]map[string][]byte{}
	for _, r := range recs {
		day := r.Time[:10]
		if out[r.Share] == nil {
			out[r.Share] = map[string][]byte{}
		}
		j, _ := json.Marshal(r)
		out[r.Share][day] = append(append(out[r.Share][day], j...), '\n')
	}
	var errs []string
	for name, days := range out {
		sh, ok := byName[name]
		if !ok {
			errs = append(errs, fmt.Sprintf("%d day(s) of records of share %s, which is gone, dropped", len(days), name))
			continue
		}
		dir := s.auditDir(sh)
		if dir == "" {
			errs = append(errs, fmt.Sprintf("share %s: its pool is not mounted, records dropped", name))
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		for day, lines := range days {
			f, err := os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if _, err := f.Write(lines); err != nil {
				errs = append(errs, err.Error())
			}
			f.Close()
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// auditPrune removes the day files older than auditKeepDays.
func (s *Server) auditPrune(st *State, t time.Time) {
	limit := t.UTC().AddDate(0, 0, -auditKeepDays).Format("2006-01-02")
	for _, sh := range st.Shares {
		dir := s.auditDir(sh)
		if dir == "" {
			continue
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			day, ok := strings.CutSuffix(e.Name(), ".jsonl")
			if ok && len(day) == 10 && day < limit {
				if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
					s.Log.Printf("audit: %s: %s removed (older than %d days)", sh.Name, day, auditKeepDays)
				}
			}
		}
	}
}

// AuditQuery is GET /v1/audit's answer.
type AuditQuery struct {
	Records   []AuditRecord `json:"records"`
	Truncated bool          `json:"truncated"` // more matched than limit
	From      string        `json:"from"`
	To        string        `json:"to"`
}

// getAudit: GET /v1/audit?share=&from=YYYY-MM-DD&to=YYYY-MM-DD&user=&act=&q=&limit=
// (dates UTC, both included, default the last 7 days; q: a part of the path, any case),
// newest first.
func (s *Server) getAudit(r *http.Request) (any, error) {
	q := r.URL.Query()
	s.mu.Lock()
	st := s.st.Clone()
	s.mu.Unlock()
	sh := st.Share(q.Get("share"))
	if sh == nil {
		return nil, notFound("no share %q", q.Get("share"))
	}
	now := s.now()
	to, from := now.Format("2006-01-02"), now.AddDate(0, 0, -6).Format("2006-01-02")
	if v := q.Get("to"); v != "" {
		to = v
	}
	if v := q.Get("from"); v != "" {
		from = v
	}
	tt, err1 := time.Parse("2006-01-02", to)
	ft, err2 := time.Parse("2006-01-02", from)
	if err1 != nil || err2 != nil || ft.After(tt) {
		return nil, bad("from and to are dates (YYYY-MM-DD), from not after to")
	}
	if tt.Sub(ft) > auditMaxDays*24*time.Hour {
		return nil, bad("at most %d days at once", auditMaxDays)
	}
	limit := 500
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, bad("limit: a number from 1")
		}
		limit = min(n, auditMaxRecords)
	}
	user, act, needle := q.Get("user"), q.Get("act"), strings.ToLower(q.Get("q"))
	res := AuditQuery{Records: []AuditRecord{}, From: from, To: to}
	dir := s.auditDir(*sh)
	if dir == "" {
		return res, nil
	}
	for d := tt; !d.Before(ft); d = d.AddDate(0, 0, -1) {
		b, err := os.ReadFile(filepath.Join(dir, d.Format("2006-01-02")+".jsonl"))
		if err != nil {
			continue
		}
		lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
		for i := len(lines) - 1; i >= 0; i-- {
			var rec AuditRecord
			if json.Unmarshal(lines[i], &rec) != nil {
				continue
			}
			if (user != "" && rec.User != user) || (act != "" && rec.Act != act) ||
				(needle != "" && !strings.Contains(strings.ToLower(rec.Path+"\x00"+rec.To), needle)) {
				continue
			}
			if len(res.Records) == limit {
				res.Truncated = true
				return res, nil
			}
			res.Records = append(res.Records, rec)
		}
	}
	return res, nil
}

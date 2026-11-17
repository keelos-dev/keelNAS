package nas

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Searching a keelMail's archive inside its backups (keelMail docs/09 §2.2 and §4): every copy
// holds the archive of the 30 days before it, in the format
//   keelmail/archive/FORMAT, keelmail/archive/YYYY/MM/DD/{index.jsonl,text.jsonl,<id>.eml}
// (the copy's root is keelMail's /data). For each day asked, the first copy taken after that day
// is mounted read-only (mount -t zfs -o ro <copy>@<snapshot>) and read; nothing leaves keelNAS.

const (
	archMountRoot  = "/var/run/keelnas-archive"
	archInCopy     = "keelmail/archive"
	archFormatWant = "keelmail-archive 1"
	archMaxResults = 500
	archAuditFile  = "archive-audit.jsonl" // next to state.json (system disk)
)

var archMu sync.Mutex // one search at a time (mounts)

// ArchEntry is one line of a day's index.jsonl (keelMail's format).
type ArchEntry struct {
	ID      string    `json:"id"`
	T       time.Time `json:"t"`
	Dir     string    `json:"dir"`
	From    string    `json:"from"`
	To      []string  `json:"to"`
	Subject string    `json:"subject"`
	Size    int       `json:"size"`
	Att     []string  `json:"att,omitempty"`
	MsgID   string    `json:"msgid,omitempty"`
	Deleted bool      `json:"deleted,omitempty"`
	Snap    string    `json:"snap,omitempty"` // keelNAS: the copy it was read from
}

type ArchQuery struct {
	From, To           time.Time // UTC days, To inclusive
	Dir                string
	Sender, Rcpt, Subj string
	Att, Body          string
}

func has(h, n string) bool {
	return n == "" || strings.Contains(strings.ToLower(h), strings.ToLower(n))
}

func (q ArchQuery) match(e ArchEntry) bool {
	if e.T.Before(q.From) || !e.T.Before(q.To.AddDate(0, 0, 1)) || q.Dir != "" && e.Dir != q.Dir {
		return false
	}
	if !has(e.From, q.Sender) || !has(e.Subject, q.Subj) || q.Rcpt != "" && !has(strings.Join(e.To, " "), q.Rcpt) {
		return false
	}
	if q.Att == "*" && len(e.Att) == 0 || q.Att != "" && q.Att != "*" && !has(strings.Join(e.Att, " "), q.Att) {
		return false
	}
	return true
}

func archDay(root string, d time.Time) string {
	return filepath.Join(root, archInCopy, d.Format("2006"), d.Format("01"), d.Format("02"))
}

func (s *Server) readLines(path string, f func([]byte)) {
	b, err := s.Sys.ReadFile(path)
	if err != nil {
		return
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		f(sc.Bytes())
	}
}

// dayEntries reads a day's index (deletions applied).
func (s *Server) dayEntries(dd string) ([]ArchEntry, bool) {
	if _, err := s.Sys.ReadFile(filepath.Join(dd, "index.jsonl")); err != nil {
		return nil, false
	}
	var l []ArchEntry
	gone := map[string]bool{}
	s.readLines(filepath.Join(dd, "index.jsonl"), func(b []byte) {
		var e ArchEntry
		if json.Unmarshal(b, &e) != nil {
			return
		}
		if e.Deleted {
			gone[e.ID] = true
		} else {
			l = append(l, e)
		}
	})
	out := l[:0]
	for _, e := range l {
		if !gone[e.ID] {
			out = append(out, e)
		}
	}
	return out, true
}

func (s *Server) dayTexts(dd string, want map[string]bool) map[string]string {
	out := map[string]string{}
	s.readLines(filepath.Join(dd, "text.jsonl"), func(b []byte) {
		var t struct{ ID, Text string }
		if json.Unmarshal(b, &t) == nil && want[t.ID] {
			out[t.ID] = t.Text
		}
	})
	return out
}

// mountSnap mounts a copy's snapshot read-only and returns the mount point and an unmount.
func (s *Server) mountSnap(ds, snap string) (string, func(), error) {
	mp := filepath.Join(archMountRoot, strings.NewReplacer("/", "_", "@", "_").Replace(ds+"@"+snap))
	if _, err := s.Sys.Run("", "mkdir", "-p", mp); err != nil {
		return "", nil, err
	}
	if _, err := s.Sys.Run("", "mount", "-t", "zfs", "-o", "ro", ds+"@"+snap, mp); err != nil {
		return "", nil, fmt.Errorf("mount %s@%s: %v", ds, snap, err)
	}
	return mp, func() {
		s.Sys.Run("", "umount", mp)
		s.Sys.Run("", "rmdir", mp)
	}, nil
}

// archSearch searches a source's copies. The copies are visited oldest first; each is asked for
// the days that ended before it was taken and were not found yet, so a day is read from the
// nearest copy that has it (a day a copy no longer holds is tried in the next one) and every
// copy is mounted at most once (the newest also for the day it was taken on). missing: the days
// after the newest copy (still only on the mail server).
func (s *Server) archSearch(src BackupSource, q ArchQuery) ([]ArchEntry, int, []string, error) {
	archMu.Lock()
	defer archMu.Unlock()
	if err := s.bkEnsureBase(src.Pool); err != nil {
		return nil, 0, nil, err
	}
	ds := src.Pool + "/" + BackupDS + "/" + src.Name
	snaps := s.bkSnaps(ds) // oldest first
	var pending []time.Time
	for d := q.From; !d.After(q.To); d = d.AddDate(0, 0, 1) {
		pending = append(pending, d)
	}
	seen := map[string]bool{}
	var res []ArchEntry
	total := 0
	for i, x := range snaps {
		newestCopy := i == len(snaps)-1
		var ask []time.Time
		for _, d := range pending {
			// a copy holds the days that ended before it was taken; the newest one also the day
			// it was taken on, up to that time
			if !x.Time.Before(d.AddDate(0, 0, 1)) || newestCopy && !x.Time.Before(d) {
				ask = append(ask, d)
			}
		}
		if len(ask) == 0 {
			continue
		}
		mp, umount, err := s.mountSnap(ds, x.Name)
		if err != nil {
			return nil, 0, nil, err
		}
		found := map[time.Time]bool{}
		for _, d := range ask {
			dd := archDay(mp, d)
			l, ok := s.dayEntries(dd)
			if !ok {
				continue // not in this copy: a later copy may have it
			}
			found[d] = true
			var hits []ArchEntry
			for _, e := range l {
				if !seen[e.ID] && q.match(e) {
					e.Snap = x.Name
					hits = append(hits, e)
				}
			}
			if q.Body != "" && len(hits) > 0 {
				want := map[string]bool{}
				for _, e := range hits {
					want[e.ID] = true
				}
				texts := s.dayTexts(dd, want)
				kept := hits[:0]
				for _, e := range hits {
					if has(texts[e.ID], q.Body) {
						kept = append(kept, e)
					}
				}
				hits = kept
			}
			for _, e := range hits {
				seen[e.ID] = true
				total++
				res = append(res, e)
			}
		}
		umount()
		rest := pending[:0]
		for _, d := range pending {
			if !found[d] {
				rest = append(rest, d)
			}
		}
		pending = rest
	}
	missing := []string{}
	var newest time.Time
	if len(snaps) > 0 {
		newest = snaps[len(snaps)-1].Time
	}
	for _, d := range pending {
		if newest.Before(d) { // after the newest copy: only on the mail server
			missing = append(missing, d.Format(time.DateOnly))
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].T.After(res[j].T) })
	if len(res) > archMaxResults {
		res = res[:archMaxResults]
	}
	return res, total, missing, nil
}

var archIDRE = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)

// archOpen reads one message (its index entry, decoded text and original) from a snapshot.
func (s *Server) archOpen(src BackupSource, snap, id string) (ArchEntry, string, []byte, error) {
	if !archIDRE.MatchString(id) || strings.ContainsAny(snap, "/@ ") {
		return ArchEntry{}, "", nil, notFound("no message %s", id)
	}
	archMu.Lock()
	defer archMu.Unlock()
	if err := s.bkEnsureBase(src.Pool); err != nil {
		return ArchEntry{}, "", nil, err
	}
	mp, umount, err := s.mountSnap(src.Pool+"/"+BackupDS+"/"+src.Name, snap)
	if err != nil {
		return ArchEntry{}, "", nil, err
	}
	defer umount()
	d, _ := time.Parse("20060102", id[:8])
	dd := archDay(mp, d)
	l, _ := s.dayEntries(dd)
	for _, e := range l {
		if e.ID == id {
			raw, err := s.Sys.ReadFile(filepath.Join(dd, id+".eml"))
			if err != nil {
				return ArchEntry{}, "", nil, notFound("the message's file is not in this copy")
			}
			e.Snap = snap
			return e, s.dayTexts(dd, map[string]bool{id: true})[id], raw, nil
		}
	}
	return ArchEntry{}, "", nil, notFound("no message %s in %s", id, snap)
}

func (s *Server) archAudit(r *http.Request, action string, what any) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	b, _ := json.Marshal(map[string]any{"t": s.now(), "via": host, "who": r.Header.Get("X-Keel-User"), "action": action, "what": what})
	p := filepath.Join(filepath.Dir(s.StatePath), archAuditFile)
	old, _ := s.Sys.ReadFile(p)
	s.Sys.WriteFile(p, append(old, append(b, '\n')...), 0o600)
}

func (s *Server) archSource(r *http.Request) (BackupSource, error) {
	src := s.State().backupSource(r.PathValue("name"))
	if src == nil {
		return BackupSource{}, notFound("backup source %s", r.PathValue("name"))
	}
	if src.Kind != "keelmail" {
		return BackupSource{}, bad("%s is a %s: only keelMail backups have a mail archive", src.Name, src.Kind)
	}
	return *src, nil
}

// GET /v1/backups/{name}/archive?from=YYYY-MM-DD&to=…&dir=&sender=&rcpt=&subject=&att=&body=
func (s *Server) getArchiveSearch(r *http.Request) (any, error) {
	src, err := s.archSource(r)
	if err != nil {
		return nil, err
	}
	v := r.URL.Query()
	from, err1 := time.Parse(time.DateOnly, v.Get("from"))
	to, err2 := time.Parse(time.DateOnly, v.Get("to"))
	if err1 != nil || err2 != nil || to.Before(from) || to.Sub(from) > 400*24*time.Hour {
		return nil, bad("from, to: YYYY-MM-DD, at most 400 days")
	}
	q := ArchQuery{From: from, To: to, Dir: v.Get("dir"), Sender: v.Get("sender"), Rcpt: v.Get("rcpt"), Subj: v.Get("subject"), Att: v.Get("att"), Body: v.Get("body")}
	res, total, missing, err := s.archSearch(src, q)
	if err != nil {
		return nil, err
	}
	s.archAudit(r, "search", v)
	if res == nil {
		res = []ArchEntry{}
	}
	if missing == nil {
		missing = []string{}
	}
	return map[string]any{"results": res, "total": total, "max": archMaxResults, "missing": missing}, nil
}

// GET /v1/backups/{name}/archive/msg/{id}?snap=S: the message's entry and text, the original
// as base64 (the page saves it as .eml).
func (s *Server) getArchiveMsg(r *http.Request) (any, error) {
	src, err := s.archSource(r)
	if err != nil {
		return nil, err
	}
	id, snap := r.PathValue("id"), r.URL.Query().Get("snap")
	e, text, raw, err := s.archOpen(src, snap, id)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"entry": e, "text": text}
	if r.URL.Query().Get("eml") == "1" {
		out["eml"] = base64.StdEncoding.EncodeToString(raw)
		s.archAudit(r, "download", id)
	} else {
		s.archAudit(r, "view", id)
	}
	return out, nil
}

// GET /v1/backups/archive-audit: the newest 500 lines.
func (s *Server) getArchiveAudit(r *http.Request) (any, error) {
	b, _ := s.Sys.ReadFile(filepath.Join(filepath.Dir(s.StatePath), archAuditFile))
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	out := []json.RawMessage{}
	for i := len(lines) - 1; i >= 0 && len(out) < 500; i-- {
		if len(lines[i]) > 0 {
			out = append(out, json.RawMessage(lines[i]))
		}
	}
	return map[string]any{"audit": out}, nil
}

// Package cli is the keelnas command: it calls keelnasd's API over the Unix socket and prints
// tables (or JSON with -j).
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"
)

const usage = `usage: keelnas [-j] COMMAND
  status
  disk list
  storage list | create [--protect] DISK... | protect NAME DISK
  storage importable | import ID [--name NAME] [--take-over]   (pools on the disks that are not imported;
                                                          --take-over: last used by another system)
  user list | add NAME | passwd NAME | del NAME       (password from stdin, or --password P)
  share list | add NAME [--dataset DS | --pool POOL] --rw U1,U2 [--ro U3] | set NAME --rw … --ro …
        | del NAME [--purge]                              (--dataset: share that dataset; without it: make
                                                          POOL/shares/NAME)
  dataset list [POOL] | add POOL/PARENT/NAME | del DATASET   (del: only datasets keelNAS made)
  dataset chown DATASET USER                              (all its files to USER:keelnas, 2770/0660; waits)
  files DATASET [PATH]                                    (read-only listing)
  lun list | add NAME [--pool POOL] --size GIB | del NAME [--purge]
  snapshot list SHARE | create SHARE | del SHARE NAME     (--lun: SHARE is a LUN)
  settings [--hourly N] [--daily N] [--weekly N]          (snapshots kept; 0 = none of that class)
  alert list
  apply
  restore                                                 (only on fresh settings)
-j prints the API's JSON.`

// CLI is one run of the command.
type CLI struct {
	HTTP     *http.Client
	Base     string // http://keelnasd
	In       io.Reader
	Out, Err io.Writer
	Terminal bool // stdin is a terminal: ask for passwords without echo
	JSON     bool
}

// New talks to keelnasd on the Unix socket.
func New(socket string) *CLI {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	fi, _ := os.Stdin.Stat()
	return &CLI{
		HTTP: &http.Client{Transport: tr, Timeout: 10 * time.Minute},
		Base: "http://keelnasd", In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		Terminal: fi != nil && fi.Mode()&os.ModeCharDevice != 0,
	}
}

// args holds positional arguments and --flags.
type args struct {
	pos   []string
	flags map[string]string
}

var boolFlags = map[string]bool{"protect": true, "purge": true, "lun": true, "take-over": true}

func parseArgs(a []string) (args, error) {
	r := args{flags: map[string]string{}}
	for i := 0; i < len(a); i++ {
		s := a[i]
		if !strings.HasPrefix(s, "--") {
			r.pos = append(r.pos, s)
			continue
		}
		k := strings.TrimPrefix(s, "--")
		if j := strings.IndexByte(k, '='); j >= 0 {
			r.flags[k[:j]] = k[j+1:]
			continue
		}
		if boolFlags[k] {
			r.flags[k] = "1"
			continue
		}
		if i+1 >= len(a) {
			return r, fmt.Errorf("--%s needs a value", k)
		}
		i++
		r.flags[k] = a[i]
	}
	return r, nil
}

func list(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// Run runs the command and returns the exit status.
func (c *CLI) Run(argv []string) int {
	var rest []string
	for _, a := range argv {
		if a == "-j" || a == "--json" {
			c.JSON = true
		} else {
			rest = append(rest, a)
		}
	}
	if err := c.run(rest); err != nil {
		fmt.Fprintln(c.Err, "keelnas:", err)
		return 1
	}
	return 0
}

type usageError struct{}

func (usageError) Error() string { return usage }

func (c *CLI) run(argv []string) error {
	if len(argv) == 0 || argv[0] == "help" || argv[0] == "-h" || argv[0] == "--help" {
		return usageError{}
	}
	a, err := parseArgs(argv[1:])
	if err != nil {
		return err
	}
	sub := ""
	if len(a.pos) > 0 {
		sub = a.pos[0]
	}
	need := func(n int) error {
		if len(a.pos) != n {
			return usageError{}
		}
		return nil
	}
	switch argv[0] {
	case "status":
		return c.get("/v1/status", c.printStatus)
	case "apply":
		return c.call("POST", "/v1/apply", map[string]any{}, func(b []byte) error {
			var r struct {
				Errors []string `json:"errors"`
			}
			json.Unmarshal(b, &r)
			if len(r.Errors) == 0 {
				fmt.Fprintln(c.Out, "applied")
				return nil
			}
			for _, e := range r.Errors {
				fmt.Fprintln(c.Out, "error:", e)
			}
			return errors.New("apply had errors")
		})
	case "restore":
		return c.call("POST", "/v1/restore", map[string]any{}, func(b []byte) error {
			var r struct {
				From   string   `json:"restored_from"`
				Errors []string `json:"errors"`
			}
			json.Unmarshal(b, &r)
			fmt.Fprintln(c.Out, "restored the settings from", r.From)
			for _, e := range r.Errors {
				fmt.Fprintln(c.Out, "error:", e)
			}
			if len(r.Errors) > 0 {
				return errors.New("apply had errors")
			}
			return nil
		})
	case "alert", "alerts":
		if sub != "list" && sub != "" {
			return usageError{}
		}
		return c.get("/v1/alerts", c.printAlerts)
	case "settings":
		if len(a.pos) != 0 {
			return usageError{}
		}
		snap := map[string]int{}
		for _, k := range []string{"hourly", "daily", "weekly"} {
			if v, ok := a.flags[k]; ok {
				var n int
				if _, err := fmt.Sscan(v, &n); err != nil {
					return fmt.Errorf("--%s needs a number", k)
				}
				snap[k] = n
			}
		}
		if len(snap) == 0 {
			return c.get("/v1/settings", c.printSettings)
		}
		return c.call("PUT", "/v1/settings", map[string]any{"snapshots": snap}, c.printSettings)
	case "snapshot", "snapshots", "snap":
		kind := "/v1/shares/"
		if a.flags["lun"] != "" {
			kind = "/v1/luns/"
		}
		switch sub {
		case "list", "":
			if err := need(2); err != nil {
				return err
			}
			return c.get(kind+url.PathEscape(a.pos[1])+"/snapshots", c.printSnapshots)
		case "create", "add":
			if err := need(2); err != nil {
				return err
			}
			return c.call("POST", kind+url.PathEscape(a.pos[1])+"/snapshots", map[string]any{}, c.printOneSnapshot)
		case "del", "delete", "rm":
			if err := need(3); err != nil {
				return err
			}
			return c.call("DELETE", kind+url.PathEscape(a.pos[1])+"/snapshots/"+url.PathEscape(a.pos[2]), nil, c.ok("deleted snapshot "+a.pos[2]))
		}
	case "debug":
		// Test hook, not in the usage: keelnas debug tick [TIME] [--force J1,J2] [--only J1,J2]
		if sub != "tick" || len(a.pos) > 2 {
			return usageError{}
		}
		body := map[string]any{"force": list(a.flags["force"])}
		if len(a.pos) == 2 {
			body["time"] = a.pos[1]
		}
		if v, ok := a.flags["only"]; ok {
			body["only"] = list(v)
		}
		return c.call("POST", "/v1/debug/tick", body, func(b []byte) error {
			var v any
			json.Unmarshal(b, &v)
			out, _ := json.MarshalIndent(v, "", "  ")
			fmt.Fprintln(c.Out, string(out))
			return nil
		})
	case "disk", "disks":
		if sub != "list" && sub != "" {
			return usageError{}
		}
		return c.get("/v1/disks", c.printDisks)
	case "storage":
		switch sub {
		case "", "list":
			return c.get("/v1/storage", c.printStorage)
		case "create":
			if len(a.pos) < 2 {
				return usageError{}
			}
			body := map[string]any{"disks": a.pos[1:], "protect": a.flags["protect"] != ""}
			return c.call("POST", "/v1/storage", body, c.printOneStorage)
		case "protect":
			if err := need(3); err != nil {
				return err
			}
			return c.call("POST", "/v1/storage/"+url.PathEscape(a.pos[1])+"/protect",
				map[string]any{"disk": a.pos[2]}, c.printOneStorage)
		case "importable":
			return c.get("/v1/importable", c.printImportable)
		case "import":
			if err := need(2); err != nil {
				return err
			}
			body := map[string]any{"name": a.flags["name"], "force": a.flags["take-over"] != ""}
			return c.call("POST", "/v1/importable/"+url.PathEscape(a.pos[1])+"/import", body, c.printJSON)
		}
	case "user", "users":
		switch sub {
		case "", "list":
			return c.get("/v1/users", c.printUsers)
		case "add", "passwd":
			if err := need(2); err != nil {
				return err
			}
			pw, err := c.password(a.flags)
			if err != nil {
				return err
			}
			if sub == "add" {
				return c.call("POST", "/v1/users", map[string]any{"name": a.pos[1], "password": pw}, c.printOneUser)
			}
			return c.call("PUT", "/v1/users/"+url.PathEscape(a.pos[1]), map[string]any{"password": pw}, c.printOneUser)
		case "del", "delete", "rm":
			if err := need(2); err != nil {
				return err
			}
			return c.call("DELETE", "/v1/users/"+url.PathEscape(a.pos[1]), nil, c.ok("deleted user "+a.pos[1]))
		}
	case "share", "shares":
		switch sub {
		case "", "list":
			return c.get("/v1/shares", c.printShares)
		case "add":
			if err := need(2); err != nil {
				return err
			}
			body := map[string]any{"name": a.pos[1], "rw": list(a.flags["rw"]), "ro": list(a.flags["ro"])}
			if d := a.flags["dataset"]; d != "" {
				body["dataset"] = d
			} else {
				body["pool"] = a.flags["pool"]
			}
			return c.call("POST", "/v1/shares", body, c.printOneShare)
		case "set":
			if err := need(2); err != nil {
				return err
			}
			body := map[string]any{"rw": list(a.flags["rw"]), "ro": list(a.flags["ro"])}
			return c.call("PUT", "/v1/shares/"+url.PathEscape(a.pos[1]), body, c.printOneShare)
		case "del", "delete", "rm":
			if err := need(2); err != nil {
				return err
			}
			return c.call("DELETE", "/v1/shares/"+url.PathEscape(a.pos[1])+purgeQuery(a), nil, c.ok("deleted share "+a.pos[1]))
		}
	case "dataset", "datasets":
		switch sub {
		case "", "list":
			if len(a.pos) > 2 {
				return usageError{}
			}
			if len(a.pos) == 2 {
				return c.get("/v1/storage/"+url.PathEscape(a.pos[1])+"/datasets", c.printDatasets)
			}
			return c.allDatasets()
		case "add":
			if err := need(2); err != nil {
				return err
			}
			i := strings.LastIndexByte(a.pos[1], '/')
			if i <= 0 {
				return fmt.Errorf("give the whole name: POOL/PARENT/NAME")
			}
			body := map[string]any{"parent": a.pos[1][:i], "name": a.pos[1][i+1:]}
			return c.call("POST", "/v1/storage/"+url.PathEscape(poolOf(a.pos[1]))+"/datasets", body, c.printOneDataset)
		case "del", "delete", "rm":
			if err := need(2); err != nil {
				return err
			}
			return c.call("DELETE", datasetPath(a.pos[1]), nil, c.ok("deleted dataset "+a.pos[1]))
		case "chown":
			if err := need(3); err != nil {
				return err
			}
			return c.call("POST", datasetPath(a.pos[1])+"/owner", map[string]any{"user": a.pos[2]}, c.waitJob)
		}
	case "files", "ls":
		if len(a.pos) < 1 || len(a.pos) > 2 {
			return usageError{}
		}
		p := datasetPath(a.pos[0]) + "/files"
		if len(a.pos) == 2 {
			p += "?path=" + url.QueryEscape(a.pos[1])
		}
		return c.get(p, c.printFiles)
	case "lun", "luns":
		switch sub {
		case "", "list":
			return c.get("/v1/luns", c.printLUNs)
		case "add":
			if err := need(2); err != nil {
				return err
			}
			var size int
			if _, err := fmt.Sscan(a.flags["size"], &size); err != nil {
				return fmt.Errorf("--size GIB is required")
			}
			body := map[string]any{"name": a.pos[1], "pool": a.flags["pool"], "size_gib": size}
			return c.call("POST", "/v1/luns", body, c.printOneLUN)
		case "del", "delete", "rm":
			if err := need(2); err != nil {
				return err
			}
			return c.call("DELETE", "/v1/luns/"+url.PathEscape(a.pos[1])+purgeQuery(a), nil, c.ok("deleted LUN "+a.pos[1]))
		}
	}
	return usageError{}
}

func purgeQuery(a args) string {
	if a.flags["purge"] != "" {
		return "?purge=1"
	}
	return ""
}

func (c *CLI) ok(msg string) func([]byte) error {
	return func([]byte) error { fmt.Fprintln(c.Out, msg); return nil }
}

// password: --password, or stdin (asked twice without echo on a terminal).
func (c *CLI) password(flags map[string]string) (string, error) {
	if p, ok := flags["password"]; ok {
		return p, nil
	}
	r := bufio.NewReader(c.In)
	if !c.Terminal {
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	echo := func(on bool) {
		arg := "-echo"
		if on {
			arg = "echo"
		}
		cmd := exec.Command("stty", arg)
		cmd.Stdin = os.Stdin
		cmd.Run()
	}
	echo(false)
	defer echo(true)
	fmt.Fprint(c.Err, "Password: ")
	p1, _ := r.ReadString('\n')
	fmt.Fprint(c.Err, "\nAgain: ")
	p2, _ := r.ReadString('\n')
	fmt.Fprintln(c.Err)
	p1, p2 = strings.TrimRight(p1, "\r\n"), strings.TrimRight(p2, "\r\n")
	if p1 != p2 {
		return "", errors.New("the passwords differ")
	}
	return p1, nil
}

func (c *CLI) get(path string, print func([]byte) error) error {
	return c.call("GET", path, nil, print)
}

func (c *CLI) call(method, path string, body any, print func([]byte) error) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("keelnasd is not answering: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if c.JSON {
		var v any
		if json.Unmarshal(b, &v) == nil {
			out, _ := json.MarshalIndent(v, "", "  ")
			fmt.Fprintln(c.Out, string(out))
			return nil
		}
		c.Out.Write(b)
		return nil
	}
	return print(b)
}

// ---- printing ----

func size(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k*k:
		return fmt.Sprintf("%.1fT", float64(n)/(k*k*k*k))
	case n >= k*k*k:
		return fmt.Sprintf("%.1fG", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1fM", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1fK", float64(n)/k)
	}
	return fmt.Sprintf("%dB", n)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (c *CLI) table() *tabwriter.Writer { return tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0) }

type pool struct {
	Name      string `json:"name"`
	Health    string `json:"health"`
	Size      int64  `json:"size"`
	Used      int64  `json:"used"`
	Free      int64  `json:"free"`
	Protected bool   `json:"protected"`
	Disks     []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"disks"`
}

func (c *CLI) printStatus(b []byte) error {
	var s struct {
		Version  string `json:"version"`
		Hostname string `json:"hostname"`
		UptimeS  int64  `json:"uptime_s"`
		Memory   struct {
			Total, Free, ARC int64
		} `json:"memory"`
		Pools     []pool          `json:"pools"`
		Services  map[string]bool `json:"services"`
		LastApply struct {
			Time   string   `json:"time"`
			Errors []string `json:"errors"`
		} `json:"last_apply"`
		Addresses    []string `json:"addresses"`
		Alerts       int      `json:"alerts"`
		RestoredFrom string   `json:"restored_from"`
		Keel         *struct {
			Registered bool   `json:"registered"`
			URL        string `json:"url"`
			Time       string `json:"time"`
			Error      string `json:"error"`
		} `json:"keel"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	up := time.Duration(s.UptimeS) * time.Second
	fmt.Fprintf(c.Out, "host      %s\nversion   %s\nuptime    %s\n", s.Hostname, s.Version, up)
	fmt.Fprintf(c.Out, "address   %s\n", dash(strings.Join(s.Addresses, " ")))
	fmt.Fprintf(c.Out, "memory    %s total, %s free, %s ZFS cache\n", size(s.Memory.Total), size(s.Memory.Free), size(s.Memory.ARC))
	fmt.Fprintf(c.Out, "services  smbd %s, ctld %s\n", run(s.Services["smbd"]), run(s.Services["ctld"]))
	for _, p := range s.Pools {
		fmt.Fprintf(c.Out, "storage   %s %s, %s of %s used, protected %s\n", p.Name, p.Health, size(p.Used), size(p.Size), yes(p.Protected))
	}
	if len(s.Pools) == 0 {
		fmt.Fprintln(c.Out, "storage   none")
	}
	fmt.Fprintf(c.Out, "applied   %s", dash(s.LastApply.Time))
	if len(s.LastApply.Errors) == 0 {
		fmt.Fprintln(c.Out, ", no errors")
	} else {
		fmt.Fprintf(c.Out, ", %d errors:\n", len(s.LastApply.Errors))
		for _, e := range s.LastApply.Errors {
			fmt.Fprintln(c.Out, "  "+e)
		}
	}
	fmt.Fprintf(c.Out, "alerts    %d\n", s.Alerts)
	if s.RestoredFrom != "" {
		fmt.Fprintf(c.Out, "restored  from storage %s\n", s.RestoredFrom)
	}
	if k := s.Keel; k != nil {
		switch {
		case k.Registered:
			fmt.Fprintf(c.Out, "keel      registered = true, %s at %s\n", k.URL, k.Time)
		case k.Error != "":
			fmt.Fprintf(c.Out, "keel      registered = false: %s\n", k.Error)
		default:
			fmt.Fprintln(c.Out, "keel      registered = false (not tried yet)")
		}
	}
	return nil
}

func run(b bool) string {
	if b {
		return "running"
	}
	return "NOT running"
}

func (c *CLI) printDisks(b []byte) error {
	var r struct {
		Disks []struct {
			Name, Model, Serial, Pool string
			PoolState                 string `json:"pool_state"`
			Content                   string `json:"content"`
			Size                      int64
			System                    bool
			Smart                     string `json:"smart"`
			Temperature               int    `json:"temperature"`
		} `json:"disks"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	t := c.table()
	fmt.Fprintln(t, "DISK\tSIZE\tMODEL\tSERIAL\tUSE\tSMART\tTEMP")
	for _, d := range r.Disks {
		use := "free"
		switch {
		case d.System:
			use = "system"
		case d.PoolState == "not_imported":
			use = "pool " + d.Pool + " (not imported)"
		case d.PoolState == "destroyed":
			use = "pool " + d.Pool + " (destroyed)"
		case d.Pool != "":
			use = "storage " + d.Pool
		case d.Content != "":
			use = "not empty: " + d.Content
		}
		temp := "-"
		if d.Temperature > 0 {
			temp = fmt.Sprintf("%dC", d.Temperature)
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", d.Name, size(d.Size), dash(d.Model), dash(d.Serial), use, dash(d.Smart), temp)
	}
	return t.Flush()
}

func (c *CLI) storageRows(t *tabwriter.Writer, ps []pool) {
	fmt.Fprintln(t, "NAME\tHEALTH\tSIZE\tUSED\tFREE\tPROTECTED\tDISKS")
	for _, p := range ps {
		var ds []string
		for _, d := range p.Disks {
			ds = append(ds, d.Name+"("+d.State+")")
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, p.Health, size(p.Size), size(p.Used), size(p.Free), yes(p.Protected), dash(strings.Join(ds, " ")))
	}
}

func (c *CLI) printStorage(b []byte) error {
	var r struct {
		Storage []pool `json:"storage"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	t := c.table()
	c.storageRows(t, r.Storage)
	return t.Flush()
}

func (c *CLI) printOneStorage(b []byte) error {
	var p pool
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	t := c.table()
	c.storageRows(t, []pool{p})
	return t.Flush()
}

type user struct {
	Name string `json:"name"`
	UID  int    `json:"uid"`
}

func (c *CLI) printUsers(b []byte) error {
	var r struct {
		Users  []user `json:"users"`
		Synced []struct {
			user
			Enabled     bool   `json:"enabled"`
			HasPassword bool   `json:"has_password"`
			Problem     string `json:"problem"`
		} `json:"synced"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	t := c.table()
	fmt.Fprintln(t, "USER\tUID\tFROM")
	for _, u := range r.Users {
		fmt.Fprintf(t, "%s\t%d\tlocal\n", u.Name, u.UID)
	}
	for _, u := range r.Synced {
		note := "keelAuth"
		switch {
		case u.Problem != "":
			note += " (not made: " + u.Problem + ")"
		case !u.HasPassword:
			note += " (no SMB password yet)"
		case !u.Enabled:
			note += " (disabled)"
		}
		fmt.Fprintf(t, "%s\t%d\t%s\n", u.Name, u.UID, note)
	}
	return t.Flush()
}

func (c *CLI) printOneUser(b []byte) error {
	var u user
	if err := json.Unmarshal(b, &u); err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "user %s (uid %d)\n", u.Name, u.UID)
	return nil
}

type share struct {
	Name    string   `json:"name"`
	Dataset string   `json:"dataset"`
	RW      []string `json:"rw"`
	RO      []string `json:"ro"`
	Path    string   `json:"path"`
	Used    int64    `json:"used"`
}

func (c *CLI) shareRows(t *tabwriter.Writer, ss []share) {
	fmt.Fprintln(t, "SHARE\tDATASET\tPATH\tUSED\tREAD-WRITE\tREAD-ONLY")
	for _, s := range ss {
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Dataset, dash(s.Path), size(s.Used), dash(strings.Join(s.RW, ",")), dash(strings.Join(s.RO, ",")))
	}
}

// ---- datasets and files ----

func poolOf(dataset string) string {
	p, _, _ := strings.Cut(dataset, "/")
	return p
}

// datasetPath is the API path of a dataset: its whole name as one escaped segment.
func datasetPath(dataset string) string {
	return "/v1/storage/" + url.PathEscape(poolOf(dataset)) + "/datasets/" + url.PathEscape(dataset)
}

type dataset struct {
	Name       string `json:"name"`
	Mountpoint string `json:"mountpoint"`
	Mounted    bool   `json:"mounted"`
	Used       int64  `json:"used"`
	Avail      int64  `json:"avail"`
	Ours       bool   `json:"ours"`
	Share      *struct {
		Name string `json:"name"`
	} `json:"share"`
	Snapshots int `json:"snapshots"`
	Owner     *struct {
		UID  int    `json:"uid"`
		User string `json:"user"`
	} `json:"owner"`
}

func (c *CLI) datasetRows(t *tabwriter.Writer, ds []dataset) {
	fmt.Fprintln(t, "DATASET\tUSED\tAVAIL\tMOUNTPOINT\tOWNER\tSHARE\tSNAPSHOTS\tMADE BY")
	for _, d := range ds {
		sh, by, mp := "-", "other", d.Mountpoint
		if d.Share != nil {
			sh = d.Share.Name
		}
		if d.Ours {
			by = "keelNAS"
		}
		if !d.Mounted {
			mp += " (not mounted)"
		}
		owner := "-"
		switch {
		case d.Owner == nil:
		case d.Owner.User != "":
			owner = d.Owner.User
		case d.Owner.UID == 0:
			owner = "root"
		default:
			owner = fmt.Sprintf("uid %d", d.Owner.UID)
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n", d.Name, size(d.Used), size(d.Avail), mp, owner, sh, d.Snapshots, by)
	}
}

func (c *CLI) printDatasets(b []byte) error {
	var r struct {
		Datasets []dataset `json:"datasets"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	t := c.table()
	c.datasetRows(t, r.Datasets)
	return t.Flush()
}

func (c *CLI) printOneDataset(b []byte) error {
	var d dataset
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	t := c.table()
	c.datasetRows(t, []dataset{d})
	return t.Flush()
}

// allDatasets lists the datasets of every storage.
func (c *CLI) allDatasets() error {
	asJSON := c.JSON
	c.JSON = false // the calls below hand their answers to us, not to the terminal
	defer func() { c.JSON = asJSON }()
	var st struct {
		Storage []struct {
			Name string `json:"name"`
		} `json:"storage"`
	}
	if err := c.get("/v1/storage", func(b []byte) error { return json.Unmarshal(b, &st) }); err != nil {
		return err
	}
	all := []json.RawMessage{}
	for _, p := range st.Storage {
		var r struct {
			Datasets []json.RawMessage `json:"datasets"`
		}
		if err := c.get("/v1/storage/"+url.PathEscape(p.Name)+"/datasets", func(b []byte) error { return json.Unmarshal(b, &r) }); err != nil {
			return err
		}
		all = append(all, r.Datasets...)
	}
	b, _ := json.Marshal(map[string]any{"datasets": all})
	if asJSON {
		return c.printJSON(b)
	}
	return c.printDatasets(b)
}

type jobView struct {
	ID     string   `json:"id"`
	State  string   `json:"state"`
	Files  int64    `json:"files"`
	Total  int64    `json:"total"`
	Failed int64    `json:"failed"`
	Errors []string `json:"errors"`
	Error  string   `json:"error"`
}

// jobPoll is how often waitJob asks (a variable for the tests).
var jobPoll = time.Second

// waitJob follows a job until it is finished, with a progress line on the terminal.
func (c *CLI) waitJob(b []byte) error {
	var j jobView
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	for j.State == "counting" || j.State == "running" {
		if c.Terminal {
			fmt.Fprintf(c.Err, "\rjob %s: %s %d/%d", j.ID, j.State, j.Files, j.Total)
		}
		time.Sleep(jobPoll)
		asJSON := c.JSON
		c.JSON = false
		err := c.get("/v1/jobs/"+url.PathEscape(j.ID), func(b []byte) error { return json.Unmarshal(b, &j) })
		c.JSON = asJSON
		if err != nil {
			return err
		}
	}
	if c.Terminal {
		fmt.Fprintln(c.Err)
	}
	if c.JSON {
		b, _ := json.Marshal(j)
		return c.printJSON(b)
	}
	fmt.Fprintf(c.Out, "job %s: %s, %d of %d entries\n", j.ID, j.State, j.Files, j.Total)
	for _, e := range j.Errors {
		fmt.Fprintln(c.Out, "error:", e)
	}
	if j.State != "done" {
		return fmt.Errorf("the owner change failed: %s", j.Error)
	}
	return nil
}

func (c *CLI) printFiles(b []byte) error {
	var r struct {
		Entries []struct {
			Name  string `json:"name"`
			Dir   bool   `json:"dir"`
			Link  bool   `json:"link"`
			Size  int64  `json:"size"`
			Mtime string `json:"mtime"`
		} `json:"entries"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	t := c.table()
	fmt.Fprintln(t, "NAME\tSIZE\tMODIFIED")
	for _, e := range r.Entries {
		n, sz := e.Name, size(e.Size)
		switch {
		case e.Dir:
			n, sz = n+"/", "-"
		case e.Link:
			n += "@"
		}
		fmt.Fprintf(t, "%s\t%s\t%s\n", n, sz, dash(e.Mtime))
	}
	if r.Truncated {
		fmt.Fprintln(t, "…\t\t(more entries not shown)")
	}
	return t.Flush()
}

func (c *CLI) printShares(b []byte) error {
	var r struct {
		Shares []share `json:"shares"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	t := c.table()
	c.shareRows(t, r.Shares)
	return t.Flush()
}

func (c *CLI) printOneShare(b []byte) error {
	var s share
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	t := c.table()
	c.shareRows(t, []share{s})
	return t.Flush()
}

type lun struct {
	Name    string `json:"name"`
	Pool    string `json:"pool"`
	SizeGiB int    `json:"size_gib"`
	IQN     string `json:"iqn"`
}

func (c *CLI) lunRows(t *tabwriter.Writer, ls []lun) {
	fmt.Fprintln(t, "LUN\tSTORAGE\tSIZE\tIQN")
	for _, l := range ls {
		fmt.Fprintf(t, "%s\t%s\t%dG\t%s\n", l.Name, l.Pool, l.SizeGiB, l.IQN)
	}
}

func (c *CLI) printLUNs(b []byte) error {
	var r struct {
		LUNs []lun `json:"luns"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	t := c.table()
	c.lunRows(t, r.LUNs)
	return t.Flush()
}

func (c *CLI) printOneLUN(b []byte) error {
	var l lun
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	t := c.table()
	c.lunRows(t, []lun{l})
	return t.Flush()
}

type snapshot struct {
	Name  string `json:"name"`
	Time  string `json:"time"`
	Class string `json:"class"`
	Used  int64  `json:"used"`
}

func (c *CLI) snapRows(ss []snapshot) error {
	t := c.table()
	fmt.Fprintln(t, "SNAPSHOT\tTIME\tCLASS\tUSED")
	for _, s := range ss {
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\n", s.Name, s.Time, s.Class, size(s.Used))
	}
	return t.Flush()
}

func (c *CLI) printSnapshots(b []byte) error {
	var r struct {
		Snapshots []snapshot `json:"snapshots"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	return c.snapRows(r.Snapshots)
}

func (c *CLI) printOneSnapshot(b []byte) error {
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	return c.snapRows([]snapshot{s})
}

func (c *CLI) printSettings(b []byte) error {
	var r struct {
		Snapshots struct {
			Hourly int `json:"hourly"`
			Daily  int `json:"daily"`
			Weekly int `json:"weekly"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "snapshots kept: hourly %d, daily %d, weekly %d\n", r.Snapshots.Hourly, r.Snapshots.Daily, r.Snapshots.Weekly)
	return nil
}

func (c *CLI) printAlerts(b []byte) error {
	var r struct {
		Alerts []struct {
			Level  string `json:"level"`
			Code   string `json:"code"`
			Object string `json:"object"`
			Text   string `json:"text"`
			Since  string `json:"since"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if len(r.Alerts) == 0 {
		fmt.Fprintln(c.Out, "no alerts")
		return nil
	}
	t := c.table()
	fmt.Fprintln(t, "LEVEL\tCODE\tOBJECT\tSINCE\tTEXT")
	for _, a := range r.Alerts {
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\n", a.Level, a.Code, a.Object, a.Since, a.Text)
	}
	return t.Flush()
}

func (c *CLI) printImportable(b []byte) error {
	var r struct {
		Pools []struct {
			Name      string `json:"name"`
			ID        string `json:"id"`
			State     string `json:"state"`
			Layout    string `json:"layout"`
			OtherHost bool   `json:"other_host"`
			LastHost  string `json:"last_host"`
			Size      int64  `json:"size"`
			Disks     []struct {
				Name, Device string
			} `json:"disks"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	t := c.table()
	fmt.Fprintln(t, "POOL\tID\tSTATE\tLAYOUT\tDISKS\tRAW\tLAST USED BY")
	for _, p := range r.Pools {
		var ds []string
		for _, d := range p.Disks {
			ds = append(ds, dash(d.Name))
		}
		last := "this system"
		if p.OtherHost {
			last = "another system " + p.LastHost
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, p.ID, p.State, p.Layout, strings.Join(ds, ","), size(p.Size), last)
	}
	return t.Flush()
}

func (c *CLI) printJSON(b []byte) error {
	var v any
	json.Unmarshal(b, &v)
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(c.Out, string(out))
	return nil
}

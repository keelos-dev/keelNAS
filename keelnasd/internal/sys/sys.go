// Package sys is the only way keelnasd touches the system: commands and files.
// Everything above it is tested on Linux with a fake.
package sys

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Sys runs commands and reads and writes files.
type Sys interface {
	// Run runs a command with stdin and returns its combined output.
	// A failure is a *CmdError carrying that output.
	Run(stdin, name string, args ...string) (string, error)
	ReadFile(path string) ([]byte, error)
	// WriteFile writes atomically (temporary file, then rename), making the directory first.
	WriteFile(path string, data []byte, perm os.FileMode) error
}

// HeadReader reads the first bytes of a disk device. Kept apart from Sys so that only the
// seed disk lookup needs it; Real and Fake implement both.
type HeadReader interface {
	// ReadHead returns up to n bytes from the start of /dev/<disk>; fewer only at the device's end.
	ReadHead(disk string, n int) ([]byte, error)
}

// CmdError is a failed command with its output.
type CmdError struct {
	Cmd string
	Out string
	Err error
}

func (e *CmdError) Error() string {
	out := strings.TrimSpace(e.Out)
	if out == "" {
		return fmt.Sprintf("%s: %v", e.Cmd, e.Err)
	}
	return fmt.Sprintf("%s: %v: %s", e.Cmd, e.Err, out)
}

// Cmdline is how a command is written in logs and errors.
func Cmdline(name string, args ...string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

// Real is the system itself.
type Real struct{}

// path is where commands are looked up; the daemon's own PATH from rc lacks /usr/local.
var path = []string{"/sbin", "/bin", "/usr/sbin", "/usr/bin", "/usr/local/sbin", "/usr/local/bin"}

func lookPath(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	for _, d := range path {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return name
}

func (Real) Run(stdin, name string, args ...string) (string, error) {
	c := exec.Command(lookPath(name), args...)
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	var b bytes.Buffer
	c.Stdout = &b
	c.Stderr = &b
	c.Env = []string{"PATH=" + strings.Join(path, ":"), "LANG=C"}
	if err := c.Run(); err != nil {
		return b.String(), &CmdError{Cmd: Cmdline(name, args...), Out: b.String(), Err: err}
	}
	return b.String(), nil
}

func (Real) ReadHead(disk string, n int) ([]byte, error) {
	f, err := os.Open("/dev/" + disk)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	k, err := io.ReadFull(f, b)
	if err == io.ErrUnexpectedEOF || (err == io.EOF && k == 0) {
		err = nil
	}
	return b[:k], err
}

func (Real) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (Real) WriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

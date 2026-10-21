package sys

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
)

// Fake records commands and answers them from handlers; files live in memory. For tests.
type Fake struct {
	mu    sync.Mutex
	Calls []string // command lines, in order
	Stdin []string // stdin of each call
	Files map[string][]byte
	// Heads are the disks' first bytes for ReadHead (disk name -> bytes); a missing disk reads as zeros.
	Heads map[string][]byte
	// Handle answers a command line (Cmdline form). ok=false means "not handled": empty output, success.
	Handle func(cmdline, stdin string) (out string, err error, ok bool)
}

func NewFake() *Fake { return &Fake{Files: map[string][]byte{}, Heads: map[string][]byte{}} }

func (f *Fake) Run(stdin, name string, args ...string) (string, error) {
	line := Cmdline(name, args...)
	f.mu.Lock()
	f.Calls = append(f.Calls, line)
	f.Stdin = append(f.Stdin, stdin)
	h := f.Handle
	f.mu.Unlock()
	if h != nil {
		if out, err, ok := h(line, stdin); ok {
			if err != nil {
				return out, &CmdError{Cmd: line, Out: out, Err: err}
			}
			return out, nil
		}
	}
	return "", nil
}

func (f *Fake) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.Files[path]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	return append([]byte(nil), b...), nil
}

func (f *Fake) ReadHead(disk string, n int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.Heads[disk]
	if !ok {
		return make([]byte, n), nil
	}
	if len(b) > n {
		b = b[:n]
	}
	return append([]byte(nil), b...), nil
}

func (f *Fake) WriteFile(path string, data []byte, _ os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Files[path] = append([]byte(nil), data...)
	return nil
}

// Ran reports whether a command line starting with prefix was run.
func (f *Fake) Ran(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// Reset forgets the recorded calls.
func (f *Fake) Reset() {
	f.mu.Lock()
	f.Calls, f.Stdin = nil, nil
	f.mu.Unlock()
}

// ErrFail is a ready-made command failure for handlers.
var ErrFail = errors.New("exit status 1")

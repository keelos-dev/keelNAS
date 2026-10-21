package sys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"syscall"
)

// TreeEntry is one entry WalkTree visits: its path relative to the root ("." = the root) and
// its Lstat.
type TreeEntry struct {
	Rel  string
	Info fs.FileInfo
}

// devOf is the device of a file (one ZFS dataset = one device).
func devOf(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// WalkTree visits root and everything below it on the same file system, through os.Root: never
// following a symbolic link (links are visited, not entered), never leaving root, never going
// into another file system mounted below (a child dataset: its mountpoint is not visited either).
// skipTop: names directly below root that are left out with all they hold (.zfs, .keelnas).
// visit's error stops the walk only when stop is returned; others are collected (at most 20)
// and the walk goes on. A directory that cannot be read is such an error too.
func WalkTree(root string, skipTop []string, visit func(r *os.Root, e TreeEntry) error) (errs []error, err error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	top, err := r.Lstat(".")
	if err != nil {
		return nil, err
	}
	dev, ok := devOf(top)
	if !ok {
		return nil, errors.New("no device number for " + root)
	}
	note := func(e error) {
		if len(errs) < 20 {
			errs = append(errs, e)
		}
	}
	skip := map[string]bool{}
	for _, n := range skipTop {
		skip[n] = true
	}
	var walk func(rel string, fi fs.FileInfo) error
	walk = func(rel string, fi fs.FileInfo) error {
		if err := visit(r, TreeEntry{rel, fi}); err != nil {
			if errors.Is(err, ErrStop) {
				return err
			}
			note(fmt.Errorf("%s: %w", rel, err))
		}
		if !fi.IsDir() {
			return nil
		}
		d, err := r.Open(rel)
		if err != nil {
			note(fmt.Errorf("%s: %w", rel, err))
			return nil
		}
		ents, err := d.ReadDir(-1)
		d.Close()
		if err != nil {
			note(fmt.Errorf("%s: %w", rel, err))
		}
		for _, de := range ents {
			if rel == "." && skip[de.Name()] {
				continue
			}
			p := pathpkg.Join(rel, de.Name())
			ci, err := r.Lstat(p)
			if err != nil {
				note(fmt.Errorf("%s: %w", p, err))
				continue
			}
			if d, ok := devOf(ci); !ok || d != dev {
				continue // another file system (a child dataset's mountpoint): not ours to walk
			}
			if err := walk(p, ci); err != nil {
				return err
			}
		}
		return nil
	}
	return errs, walk(".", top)
}

// ErrStop from a visit function stops WalkTree (a job that was cancelled).
var ErrStop = errors.New("stopped")

// ChownEntry gives one entry the owner uid:gid and, unless it is a symbolic link, the mode
// (dirMode for directories, fileMode for regular files; other kinds keep theirs). Links are
// changed with lchown, never followed.
func ChownEntry(r *os.Root, e TreeEntry, uid, gid int, dirMode, fileMode os.FileMode) error {
	if err := r.Lchown(e.Rel, uid, gid); err != nil {
		return err
	}
	switch {
	case e.Info.Mode()&os.ModeSymlink != 0:
		return nil
	case e.Info.IsDir():
		return r.Chmod(e.Rel, dirMode)
	case e.Info.Mode().IsRegular():
		return r.Chmod(e.Rel, fileMode)
	}
	return nil
}

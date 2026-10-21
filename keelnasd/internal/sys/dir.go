package sys

import (
	"errors"
	"os"
	pathpkg "path"
	"slices"
	"strings"
	"time"
)

// DirEntry is one entry of a directory listing (ListDir).
type DirEntry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Link  bool   `json:"link"` // a symbolic link: listed, never followed
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"` // RFC 3339, UTC
}

var (
	// ErrEscape: the path leaves the root (.., or a symbolic link pointing outside).
	ErrEscape = errors.New("the path leaves the dataset")
	// ErrNotDir: the path is not a directory.
	ErrNotDir = errors.New("not a directory")
)

// CleanRel checks a path relative to a root: no "..", no NUL; leading and trailing slashes
// are dropped; "" is the root itself.
func CleanRel(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", ErrEscape
	}
	rel = strings.Trim(rel, "/")
	for _, el := range strings.Split(rel, "/") {
		if el == ".." {
			return "", ErrEscape
		}
	}
	if rel == "" {
		return ".", nil
	}
	return pathpkg.Clean(rel), nil
}

// ListDir lists the directory rel below root without leaving root: everything is opened
// through os.Root, so "..", absolute symbolic links and links that point outside fail with
// ErrEscape; links inside are listed as links (Lstat). At most max entries (directories first,
// then by name); more reports truncated.
func ListDir(root, rel string, max int) (ents []DirEntry, truncated bool, err error) {
	rel, err = CleanRel(rel)
	if err != nil {
		return nil, false, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	f, err := r.Open(rel)
	if err != nil {
		return nil, false, rootErr(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !fi.IsDir() {
		return nil, false, ErrNotDir
	}
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, false, err
	}
	ents = []DirEntry{}
	for _, de := range des {
		e := DirEntry{Name: de.Name(), Dir: de.IsDir(), Link: de.Type()&os.ModeSymlink != 0}
		if li, err := r.Lstat(pathpkg.Join(rel, de.Name())); err == nil {
			e.Dir = li.IsDir()
			e.Link = li.Mode()&os.ModeSymlink != 0
			if !e.Dir {
				e.Size = li.Size()
			}
			e.Mtime = li.ModTime().UTC().Format(time.RFC3339)
		}
		ents = append(ents, e)
	}
	slices.SortFunc(ents, func(a, b DirEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	if max > 0 && len(ents) > max {
		return ents[:max], true, nil
	}
	return ents, false, nil
}

// rootErr turns os.Root's "path escapes from parent" into ErrEscape.
func rootErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "escapes") {
		return ErrEscape
	}
	return err
}

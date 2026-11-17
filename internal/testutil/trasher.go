package testutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DirTrasher is a test double for the Trash: it renames items into Dir,
// de-duplicating names the way Finder does ("name 2", "name 3", and for files
// "report 2.pdf"). It never follows symlinks: a symlink is moved as itself.
type DirTrasher struct {
	Dir string
}

// Trash moves path into d.Dir and returns where it landed.
func (d DirTrasher) Trash(path string) (string, error) {
	if d.Dir == "" {
		return "", errors.New("DirTrasher: Dir is empty")
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return "", err
	}
	base := filepath.Base(path)
	stem, ext := base, ""
	if fi.Mode().IsRegular() {
		if e := filepath.Ext(base); e != "" && e != base {
			stem, ext = strings.TrimSuffix(base, e), e
		}
	}
	for i := 1; i < 10000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s %d%s", stem, i, ext)
		}
		dest := filepath.Join(d.Dir, name)
		if _, err := os.Lstat(dest); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if err := os.Rename(path, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("DirTrasher: too many items named %q", base)
}

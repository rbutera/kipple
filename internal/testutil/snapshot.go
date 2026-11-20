package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Entry is one node of a fixture tree as read back from disk with lstat.
type Entry struct {
	Path   string // root-relative, slash-separated
	Type   string // "file", "dir", "symlink", "other"
	Size   int64  // files only
	Mode   fs.FileMode
	MTime  time.Time
	Target string // symlinks only
	SHA256 string // files only
}

// Snapshot walks root with lstat (never following symlinks) and returns every
// entry below it in lexical order.
func Snapshot(t testing.TB, root string) []Entry {
	t.Helper()
	var out []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		e := Entry{Path: filepath.ToSlash(rel), Mode: fi.Mode(), MTime: fi.ModTime()}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			e.Type = "symlink"
			if e.Target, err = os.Readlink(p); err != nil {
				return err
			}
		case fi.IsDir():
			e.Type = "dir"
		case fi.Mode().IsRegular():
			e.Type = "file"
			e.Size = fi.Size()
			if e.SHA256, err = hashFile(p); err != nil {
				return err
			}
		default:
			e.Type = "other"
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		t.Fatalf("testutil.Snapshot: %v", err)
	}
	return out
}

// Checksum is a digest of Snapshot: paths, types, sizes, modes, mtimes,
// symlink targets and file contents. Equal checksums mean the tree is unchanged.
func Checksum(t testing.TB, root string) string {
	t.Helper()
	h := sha256.New()
	for _, e := range Snapshot(t, root) {
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%o\x00%d\x00%s\x00%s\n",
			e.Path, e.Type, e.Size, uint32(e.Mode), e.MTime.UnixNano(), e.Target, e.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ContentChecksum is Checksum without directory modification times, and
// without the entries at or below the root-relative slash paths in skip.
// Moving an item out and back changes its parent folders' mtimes, which
// nothing in kipple promises to preserve; every file's content, size, mode
// and mtime, every directory's existence and mode, and every symlink target
// must still match.
func ContentChecksum(t testing.TB, root string, skip ...string) string {
	t.Helper()
	h := sha256.New()
	for _, e := range Snapshot(t, root) {
		skipped := false
		for _, s := range skip {
			skipped = skipped || e.Path == s || strings.HasPrefix(e.Path, s+"/")
		}
		if skipped {
			continue
		}
		mtime := e.MTime.UnixNano()
		if e.Type == "dir" {
			mtime = 0
		}
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%o\x00%d\x00%s\x00%s\n",
			e.Path, e.Type, e.Size, uint32(e.Mode), mtime, e.Target, e.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

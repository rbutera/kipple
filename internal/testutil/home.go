package testutil

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Spec declares a fixture home folder. All paths are home-relative and
// slash-separated; absolute paths and ".." segments are rejected so a fixture
// can never escape its temp directory.
//
// Build order: Dirs, Repos, Files, Hardlinks, Symlinks, then Repo.MTime ageing,
// then explicit File and Dir mtimes (deepest directories last-set first), then
// Unreadable permissions.
type Spec struct {
	Dirs      []Dir
	Files     []File
	Symlinks  []Symlink
	Hardlinks []Hardlink
	Repos     []Repo
}

// File is a regular file. Content wins over Size; with only Size the file is
// filled with deterministic non-zero bytes (so it is not sparse).
type File struct {
	Path       string
	Content    string
	Size       int64
	MTime      time.Time   // zero: leave as created
	Mode       fs.FileMode // zero: 0o644
	Unreadable bool        // chmod 0o000 at the end of the build
}

// Dir is a directory. Parents of every entry are created automatically.
type Dir struct {
	Path       string
	MTime      time.Time
	Mode       fs.FileMode // zero: 0o755
	Unreadable bool        // chmod 0o000 at the end of the build (restored on cleanup)
}

// Symlink creates Path pointing at Target. A Target starting with "~/" is
// expanded to the fixture home; anything else is written verbatim.
type Symlink struct {
	Path   string
	Target string
}

// Hardlink creates Path as a second directory entry for the existing file Target.
type Hardlink struct {
	Path   string
	Target string
}

// Repo is a git working tree at Path.
type Repo struct {
	Path    string
	Branch  string   // default "main"
	Commits []Commit // committed, and pushed when Pushed is set
	// Upstream creates a bare repository outside the fixture home (see
	// UpstreamDir) and adds it as remote "origin".
	Upstream bool
	// Pushed pushes Commits to the upstream and sets tracking, giving git
	// state clean_pushed. Requires Upstream and at least one commit.
	Pushed bool
	// LocalCommits are committed after the push and never pushed (unpushed state).
	LocalCommits []Commit
	// MTime, if set, is applied to every entry in the working tree (including
	// .git) after the build, so the project looks idle.
	MTime time.Time
}

// Commit writes Files then commits them with Message.
type Commit struct {
	Message string
	Files   []File
	Time    time.Time // zero: a fixed date plus one minute per commit
}

// NewHome builds spec inside a fresh t.TempDir and returns the fixture home.
// It sets KIPPLE_HOME to the home and KIPPLE_STATE_DIR to a sibling state
// directory (outside the home, so receipts never change a fixture checksum),
// then runs GuardRealHome. Paths are symlink-resolved so the returned home
// has no symlinked ancestors (macOS /var -> /private/var).
func NewHome(t testing.TB, spec Spec) string {
	t.Helper()
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	home := filepath.Join(base, "home")
	state := filepath.Join(base, "state")
	for _, d := range []string{home, state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("testutil.NewHome: %v", err)
		}
	}
	t.Setenv("KIPPLE_HOME", home)
	t.Setenv("KIPPLE_STATE_DIR", state)
	GuardRealHome(t)

	restore, err := build(home, spec)
	t.Cleanup(restore)
	if err != nil {
		t.Fatalf("testutil.NewHome: %v", err)
	}
	return home
}

// StateDir returns the state directory NewHome created beside home.
func StateDir(home string) string {
	return filepath.Join(filepath.Dir(home), "state")
}

// UpstreamDir returns the bare upstream repository NewHome creates for the
// repo at the home-relative path repoPath. It lives outside the fixture home.
func UpstreamDir(home, repoPath string) string {
	name := strings.ReplaceAll(filepath.ToSlash(filepath.Clean(repoPath)), "/", "__") + ".git"
	return filepath.Join(filepath.Dir(home), "upstreams", name)
}

// Abs joins a home-relative slash path onto home.
func Abs(home, rel string) string {
	return filepath.Join(home, filepath.FromSlash(rel))
}

// resolve validates a spec path and returns its absolute location.
func resolve(home, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path in spec")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "~") {
		return "", fmt.Errorf("spec path %q must be home-relative", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", fmt.Errorf("spec path %q must not contain a parent (..) segment", rel)
		}
	}
	if path.Clean(rel) != rel {
		return "", fmt.Errorf("spec path %q is not clean", rel)
	}
	return Abs(home, rel), nil
}

// build materialises spec under home. The returned func restores permissions
// of Unreadable entries so t.TempDir cleanup can remove them.
func build(home string, spec Spec) (func(), error) {
	var unreadable []string
	restore := func() {
		for _, p := range unreadable {
			_ = os.Chmod(p, 0o755)
		}
	}

	for _, d := range spec.Dirs {
		p, err := resolve(home, d.Path)
		if err != nil {
			return restore, err
		}
		mode := d.Mode
		if mode == 0 {
			mode = 0o755
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return restore, err
		}
		if err := os.Chmod(p, mode.Perm()); err != nil {
			return restore, err
		}
	}

	for _, r := range spec.Repos {
		if err := buildRepo(home, r); err != nil {
			return restore, fmt.Errorf("repo %s: %w", r.Path, err)
		}
	}

	for _, f := range spec.Files {
		if err := writeFile(home, f); err != nil {
			return restore, err
		}
	}

	for _, h := range spec.Hardlinks {
		p, err := resolve(home, h.Path)
		if err != nil {
			return restore, err
		}
		target, err := resolve(home, h.Target)
		if err != nil {
			return restore, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return restore, err
		}
		if err := os.Link(target, p); err != nil {
			return restore, err
		}
	}

	for _, s := range spec.Symlinks {
		p, err := resolve(home, s.Path)
		if err != nil {
			return restore, err
		}
		target := s.Target
		if strings.HasPrefix(target, "~/") {
			if target, err = resolve(home, strings.TrimPrefix(target, "~/")); err != nil {
				return restore, err
			}
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return restore, err
		}
		if err := os.Symlink(target, p); err != nil {
			return restore, err
		}
	}

	for _, r := range spec.Repos {
		if r.MTime.IsZero() {
			continue
		}
		root, _ := resolve(home, r.Path)
		if err := ageTree(root, r.MTime); err != nil {
			return restore, err
		}
	}

	for _, f := range spec.Files {
		if f.MTime.IsZero() {
			continue
		}
		p, _ := resolve(home, f.Path)
		if err := os.Chtimes(p, f.MTime, f.MTime); err != nil {
			return restore, err
		}
	}

	dirs := append([]Dir(nil), spec.Dirs...)
	sort.SliceStable(dirs, func(i, j int) bool {
		return strings.Count(dirs[i].Path, "/") > strings.Count(dirs[j].Path, "/")
	})
	for _, d := range dirs {
		if d.MTime.IsZero() {
			continue
		}
		p, _ := resolve(home, d.Path)
		if err := os.Chtimes(p, d.MTime, d.MTime); err != nil {
			return restore, err
		}
	}

	for _, f := range spec.Files {
		if f.Unreadable {
			p, _ := resolve(home, f.Path)
			unreadable = append(unreadable, p)
		}
	}
	for _, d := range spec.Dirs {
		if d.Unreadable {
			p, _ := resolve(home, d.Path)
			unreadable = append(unreadable, p)
		}
	}
	for _, p := range unreadable {
		if err := os.Chmod(p, 0); err != nil {
			return restore, err
		}
	}
	return restore, nil
}

func writeFile(home string, f File) error {
	p, err := resolve(home, f.Path)
	if err != nil {
		return err
	}
	if f.Content != "" && f.Size != 0 && int64(len(f.Content)) != f.Size {
		return fmt.Errorf("file %s: Content length %d disagrees with Size %d", f.Path, len(f.Content), f.Size)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	mode := f.Mode
	if mode == 0 {
		mode = 0o644
	}
	fh, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(fh)
	if f.Content != "" {
		_, err = w.WriteString(f.Content)
	} else {
		err = writeFiller(w, f.Size)
	}
	if err == nil {
		err = w.Flush()
	}
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("file %s: %w", f.Path, err)
	}
	return os.Chmod(p, mode.Perm())
}

// writeFiller writes n deterministic, non-zero bytes.
func writeFiller(w *bufio.Writer, n int64) error {
	const pattern = "kipple-fixture-0123456789abcdefghijklmnopqrstuvwxyz\n"
	for n > 0 {
		chunk := int64(len(pattern))
		if n < chunk {
			chunk = n
		}
		if _, err := w.WriteString(pattern[:chunk]); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}

// ageTree sets the mtime of every non-symlink entry under root.
func ageTree(root string, t time.Time) error {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink == 0 {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Children before parents, so setting a child does not bump the parent.
	for i := len(paths) - 1; i >= 0; i-- {
		if err := os.Chtimes(paths[i], t, t); err != nil {
			return err
		}
	}
	return nil
}

//go:build unix

package trash

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

// parentFd opens dir for a test and closes it when the test ends.
func parentFd(t *testing.T, dir string) int {
	t.Helper()
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	return fd
}

func trashDir(t *testing.T, home string) *DirTrasher {
	t.Helper()
	dir := filepath.Join(filepath.Dir(home), "trash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tr, err := OpenDirTrasher(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func TestDirTrasher(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Downloads/a.dmg", Content: "1"},
			{Path: "x/a.dmg", Content: "2"},
			{Path: "y/a.dmg", Content: "3"},
			{Path: "Library/Caches/one/cache/f", Content: "d1"},
			{Path: "Library/Caches/two/cache/f", Content: "d2"},
			{Path: "Library/Caches/three/cache/f", Content: "d3"},
			{Path: "Documents/secret", Content: "s"},
			{Path: "x/.hidden", Content: "h"},
		},
		Symlinks: []testutil.Symlink{{Path: "Library/Caches/link", Target: "~/Documents"}},
	})
	tr := trashDir(t, home)
	cases := []struct{ name, src, want string }{
		{"first file keeps its name", "Downloads/a.dmg", "a.dmg"},
		{"second file gets 2 before the extension", "x/a.dmg", "a 2.dmg"},
		{"third file gets 3", "y/a.dmg", "a 3.dmg"},
		{"first dir", "Library/Caches/one/cache", "cache"},
		{"second dir gets 2", "Library/Caches/two/cache", "cache 2"},
		{"third dir gets 3", "Library/Caches/three/cache", "cache 3"},
		{"dotfile has no extension", "x/.hidden", ".hidden"},
		{"symlink is moved as itself", "Library/Caches/link", "link"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := testutil.Abs(home, tc.src)
			before, err := os.Lstat(src)
			if err != nil {
				t.Fatal(err)
			}
			dest, err := tr.Trash(parentFd(t, filepath.Dir(src)), filepath.Base(src))
			if err != nil {
				t.Fatal(err)
			}
			if dest != filepath.Join(tr.Path(), tc.want) {
				t.Fatalf("dest = %q, want %q", dest, filepath.Join(tr.Path(), tc.want))
			}
			if _, err := os.Lstat(src); !os.IsNotExist(err) {
				t.Fatalf("source still present: %v", err)
			}
			after, err := os.Lstat(dest)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("the trashed object is not the original: %v", err)
			}
		})
	}
	if _, err := os.Stat(testutil.Abs(home, "Documents/secret")); err != nil {
		t.Fatalf("symlink target was disturbed: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(tr.Path(), "link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("trashed symlink is not a symlink: %v", err)
	}
	t.Run("missing item errors", func(t *testing.T) {
		if _, err := tr.Trash(parentFd(t, home), "missing"); !errors.Is(err, unix.ENOENT) {
			t.Fatalf("err = %v, want ENOENT", err)
		}
	})
	t.Run("invalid names are refused", func(t *testing.T) {
		for _, n := range []string{"", ".", "..", "a/b"} {
			if _, err := tr.Trash(parentFd(t, home), n); !errors.Is(err, ErrInvalidName) {
				t.Errorf("Trash(%q) = %v, want ErrInvalidName", n, err)
			}
		}
	})
}

func TestTrashFolderSwappedForSymlink(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/a/f", Content: "x"}}})
	tr := trashDir(t, home)
	elsewhere := filepath.Join(filepath.Dir(home), "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tr.Path(), tr.Path()+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, tr.Path()); err != nil {
		t.Fatal(err)
	}
	_, err := tr.Trash(parentFd(t, testutil.Abs(home, "Library/Caches")), "a")
	if !errors.Is(err, ErrTrashChanged) {
		t.Fatalf("err = %v, want ErrTrashChanged", err)
	}
	if _, err := os.Lstat(testutil.Abs(home, "Library/Caches/a")); err != nil {
		t.Fatalf("item moved despite the refusal: %v", err)
	}
}

func TestOpenDirTrasherRefusesSymlinks(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Dirs:     []testutil.Dir{{Path: "real/trash"}},
		Symlinks: []testutil.Symlink{{Path: "link", Target: "~/real"}},
	})
	for _, p := range []string{testutil.Abs(home, "link"), testutil.Abs(home, "link/trash")} {
		if tr, err := OpenDirTrasher(p); err == nil {
			_ = tr.Close()
			t.Errorf("OpenDirTrasher(%s) followed a symlink", p)
		}
	}
	if _, err := OpenDirTrasher("relative"); err == nil {
		t.Error("relative path accepted")
	}
}

func TestMoveNoReplace(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "src/file", Content: "new"},
			{Path: "src/dir/f", Content: "new"},
			{Path: "dst/file", Content: "old"},
			{Path: "dst/dir/f", Content: "old"},
			{Path: "target/x", Content: "t"},
		},
		Symlinks: []testutil.Symlink{{Path: "dst/link", Target: "~/target"}},
	})
	src, dst := parentFd(t, testutil.Abs(home, "src")), parentFd(t, testutil.Abs(home, "dst"))
	for _, tc := range []struct{ from, to string }{{"file", "file"}, {"dir", "dir"}, {"file", "link"}, {"dir", "link"}} {
		if err := MoveNoReplace(src, tc.from, dst, tc.to); !errors.Is(err, unix.EEXIST) {
			t.Errorf("MoveNoReplace(%s -> %s) = %v, want EEXIST", tc.from, tc.to, err)
		}
	}
	if b, _ := os.ReadFile(testutil.Abs(home, "dst/file")); string(b) != "old" {
		t.Fatal("destination file was replaced")
	}
	if b, _ := os.ReadFile(testutil.Abs(home, "target/x")); string(b) != "t" {
		t.Fatal("symlink target was disturbed")
	}
	if err := MoveNoReplace(src, "file", dst, "fresh"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(testutil.Abs(home, "dst/fresh")); string(b) != "new" {
		t.Fatal("vacant destination not written")
	}
	if err := MoveNoReplace(src, "..", dst, "x"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
}

//go:build linux

package trash

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/testutil"
)

// fdTrash opens a fixture's freedesktop Trash.
func fdTrash(t *testing.T, home string) *FreedesktopTrasher {
	t.Helper()
	root := testutil.Abs(home, ".local/share/Trash")
	for _, d := range []string{root + "/files", root + "/info"} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.Open(root + "/files")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = files.Close() }()
	info, err := os.Open(root + "/info")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = info.Close() }()
	tr, err := NewFreedesktopTrasher(root, files, info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func identity(t *testing.T, p string) (uint64, uint64) {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	return st.Dev, st.Ino
}

func readInfo(t *testing.T, p string) TrashInfo {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	info, err := DecodeTrashInfo(b)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return info
}

// One case per specs/clean-apply "Freedesktop Trash entries on Linux"
// scenario, at the Trasher level.
func TestFreedesktopTrasher(t *testing.T) {
	t.Run("Trash info written", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: ".cache/pip/http/f", Content: "x"}}})
		tr := fdTrash(t, home)
		fixed := time.Date(2026, 11, 21, 10, 30, 0, 0, time.Local)
		tr.now = func() time.Time { return fixed }
		dev, ino := identity(t, testutil.Abs(home, ".cache/pip/http"))
		dest, err := tr.TrashExpect(parentFd(t, testutil.Abs(home, ".cache/pip")), "http", dev, ino)
		if err != nil {
			t.Fatal(err)
		}
		if dest != testutil.Abs(home, ".local/share/Trash/files/http") {
			t.Fatalf("dest %q", dest)
		}
		if d, i := identity(t, dest); d != dev || i != ino {
			t.Fatal("the object in the Trash is not the item")
		}
		infoPath := testutil.Abs(home, ".local/share/Trash/info/http.trashinfo")
		raw, _ := os.ReadFile(infoPath)
		want := "[Trash Info]\nPath=" + testutil.Abs(home, ".cache/pip/http") + "\nDeletionDate=2026-11-21T10:30:00\n"
		if string(raw) != want {
			t.Fatalf("info file %q, want %q", raw, want)
		}
		if fi, _ := os.Stat(infoPath); fi.Mode().Perm() != 0o600 {
			t.Fatalf("info file mode %v, want 0600", fi.Mode().Perm())
		}
	})
	t.Run("Name collision in the Trash", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
			{Path: ".cache/pip/http/f", Content: "new"},
			{Path: ".local/share/Trash/files/http/old", Content: "old"},
			{Path: ".local/share/Trash/info/http.trashinfo", Content: "[Trash Info]\nPath=/elsewhere/http\nDeletionDate=2026-01-01T00:00:00\n"},
			{Path: ".cache/x/report.pdf", Content: "r1"},
			{Path: ".local/share/Trash/files/report.pdf", Content: "r0"},
			{Path: ".local/share/Trash/info/report.pdf.trashinfo", Content: "[Trash Info]\nPath=/elsewhere/report.pdf\nDeletionDate=2026-01-01T00:00:00\n"},
		}})
		tr := fdTrash(t, home)
		before := testutil.Checksum(t, testutil.Abs(home, ".local/share/Trash/files/http"))
		dest, err := tr.Trash(parentFd(t, testutil.Abs(home, ".cache/pip")), "http")
		if err != nil || dest != testutil.Abs(home, ".local/share/Trash/files/http 2") {
			t.Fatalf("dest %q, %v", dest, err)
		}
		if got := readInfo(t, testutil.Abs(home, ".local/share/Trash/info/http 2.trashinfo")); got.Path != testutil.Abs(home, ".cache/pip/http") {
			t.Fatalf("info Path %q", got.Path)
		}
		if testutil.Checksum(t, testutil.Abs(home, ".local/share/Trash/files/http")) != before {
			t.Fatal("the existing entry was touched")
		}
		if got := readInfo(t, testutil.Abs(home, ".local/share/Trash/info/http.trashinfo")); got.Path != "/elsewhere/http" {
			t.Fatal("the existing info file was touched")
		}
		dest, err = tr.Trash(parentFd(t, testutil.Abs(home, ".cache/x")), "report.pdf")
		if err != nil || dest != testutil.Abs(home, ".local/share/Trash/files/report 2.pdf") {
			t.Fatalf("file collision: dest %q, %v", dest, err)
		}
		if _, err := os.Stat(testutil.Abs(home, ".local/share/Trash/info/report 2.pdf.trashinfo")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Orphan file without info", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
			{Path: ".cache/pip/http/f", Content: "new"},
			{Path: ".local/share/Trash/files/http/orphan", Content: "orphan"},
		}})
		tr := fdTrash(t, home)
		dest, err := tr.Trash(parentFd(t, testutil.Abs(home, ".cache/pip")), "http")
		if err != nil || dest != testutil.Abs(home, ".local/share/Trash/files/http 2") {
			t.Fatalf("dest %q, %v", dest, err)
		}
		if b, _ := os.ReadFile(testutil.Abs(home, ".local/share/Trash/files/http/orphan")); string(b) != "orphan" {
			t.Fatal("the orphan was touched")
		}
		if _, err := os.Lstat(testutil.Abs(home, ".local/share/Trash/info/http.trashinfo")); !os.IsNotExist(err) {
			t.Fatalf("an info file for the orphan's name was left behind: %v", err)
		}
		if _, err := os.Stat(testutil.Abs(home, ".local/share/Trash/info/http 2.trashinfo")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Identity mismatch moves nothing and writes no info", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a/f", Content: "x"}, {Path: "c/b/f", Content: "y"}}})
		tr := fdTrash(t, home)
		dev, ino := identity(t, testutil.Abs(home, "c/b"))
		if _, err := tr.TrashExpect(parentFd(t, testutil.Abs(home, "c")), "a", dev, ino); !errors.Is(err, ErrItemChanged) {
			t.Fatalf("err = %v", err)
		}
		if entries, _ := os.ReadDir(testutil.Abs(home, ".local/share/Trash/info")); len(entries) != 0 {
			t.Fatalf("info written: %v", entries)
		}
	})
	t.Run("Swap during the move is moved back and its info removed", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a/f", Content: "approved"}, {Path: "decoy/f", Content: "decoy"}}})
		tr := fdTrash(t, home)
		dev, ino := identity(t, testutil.Abs(home, "c/a"))
		tr.beforeRename = func(string) {
			swap(t, testutil.Abs(home, "c/a"), testutil.Abs(home, "decoy"))
		}
		if _, err := tr.TrashExpect(parentFd(t, testutil.Abs(home, "c")), "a", dev, ino); !errors.Is(err, ErrItemChanged) {
			t.Fatalf("err = %v, want ErrItemChanged", err)
		}
		if b, _ := os.ReadFile(testutil.Abs(home, "c/a/f")); string(b) != "decoy" {
			t.Fatal("the swapped-in object was not put back")
		}
		for _, d := range []string{"files", "info"} {
			if entries, _ := os.ReadDir(testutil.Abs(home, ".local/share/Trash/"+d)); len(entries) != 0 {
				t.Fatalf("%s left: %v", d, entries)
			}
		}
	})
	t.Run("Displaced entry keeps its info file", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a/f", Content: "approved"}, {Path: "decoy/f", Content: "decoy"}, {Path: "blocker/f", Content: "b"}}})
		tr := fdTrash(t, home)
		dev, ino := identity(t, testutil.Abs(home, "c/a"))
		tr.beforeRename = func(string) { swap(t, testutil.Abs(home, "c/a"), testutil.Abs(home, "decoy")) }
		// Something takes the name back before the move back: the
		// displaced object stays in the Trash.
		tr.afterRename = func(string) {
			if err := os.Rename(testutil.Abs(home, "blocker"), testutil.Abs(home, "c/a")); err != nil {
				t.Fatal(err)
			}
		}
		dest, err := tr.TrashExpect(parentFd(t, testutil.Abs(home, "c")), "a", dev, ino)
		var de *DisplacedError
		if !errors.As(err, &de) || dest != testutil.Abs(home, ".local/share/Trash/files/a") || !de.Known {
			t.Fatalf("dest %q err %v, want a DisplacedError", dest, err)
		}
		if got := readInfo(t, testutil.Abs(home, ".local/share/Trash/info/a.trashinfo")); got.Path != testutil.Abs(home, "c/a") {
			t.Fatalf("info Path %q", got.Path)
		}
	})
	t.Run("Cross-device item is refused without copying", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{})
		tr := fdTrash(t, home)
		other := testutil.OtherDevice(t, home)
		if err := os.WriteFile(filepath.Join(other, "x"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Trash(parentFd(t, other), "x"); !errors.Is(err, ErrDifferentDevice) {
			t.Fatalf("err = %v, want ErrDifferentDevice", err)
		}
		if _, err := os.Stat(filepath.Join(other, "x")); err != nil {
			t.Fatal("source not intact")
		}
		for _, d := range []string{"files", "info"} {
			if entries, _ := os.ReadDir(testutil.Abs(home, ".local/share/Trash/"+d)); len(entries) != 0 {
				t.Fatalf("%s written: %v", d, entries)
			}
		}
	})
	t.Run("No RENAME_NOREPLACE removes the reservation", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a", Content: "x"}}})
		tr := fdTrash(t, home)
		renameat2 = func(int, string, int, string, uint) error { return syscall.EINVAL }
		t.Cleanup(func() { renameat2 = realRenameat2 })
		if _, err := tr.Trash(parentFd(t, testutil.Abs(home, "c")), "a"); !errors.Is(err, ErrNoReplaceUnsupported) {
			t.Fatalf("err = %v", err)
		}
		if entries, _ := os.ReadDir(testutil.Abs(home, ".local/share/Trash/info")); len(entries) != 0 {
			t.Fatalf("info left: %v", entries)
		}
	})
	t.Run("Trash info folder swapped is refused", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a", Content: "x"}}})
		tr := fdTrash(t, home)
		info := testutil.Abs(home, ".local/share/Trash/info")
		if err := os.Rename(info, info+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(info, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Trash(parentFd(t, testutil.Abs(home, "c")), "a"); !errors.Is(err, ErrTrashChanged) {
			t.Fatalf("err = %v, want ErrTrashChanged", err)
		}
	})
}

// RemoveInfo removes only a regular info file whose Path is the original.
func TestFreedesktopRemoveInfo(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
		{Path: ".local/share/Trash/info/ok.trashinfo", Content: "[Trash Info]\nPath=/home/me/.cache/ok\nDeletionDate=2026-11-21T10:30:00\n"},
		{Path: ".local/share/Trash/info/other.trashinfo", Content: "[Trash Info]\nPath=/home/me/.cache/elsewhere\nDeletionDate=2026-11-21T10:30:00\n"},
		{Path: ".local/share/Trash/info/bad.trashinfo", Content: "garbage"},
		{Path: "target", Content: "[Trash Info]\nPath=/home/me/.cache/link\nDeletionDate=2026-11-21T10:30:00\n"},
	}, Symlinks: []testutil.Symlink{{Path: ".local/share/Trash/info/link.trashinfo", Target: "~/target"}}})
	tr := fdTrash(t, home)
	info := func(n string) string { return testutil.Abs(home, ".local/share/Trash/info/"+n+".trashinfo") }
	if ok, err := tr.RemoveInfo("ok", "/home/me/.cache/ok", 0); !ok || err != nil {
		t.Fatalf("matching info: %v %v", ok, err)
	}
	if _, err := os.Lstat(info("ok")); !os.IsNotExist(err) {
		t.Fatal("matching info not removed")
	}
	for _, n := range []string{"other", "bad", "link"} {
		ok, err := tr.RemoveInfo(n, "/home/me/.cache/"+n, 0)
		if ok || err == nil || !strings.Contains(err.Error(), "left in place") {
			t.Errorf("%s: %v %v, want a refusal", n, ok, err)
		}
		if _, err := os.Lstat(info(n)); err != nil {
			t.Errorf("%s was removed", n)
		}
	}
	// With an inode recorded, only that file is removed.
	mine := info("other")
	_, otherIno := identity(t, mine)
	if ok, err := tr.RemoveInfo("other", "/home/me/.cache/elsewhere", otherIno+1); ok || err == nil || !exists(mine) {
		t.Fatalf("an info file with another inode: %v %v", ok, err)
	}
	if ok, err := tr.RemoveInfo("other", "/home/me/.cache/elsewhere", otherIno); !ok || err != nil || exists(mine) {
		t.Fatalf("the recorded inode: %v %v", ok, err)
	}
	if ok, err := tr.RemoveInfo("absent", "/x", 0); ok || err != nil {
		t.Fatalf("absent info: %v %v", ok, err)
	}
}

// swap exchanges two directory entries through a temporary name.
func swap(t *testing.T, a, b string) {
	t.Helper()
	tmp := a + ".swap"
	for _, r := range [][2]string{{a, tmp}, {b, a}, {tmp, b}} {
		if err := os.Rename(r[0], r[1]); err != nil {
			t.Fatal(err)
		}
	}
}

// Should-fix from the wave 1 review: the info file records the parent the
// caller verified, and a parent moved before the reservation or during the
// rename means the item stays (or is put back) and no info file is left.
func TestFreedesktopParentMoved(t *testing.T) {
	t.Run("Parent moved before the move", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a/f", Content: "x"}}})
		tr := fdTrash(t, home)
		dev, ino := identity(t, testutil.Abs(home, "c/a"))
		pfd := parentFd(t, testutil.Abs(home, "c"))
		if err := os.Rename(testutil.Abs(home, "c"), testutil.Abs(home, "moved")); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.TrashExpectAt(pfd, "a", testutil.Abs(home, "c"), dev, ino); !errors.Is(err, ErrItemChanged) {
			t.Fatalf("err = %v, want ErrItemChanged", err)
		}
		if !exists(testutil.Abs(home, "moved/a")) || entries(t, testutil.Abs(home, ".local/share/Trash/info")) != 0 {
			t.Fatal("something moved or an info file was left")
		}
	})
	t.Run("Parent exchanged during the move", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a/f", Content: "x"}, {Path: "other/keep", Content: "k"}}})
		tr := fdTrash(t, home)
		dev, ino := identity(t, testutil.Abs(home, "c/a"))
		tr.afterRename = func(string) {
			if err := unix.Renameat2(unix.AT_FDCWD, testutil.Abs(home, "c"), unix.AT_FDCWD, testutil.Abs(home, "other"), unix.RENAME_EXCHANGE); err != nil {
				t.Fatal(err)
			}
		}
		_, err := tr.TrashExpectAt(parentFd(t, testutil.Abs(home, "c")), "a", testutil.Abs(home, "c"), dev, ino)
		if !errors.Is(err, ErrItemChanged) {
			t.Fatalf("err = %v, want ErrItemChanged", err)
		}
		// The original parent is now at other/; the item went back into it.
		if d, i := identity(t, testutil.Abs(home, "other/a")); d != dev || i != ino {
			t.Fatal("the item was not put back into its parent")
		}
		for _, d := range []string{"files", "info"} {
			if n := entries(t, testutil.Abs(home, ".local/share/Trash/"+d)); n != 0 {
				t.Fatalf("%d entries left in %s", n, d)
			}
		}
	})
	t.Run("The info file records the verified parent", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a/f", Content: "x"}}})
		tr := fdTrash(t, home)
		dev, ino := identity(t, testutil.Abs(home, "c/a"))
		if _, err := tr.TrashExpectAt(parentFd(t, testutil.Abs(home, "c")), "a", testutil.Abs(home, "c"), dev, ino); err != nil {
			t.Fatal(err)
		}
		if got := readInfo(t, testutil.Abs(home, ".local/share/Trash/info/a.trashinfo")); got.Path != testutil.Abs(home, "c/a") {
			t.Fatalf("Path %q", got.Path)
		}
	})
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func entries(t *testing.T, dir string) int {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

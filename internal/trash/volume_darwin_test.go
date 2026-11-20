//go:build darwin

package trash

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

// mountVolume creates and attaches a 2 MB APFS disk image inside the test's
// temp folder and returns its mount point, detached when the test ends. It
// skips the test when hdiutil is unavailable (some CI sandboxes). Nothing is
// mounted or written outside the temp folder.
func mountVolume(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("hdiutil not available")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(base, "vol.dmg")
	mnt := filepath.Join(base, "mnt")
	if err := os.Mkdir(mnt, 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	create := exec.Command("hdiutil", "create", "-quiet", "-size", "2m", "-fs", "APFS", "-volname", "kipple-test", "-layout", "NONE", img)
	create.Stdout, create.Stderr = &out, &out
	if err := create.Run(); err != nil {
		t.Skipf("hdiutil create failed: %v: %s", err, out.String())
	}
	attach := exec.Command("hdiutil", "attach", "-quiet", "-nobrowse", "-noautoopen", "-mountpoint", mnt, img)
	attach.Stdout, attach.Stderr = &out, &out
	if err := attach.Run(); err != nil {
		t.Skipf("hdiutil attach failed: %v: %s", err, out.String())
	}
	t.Cleanup(func() {
		if err := exec.Command("hdiutil", "detach", "-quiet", "-force", mnt).Run(); err != nil {
			t.Errorf("hdiutil detach %s: %v", mnt, err)
		}
	})
	return mnt
}

// An item on another device is refused, never copied.
func TestDifferentDeviceIsRefused(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{})
	tr := trashDir(t, home)
	mnt := mountVolume(t)
	if err := os.WriteFile(filepath.Join(mnt, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := tr.Trash(parentFd(t, mnt), "f")
	if !errors.Is(err, ErrDifferentDevice) {
		t.Fatalf("err = %v, want ErrDifferentDevice", err)
	}
	if _, err := os.Lstat(filepath.Join(mnt, "f")); err != nil {
		t.Fatalf("item moved: %v", err)
	}
	entries, _ := os.ReadDir(tr.Path())
	if len(entries) != 0 {
		t.Fatalf("something was copied into the Trash: %v", entries)
	}
}

// The darwin Trasher test from task 3.1: a file created inside a fixture
// under KIPPLE_HOME is trashed with NSFileManager and found at the returned
// path. The fixture home lives on its own small disk image, so the item goes
// to that volume's .Trashes/<uid>, never to the real ~/.Trash.
func TestFinderTrasher(t *testing.T) {
	mnt := mountVolume(t)
	home := filepath.Join(mnt, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIPPLE_HOME", home)
	t.Setenv("KIPPLE_STATE_DIR", filepath.Join(filepath.Dir(mnt), "state"))
	testutil.GuardRealHome(t)
	src := filepath.Join(home, "blob.txt")
	if err := os.WriteFile(src, []byte("kipple"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(src)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := NewFinderTrasher(nil)
	if errors.Is(err, ErrUnsupported) {
		t.Skip("built without cgo")
	}
	if err != nil {
		t.Fatal(err)
	}
	dest, err := tr.Trash(parentFd(t, home), "blob.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dest, mnt+"/") {
		t.Fatalf("trashed outside the fixture volume: %s", dest)
	}
	after, err := os.Lstat(dest)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("not found at the returned path %s: %v", dest, err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
}

// FinderTrasher acts on the approved object: a mismatched identity moves
// nothing; the approved one lands at the returned path and is gone from
// its folder.
func TestFinderTrasherChecksIdentity(t *testing.T) {
	mnt := mountVolume(t)
	home := filepath.Join(mnt, "home")
	if err := os.MkdirAll(filepath.Join(home, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIPPLE_HOME", home)
	t.Setenv("KIPPLE_STATE_DIR", filepath.Join(filepath.Dir(mnt), "state"))
	testutil.GuardRealHome(t)
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(home, n), []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := NewFinderTrasher(nil)
	if errors.Is(err, ErrUnsupported) {
		t.Skip("built without cgo")
	}
	fa, _ := os.Lstat(filepath.Join(home, "a.txt"))
	fb, _ := os.Lstat(filepath.Join(home, "b.txt"))
	sa, sb := fa.Sys().(*syscall.Stat_t), fb.Sys().(*syscall.Stat_t)
	pfd := parentFd(t, home)
	if _, err := tr.TrashExpect(pfd, "a.txt", uint64(sb.Dev), uint64(sb.Ino)); !errors.Is(err, ErrItemChanged) {
		t.Fatalf("err = %v, want ErrItemChanged", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "a.txt")); err != nil {
		t.Fatal("moved despite the identity mismatch")
	}
	dest, err := tr.TrashExpect(pfd, "a.txt", uint64(sa.Dev), uint64(sa.Ino))
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(dest)
	if err != nil || !os.SameFile(fa, after) {
		t.Fatalf("not at %s: %v", dest, err)
	}
	if _, err := os.Lstat(filepath.Join(home, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("still in its folder")
	}
	dd, _ := os.Lstat(filepath.Join(home, "d"))
	if dest, err := tr.TrashExpect(pfd, "d", uint64(dd.Sys().(*syscall.Stat_t).Dev), uint64(dd.Sys().(*syscall.Stat_t).Ino)); err != nil || !strings.HasPrefix(dest, mnt+"/") {
		t.Fatalf("directory: %s %v", dest, err)
	}
}

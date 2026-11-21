//go:build linux

package trash

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/testutil"
)

// realRenameat2 is the real system call, for tests to restore renameat2.
var realRenameat2 = unix.Renameat2

// A file system without RENAME_NOREPLACE (EINVAL or ENOSYS from renameat2)
// is ErrNoReplaceUnsupported and nothing moves; there is no fallback.
func TestMoveNoReplaceUnsupported(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "src/a", Content: "x"}}, Dirs: []testutil.Dir{{Path: "dst"}}})
	for _, errno := range []unix.Errno{unix.EINVAL, unix.ENOSYS} {
		t.Run(errno.Error(), func(t *testing.T) {
			calls := 0
			renameat2 = func(int, string, int, string, uint) error { calls++; return errno }
			t.Cleanup(func() { renameat2 = realRenameat2 })
			err := MoveNoReplace(parentFd(t, testutil.Abs(home, "src")), "a", parentFd(t, testutil.Abs(home, "dst")), "a")
			if !errors.Is(err, ErrNoReplaceUnsupported) || calls != 1 {
				t.Fatalf("err = %v after %d calls, want ErrNoReplaceUnsupported after 1", err, calls)
			}
			if _, err := os.Lstat(testutil.Abs(home, "src/a")); err != nil {
				t.Fatalf("source disturbed: %v", err)
			}
			if _, err := os.Lstat(testutil.Abs(home, "dst/a")); !os.IsNotExist(err) {
				t.Fatalf("something reached the destination: %v", err)
			}
		})
	}
}

// Across devices the move is EXDEV and DirTrasher returns
// ErrDifferentDevice with the source intact: nothing is copied.
func TestCrossDeviceMoveLinux(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/a/f", Content: "x"}}})
	other := testutil.OtherDevice(t, home)
	dst := parentFd(t, other)
	if err := MoveNoReplace(parentFd(t, testutil.Abs(home, "Library/Caches")), "a", dst, "a"); !errors.Is(err, unix.EXDEV) {
		t.Fatalf("MoveNoReplace across devices = %v, want EXDEV", err)
	}
	tr, err := OpenDirTrasher(other)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()
	if _, err := tr.Trash(parentFd(t, testutil.Abs(home, "Library/Caches")), "a"); !errors.Is(err, ErrDifferentDevice) {
		t.Fatalf("Trash across devices = %v, want ErrDifferentDevice", err)
	}
	if b, err := os.ReadFile(testutil.Abs(home, "Library/Caches/a/f")); err != nil || string(b) != "x" {
		t.Fatalf("source not intact: %q %v", b, err)
	}
	if entries, _ := os.ReadDir(other); len(entries) != 0 {
		t.Fatalf("something was copied to the other device: %v", entries)
	}
}

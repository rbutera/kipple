//go:build linux

package policy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/testutil"
)

// On Linux the descriptor's path comes from /proc/self/fd: a directory
// renamed after it was opened reports its new location, and a deleted one
// is refused.
func TestDescriptorPathLinux(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Dirs: []testutil.Dir{{Path: "a/b"}, {Path: "gone"}}})
	open := func(p string) int {
		fd, err := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = unix.Close(fd) })
		return fd
	}
	fd := open(filepath.Join(home, "a/b"))
	if got, err := DescriptorPath(fd); err != nil || got != filepath.Join(home, "a/b") {
		t.Fatalf("DescriptorPath = %q, %v", got, err)
	}
	if err := os.Rename(filepath.Join(home, "a"), filepath.Join(home, "moved")); err != nil {
		t.Fatal(err)
	}
	if got, err := DescriptorPath(fd); err != nil || got != filepath.Join(home, "moved/b") {
		t.Fatalf("after the rename: DescriptorPath = %q, %v", got, err)
	}
	gone := open(filepath.Join(home, "gone"))
	if err := os.Remove(filepath.Join(home, "gone")); err != nil {
		t.Fatal(err)
	}
	if got, err := DescriptorPath(gone); !errors.Is(err, ErrNoDescriptorPath) {
		t.Fatalf("deleted directory: DescriptorPath = %q, %v, want ErrNoDescriptorPath", got, err)
	}
	if _, err := DescriptorPath(-1); !errors.Is(err, ErrNoDescriptorPath) {
		t.Fatalf("bad descriptor: %v", err)
	}
}

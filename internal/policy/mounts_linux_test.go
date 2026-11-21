//go:build linux

package policy

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/testutil"
)

func homeFd(t *testing.T, home string) int {
	t.Helper()
	fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	return fd
}

// The kernel's mount-root attribute (statx, Linux 5.8+) is used when
// reported: /proc is a mount root, a fixture folder is not.
func TestStatxMounts(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Dirs: []testutil.Dir{{Path: "plain"}}})
	m := systemMounts(home, homeFd(t, home))
	if _, ok := m.(statxMounts); !ok {
		t.Fatalf("oracle %T, want statxMounts on this kernel", m)
	}
	if mr, err := m.MountRoot(unix.AT_FDCWD, "/proc", 0, 0); err != nil || !mr { // 0, 0: identity not checked
		t.Fatalf("/proc: %v %v", mr, err)
	}
	if mr, err := m.MountRoot(homeFd(t, home), "plain", 0, 0); err != nil || mr {
		t.Fatalf("plain folder: %v %v", mr, err)
	}
	// The kernel must have looked at the object the walk identified.
	if _, err := m.MountRoot(unix.AT_FDCWD, "/proc", 1, 2); err == nil {
		t.Fatal("an identity mismatch was not reported")
	}
}

// Without the statx attribute, the mount points under home listed in
// /proc/self/mountinfo are compared by identity; a table that cannot be
// read or parsed makes mount points unavailable (fail closed).
func TestMountinfoFallback(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Dirs: []testutil.Dir{{Path: ".cache/foo/docs"}, {Path: ".cache/plain"}}})
	noAttr := func(dirfd int, path string, flags, mask int, stx *unix.Statx_t) error {
		err := unix.Statx(dirfd, path, flags, mask, stx)
		stx.Attributes_mask &^= unix.STATX_ATTR_MOUNT_ROOT
		stx.Attributes &^= unix.STATX_ATTR_MOUNT_ROOT
		return err
	}
	statx = noAttr
	t.Cleanup(func() { statx = unix.Statx; openMountinfo = realOpenMountinfo })
	table := func(s string) func() (io.ReadCloser, error) {
		return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
	}
	esc := strings.ReplaceAll(home, " ", `\040`)
	t.Run("Listed mount point under home", func(t *testing.T) {
		openMountinfo = table("1 0 8:1 / / rw - ext4 /dev/sda1 rw\n" +
			"50 1 8:1 /home/me/Documents " + esc + "/.cache/foo/docs rw - ext4 /dev/sda1 rw\n" +
			"51 1 0:9 / /elsewhere rw - tmpfs tmpfs rw\n")
		m := systemMounts(home, homeFd(t, home))
		if err := m.Available(); err != nil {
			t.Fatal(err)
		}
		docs, plain := identityAt(t, home+"/.cache/foo/docs"), identityAt(t, home+"/.cache/plain")
		if mr, _ := m.MountRoot(-1, "", docs.Dev, docs.Ino); !mr {
			t.Fatal("the listed bind mount is not a mount root")
		}
		if mr, _ := m.MountRoot(-1, "", plain.Dev, plain.Ino); mr {
			t.Fatal("an ordinary folder is a mount root")
		}
	})
	t.Run("Refresh sees a mount made after the session started", func(t *testing.T) {
		table1 := "1 0 8:1 / / rw - ext4 /dev/sda1 rw\n"
		openMountinfo = table(table1)
		m := systemMounts(home, homeFd(t, home))
		plain := identityAt(t, home+"/.cache/plain")
		if mr, _ := m.MountRoot(-1, "", plain.Dev, plain.Ino); mr {
			t.Fatal("no mount yet")
		}
		openMountinfo = table(table1 + "60 1 8:1 /x " + esc + "/.cache/plain rw - ext4 /dev/sda1 rw\n")
		r, ok := m.(mountRefresher)
		if !ok || r.Refresh() != nil {
			t.Fatal("the snapshot oracle cannot refresh")
		}
		if mr, _ := m.MountRoot(-1, "", plain.Dev, plain.Ino); !mr {
			t.Fatal("a mount made later was not seen after Refresh")
		}
		openMountinfo = func() (io.ReadCloser, error) { return nil, os.ErrPermission }
		if r.Refresh() == nil || !errors.Is(m.Available(), ErrMountsUnavailable) {
			t.Fatal("a failed refresh must make mount points unavailable")
		}
	})
	t.Run("Mount table unavailable", func(t *testing.T) {
		openMountinfo = func() (io.ReadCloser, error) { return nil, os.ErrNotExist }
		m := systemMounts(home, homeFd(t, home))
		if !errors.Is(m.Available(), ErrMountsUnavailable) {
			t.Fatalf("Available = %v", m.Available())
		}
		if _, err := m.MountRoot(-1, "", 1, 1); !errors.Is(err, ErrMountsUnavailable) {
			t.Fatalf("MountRoot err = %v", err)
		}
	})
	t.Run("Malformed mount table", func(t *testing.T) {
		openMountinfo = table("1 0 8:1 / / rw ext4\n")
		if m := systemMounts(home, homeFd(t, home)); !errors.Is(m.Available(), ErrMountsUnavailable) {
			t.Fatalf("Available = %v", m.Available())
		}
	})
	t.Run("Listed mount point that cannot be opened", func(t *testing.T) {
		openMountinfo = table("1 0 8:1 / / rw - ext4 /dev/sda1 rw\n50 1 8:1 / " + esc + "/gone rw - ext4 /dev/sda1 rw\n")
		if m := systemMounts(home, homeFd(t, home)); !errors.Is(m.Available(), ErrMountsUnavailable) {
			t.Fatalf("Available = %v", m.Available())
		}
	})
}

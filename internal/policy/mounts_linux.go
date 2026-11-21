//go:build linux

package policy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/mountinfo"
)

// atNoAutomount is AT_NO_AUTOMOUNT: do not trigger an automount when
// inspecting an entry.
const atNoAutomount = 0x800

// Test hooks: the statx call and the mount table.
var (
	statx         = unix.Statx
	openMountinfo = realOpenMountinfo
)

func realOpenMountinfo() (io.ReadCloser, error) { return os.Open("/proc/self/mountinfo") }

// systemMounts returns the Linux oracle for the home open at homeFd: statx
// STATX_ATTR_MOUNT_ROOT when the kernel reports it, otherwise a snapshot of
// the mount points under home from /proc/self/mountinfo, compared by
// identity; failing both, unavailableMounts.
func systemMounts(home string, homeFd int) MountOracle {
	var stx unix.Statx_t
	err := statx(homeFd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_TYPE|unix.STATX_INO, &stx)
	if err == nil && stx.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT != 0 {
		return statxMounts{}
	}
	snap, serr := mountSnapshot(home)
	if serr != nil {
		if err == nil {
			err = errors.New("statx does not report the mount-root attribute")
		}
		return unavailableMounts{err: fmt.Errorf("%w: statx: %v; /proc/self/mountinfo: %v", ErrMountsUnavailable, err, serr)}
	}
	return snap
}

// statxMounts asks the kernel about each entry.
type statxMounts struct{}

func (statxMounts) Available() error { return nil }

func (statxMounts) MountRoot(dirfd int, name string, _, _ uint64) (bool, error) {
	var stx unix.Statx_t
	for {
		err := statx(dirfd, name, unix.AT_SYMLINK_NOFOLLOW|atNoAutomount, unix.STATX_TYPE|unix.STATX_INO, &stx)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if stx.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT == 0 {
			return false, fmt.Errorf("%w: statx stopped reporting the mount-root attribute", ErrMountsUnavailable)
		}
		return stx.Attributes&unix.STATX_ATTR_MOUNT_ROOT != 0, nil
	}
}

// snapshotMounts is the set of identities of the mount points under home,
// taken when the session started.
type snapshotMounts struct{ roots map[fileID]bool }

func (snapshotMounts) Available() error { return nil }

func (s snapshotMounts) MountRoot(_ int, _ string, dev, ino uint64) (bool, error) {
	return s.roots[fileID{dev: dev, ino: ino}], nil
}

// mountSnapshot reads /proc/self/mountinfo and opens every mount point at
// or under home (O_PATH|O_NOFOLLOW, so spelling and escapes do not matter)
// to record its identity. A table that cannot be read or parsed, or a
// listed mount point under home that cannot be opened, is an error.
func mountSnapshot(home string) (snapshotMounts, error) {
	f, err := openMountinfo()
	if err != nil {
		return snapshotMounts{}, err
	}
	defer func() { _ = f.Close() }()
	ms, err := mountinfo.Parse(f)
	if err != nil {
		return snapshotMounts{}, err
	}
	roots := map[fileID]bool{}
	for _, m := range ms {
		if m.MountPoint != home && !strings.HasPrefix(m.MountPoint, home+"/") {
			continue
		}
		fd, err := unix.Open(m.MountPoint, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return snapshotMounts{}, fmt.Errorf("mount point %s: %w", m.MountPoint, err)
		}
		var st unix.Stat_t
		err = unix.Fstat(fd, &st)
		_ = unix.Close(fd)
		if err != nil {
			return snapshotMounts{}, fmt.Errorf("mount point %s: %w", m.MountPoint, err)
		}
		roots[fileID{dev: st.Dev, ino: st.Ino}] = true
	}
	return snapshotMounts{roots: roots}, nil
}

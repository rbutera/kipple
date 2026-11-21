//go:build linux

package policy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

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
	snap, serr := newSnapshotMounts(home)
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

func (statxMounts) MountRoot(dirfd int, name string, dev, ino uint64) (bool, error) {
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
			return false, errors.New("statx stopped reporting the mount-root attribute")
		}
		// The kernel must have inspected the object the walk did (the name
		// could have been replaced, or a mount made, in between).
		if dev != 0 || ino != 0 {
			if got := unix.Mkdev(stx.Dev_major, stx.Dev_minor); got != dev || stx.Ino != ino {
				return false, errors.New("the entry changed while it was checked for a mount")
			}
		}
		return stx.Attributes&unix.STATX_ATTR_MOUNT_ROOT != 0, nil
	}
}

// snapshotMounts is the set of identities of the mount points under home
// read from /proc/self/mountinfo. It is read when the session starts and
// again whenever Refresh is called (apply refreshes it before each item's
// checks and before its post-move check), so a mount made during a run is
// seen at the next item. A refresh that fails makes it unavailable.
type snapshotMounts struct {
	home  string
	mu    sync.Mutex
	roots map[fileID]bool
	err   error
}

func newSnapshotMounts(home string) (*snapshotMounts, error) {
	roots, err := mountSnapshot(home)
	if err != nil {
		return nil, err
	}
	return &snapshotMounts{home: home, roots: roots}, nil
}

func (s *snapshotMounts) Available() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *snapshotMounts) MountRoot(_ int, _ string, dev, ino uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	return s.roots[fileID{dev: dev, ino: ino}], nil
}

// Refresh re-reads the mount table.
func (s *snapshotMounts) Refresh() error {
	roots, err := mountSnapshot(s.home)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.err = fmt.Errorf("%w: /proc/self/mountinfo could not be read again: %v", ErrMountsUnavailable, err)
		return s.err
	}
	s.roots = roots
	return nil
}

// mountSnapshot reads /proc/self/mountinfo and opens every mount point at
// or under home (O_PATH|O_NOFOLLOW, so spelling and escapes do not matter)
// to record its identity. A table that cannot be read or parsed, or a
// listed mount point under home that cannot be opened, is an error.
func mountSnapshot(home string) (map[fileID]bool, error) {
	f, err := openMountinfo()
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	ms, err := mountinfo.Parse(f)
	if err != nil {
		return nil, err
	}
	roots := map[fileID]bool{}
	for _, m := range ms {
		if m.MountPoint != home && !strings.HasPrefix(m.MountPoint, home+"/") {
			continue
		}
		fd, err := unix.Open(m.MountPoint, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("mount point %s: %w", m.MountPoint, err)
		}
		var st unix.Stat_t
		err = unix.Fstat(fd, &st)
		_ = unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("mount point %s: %w", m.MountPoint, err)
		}
		roots[fileID{dev: st.Dev, ino: st.Ino}] = true
	}
	return roots, nil
}

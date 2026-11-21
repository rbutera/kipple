package policy

import "errors"

// MountOracle tells whether a directory entry is the root of a mount
// (linux-port design L3). On macOS every mount has its own st_dev, so the
// device check already stops at mounts; on Linux a bind mount from home's
// own file system keeps home's st_dev, so the policy session, the size walk
// and the apply walk ask the oracle about every entry they pass.
type MountOracle interface {
	// Available is nil when the oracle can answer; otherwise
	// ErrMountsUnavailable (wrapped), and every MountRoot call fails with it.
	Available() error
	// MountRoot reports whether name, in the directory open at dirfd, is a
	// mount root; dev and ino are its identity from fstatat. dirfd may be
	// AT_FDCWD with an absolute name.
	MountRoot(dirfd int, name string, dev, ino uint64) (bool, error)
}

// mountRefresher is a MountOracle that answers from a snapshot and can
// re-read it (the /proc/self/mountinfo fallback).
type mountRefresher interface {
	Refresh() error
}

// ErrMountsUnavailable: neither the kernel's mount-root attribute (statx
// STATX_ATTR_MOUNT_ROOT, Linux 5.8+) nor /proc/self/mountinfo can be read,
// so mount points cannot be told apart from ordinary folders. Scan marks
// every item contains_mount and apply stops with mounts_unavailable.
var ErrMountsUnavailable = errors.New("kipple cannot determine mount points (no statx mount-root attribute and no readable /proc/self/mountinfo)")

// unavailableMounts is the oracle when nothing can answer.
type unavailableMounts struct{ err error }

func (u unavailableMounts) Available() error { return u.err }
func (u unavailableMounts) MountRoot(int, string, uint64, uint64) (bool, error) {
	return false, u.err
}

// StubMounts is an injectable oracle for tests: Roots holds the (dev,
// ino) identities that are mount roots, and Err, when set, makes it
// unavailable.
type StubMounts struct {
	Roots map[FileIdentity]bool
	Err   error
	// Failing makes the check of these identities fail with the error.
	Failing map[FileIdentity]error
}

// Available implements MountOracle.
func (s StubMounts) Available() error { return s.Err }

// MountRoot implements MountOracle.
func (s StubMounts) MountRoot(_ int, _ string, dev, ino uint64) (bool, error) {
	if s.Err != nil {
		return false, s.Err
	}
	if err := s.Failing[FileIdentity{Dev: dev, Ino: ino}]; err != nil {
		return false, err
	}
	return s.Roots[FileIdentity{Dev: dev, Ino: ino}], nil
}

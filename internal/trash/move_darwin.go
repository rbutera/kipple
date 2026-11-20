//go:build darwin

package trash

import (
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// MoveNoReplace renames the entry from in the directory open at fromFd to the
// entry to in the directory open at toFd, failing with EEXIST if to exists
// and EXDEV across devices. It is renameatx_np with RENAME_EXCL (and
// RENAME_NOFOLLOW_ANY where the kernel has it): neither
// name's final component is followed if it is a symlink, and nothing is
// ever replaced or copied.
func MoveNoReplace(fromFd int, from string, toFd int, to string) error {
	if !validName(from) || !validName(to) {
		return ErrInvalidName
	}
	flags := uint32(unix.RENAME_EXCL)
	if !noFollowAnyUnsupported.Load() {
		flags |= renameNoFollowAny
	}
	for {
		err := unix.RenameatxNp(fromFd, from, toFd, to, flags)
		if err == unix.EINVAL && flags&renameNoFollowAny != 0 {
			// Older kernels reject RENAME_NOFOLLOW_ANY; both names are
			// single components below verified descriptors anyway.
			noFollowAnyUnsupported.Store(true)
			flags &^= renameNoFollowAny
			continue
		}
		if err != unix.EINTR {
			return err
		}
	}
}

// renameNoFollowAny is RENAME_NOFOLLOW_ANY (sys/stdio.h): fail if any
// symbolic link is met while resolving either name. Belt and braces, since
// the names are single components relative to verified descriptors.
const renameNoFollowAny = 0x10

var noFollowAnyUnsupported atomic.Bool

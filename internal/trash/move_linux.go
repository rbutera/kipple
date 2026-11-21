//go:build linux

package trash

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// renameat2 is unix.Renameat2; tests replace it to simulate a file system
// without RENAME_NOREPLACE.
var renameat2 = unix.Renameat2

// MoveNoReplace renames the entry from in the directory open at fromFd to the
// entry to in the directory open at toFd, failing with EEXIST if to exists
// and EXDEV across devices. It is renameat2 with RENAME_NOREPLACE: the final
// component of neither name is followed, and nothing is ever replaced or
// copied. Where the file system or kernel cannot do a no-replace rename
// (EINVAL or ENOSYS: NFS, some FUSE file systems, kernels before 3.15) it
// returns ErrNoReplaceUnsupported, having moved nothing; there is no
// fallback to a plain rename, a link and unlink, or a copy.
func MoveNoReplace(fromFd int, from string, toFd int, to string) error {
	if !validName(from) || !validName(to) {
		return ErrInvalidName
	}
	for {
		err := renameat2(fromFd, from, toFd, to, unix.RENAME_NOREPLACE)
		switch err {
		case unix.EINTR:
			continue
		case unix.EINVAL, unix.ENOSYS:
			return fmt.Errorf("%w (%v)", ErrNoReplaceUnsupported, err)
		}
		return err
	}
}

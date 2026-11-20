//go:build darwin

package trash

import "golang.org/x/sys/unix"

// MoveNoReplace renames the entry from in the directory open at fromFd to the
// entry to in the directory open at toFd, failing with EEXIST if to exists
// and EXDEV across devices. It is renameatx_np with RENAME_EXCL: neither
// name's final component is followed if it is a symlink, and nothing is
// ever replaced or copied.
func MoveNoReplace(fromFd int, from string, toFd int, to string) error {
	if !validName(from) || !validName(to) {
		return ErrInvalidName
	}
	for {
		err := unix.RenameatxNp(fromFd, from, toFd, to, unix.RENAME_EXCL)
		if err != unix.EINTR {
			return err
		}
	}
}

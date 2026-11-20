//go:build darwin

package apply

import (
	"bytes"
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// tagName is the extended attribute apply sets on every item it moves to
// the Trash, and restore requires: it binds the Trash entry to one receipt,
// one item and one home, so a receipt that merely copies a foreign Trash
// entry's identity cannot restore it.
const tagName = "com.rbutera.kipple"

// setTag sets the tag on the object open at fd. A read-only object gets
// owner write permission for the call (macOS needs it to write an extended
// attribute), restored afterwards through the same descriptor.
func setTag(fd int, value []byte) error {
	return withWrite(fd, func() error { return unix.Fsetxattr(fd, tagName, value, 0) })
}

// clearTag removes the tag from the object open at fd (best effort, after
// a move that did not happen).
func clearTag(fd int) error {
	err := withWrite(fd, func() error { return unix.Fremovexattr(fd, tagName) })
	if errors.Is(err, unix.ENOATTR) {
		return nil
	}
	return err
}

// tagMatches reports whether the object open at fd carries exactly value.
func tagMatches(fd int, value []byte) bool {
	buf := make([]byte, len(value)+16)
	n, err := unix.Fgetxattr(fd, tagName, buf)
	return err == nil && bytes.Equal(buf[:n], value)
}

func withWrite(fd int, fn func() error) error {
	err := fn()
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || int(st.Uid) != os.Geteuid() || st.Mode&unix.S_IWUSR != 0 {
		return err
	}
	mode := uint32(st.Mode) & 0o7777
	if cerr := unix.Fchmod(fd, mode|unix.S_IWUSR); cerr != nil {
		return err
	}
	err = fn()
	_ = unix.Fchmod(fd, mode)
	return err
}

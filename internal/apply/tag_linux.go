//go:build linux

package apply

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/trash"
)

// tagName is the extended attribute apply sets on every item it moves to
// the Trash, and restore requires: it binds the Trash entry to one receipt,
// one item and one home, so a receipt that merely copies a foreign Trash
// entry's identity cannot restore it. Linux allows unprivileged processes
// only the user. namespace.
const tagName = "user.com.rbutera.kipple"

// Extended-attribute calls; tests replace fsetxattr to simulate a file
// system without user extended attributes.
var (
	fsetxattr    = unix.Fsetxattr
	fgetxattr    = unix.Fgetxattr
	fremovexattr = unix.Fremovexattr
)

// errNoUserXattr: the file system does not support user extended
// attributes, so the item cannot be tagged and is not moved (an untagged
// Trash entry could not be restored). It deliberately does not wrap the
// errno: EPERM would otherwise read as a permission problem.
type errNoUserXattr struct{ fs string }

func (e *errNoUserXattr) Error() string {
	return "the file system (" + e.fs + ") does not support user extended attributes, so kipple cannot tag the item for restore and did not move it"
}

// fstatOwner, in tests, replaces reading the owner of the object open at
// fd.
var fstatOwner func(fd int) int

// ownerOf returns the owner of the object open at fd, or -1.
func ownerOf(fd int) int {
	if fstatOwner != nil {
		return fstatOwner(fd)
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil {
		return -1
	}
	return int(st.Uid)
}

// taggableMode reports whether an item of this type can carry the tag and
// may be opened to set it. Linux allows user. attributes only on regular
// files and directories; device nodes, FIFOs and sockets are refused
// before any open (opening a device or FIFO can have side effects).
func taggableMode(m fs.FileMode) bool { return m.IsRegular() || m.IsDir() }

// setTag sets the tag on the object open at fd. Setting a user. attribute
// needs write permission, so a read-only object owned by the user gets
// owner write for the call, restored afterwards through the same
// descriptor (Go's module cache is read-only).
func setTag(fd int, value []byte) error {
	err := withWrite(fd, func() error { return fsetxattr(fd, tagName, value, 0) })
	if errors.Is(err, fs.ErrPermission) {
		// Not owning the item is a permission problem, not the file
		// system's: say so (permission_denied).
		if uid := ownerOf(fd); uid >= 0 && uid != os.Geteuid() {
			return fmt.Errorf("the item is owned by another user (uid %d), so kipple cannot tag it for restore and did not move it: %w", uid, err)
		}
	}
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) {
		return &errNoUserXattr{fs: trash.FileSystemName(fd)}
	}
	if err != nil {
		return fmt.Errorf("setting %s: %w", tagName, err)
	}
	return nil
}

// clearTag removes the tag from the object open at fd (best effort, after
// a move that did not happen).
func clearTag(fd int) error {
	err := withWrite(fd, func() error { return fremovexattr(fd, tagName) })
	if errors.Is(err, unix.ENODATA) {
		return nil
	}
	return err
}

// tagMatches reports whether the object open at fd carries exactly value.
func tagMatches(fd int, value []byte) bool {
	buf := make([]byte, len(value)+16)
	n, err := fgetxattr(fd, tagName, buf)
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
	mode := st.Mode & 0o7777
	if cerr := unix.Fchmod(fd, mode|unix.S_IWUSR); cerr != nil {
		return err
	}
	err = fn()
	_ = unix.Fchmod(fd, mode)
	return err
}

//go:build unix

package apply

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

// errCrossDevice: a permanent delete met another device inside the item
// and stopped there.
var errCrossDevice = errors.New("another volume is mounted inside the item; stopped")

// maxRemoveDepth bounds the permanent-delete recursion.
const maxRemoveDepth = 256

func openDirAt(dirfd int, name string) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func fstatat(dirfd int, name string, st *unix.Stat_t) error {
	for {
		err := unix.Fstatat(dirfd, name, st, unix.AT_SYMLINK_NOFOLLOW)
		if err != unix.EINTR {
			return err
		}
	}
}

// openItem opens the approved directory item from its verified parent
// descriptor and checks it is the object (dev, ino).
func openItem(f *policy.Fresh, dev, ino uint64) (*os.File, error) {
	fd, err := openDirAt(f.ParentFd, f.Name)
	if err != nil {
		return nil, fmt.Errorf("the item could not be opened: %v", err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || uint64(st.Dev) != dev || uint64(st.Ino) != ino {
		_ = unix.Close(fd)
		return nil, errors.New("the item changed while it was opened")
	}
	return os.NewFile(uintptr(fd), f.Name), nil
}

// withOwnerWrite runs fn with owner write permission added to the directory
// open as dir, if it lacks it, and restores the original mode afterwards
// through the same descriptor (wherever fn moved the directory).
func withOwnerWrite(dir *os.File, fn func() error) error {
	var st unix.Stat_t
	if err := unix.Fstat(int(dir.Fd()), &st); err != nil {
		return err
	}
	mode := uint32(st.Mode) & 0o7777
	if mode&unix.S_IWUSR != 0 || int(st.Uid) != os.Geteuid() {
		return fn()
	}
	if err := unix.Fchmod(int(dir.Fd()), mode|unix.S_IWUSR); err != nil {
		return err
	}
	err := fn()
	if cerr := unix.Fchmod(int(dir.Fd()), mode); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

func isPermission(err error) bool { return errors.Is(err, fs.ErrPermission) }

// removeItem deletes the approved item for --permanent, from its verified
// parent descriptor, never following a symlink and stopping at a device
// change.
func removeItem(f *policy.Fresh, dev, ino uint64) error {
	var st unix.Stat_t
	if err := fstatat(f.ParentFd, f.Name, &st); err != nil {
		return err
	}
	if uint64(st.Dev) != dev || uint64(st.Ino) != ino {
		return trash.ErrItemChanged
	}
	return removeTree(f.ParentFd, f.Name, dev, 0)
}

func removeTree(dirfd int, name string, dev uint64, depth int) error {
	if depth > maxRemoveDepth {
		return fmt.Errorf("deeper than %d levels; stopped", maxRemoveDepth)
	}
	var st unix.Stat_t
	if err := fstatat(dirfd, name, &st); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if uint64(st.Dev) != dev {
		return errCrossDevice
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return unix.Unlinkat(dirfd, name, 0)
	}
	fd, err := openDirAt(dirfd, name)
	if err != nil {
		return err
	}
	var cst unix.Stat_t
	if err := unix.Fstat(fd, &cst); err != nil || cst.Dev != st.Dev || cst.Ino != st.Ino {
		_ = unix.Close(fd)
		return trash.ErrItemChanged
	}
	names, err := readNames(fd)
	if err == nil {
		for _, n := range names {
			if err = removeTree(fd, n, dev, depth+1); err != nil {
				break
			}
		}
	}
	_ = unix.Close(fd)
	if err != nil {
		return err
	}
	return unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR)
}

// readNames lists the directory open at fd through a fresh descriptor.
func readNames(fd int) ([]string, error) {
	lfd, err := openDirAt(fd, ".")
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(lfd), ".")
	defer func() { _ = f.Close() }()
	var all []string
	for {
		names, err := f.Readdirnames(1024)
		all = append(all, names...)
		if err == io.EOF {
			return all, nil
		}
		if err != nil {
			return all, err
		}
	}
}

// classify maps a move error to a skip reason.
func classify(err error) string {
	switch {
	case errors.Is(err, trash.ErrDifferentDevice), errors.Is(err, unix.EXDEV), errors.Is(err, errCrossDevice):
		return plan.SkipDifferentDevice
	case errors.Is(err, trash.ErrTrashChanged):
		return plan.SkipTrashChanged
	case errors.Is(err, trash.ErrItemChanged):
		return plan.SkipIdentityChanged
	case errors.Is(err, unix.EEXIST):
		return plan.SkipDestinationExists
	case errors.Is(err, fs.ErrPermission):
		return plan.SkipPermissionDenied
	default:
		return plan.SkipMoveFailed
	}
}

// openHomeTrash opens <home>/.Trash through the session (no symlink on the
// way, same device as home, stored names) and checks it is a directory
// owned by this user.
func openHomeTrash(sess *policy.Session, home string) (*trash.DirTrasher, error) {
	path := home + "/.Trash"
	e, err := sess.Open(path)
	if err != nil {
		return nil, fmt.Errorf("the Trash folder %s cannot be used: %w", path, err)
	}
	if e.Dir == nil {
		return nil, fmt.Errorf("the Trash folder %s is not a folder", path)
	}
	defer func() { _ = e.Dir.Close() }()
	if st, ok := e.Info.Sys().(*unix.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("the Trash folder %s is not owned by you", path)
	}
	return trash.NewDirTrasher(e.Dir, path)
}

// statAt returns the type and identity of name in the directory open at
// dirfd, without following a symlink.
func statAt(dirfd int, name string) (fs.FileMode, uint64, uint64, error) {
	var st unix.Stat_t
	if err := fstatat(dirfd, name, &st); err != nil {
		return 0, 0, 0, err
	}
	return policy.StatMode(uint32(st.Mode)), uint64(st.Dev), uint64(st.Ino), nil
}

// moveBack moves name from the Trash descriptor to the authorised
// destination, never replacing anything, after checking the Trash entry is
// still (dev, ino). A read-only directory gets owner write permission for
// the duration of the rename, as when it was trashed.
func moveBack(trashFd int, name string, dst *policy.Fresh, dev, ino uint64, isDir bool) error {
	move := func() error {
		var st unix.Stat_t
		if err := fstatat(trashFd, name, &st); err != nil {
			return err
		}
		if uint64(st.Dev) != dev || uint64(st.Ino) != ino {
			return trash.ErrItemChanged
		}
		return trash.MoveNoReplace(trashFd, name, dst.ParentFd, dst.Name)
	}
	err := move()
	if err != nil && isDir && isPermission(err) {
		fd, oerr := openDirAt(trashFd, name)
		if oerr != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), name)
		defer func() { _ = f.Close() }()
		err = withOwnerWrite(f, move)
	}
	return err
}

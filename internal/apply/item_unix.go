//go:build unix

package apply

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

// errCrossDevice: a permanent delete met another device inside the item
// and stopped there.
var errCrossDevice = errors.New("another volume is mounted inside the item; stopped")

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
	// Put the mode back through the descriptor wherever the folder now is.
	// A failure here leaves owner write on a folder that moved; it never
	// turns a completed move into a reported failure.
	_ = unix.Fchmod(int(dir.Fd()), mode)
	return err
}

func isPermission(err error) bool { return errors.Is(err, fs.ErrPermission) }

type ident struct{ dev, ino uint64 }

func identOf(st *unix.Stat_t) ident { return ident{uint64(st.Dev), uint64(st.Ino)} }

// fstatOf fstats fd.
func fstatOf(fd int) (unix.Stat_t, error) {
	var st unix.Stat_t
	for {
		err := unix.Fstat(fd, &st)
		if err != unix.EINTR {
			return st, err
		}
	}
}

// classify maps a move error to a skip reason.
func classify(err error) string {
	switch {
	case errors.As(err, new(*errProtected)):
		return plan.ReasonContainsProtected
	case errors.As(err, new(*errChanged)):
		return plan.SkipIdentityChanged
	case errors.As(err, new(*errRestoreArrived)):
		return plan.SkipTrashItemMismatch
	case errors.Is(err, errCrossDevice):
		return plan.ReasonContainsMount
	case errors.Is(err, trash.ErrDifferentDevice), errors.Is(err, unix.EXDEV):
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

// tagTarget returns a descriptor of the approved item to tag: the held
// folder, or the file opened from its verified parent (never following a
// symlink, never blocking on a FIFO) and checked to be (dev, ino).
func tagTarget(f *policy.Fresh, dev, ino uint64, dir *os.File) (int, func(), error) {
	if dir != nil {
		return int(dir.Fd()), func() {}, nil
	}
	fd, err := unix.Openat(f.ParentFd, f.Name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, nil, fmt.Errorf("could not open the item to tag it: %w", err)
	}
	st, err := fstatOf(fd)
	if err != nil || identOf(&st) != (ident{dev, ino}) {
		_ = unix.Close(fd)
		return -1, nil, trash.ErrItemChanged
	}
	return fd, func() { _ = unix.Close(fd) }, nil
}

// openEntry opens the Trash entry name through the verified Trash
// descriptor without following a symlink or blocking on a FIFO, and
// returns the descriptor and the entry's identity.
func openEntry(trashFd int, name string) (int, entryInfo, error) {
	fd, err := unix.Openat(trashFd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, entryInfo{}, err
	}
	st, err := fstatOf(fd)
	if err != nil {
		_ = unix.Close(fd)
		return -1, entryInfo{}, err
	}
	return fd, entryInfo{kind: policy.StatMode(uint32(st.Mode)), dev: uint64(st.Dev), ino: uint64(st.Ino), size: st.Size, mtime: time.Unix(st.Mtim.Unix()).UTC()}, nil
}

// errRestoreArrived: after a restore's rename, the object at the
// destination was not the approved entry. note says what was done.
type errRestoreArrived struct{ note string }

func (e *errRestoreArrived) Error() string { return e.note }

// moveBack moves the entry name from the Trash descriptor to the
// authorised destination, never replacing anything: the entry must still
// be want (by descriptor) immediately before, and the object that arrives
// must be it (else the receipt notes what happened). efd is the entry, open,
// for the read-only-folder permission step.
func moveBack(trashFd int, name string, dst *policy.Fresh, want entryInfo, efd int) error {
	move := func() error {
		var st unix.Stat_t
		if err := fstatat(trashFd, name, &st); err != nil {
			return err
		}
		if uint64(st.Dev) != want.dev || uint64(st.Ino) != want.ino {
			return trash.ErrItemChanged
		}
		return trash.MoveNoReplace(trashFd, name, dst.ParentFd, dst.Name)
	}
	err := move()
	if err != nil && want.kind.IsDir() && isPermission(err) {
		f := os.NewFile(uintptr(efd), name) // not closed here: the caller owns efd
		err = withOwnerWrite(f, move)
	}
	if err != nil {
		return err
	}
	if afterRestoreRename != nil {
		afterRestoreRename()
	}
	var st unix.Stat_t
	if err := fstatat(dst.ParentFd, dst.Name, &st); err == nil && uint64(st.Dev) == want.dev && uint64(st.Ino) == want.ino {
		return nil
	}
	// The entry left the Trash, but its destination now holds something
	// else (another program moved things there). Nothing more is moved:
	// moving "whatever is there" back could move an unapproved object. The
	// receipt records what happened.
	return &errRestoreArrived{note: "the object restored to " + dst.Name + " was replaced there by another program right after the move; the entry is no longer in the Trash and was not found at its destination"}
}

// afterRestoreRename, in tests, runs between a restore's rename and its
// post-move check.
var afterRestoreRename func()

func closeFd(fd int) { _ = unix.Close(fd) }

// dupFile returns a new *os.File for a duplicate of fd.
func dupFile(fd int, name string) (*os.File, error) {
	d, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(d)
	return os.NewFile(uintptr(d), name), nil
}

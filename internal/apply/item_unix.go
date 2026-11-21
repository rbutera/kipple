//go:build unix

package apply

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

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
		return nil, fmt.Errorf("the item could not be opened: %w", err)
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
	case errors.Is(err, errCrossDevice), errors.Is(err, policy.ErrMountsUnavailable):
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

// errSpecialFile: the item is a device node, FIFO or socket, which cannot
// carry the tag on this platform; it is refused without being opened.
var errSpecialFile = errors.New("the item is a device, FIFO or socket; kipple does not open or move it")

// tagTarget returns a descriptor of the approved item to tag: the held
// folder, or the file opened from its verified parent (never following a
// symlink, never blocking on a FIFO) and checked to be (dev, ino).
func tagTarget(f *policy.Fresh, dev, ino uint64, dir *os.File) (int, func(), error) {
	if dir != nil {
		return int(dir.Fd()), func() {}, nil
	}
	if !taggableMode(f.Info.Mode()) {
		return -1, nil, errSpecialFile
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

// restoreSwapNote explains a restore that met a different object; the
// Trash name it was put back under follows.
const restoreSwapNote = "a different object was at the Trash name at the moment of the move; it was put back in the Trash"

// Test hooks for restore's moves.
var (
	// beforeStage runs after restore's checks, before the entry is renamed
	// to its staging name.
	beforeStage func()
	// afterStage runs after the staged entry was verified, before it is
	// renamed to the destination.
	afterStage func()
	// afterRestoreRename runs between the rename to the destination and
	// the post-move check.
	afterRestoreRename func()
)

// stagingName returns a fresh, unpredictable, visible name inside the
// Trash for the entry name: "<name> (kipple <what> <hex>)". No leading dot,
// so an entry stranded there (a restore killed midway) or put back there
// stays visible in Finder's Trash window; restore also finds it again by
// identity. The name is kept within NAME_MAX.
func stagingName(name, what string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	suffix := " (kipple " + what + " " + hex.EncodeToString(b[:]) + ")"
	const nameMax = 255
	if len(name)+len(suffix) > nameMax {
		cut := nameMax - len(suffix)
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut]
	}
	return name + suffix
}

// moveBack restores the Trash entry name to the authorised destination.
// macOS cannot rename by descriptor, so it narrows the window: the entry
// is first renamed (RENAME_EXCL) to a random staging name inside the
// Trash, and the object at the staging name is verified by descriptor
// (identity, size, mtime and tag); only then is it renamed to the
// destination. If what arrives there is not the entry, it is renamed
// straight back into the Trash. Nothing is ever replaced.
func moveBack(trashFd int, name string, dst *policy.Fresh, want entryInfo, tag []byte) error {
	if beforeStage != nil {
		beforeStage()
	}
	staging := stagingName(name, "restoring")
	if err := trash.MoveNoReplace(trashFd, name, trashFd, staging); err != nil {
		return err
	}
	// putBack returns the staged object to the Trash name, or to a visible
	// "returned" name if that is taken, and says where it is.
	putBack := func() string {
		if trash.MoveNoReplace(trashFd, staging, trashFd, name) == nil {
			return name
		}
		ret := stagingName(name, "returned")
		if trash.MoveNoReplace(trashFd, staging, trashFd, ret) == nil {
			return ret
		}
		return staging
	}
	sfd, got, err := openEntry(trashFd, staging)
	if err != nil {
		return &errRestoreArrived{note: restoreSwapNote + " as " + putBack()}
	}
	defer func() { _ = unix.Close(sfd) }()
	if got.dev != want.dev || got.ino != want.ino || got.size != want.size || !got.mtime.Equal(want.mtime) || !tagMatches(sfd, tag) {
		return &errRestoreArrived{note: restoreSwapNote + " as " + putBack()}
	}
	if afterStage != nil {
		afterStage()
	}
	move := func() error { return trash.MoveNoReplace(trashFd, staging, dst.ParentFd, dst.Name) }
	err = move()
	if err != nil && want.kind.IsDir() && isPermission(err) {
		if f, derr := dupFile(sfd, staging); derr == nil {
			err = withOwnerWrite(f, move)
			_ = f.Close()
		}
	}
	if err != nil {
		_ = putBack()
		return err
	}
	if afterRestoreRename != nil {
		afterRestoreRename()
	}
	var st unix.Stat_t
	if err := fstatat(dst.ParentFd, dst.Name, &st); err == nil && uint64(st.Dev) == want.dev && uint64(st.Ino) == want.ino {
		_ = clearTag(sfd)
		return nil
	}
	// A different object reached the destination: put it straight back in
	// the Trash under a fresh name.
	ret := stagingName(name, "returned")
	if berr := trash.MoveNoReplace(dst.ParentFd, dst.Name, trashFd, ret); berr != nil {
		return &errRestoreArrived{note: "a different object reached " + dst.Name + " at the moment of the move and could not be put back in the Trash (" + berr.Error() + ")"}
	}
	return &errRestoreArrived{note: restoreSwapNote + " as " + ret}
}

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

// findInTrash returns the name of the entry in the Trash folder open at
// trashFd whose (dev, ino) is the given identity, without following links.
func findInTrash(trashFd int, dev, ino uint64) (string, bool) {
	lfd, err := openDirAt(trashFd, ".")
	if err != nil {
		return "", false
	}
	f := os.NewFile(uintptr(lfd), ".Trash")
	defer func() { _ = f.Close() }()
	for {
		names, err := f.Readdirnames(1024)
		for _, n := range names {
			var st unix.Stat_t
			if fstatat(trashFd, n, &st) == nil && uint64(st.Dev) == dev && uint64(st.Ino) == ino {
				return n, true
			}
		}
		if err != nil {
			return "", false
		}
	}
}

// entryIs reports whether name in the Trash folder open at trashFd is the
// object (dev, ino), without following a link.
func entryIs(trashFd int, name string, dev, ino uint64) bool {
	var st unix.Stat_t
	return fstatat(trashFd, name, &st) == nil && uint64(st.Dev) == dev && uint64(st.Ino) == ino
}

// tagDisplaced tags a displaced object through a descriptor: the path is
// opened without following a symlink or blocking, and the descriptor must
// be the identity recorded for the object before the tag is written.
func tagDisplaced(path string, dev, ino uint64, tag []byte) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	st, err := fstatOf(fd)
	if err != nil {
		return err
	}
	if identOf(&st) != (ident{dev, ino}) {
		return trash.ErrItemChanged
	}
	return setTag(fd, tag)
}

// taggedAt reports whether path, reached through the session's verified
// walk to its parent (no symlink followed), is a regular file or directory
// with identity (dev, ino) carrying exactly tag: an item that something
// other than kipple put back from the Trash.
func taggedAt(sess *policy.Session, path string, dev, ino uint64, tag []byte) bool {
	parent, err := sess.Open(filepath.Dir(path))
	if err != nil || parent.Dir == nil {
		return false
	}
	defer func() { _ = parent.Dir.Close() }()
	pfd, name := int(parent.Dir.Fd()), filepath.Base(path)
	var st unix.Stat_t
	if fstatat(pfd, name, &st) != nil || identOf(&st) != (ident{dev, ino}) || !taggableMode(policy.StatMode(uint32(st.Mode))) {
		return false
	}
	fd, err := unix.Openat(pfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer func() { _ = unix.Close(fd) }()
	got, err := fstatOf(fd)
	return err == nil && identOf(&got) == (ident{dev, ino}) && tagMatches(fd, tag)
}

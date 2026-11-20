//go:build unix

package apply

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"time"

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
	// Put the mode back through the descriptor wherever the folder now is.
	// A failure here leaves owner write on a folder that moved; it never
	// turns a completed move into a reported failure.
	_ = unix.Fchmod(int(dir.Fd()), mode)
	return err
}

func isPermission(err error) bool { return errors.Is(err, fs.ErrPermission) }

// protectFunc reports whether an entry inside an item is protected
// (policy.Guard.CheckMode) and why.
type protectFunc func(rel, name string, dev, ino uint64, mode fs.FileMode) (string, bool)

// removal counts what a permanent delete removed.
type removal struct{ count, bytes int64 }

// errProtected: a permanent delete met a protected entry and stopped.
type errProtected struct{ rel, reason string }

func (e *errProtected) Error() string {
	return "found " + e.rel + " (" + e.reason + ") inside the item; stopped"
}

// errChanged: during a permanent delete an entry was not the one the
// check pass recorded, was moved, or changed after the move-time checks
// began. Nothing more is deleted.
type errChanged struct{ rel, why string }

func (e *errChanged) Error() string {
	rel := e.rel
	if rel == "" {
		rel = "the item"
	}
	return rel + " " + e.why + "; stopped"
}

// Test hooks for the permanent delete.
var (
	// afterPrecheck runs between the check pass and the delete pass.
	afterPrecheck func()
	// beforeDescend runs in the delete pass before a subfolder is opened.
	beforeDescend func(rel string)
	// afterDescend runs in the delete pass after a subfolder is opened and
	// before anything inside it is checked or deleted.
	afterDescend func(rel string)
)

type ident struct{ dev, ino uint64 }

func identOf(st *unix.Stat_t) ident { return ident{uint64(st.Dev), uint64(st.Ino)} }

func ctimeOf(st *unix.Stat_t) time.Time { return time.Unix(st.Ctim.Unix()) }

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

// removeItem deletes the approved item for --permanent. It is one walk
// that holds a descriptor for every folder it works in and never opens
// anything by path:
//
//   - The item is opened from its verified parent descriptor and must be
//     the approved (dev, ino).
//   - A check pass, which deletes nothing, records the (dev, ino) of every
//     entry by its item-relative path, and refuses the item if any entry is
//     protected, on another device, or changed (ctime) at or after since,
//     the moment the move-time checks began: a folder swapped in, or a
//     file added, after that is refused.
//   - The delete pass walks depth-first through held descriptors. Before
//     touching anything inside a folder it checks that the folder's
//     descriptor is still the recorded object, that its ".." is the held
//     parent, and that it is on the item's device. Every entry must be the
//     recorded object and not protected before it is unlinked; a folder is
//     removed only after its contents were deleted through its own held
//     descriptor and only if its name still leads to that object.
//
// Any mismatch stops the delete. It returns what was removed, also on
// error.
func removeItem(f *policy.Fresh, dev, ino uint64, protect protectFunc, since time.Time) (removal, error) {
	var rm removal
	want := ident{dev, ino}
	var st unix.Stat_t
	if err := fstatat(f.ParentFd, f.Name, &st); err != nil {
		return rm, err
	}
	if identOf(&st) != want {
		return rm, trash.ErrItemChanged
	}
	pst, err := fstatOf(f.ParentFd)
	if err != nil {
		return rm, err
	}
	parent := identOf(&pst)
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if !ctimeOf(&st).Before(since) {
			return rm, &errChanged{why: "changed after the checks began"}
		}
		if err := unix.Unlinkat(f.ParentFd, f.Name, 0); err != nil {
			return rm, err
		}
		rm.count++
		rm.bytes += st.Blocks * 512
		return rm, nil
	}
	fd, err := openDirAt(f.ParentFd, f.Name)
	if err != nil {
		return rm, err
	}
	defer func() { _ = unix.Close(fd) }()
	ids := map[string]ident{}
	if err := precheck(fd, "", want, parent, dev, protect, since, ids, 0); err != nil {
		return rm, err
	}
	if afterPrecheck != nil {
		afterPrecheck()
	}
	if err := deleteContents(fd, "", parent, dev, protect, ids, &rm, 0); err != nil {
		return rm, err
	}
	if err := checkHeld(fd, "", want, parent, dev); err != nil {
		return rm, err
	}
	if err := fstatat(f.ParentFd, f.Name, &st); err != nil || identOf(&st) != want {
		return rm, &errChanged{why: "is no longer at its path"}
	}
	if err := unix.Unlinkat(f.ParentFd, f.Name, unix.AT_REMOVEDIR); err != nil {
		return rm, err
	}
	rm.count++
	rm.bytes += st.Blocks * 512
	return rm, nil
}

// checkHeld verifies the folder open at fd is the object want, that its
// ".." is parent, and that it is on device dev.
func checkHeld(fd int, rel string, want, parent ident, dev uint64) error {
	st, err := fstatOf(fd)
	if err != nil {
		return err
	}
	if identOf(&st) != want {
		return &errChanged{rel: rel, why: "is not the folder that was checked"}
	}
	if uint64(st.Dev) != dev {
		return errCrossDevice
	}
	var up unix.Stat_t
	if err := fstatat(fd, "..", &up); err != nil || identOf(&up) != parent {
		return &errChanged{rel: rel, why: "was moved out of its folder"}
	}
	return nil
}

func join(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// precheck walks the folder open at fd (the object self, inside parent)
// without deleting anything, recording identities in ids.
func precheck(fd int, rel string, self, parent ident, dev uint64, protect protectFunc, since time.Time, ids map[string]ident, depth int) error {
	if depth > maxRemoveDepth {
		return fmt.Errorf("deeper than %d levels; stopped", maxRemoveDepth)
	}
	if err := checkHeld(fd, rel, self, parent, dev); err != nil {
		return err
	}
	ids[rel] = self
	names, err := readNames(fd)
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		child := join(rel, n)
		var st unix.Stat_t
		if err := fstatat(fd, n, &st); err != nil {
			return &errChanged{rel: child, why: "could not be inspected (" + err.Error() + ")"}
		}
		if uint64(st.Dev) != dev {
			return errCrossDevice
		}
		if protect != nil {
			if reason, hit := protect(child, n, uint64(st.Dev), uint64(st.Ino), policy.StatMode(uint32(st.Mode))); hit {
				return &errProtected{rel: child, reason: reason}
			}
		}
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			cfd, err := openDirAt(fd, n)
			if err != nil {
				return err
			}
			err = precheck(cfd, child, identOf(&st), self, dev, protect, since, ids, depth+1)
			_ = unix.Close(cfd)
			if err != nil {
				return err
			}
		} else {
			ids[child] = identOf(&st)
		}
		if !ctimeOf(&st).Before(since) {
			return &errChanged{rel: child, why: "changed after the checks began"}
		}
	}
	// Checked last, so a protected entry inside is reported as such.
	st, err := fstatOf(fd)
	if err != nil {
		return err
	}
	if !ctimeOf(&st).Before(since) {
		return &errChanged{rel: rel, why: "changed after the checks began"}
	}
	return nil
}

// deleteContents deletes everything inside the folder open at fd (item-
// relative rel, inside parent) through held descriptors.
func deleteContents(fd int, rel string, parent ident, dev uint64, protect protectFunc, ids map[string]ident, rm *removal, depth int) error {
	if depth > maxRemoveDepth {
		return fmt.Errorf("deeper than %d levels; stopped", maxRemoveDepth)
	}
	self, ok := ids[rel]
	if !ok {
		return &errChanged{rel: rel, why: "was not checked"}
	}
	if err := checkHeld(fd, rel, self, parent, dev); err != nil {
		return err
	}
	names, err := readNames(fd)
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		child := join(rel, n)
		var st unix.Stat_t
		if err := fstatat(fd, n, &st); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return err
		}
		if protect != nil {
			if reason, hit := protect(child, n, uint64(st.Dev), uint64(st.Ino), policy.StatMode(uint32(st.Mode))); hit {
				return &errProtected{rel: child, reason: reason}
			}
		}
		want, ok := ids[child]
		if !ok || identOf(&st) != want {
			return &errChanged{rel: child, why: "is not the entry that was checked"}
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			if err := unix.Unlinkat(fd, n, 0); err != nil {
				return err
			}
			rm.count++
			rm.bytes += st.Blocks * 512
			continue
		}
		if beforeDescend != nil {
			beforeDescend(child)
		}
		cfd, err := openDirAt(fd, n)
		if err != nil {
			return err
		}
		if afterDescend != nil {
			afterDescend(child)
		}
		err = deleteContents(cfd, child, self, dev, protect, ids, rm, depth+1)
		if err == nil {
			err = checkHeld(cfd, child, want, self, dev)
		}
		_ = unix.Close(cfd)
		if err != nil {
			return err
		}
		var now unix.Stat_t
		if err := fstatat(fd, n, &now); err != nil || identOf(&now) != want {
			return &errChanged{rel: child, why: "is no longer at its path"}
		}
		if err := unix.Unlinkat(fd, n, unix.AT_REMOVEDIR); err != nil {
			return err
		}
		rm.count++
		rm.bytes += st.Blocks * 512
	}
	return nil
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

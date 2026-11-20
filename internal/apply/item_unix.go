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
	if cerr := unix.Fchmod(int(dir.Fd()), mode); cerr != nil && err == nil {
		err = cerr
	}
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

// afterPrecheck, in tests, runs between the check-only pass and the
// deleting pass of a permanent delete.
var afterPrecheck func()

// removeItem deletes the approved item for --permanent, from its verified
// parent descriptor. It never follows a symlink, stops at a device change,
// and checks every entry against protect: first in a pass that deletes
// nothing, then again as it deletes, stopping at the first protected entry.
// It returns what was removed, also on error.
func removeItem(f *policy.Fresh, dev, ino uint64, protect protectFunc) (removal, error) {
	var rm removal
	var st unix.Stat_t
	if err := fstatat(f.ParentFd, f.Name, &st); err != nil {
		return rm, err
	}
	if uint64(st.Dev) != dev || uint64(st.Ino) != ino {
		return rm, trash.ErrItemChanged
	}
	if st.Mode&unix.S_IFMT == unix.S_IFDIR {
		if err := walkTree(f.ParentFd, f.Name, "", dev, protect, nil, 0); err != nil {
			return rm, err
		}
		if afterPrecheck != nil {
			afterPrecheck()
		}
	}
	err := walkTree(f.ParentFd, f.Name, "", dev, protect, &rm, 0)
	return rm, err
}

// walkTree checks (rm nil) or deletes (rm set) the entry name in dirfd and,
// for a directory, everything below it in name order. rel is the entry's
// item-relative path ("" for the item itself).
func walkTree(dirfd int, name, rel string, dev uint64, protect protectFunc, rm *removal, depth int) error {
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
	if rel != "" && protect != nil {
		if reason, hit := protect(rel, name, uint64(st.Dev), uint64(st.Ino), policy.StatMode(uint32(st.Mode))); hit {
			return &errProtected{rel: rel, reason: reason}
		}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if rm == nil {
			return nil
		}
		if err := unix.Unlinkat(dirfd, name, 0); err != nil {
			return err
		}
		rm.count++
		rm.bytes += st.Blocks * 512
		return nil
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
	sort.Strings(names)
	if err == nil {
		for _, n := range names {
			child := n
			if rel != "" {
				child = rel + "/" + n
			}
			if err = walkTree(fd, n, child, dev, protect, rm, depth+1); err != nil {
				break
			}
		}
	}
	_ = unix.Close(fd)
	if err != nil || rm == nil {
		return err
	}
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	rm.count++
	rm.bytes += st.Blocks * 512
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

func infoOf(st *unix.Stat_t) entryInfo {
	return entryInfo{kind: policy.StatMode(uint32(st.Mode)), dev: uint64(st.Dev), ino: uint64(st.Ino), size: st.Size, mtime: time.Unix(st.Mtim.Unix()).UTC()}
}

// fdSource reaches Trash entries through the verified Trash descriptor.
type fdSource struct{ tr *trash.DirTrasher }

func (s fdSource) dir() string { return s.tr.Path() }

func (s fdSource) stat(name string) (entryInfo, error) {
	var st unix.Stat_t
	if err := fstatat(s.tr.Fd(), name, &st); err != nil {
		return entryInfo{}, err
	}
	return infoOf(&st), nil
}

func (s fdSource) moveOut(name string, dst *policy.Fresh, want entryInfo) error {
	return moveBack(s.tr.Fd(), name, dst, want.dev, want.ino, want.kind.IsDir())
}

// pathSource reaches Trash entries by path, for when the Trash folder
// cannot be opened. Each move is checked: the entry must be the approved
// object immediately before, and the object that arrives must be it.
type pathSource struct{ d string }

func (s pathSource) dir() string { return s.d }

func (s pathSource) stat(name string) (entryInfo, error) {
	var st unix.Stat_t
	if err := unix.Lstat(s.d+"/"+name, &st); err != nil {
		return entryInfo{}, err
	}
	return infoOf(&st), nil
}

func (s pathSource) moveOut(name string, dst *policy.Fresh, want entryInfo) error {
	got, err := s.stat(name)
	if err != nil {
		return err
	}
	if got.dev != want.dev || got.ino != want.ino {
		return trash.ErrItemChanged
	}
	if err := trash.MoveNoReplaceFrom(s.d+"/"+name, dst.ParentFd, dst.Name); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := fstatat(dst.ParentFd, dst.Name, &st); err != nil || uint64(st.Dev) != want.dev || uint64(st.Ino) != want.ino {
		return fmt.Errorf("%w: the object restored to %s is not the approved one", trash.ErrItemChanged, dst.Name)
	}
	return nil
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

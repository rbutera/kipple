//go:build linux

package trash

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/policy"
)

// infoSuffix ends every file in the Trash's info folder.
const infoSuffix = ".trashinfo"

// FreedesktopTrasher moves items into a freedesktop.org Trash (Trash
// specification 1.0; design L4 of linux-port): the home Trash
// ~/.local/share/Trash, with its files and info folders held open by
// descriptor. Build it with NewFreedesktopTrasher from descriptors the
// caller verified (apply opens them through the policy session).
//
// Moving one item:
//
//  1. The item is checked by fstatat on its verified parent descriptor: it
//     must be the approved (dev, ino) and on the same device as files/,
//     or nothing happens (ErrItemChanged, ErrDifferentDevice).
//  2. A name is reserved by creating info/<name>.trashinfo with O_EXCL
//     (Finder-style "name 2" when taken), then written with Path and
//     DeletionDate and synced, together with the info folder.
//  3. The item is renamed into files/<name> with renameat2
//     RENAME_NOREPLACE from the parent descriptor. An orphan already at
//     files/<name> (EEXIST) makes it remove its own info file and try the
//     next name; EXDEV removes it and returns ErrDifferentDevice. Nothing is
//     ever copied or replaced.
//  4. The object that arrived must be the one inspected; otherwise it is
//     moved back (ErrItemChanged, its info file removed) or, if that fails,
//     reported as a DisplacedError with its info file kept so a file
//     manager can still restore it.
//
// An info file is removed only after fstatat shows the inode written in
// step 2: kipple never removes an info file it did not write. A process
// killed between steps 2 and 3 leaves an info file with no item, which the
// specification tells implementations to ignore.
type FreedesktopTrasher struct {
	mu       sync.Mutex
	path     string // the Trash folder: <home>/.local/share/Trash
	files    *DirTrasher
	infoFd   int
	infoDev  uint64
	infoIno  uint64
	infoPath string

	// now stamps DeletionDate (time.Now when nil).
	now func() time.Time
	// beforeRename and afterRename, in tests, run around the rename into
	// files/ with the candidate name.
	beforeRename, afterRename func(cand string)
}

// NewFreedesktopTrasher wraps the open files and info folders of the
// Trash at path (both verified by the caller, for example with
// policy.Session.Open). The descriptors are duplicated, so the caller
// still closes its files. Both must be directories on one device, and
// path+"/files" and path+"/info" must still name them.
func NewFreedesktopTrasher(path string, files, info *os.File) (*FreedesktopTrasher, error) {
	ft, err := NewDirTrasher(files, path+"/files")
	if err != nil {
		return nil, err
	}
	fd, err := unix.Dup(int(info.Fd()))
	if err != nil {
		_ = ft.Close()
		return nil, fmt.Errorf("trash: %w", err)
	}
	unix.CloseOnExec(fd)
	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = ft.Close()
		_ = unix.Close(fd)
		return nil, fmt.Errorf("trash: %s/info is not a directory", path)
	}
	t := &FreedesktopTrasher{path: path, files: ft, infoFd: fd, infoDev: st.Dev, infoIno: st.Ino, infoPath: path + "/info"}
	if t.infoDev != ft.dev {
		_ = t.Close()
		return nil, fmt.Errorf("%w: %s/files and %s/info", ErrDifferentDevice, path, path)
	}
	if err := t.verify(); err != nil {
		_ = t.Close()
		return nil, err
	}
	return t, nil
}

// Path returns the Trash folder (holding files and info).
func (t *FreedesktopTrasher) Path() string { return t.path }

// Files returns the files folder, where items are; restore moves them out
// of it. It is owned by t.
func (t *FreedesktopTrasher) Files() *DirTrasher { return t.files }

// InfoPath returns the info folder.
func (t *FreedesktopTrasher) InfoPath() string { return t.infoPath }

// Close releases both descriptors.
func (t *FreedesktopTrasher) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	err := t.files.Close()
	if t.infoFd >= 0 {
		if cerr := unix.Close(t.infoFd); err == nil {
			err = cerr
		}
		t.infoFd = -1
	}
	return err
}

// verify checks that both folders' paths still name the directories held.
func (t *FreedesktopTrasher) verify() error {
	if err := t.files.Verify(); err != nil {
		return err
	}
	if t.infoFd < 0 {
		return fmt.Errorf("trash: %s: closed", t.infoPath)
	}
	fd, err := openNoFollow(t.infoPath)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrTrashChanged, t.infoPath, err)
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil || st.Dev != t.infoDev || st.Ino != t.infoIno {
		return fmt.Errorf("%w: %s", ErrTrashChanged, t.infoPath)
	}
	return nil
}

// Trash implements Trasher.
func (t *FreedesktopTrasher) Trash(parentFd int, name string) (string, error) {
	return t.trash(parentFd, name, nil)
}

// TrashExpect implements ExpectTrasher: it refuses with ErrItemChanged,
// moving nothing, unless the entry is the object (dev, ino).
func (t *FreedesktopTrasher) TrashExpect(parentFd int, name string, dev, ino uint64) (string, error) {
	return t.trash(parentFd, name, &[2]uint64{dev, ino})
}

// TrashExpectAt is TrashExpect for an item whose parent folder the caller
// verified to be parent (an absolute path) immediately before: the info
// file records parent/name, not whatever the descriptor's path is by then.
// The descriptor must still be at parent before the name is reserved and
// again after the rename; if it moved in between (a rename or exchange of
// the parent), the item is moved back and ErrItemChanged is returned, so
// no info file ever describes a location the item did not come from.
func (t *FreedesktopTrasher) TrashExpectAt(parentFd int, name, parent string, dev, ino uint64) (string, error) {
	dest, _, err := t.TrashExpectAtInfo(parentFd, name, parent, dev, ino)
	return dest, err
}

// TrashExpectAtInfo is TrashExpectAt that also returns the inode of the
// info file it wrote, for the receipt: restore later removes only that file.
func (t *FreedesktopTrasher) TrashExpectAtInfo(parentFd int, name, parent string, dev, ino uint64) (string, uint64, error) {
	if !strings.HasPrefix(parent, "/") {
		return "", 0, fmt.Errorf("trash: parent %q is not absolute", parent)
	}
	return t.trashAt(parentFd, name, parent, &[2]uint64{dev, ino})
}

func (t *FreedesktopTrasher) trash(parentFd int, name string, want *[2]uint64) (string, error) {
	dest, _, err := t.trashAt(parentFd, name, "", want)
	return dest, err
}

// parentAt checks that the descriptor parentFd is still at parent.
func parentAt(parentFd int, parent string) error {
	got, err := policy.DescriptorPath(parentFd)
	if err != nil {
		return fmt.Errorf("trash: the location of the parent folder: %w", err)
	}
	if got != parent {
		return fmt.Errorf("%w: the parent folder moved from %s to %s", ErrItemChanged, parent, got)
	}
	return nil
}

func (t *FreedesktopTrasher) trashAt(parentFd int, name, planned string, want *[2]uint64) (string, uint64, error) {
	if !validName(name) {
		return "", 0, ErrInvalidName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.verify(); err != nil {
		return "", 0, err
	}
	var pst, ist unix.Stat_t
	if err := fstat(parentFd, &pst); err != nil {
		return "", 0, fmt.Errorf("trash: parent of %s: %w", name, err)
	}
	if err := fstatat(parentFd, name, &ist); err != nil {
		return "", 0, fmt.Errorf("trash: %s: %w", name, err)
	}
	if want != nil && (ist.Dev != want[0] || ist.Ino != want[1]) {
		return "", 0, ErrItemChanged
	}
	if pst.Dev != t.files.dev || ist.Dev != t.files.dev {
		return "", 0, ErrDifferentDevice
	}
	parent := planned
	if parent == "" {
		p, err := policy.DescriptorPath(parentFd)
		if err != nil {
			return "", 0, fmt.Errorf("trash: the original location of %s: %w", name, err)
		}
		parent = p
	} else if err := parentAt(parentFd, parent); err != nil {
		return "", 0, err
	}
	original := parent + "/" + name
	if parent == "/" {
		original = "/" + name
	}
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	content, err := EncodeTrashInfo(original, now())
	if err != nil {
		return "", 0, err
	}
	regular := ist.Mode&unix.S_IFMT == unix.S_IFREG
	for i := 1; i < maxCollisions; i++ {
		cand := candidateName(name, regular, i)
		info, err := t.reserve(cand, content)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", 0, err
		}
		if t.beforeRename != nil {
			t.beforeRename(cand)
		}
		err = MoveNoReplace(parentFd, name, t.files.fd, cand)
		if t.afterRename != nil && err == nil {
			t.afterRename(cand)
		}
		switch {
		case err == nil:
			if aerr := t.files.checkArrived(parentFd, name, cand, ist); aerr != nil {
				var de *DisplacedError
				if errors.As(aerr, &de) {
					// Something moved and stayed: keep its info file so
					// a file manager can restore it too.
					de.InfoIno = info[1]
					return de.Dest, info[1], aerr
				}
				t.removeOwn(cand, info)
				return "", 0, aerr
			}
			if planned != "" {
				if perr := parentAt(parentFd, planned); perr != nil {
					// The parent moved while the item was being moved:
					// the info file would name the wrong place. Put
					// the item back into its parent, wherever that is.
					if berr := MoveNoReplace(t.files.fd, cand, parentFd, name); berr != nil {
						de := &DisplacedError{Dest: t.files.path + "/" + cand, Why: "the parent folder moved during the move and the item could not be put back (" + berr.Error() + ")"}
						var got unix.Stat_t
						if fstatat(t.files.fd, cand, &got) == nil {
							de.Dev, de.Ino, de.Size, de.Mtime, de.Known = got.Dev, got.Ino, got.Size, time.Unix(got.Mtim.Unix()).UTC(), true
						}
						de.InfoIno = info[1]
						return de.Dest, info[1], de
					}
					t.removeOwn(cand, info)
					return "", 0, perr
				}
			}
			return t.files.path + "/" + cand, info[1], nil
		case errors.Is(err, unix.EEXIST):
			// An orphan in files/ with no info file: leave it alone.
			t.removeOwn(cand, info)
			continue
		case errors.Is(err, unix.EXDEV):
			t.removeOwn(cand, info)
			return "", 0, ErrDifferentDevice
		default:
			t.removeOwn(cand, info)
			if errors.Is(err, ErrNoReplaceUnsupported) {
				// EINVAL also means other things (moving a folder into
				// itself); only a no-replace rename that fails on its own
				// proves the file system lacks it.
				if t.noReplaceWorks() {
					return "", 0, fmt.Errorf("trash: move %s: the rename was refused as invalid (%v)", name, unix.EINVAL)
				}
				return "", 0, err
			}
			return "", 0, fmt.Errorf("trash: move %s: %w", name, err)
		}
	}
	return "", 0, fmt.Errorf("trash: too many items named %q in %s", name, t.files.path)
}

// reserve creates info/<cand>.trashinfo exclusively, writes content, and
// syncs the file and the info folder. It returns the file's identity.
// EEXIST means the name is taken.
func (t *FreedesktopTrasher) reserve(cand string, content []byte) ([2]uint64, error) {
	name := cand + infoSuffix
	var fd int
	var err error
	for {
		fd, err = unix.Openat(t.infoFd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != unix.EINTR {
			break
		}
	}
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			return [2]uint64{}, err
		}
		if errors.Is(err, unix.ENAMETOOLONG) {
			// Names of 246 to 255 bytes fit in files/ but not with the
			// .trashinfo suffix. kipple does not shorten them (a shortened
			// name would no longer say what the item was): the item stays.
			return [2]uint64{}, fmt.Errorf("trash: the name is too long for the Trash: %q plus %q is more than 255 bytes, so the item was not moved", cand, infoSuffix)
		}
		return [2]uint64{}, fmt.Errorf("trash: reserving %s/%s: %w", t.infoPath, name, err)
	}
	f := os.NewFile(uintptr(fd), t.infoPath+"/"+name)
	var st unix.Stat_t
	err = fstat(fd, &st)
	id := [2]uint64{st.Dev, st.Ino}
	if err == nil {
		_, err = f.Write(content)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = fsyncFd(t.infoFd)
	}
	if err != nil {
		t.removeOwn(cand, id)
		return [2]uint64{}, fmt.Errorf("trash: writing %s/%s: %w", t.infoPath, name, err)
	}
	return id, nil
}

// removeOwn removes info/<cand>.trashinfo only if it is still the file
// reserve wrote (by inode).
func (t *FreedesktopTrasher) removeOwn(cand string, id [2]uint64) {
	name := cand + infoSuffix
	var st unix.Stat_t
	if fstatat(t.infoFd, name, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Dev != id[0] || st.Ino != id[1] {
		return
	}
	_ = unix.Unlinkat(t.infoFd, name, 0)
}

// RemoveInfo removes info/<name>.trashinfo after restore moved files/<name>
// back to original, but only if it is a regular file whose decoded Path is
// original and, when ino is not 0, whose inode is ino (the file apply
// wrote, recorded in the receipt). It reports whether it removed one; no info file at all is
// (false, nil). A file that is a symlink, not regular, unreadable, or
// describes another location is left in place and returned as an error,
// for restore to report as a warning.
func (t *FreedesktopTrasher) RemoveInfo(name, original string, ino uint64) (bool, error) {
	if !validName(name) || !validName(name+infoSuffix) {
		return false, ErrInvalidName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.infoFd < 0 {
		return false, fmt.Errorf("trash: %s: closed", t.infoPath)
	}
	file := t.infoPath + "/" + name + infoSuffix
	var fd int
	var err error
	for {
		fd, err = unix.Openat(t.infoFd, name+infoSuffix, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			break
		}
	}
	switch {
	case errors.Is(err, unix.ENOENT):
		return false, nil
	case errors.Is(err, unix.ELOOP):
		return false, fmt.Errorf("%s is a symbolic link; left in place", file)
	case err != nil:
		return false, fmt.Errorf("%s could not be read (%v); left in place", file, err)
	}
	f := os.NewFile(uintptr(fd), file)
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return false, fmt.Errorf("%s is not a regular file; left in place", file)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxInfoSize+1))
	if err != nil {
		return false, fmt.Errorf("%s could not be read (%v); left in place", file, err)
	}
	info, err := DecodeTrashInfo(data)
	if err != nil {
		return false, fmt.Errorf("%s is not a valid Trash info file (%v); left in place", file, err)
	}
	if info.Path != original {
		return false, fmt.Errorf("%s describes %s, not %s; left in place", file, strings.ToValidUTF8(info.Path, "\uFFFD"), original)
	}
	if ino != 0 && st.Ino != ino {
		return false, fmt.Errorf("%s is not the info file kipple wrote for this item; left in place", file)
	}
	var now unix.Stat_t
	if err := fstatat(t.infoFd, name+infoSuffix, &now); err != nil || now.Dev != st.Dev || now.Ino != st.Ino {
		return false, fmt.Errorf("%s changed while it was read; left in place", file)
	}
	if err := unix.Unlinkat(t.infoFd, name+infoSuffix, 0); err != nil {
		return false, fmt.Errorf("%s could not be removed: %v", file, err)
	}
	return true, nil
}

// noReplaceWorks reports whether a no-replace rename works in the Trash's
// file system (the item's, since they share a device): it creates an empty
// probe file in info/, renames it there with RENAME_NOREPLACE and removes
// it. A probe that cannot even be created counts as not working.
func (t *FreedesktopTrasher) noReplaceWorks() bool {
	var b [6]byte
	_, _ = rand.Read(b[:])
	a := ".kipple-probe-" + hex.EncodeToString(b[:])
	fd, err := unix.Openat(t.infoFd, a, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return false
	}
	_ = unix.Close(fd)
	err = MoveNoReplace(t.infoFd, a, t.infoFd, a+"-2")
	if err == nil {
		_ = unix.Unlinkat(t.infoFd, a+"-2", 0)
		return true
	}
	_ = unix.Unlinkat(t.infoFd, a, 0)
	return false
}

func fsyncFd(fd int) error {
	for {
		err := unix.Fsync(fd)
		if err != unix.EINTR {
			return err
		}
	}
}

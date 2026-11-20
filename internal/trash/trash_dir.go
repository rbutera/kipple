//go:build unix

package trash

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// maxCollisions bounds the "name N" search.
const maxCollisions = 10000

// DirTrasher moves items into one directory, held open by descriptor. Build
// it with OpenDirTrasher (a path, walked from / without following any
// symlink) or NewDirTrasher (a descriptor the caller already verified, such
// as the home folder's .Trash opened through a policy session).
//
// Before every move it checks that its path still names the directory it
// holds (ErrTrashChanged otherwise) and that the item, its parent and the
// Trash share a device (ErrDifferentDevice otherwise). After the move it
// checks that the object that arrived is the one it inspected, and moves it
// back if not (ErrItemChanged).
type DirTrasher struct {
	mu       sync.Mutex
	fd       int
	path     string
	dev, ino uint64
}

// OpenDirTrasher opens path, which must be absolute and clean, walking from
// the root with O_NOFOLLOW|O_DIRECTORY at every component.
func OpenDirTrasher(path string) (*DirTrasher, error) {
	fd, err := openNoFollow(path)
	if err != nil {
		return nil, fmt.Errorf("trash: open %s: %w", path, err)
	}
	return newDir(fd, path)
}

// NewDirTrasher wraps an open directory the caller has verified (for
// example with policy.Session.Open); path is where that directory is. The
// descriptor is duplicated, so the caller still closes f.
func NewDirTrasher(f *os.File, path string) (*DirTrasher, error) {
	fd, err := unix.Dup(int(f.Fd()))
	if err != nil {
		return nil, fmt.Errorf("trash: %w", err)
	}
	unix.CloseOnExec(fd)
	return newDir(fd, path)
}

func newDir(fd int, path string) (*DirTrasher, error) {
	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("trash: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("trash: %s is not a directory", path)
	}
	d := &DirTrasher{fd: fd, path: path, dev: uint64(st.Dev), ino: uint64(st.Ino)}
	if err := d.verify(); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return d, nil
}

// Path returns the Trash folder's path.
func (d *DirTrasher) Path() string { return d.path }

// Fd returns the Trash folder's descriptor, owned by d. Restore moves items
// out of it with MoveNoReplace.
func (d *DirTrasher) Fd() int { return d.fd }

// Identity returns the Trash folder's (st_dev, st_ino).
func (d *DirTrasher) Identity() (dev, ino uint64) { return d.dev, d.ino }

// Close releases the descriptor.
func (d *DirTrasher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fd < 0 {
		return nil
	}
	err := unix.Close(d.fd)
	d.fd = -1
	return err
}

// Verify checks that Path still names the directory d holds, walking from
// the root without following symlinks.
func (d *DirTrasher) Verify() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.verify()
}

func (d *DirTrasher) verify() error {
	if d.fd < 0 {
		return fmt.Errorf("trash: %s: closed", d.path)
	}
	fd, err := openNoFollow(d.path)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrTrashChanged, d.path, err)
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil || uint64(st.Dev) != d.dev || uint64(st.Ino) != d.ino {
		return fmt.Errorf("%w: %s", ErrTrashChanged, d.path)
	}
	return nil
}

// Trash implements Trasher.
func (d *DirTrasher) Trash(parentFd int, name string) (string, error) {
	return d.trash(parentFd, name, nil)
}

// TrashExpect is Trash for an item whose identity the caller approved: it
// refuses with ErrItemChanged, moving nothing, unless the entry is the
// object (dev, ino), and checks again after the move.
func (d *DirTrasher) TrashExpect(parentFd int, name string, dev, ino uint64) (string, error) {
	return d.trash(parentFd, name, &[2]uint64{dev, ino})
}

func (d *DirTrasher) trash(parentFd int, name string, want *[2]uint64) (string, error) {
	if !validName(name) {
		return "", ErrInvalidName
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.verify(); err != nil {
		return "", err
	}
	var pst, ist unix.Stat_t
	if err := fstat(parentFd, &pst); err != nil {
		return "", fmt.Errorf("trash: parent of %s: %w", name, err)
	}
	if err := fstatat(parentFd, name, &ist); err != nil {
		return "", fmt.Errorf("trash: %s: %w", name, err)
	}
	if want != nil && (uint64(ist.Dev) != want[0] || uint64(ist.Ino) != want[1]) {
		return "", ErrItemChanged
	}
	if uint64(pst.Dev) != d.dev || uint64(ist.Dev) != d.dev {
		return "", ErrDifferentDevice
	}
	stem, ext := name, ""
	if ist.Mode&unix.S_IFMT == unix.S_IFREG {
		if e := filepath.Ext(name); e != "" && e != name {
			stem, ext = strings.TrimSuffix(name, e), e
		}
	}
	for i := 1; i < maxCollisions; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s %d%s", stem, i, ext)
		}
		err := MoveNoReplace(parentFd, name, d.fd, cand)
		switch {
		case err == nil:
			if aerr := d.checkArrived(parentFd, name, cand, ist); aerr != nil {
				var de *DisplacedError
				if errors.As(aerr, &de) {
					return de.Dest, aerr
				}
				return "", aerr
			}
			return d.path + "/" + cand, nil
		case errors.Is(err, unix.EEXIST):
			continue
		case errors.Is(err, unix.EXDEV):
			return "", ErrDifferentDevice
		default:
			return "", fmt.Errorf("trash: move %s: %w", name, err)
		}
	}
	return "", fmt.Errorf("trash: too many items named %q in %s", name, d.path)
}

// checkArrived confirms the object now at cand in the Trash is the one
// inspected before the move. If another object was swapped in between, it
// is moved back to its name and ErrItemChanged is returned.
func (d *DirTrasher) checkArrived(parentFd int, name, cand string, want unix.Stat_t) error {
	var got unix.Stat_t
	if err := fstatat(d.fd, cand, &got); err == nil && got.Dev == want.Dev && got.Ino == want.Ino {
		return nil
	}
	if err := MoveNoReplace(d.fd, cand, parentFd, name); err != nil {
		de := &DisplacedError{Dest: d.path + "/" + cand, Why: "it could not be put back (" + err.Error() + ")"}
		if fstatat(d.fd, cand, &got) == nil {
			de.Dev, de.Ino, de.Size, de.Mtime, de.Known = uint64(got.Dev), uint64(got.Ino), got.Size, time.Unix(got.Mtim.Unix()).UTC(), true
		}
		return de
	}
	return ErrItemChanged
}

// openNoFollow opens an absolute, clean directory path one component at a
// time from the root with O_NOFOLLOW|O_DIRECTORY, so no symlink anywhere in
// the path is followed.
func openNoFollow(path string) (int, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, fmt.Errorf("%q is not an absolute clean path", path)
	}
	fd, err := openDirAt(unix.AT_FDCWD, "/")
	if err != nil {
		return -1, err
	}
	if path == "/" {
		return fd, nil
	}
	for _, c := range strings.Split(path[1:], "/") {
		next, err := openDirAt(fd, c)
		_ = unix.Close(fd)
		if err != nil {
			if errors.Is(err, unix.ELOOP) {
				return -1, fmt.Errorf("%s is or passes through a symlink: %w", path, fs.ErrInvalid)
			}
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

func openDirAt(dirfd int, name string) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func fstat(fd int, st *unix.Stat_t) error {
	for {
		err := unix.Fstat(fd, st)
		if err != unix.EINTR {
			return err
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

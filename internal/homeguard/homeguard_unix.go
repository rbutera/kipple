//go:build unix

package homeguard

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// maxDepth bounds the ".." walk; no real path is this deep.
const maxDepth = 4096

// System is the real file system.
func System() FS { return FS{Chain: chain, Stat: stat, MountPoint: mountPoint} }

func open(path string, flags int) (int, error) {
	for {
		fd, err := unix.Open(path, flags, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func openat(dirfd int, name string, flags int) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, flags, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func fstatID(fd int) (ID, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return ID{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return ID{}, unix.ENOTDIR
	}
	return ID{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, nil //nolint:unconvert // field widths differ by platform
}

func chain(dir string, nofollow bool) ([]ID, error) {
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC
	if nofollow {
		flags |= unix.O_NOFOLLOW
	}
	fd, err := open(dir, flags)
	if err != nil {
		return nil, err
	}
	var out []ID
	for i := 0; ; i++ {
		id, err := fstatID(fd)
		if err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1] == id {
			_ = unix.Close(fd) // ".." of the root is the root
			return out, nil
		}
		if i >= maxDepth {
			_ = unix.Close(fd)
			return nil, fmt.Errorf("more than %d ancestors", maxDepth)
		}
		out = append(out, id)
		parent, err := openat(fd, "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC)
		_ = unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("opening an ancestor: %w", err)
		}
		fd = parent
	}
}

func stat(path string) (ID, bool, error) {
	var st unix.Stat_t
	for {
		err := unix.Stat(path, &st)
		if err == unix.EINTR {
			continue
		}
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) {
			return ID{}, false, nil
		}
		if err != nil {
			return ID{}, false, err
		}
		return ID{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, true, nil //nolint:unconvert // field widths differ by platform
	}
}

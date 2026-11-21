//go:build unix

package policy

import (
	"io"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// device returns the st_dev of an lstat result (os.Lstat or a Session's
// fstatat).
func device(fi fs.FileInfo) (uint64, bool) {
	id, ok := identityOf(fi)
	return id.dev, ok
}

// identityOf returns the (st_dev, st_ino) of an lstat result (os.Lstat or a
// Session's fstatat).
func identityOf(fi fs.FileInfo) (fileID, bool) {
	switch st := fi.Sys().(type) {
	case *syscall.Stat_t:
		return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
	case *unix.Stat_t:
		return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
	default:
		return fileID{}, false
	}
}

// storedName reports whether dir (already verified by lstat to be a real
// directory) holds an entry whose stored name is byte-identical to name. It
// opens dir without following a symlink and stops reading once found.
func storedName(dir, name string) (bool, error) {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	for {
		names, err := f.Readdirnames(256)
		for _, n := range names {
			if n == name {
				return true, nil
			}
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}

// checkMounts refuses, with ReasonDifferentDevice, a path with a mount root
// between home and itself (inclusive), asking env.Mounts or the platform's
// oracle. It runs after checkFilesystem, so no component is a symlink.
func checkMounts(env Env, comps []string) Decision {
	m := env.Mounts
	if m == nil {
		fd, err := openHomeForMounts(env.Home)
		if err != nil {
			return refuse(missingOr(err))
		}
		m = systemMounts(env.Home, fd)
		_ = unix.Close(fd)
	}
	if m.Available() != nil {
		// As in a session: scan marks items contains_mount and apply
		// refuses to run (mounts_unavailable).
		return Decision{Allowed: true}
	}
	cur := env.Home
	for _, c := range comps {
		cur += "/" + c
		fi, err := lstat(cur)
		if err != nil {
			return refuse(missingOr(err))
		}
		id, ok := identityOf(fi)
		if !ok {
			return refuse(ReasonDifferentDevice)
		}
		mr, err := m.MountRoot(unix.AT_FDCWD, cur, id.dev, id.ino)
		if err != nil {
			return refuse(missingOr(err))
		}
		if mr {
			return refuse(ReasonDifferentDevice)
		}
	}
	return Decision{Allowed: true}
}

func openHomeForMounts(home string) (int, error) {
	for {
		fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

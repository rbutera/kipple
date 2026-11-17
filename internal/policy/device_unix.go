//go:build unix

package policy

import (
	"io"
	"io/fs"
	"os"
	"syscall"
)

// device returns the st_dev of an lstat result.
func device(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// identityOf returns the (st_dev, st_ino) of an lstat result.
func identityOf(fi fs.FileInfo) (fileID, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}
	return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
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

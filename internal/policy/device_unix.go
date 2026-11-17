//go:build unix

package policy

import (
	"io/fs"
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

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

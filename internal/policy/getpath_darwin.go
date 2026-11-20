//go:build darwin

package policy

import (
	"bytes"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// DescriptorPath returns the path the kernel currently associates with the
// open descriptor fd (fcntl F_GETPATH). Apply uses it immediately before a
// move to confirm that the verified parent directory is still where the
// plan says it is: a directory renamed or swapped after it was opened
// reports its new location.
func DescriptorPath(fd int) (string, error) {
	buf := make([]byte, unix.PathMax)
	for {
		//nolint:gosec // G103: F_GETPATH writes into buf; buf is kept alive below.
		_, err := unix.FcntlInt(uintptr(fd), unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buf[0]))))
		runtime.KeepAlive(buf)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return "", err
		}
		break
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf), nil
}

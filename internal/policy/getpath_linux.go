//go:build linux

package policy

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrNoDescriptorPath: /proc/self/fd could not be read (/proc is not
// mounted or not readable), so no descriptor's path can be confirmed.
var ErrNoDescriptorPath = errors.New("policy: /proc is not available, so descriptor paths cannot be read")

// procSelfFd is where the kernel exposes this process's descriptors.
const procSelfFd = "/proc/self/fd/"

// DescriptorPath returns the path the kernel currently associates with the
// open descriptor fd: the target of /proc/self/fd/<fd>. Apply uses it
// immediately before a move to confirm that the verified parent directory
// is still where the plan says it is: a directory renamed or moved after it
// was opened reports its new location. A deleted directory (the kernel
// appends " (deleted)"), a path that is not absolute, or /proc not being
// mounted is an error, so the move is refused.
func DescriptorPath(fd int) (string, error) {
	buf := make([]byte, unix.PathMax)
	for {
		n, err := unix.Readlink(procSelfFd+strconv.Itoa(fd), buf)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoDescriptorPath, err)
		}
		if n >= len(buf) {
			return "", errors.New("policy: the descriptor's path is too long")
		}
		p := string(buf[:n])
		if !strings.HasPrefix(p, "/") || strings.HasSuffix(p, " (deleted)") {
			return "", fmt.Errorf("policy: the descriptor's folder is gone or unreachable (the kernel reports %q)", p)
		}
		return p, nil
	}
}

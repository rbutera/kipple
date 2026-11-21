//go:build unix

package config

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// openConfig opens path read-only without following a symlink at the final
// component, and without blocking on a FIFO (the caller then rejects
// anything that is not a regular file).
func openConfig(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// describeOpenError explains a refused symlink.
func describeOpenError(path string, err error) error {
	if errors.Is(err, syscall.ELOOP) {
		return fmt.Errorf("%s is a symbolic link; kipple only reads a regular config file", path)
	}
	return err
}

// openFollowNonBlocking opens path following symlinks, without blocking on
// a FIFO or device (readConfig then requires a regular file).
func openFollowNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

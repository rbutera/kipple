//go:build !darwin

package policy

import "errors"

// ErrNoDescriptorPath is returned by DescriptorPath on platforms without
// fcntl(F_GETPATH). A Linux port reads /proc/self/fd/<fd> instead.
var ErrNoDescriptorPath = errors.New("policy: descriptor paths are not supported on this platform")

// DescriptorPath is unsupported here, so apply refuses every move.
func DescriptorPath(int) (string, error) { return "", ErrNoDescriptorPath }

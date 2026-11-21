//go:build !darwin && !linux

package policy

import "errors"

// ErrNoDescriptorPath is returned by DescriptorPath on platforms without a
// way to ask the kernel for a descriptor's path.
var ErrNoDescriptorPath = errors.New("policy: descriptor paths are not supported on this platform")

// DescriptorPath is unsupported here, so apply refuses every move.
func DescriptorPath(int) (string, error) { return "", ErrNoDescriptorPath }

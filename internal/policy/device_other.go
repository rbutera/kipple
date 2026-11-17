//go:build !unix

package policy

import "io/fs"

// device is unknown on non-unix platforms, so every path is refused with
// ReasonDifferentDevice. kipple targets macOS.
func device(fs.FileInfo) (uint64, bool) { return 0, false }

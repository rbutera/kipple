//go:build !unix

package policy

import "io/fs"

// device is unknown on non-unix platforms, so every path is refused with
// ReasonDifferentDevice. kipple targets macOS.
func device(fs.FileInfo) (uint64, bool) { return 0, false }

// storedName cannot be checked on non-unix platforms, so every path is refused.
func storedName(string, string) (bool, error) { return false, nil }

// identityOf is unknown on non-unix platforms.
func identityOf(fs.FileInfo) (fileID, bool) { return fileID{}, false }

// checkMounts has nothing to check here (every path is refused already).
func checkMounts(Env, []string) Decision { return Decision{Allowed: true} }

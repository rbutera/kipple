//go:build !darwin || !cgo

package trash

// NewFinderTrasher is the fallback for builds without the cgo NSFileManager
// shim (CGO_ENABLED=0, or not macOS): it returns ErrUnsupported.
func NewFinderTrasher(*DirTrasher) (ExpectTrasher, error) { return nil, ErrUnsupported }

// FinderAvailable reports whether this build has the NSFileManager
// fallback (macOS with cgo). kipple version prints it.
const FinderAvailable = false

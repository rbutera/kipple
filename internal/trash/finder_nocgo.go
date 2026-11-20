//go:build !darwin || !cgo

package trash

// NewFinderTrasher is the fallback for builds without the cgo NSFileManager
// shim (CGO_ENABLED=0, or not macOS): it returns ErrUnsupported, and kipple
// uses DirTrasher with the verified home Trash instead.
func NewFinderTrasher(*DirTrasher) (Trasher, error) { return nil, ErrUnsupported }

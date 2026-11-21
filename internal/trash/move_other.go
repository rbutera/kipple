//go:build !darwin && !linux

package trash

// MoveNoReplace is unsupported here: kipple supports macOS (move_darwin.go)
// and Linux (move_linux.go) only.
func MoveNoReplace(int, string, int, string) error { return ErrUnsupported }

//go:build !darwin

package trash

// MoveNoReplace is unsupported here. A Linux port implements it with
// renameat2(RENAME_NOREPLACE) in move_linux.go (design D17).
func MoveNoReplace(int, string, int, string) error { return ErrUnsupported }

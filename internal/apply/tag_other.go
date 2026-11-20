//go:build !darwin

package apply

import "github.com/rbutera/kipple/internal/trash"

// Extended-attribute tags are implemented for macOS only; elsewhere apply
// cannot tag, so it moves nothing (MoveNoReplace is unsupported there too).

func setTag(int, []byte) error     { return trash.ErrUnsupported }
func clearTag(int) error           { return trash.ErrUnsupported }
func tagMatches(int, []byte) bool  { return false }
func tagPath(string, []byte) error { return trash.ErrUnsupported }

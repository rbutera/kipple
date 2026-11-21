//go:build !darwin && !linux

package apply

import (
	"io/fs"

	"github.com/rbutera/kipple/internal/trash"
)

// Extended-attribute tags are implemented for macOS and Linux only;
// elsewhere apply cannot tag, so it moves nothing (MoveNoReplace is
// unsupported there too).

func setTag(int, []byte) error    { return trash.ErrUnsupported }
func clearTag(int) error          { return trash.ErrUnsupported }
func tagMatches(int, []byte) bool { return false }
func taggableMode(fs.FileMode) bool {
	return false
}

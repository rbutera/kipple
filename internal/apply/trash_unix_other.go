//go:build unix && !darwin && !linux

package apply

import (
	"fmt"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

// homeTrashRel: kipple supports macOS and Linux only.
const homeTrashRel = ".Trash"

const finderFallback = false

var errTrashCreatable error

func openHomeTrash(*policy.Session, string, bool) (*homeTrash, error) {
	return nil, trash.ErrUnsupported
}

func noReplaceError(_ int, err error) error { return fmt.Errorf("%w: %v", ErrTrashAccess, err) }

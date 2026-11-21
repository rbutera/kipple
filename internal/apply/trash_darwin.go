//go:build darwin

package apply

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

// homeTrashRel is where the macOS Trash's entries are, relative to home.
const homeTrashRel = ".Trash"

// finderFallback: when ~/.Trash cannot be opened for lack of permission
// (TCC: no Full Disk Access), Options.Finder may move items instead.
const finderFallback = true

// openHomeTrash opens <home>/.Trash through the session (no symlink on the
// way, same device as home, stored names) and checks it is a directory
// owned by this user. macOS creates ~/.Trash itself, so create is ignored:
// kipple never creates it.
func openHomeTrash(sess *policy.Session, home string, _ bool) (*homeTrash, error) {
	path := home + "/.Trash"
	e, err := sess.Open(path)
	if err != nil {
		return nil, fmt.Errorf("the Trash folder %s cannot be used: %w", path, err)
	}
	if e.Dir == nil {
		return nil, fmt.Errorf("the Trash folder %s is not a folder", path)
	}
	defer func() { _ = e.Dir.Close() }()
	if st, ok := e.Info.Sys().(*unix.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("the Trash folder %s is not owned by you", path)
	}
	d, err := trash.NewDirTrasher(e.Dir, path)
	if err != nil {
		return nil, err
	}
	return &homeTrash{dir: d, mover: d, name: plan.MoverRename, close: d.Close}, nil
}

// noReplaceError never happens on macOS (renameatx_np RENAME_EXCL is
// always available); it keeps apply's handling platform-neutral.
func noReplaceError(_ int, err error) error { return fmt.Errorf("%w: %v", ErrTrashAccess, err) }

// errTrashCreatable is never returned on macOS.
var errTrashCreatable error

package apply

import (
	"errors"
	"fmt"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

// TrashMode is how Run would reach the Trash with a given set of options.
type TrashMode string

const (
	// TrashRename: the home's Trash folder opens, so items move straight
	// into it (plan.MoverRename).
	TrashRename TrashMode = "rename"
	// TrashFallback: the Trash folder cannot be opened for lack of
	// permission (no Full Disk Access), and the NSFileManager mover is
	// available (plan.MoverNSFileManager).
	TrashFallback TrashMode = "fallback"
	// TrashUnavailable: neither works; Run would return an error (for lack
	// of permission, ErrTrashAccess) and move nothing.
	TrashUnavailable TrashMode = "unavailable"
)

// ProbeTrash reports how Run would move items with o, so a front end can
// say so before anything is attempted. It makes the same choice Run makes,
// in the same order, but only opens the Trash folder through a policy
// session and closes it again: it moves nothing, writes nothing and creates
// no receipt. For TrashUnavailable the error says why.
func ProbeTrash(o Options) (TrashMode, error) {
	if o.Env.HomeIdentity == nil {
		return TrashUnavailable, errors.New("apply: the home folder's identity is not pinned")
	}
	if o.Trash != nil {
		return TrashRename, nil
	}
	sess, err := policy.NewSession(o.Env)
	if err != nil {
		_ = sess.Close()
		return TrashUnavailable, fmt.Errorf("apply: %w", err)
	}
	defer func() { _ = sess.Close() }()
	open := o.openTrash
	if open == nil {
		open = func() (*trash.DirTrasher, error) { return openHomeTrash(sess, o.Env.Home) }
	}
	tr, err := open()
	switch {
	case err == nil:
		_ = tr.Close()
		return TrashRename, nil
	case isPermission(err) && o.Finder != nil:
		if _, ferr := o.Finder(); ferr != nil {
			return TrashUnavailable, fmt.Errorf("%w: %v; the NSFileManager fallback is unavailable: %v", ErrTrashAccess, err, ferr)
		}
		return TrashFallback, nil
	case isPermission(err):
		return TrashUnavailable, fmt.Errorf("%w: %v", ErrTrashAccess, err)
	default:
		return TrashUnavailable, fmt.Errorf("apply: %w", err)
	}
}

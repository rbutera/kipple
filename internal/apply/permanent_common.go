package apply

import (
	"errors"
	"io/fs"
)

// ErrPermanentUnavailable: this build has no permanent deletion. v0.1.0
// ships without it (build tag kipple_permanent); items go to the Trash.
var ErrPermanentUnavailable = errors.New("permanent deletion is not available in this release; items go to the Trash. See docs/safety.md#permanent")

// protectFunc reports whether an entry inside an item is protected
// (policy.Guard.CheckMode) and why.
type protectFunc func(rel, name string, dev, ino uint64, mode fs.FileMode) (string, bool)

// removal counts what a permanent delete removed.
type removal struct{ count, bytes int64 }

// errProtected: a permanent delete met a protected entry and stopped.
type errProtected struct{ rel, reason string }

func (e *errProtected) Error() string {
	return "found " + e.rel + " (" + e.reason + ") inside the item; stopped"
}

// errChanged: during a permanent delete an entry was not the one the
// check pass recorded, was moved, or changed after the move-time checks
// began. Nothing more is deleted.
type errChanged struct{ rel, why string }

func (e *errChanged) Error() string {
	rel := e.rel
	if rel == "" {
		rel = "the item"
	}
	return rel + " " + e.why + "; stopped"
}

// mountFunc is policy.Session.MountRoot: whether an entry is a mount root
// (a same-device bind mount on Linux), which a permanent delete must not
// cross.
type mountFunc func(dirfd int, name string, dev, ino uint64) (bool, error)

// refreshFunc is policy.Session.RefreshMounts: it reads the mount table
// again where the mount oracle works from a snapshot (/proc/self/mountinfo
// when statx is denied). The permanent delete calls it before deleting in
// each folder, so a mount made after the check pass is still seen.
type refreshFunc func() error

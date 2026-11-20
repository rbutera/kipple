package apply

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/trash"
)

// RestoreOptions configure Restore.
type RestoreOptions struct {
	// Env is the policy environment, with HomeIdentity pinned.
	Env policy.Env
	// Rules is the effective catalogue; each item is re-authorised for its
	// own rule.
	Rules []rules.Rule
	// Receipt is the receipt to restore from (untrusted input).
	Receipt receipt.Entry
	// Store saves the receipt's restored annotations. Leave the zero value
	// for a receipt loaded from a file outside the store; it is then not
	// annotated.
	Store *receipt.Store
	// Only, when set, restores only these item IDs.
	Only []string
	// Apply moves items; without it Restore only reports what it would do.
	Apply bool
	// Trash is the Trash folder items are restored from (the home folder's
	// .Trash when nil). An item whose recorded Trash path is not directly
	// inside it is skipped with not_in_trash.
	Trash *trash.DirTrasher
	// Now is the clock (time.Now when nil).
	Now func() time.Time

	// beforeMove, in tests, runs immediately before the parent re-check.
	beforeMove func(plan.MovedItem)
	// openTrash, in tests, replaces opening the home .Trash.
	openTrash func() (*trash.DirTrasher, error)
}

// Restore moves items recorded in a receipt from the Trash back to their
// original paths: only when the original path is vacant, its parent still
// exists, and policy.Session.AuthorizeDestination approves it for the
// item's rule with the same lexical, walk and identity checks as a move;
// only when the Trash entry is the very object kipple moved there (dev,
// ino); and with renameatx_np(RENAME_EXCL), so nothing is ever replaced.
func Restore(ctx context.Context, o RestoreOptions) (plan.Result, error) {
	rc := o.Receipt.Receipt
	res := plan.Result{SchemaVersion: plan.SchemaVersion, Command: "restore", DryRun: !o.Apply, Home: o.Env.Home, ReceiptID: rc.ID, ReceiptPath: o.Receipt.Path}
	res.Normalize()
	if o.Env.HomeIdentity == nil {
		return res, errors.New("restore: the home folder's identity is not pinned")
	}
	if rc.Home != o.Env.Home {
		return res, fmt.Errorf("restore: receipt %s is for home %s, not %s", rc.ID, rc.Home, o.Env.Home)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	sess, err := policy.NewSession(o.Env)
	if err != nil {
		_ = sess.Close()
		return res, fmt.Errorf("restore: %w", err)
	}
	defer func() { _ = sess.Close() }()

	skip := func(m plan.MovedItem, reason, detail string) {
		res.Skipped = append(res.Skipped, plan.SkippedItem{ID: m.ID, Rule: m.Rule, Path: m.Path, Reason: reason, Detail: detail})
	}
	byID := map[string]plan.MovedItem{}
	for _, m := range rc.Moved {
		byID[m.ID] = m
	}
	var asked []plan.MovedItem
	if len(o.Only) > 0 {
		seen := map[string]bool{}
		for _, id := range o.Only {
			if seen[id] {
				continue
			}
			seen[id] = true
			m, ok := byID[id]
			if !ok {
				skip(plan.MovedItem{ID: id}, plan.SkipNotFound, "not in receipt "+rc.ID)
				continue
			}
			asked = append(asked, m)
		}
	} else {
		asked = rc.Moved
	}
	ruleByID := map[string]rules.Rule{}
	for _, r := range o.Rules {
		ruleByID[r.ID] = r
	}

	var todo []plan.MovedItem
	for _, m := range asked {
		switch {
		case m.Permanent:
			skip(m, plan.SkipDeletedPermanently, "deleted with --permanent; nothing to restore")
		case m.RestoredAt != nil:
			skip(m, plan.SkipAlreadyRestored, "restored "+m.RestoredAt.Format(time.RFC3339))
		default:
			if _, ok := ruleByID[m.Rule]; !ok {
				skip(m, plan.SkipNotFound, "unknown rule "+m.Rule)
				continue
			}
			todo = append(todo, m)
		}
	}
	if len(todo) == 0 {
		return res, nil
	}
	var src trashSource
	if o.Trash != nil {
		if err := o.Trash.Verify(); err != nil {
			return res, fmt.Errorf("restore: %w", err)
		}
		src = fdSource{o.Trash}
	} else {
		open := o.openTrash
		if open == nil {
			open = func() (*trash.DirTrasher, error) { return openHomeTrash(sess, o.Env.Home) }
		}
		tr, err := open()
		switch {
		case err == nil:
			defer func() { _ = tr.Close() }()
			src = fdSource{tr}
		case isPermission(err):
			// No Full Disk Access: reach each entry by its path in the Trash,
			// with the same identity checks before and after the move.
			src = pathSource{d: o.Env.Home + "/.Trash"}
			res.Warnings = append(res.Warnings, "the Trash folder cannot be opened without Full Disk Access; reaching items by path")
		default:
			return res, fmt.Errorf("restore: %w", err)
		}
	}
	var denied error

	annotated := false
	for _, m := range todo {
		if err := ctx.Err(); err != nil {
			skip(m, plan.SkipMoveFailed, "not attempted: interrupted")
			continue
		}
		if denied != nil {
			skip(m, plan.SkipMoveFailed, "not attempted: the Trash cannot be used")
			continue
		}
		name := filepath.Base(m.TrashPath)
		if m.TrashPath == "" || filepath.Dir(m.TrashPath) != src.dir() || !validName(name) {
			skip(m, plan.SkipNotInTrash, "the recorded Trash path is not directly inside "+src.dir())
			continue
		}
		info, err := src.stat(name)
		if err != nil {
			if isPermission(err) && len(res.Restored) == 0 {
				denied = err
				skip(m, plan.SkipMoveFailed, "the Trash cannot be used: "+err.Error())
				continue
			}
			skip(m, plan.SkipTrashItemMissing, "nothing at "+m.TrashPath)
			continue
		}
		if m.Dev == 0 || info.dev != m.Dev || info.ino != m.Ino || info.size != m.Size || !info.mtime.Equal(m.Mtime) {
			skip(m, plan.SkipTrashItemMismatch, m.TrashPath+" is not the object kipple moved there (identity, size or modification time differ from the receipt)")
			continue
		}
		dst, d := sess.AuthorizeDestination(ruleByID[m.Rule], m.Path, info.kind)
		if !d.Allowed {
			reason := plan.Refused(d.Reason)
			if d.Reason == policy.ReasonDestinationExists {
				reason = plan.SkipDestinationExists
			}
			skip(m, reason, "the original path cannot take the item back")
			continue
		}
		restored := plan.RestoredItem{ID: m.ID, Rule: m.Rule, Path: m.Path, TrashPath: m.TrashPath, Bytes: m.Bytes}
		if !o.Apply {
			_ = dst.Close()
			res.Restored = append(res.Restored, restored)
			continue
		}
		if o.beforeMove != nil {
			o.beforeMove(m)
		}
		if d := sess.VerifyParent(dst, filepath.Dir(m.Path)); !d.Allowed {
			_ = dst.Close()
			reason := plan.SkipParentChanged
			if d.Reason != policy.ReasonParentChanged {
				reason = plan.Refused(d.Reason)
			}
			skip(m, reason, "the destination folder changed immediately before the move")
			continue
		}
		err = src.moveOut(name, dst, info)
		_ = dst.Close()
		if err != nil {
			if isPermission(err) && len(res.Restored) == 0 {
				denied = err
			}
			skip(m, classify(err), err.Error())
			continue
		}
		res.Restored = append(res.Restored, restored)
		if receipt.MarkRestored(&rc, m.ID, o.Now()) {
			annotated = true
		}
	}
	if annotated && o.Store != nil {
		if err := o.Store.Save(rc); err != nil {
			res.Warnings = append(res.Warnings, "the receipt could not be annotated as restored: "+err.Error())
		}
	}
	res.Normalize()
	if denied != nil {
		return res, fmt.Errorf("%w: %v", ErrTrashAccess, denied)
	}
	return res, nil
}

// entryInfo is a Trash entry's type and identity.
type entryInfo struct {
	kind     fs.FileMode
	dev, ino uint64
	size     int64
	mtime    time.Time
}

// trashSource reaches entries of the Trash folder: by its verified
// descriptor (fdSource) or, without Full Disk Access, by path (pathSource).
type trashSource interface {
	dir() string
	stat(name string) (entryInfo, error)
	moveOut(name string, dst *policy.Fresh, want entryInfo) error
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && name != "/"
}

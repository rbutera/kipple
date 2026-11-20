package apply

import (
	"context"
	"errors"
	"fmt"
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
	tr := o.Trash
	if tr == nil {
		if tr, err = openHomeTrash(sess, o.Env.Home); err != nil {
			return res, fmt.Errorf("restore: %w", err)
		}
		defer func() { _ = tr.Close() }()
	}
	if err := tr.Verify(); err != nil {
		return res, fmt.Errorf("restore: %w", err)
	}

	annotated := false
	for _, m := range todo {
		if err := ctx.Err(); err != nil {
			skip(m, plan.SkipMoveFailed, "not attempted: interrupted")
			continue
		}
		name := filepath.Base(m.TrashPath)
		if m.TrashPath == "" || filepath.Dir(m.TrashPath) != tr.Path() || !validName(name) {
			skip(m, plan.SkipNotInTrash, "the recorded Trash path is not directly inside "+tr.Path())
			continue
		}
		kind, dev, ino, err := statAt(tr.Fd(), name)
		if err != nil {
			skip(m, plan.SkipTrashItemMissing, "nothing at "+m.TrashPath)
			continue
		}
		if m.Dev == 0 || dev != m.Dev || ino != m.Ino {
			skip(m, plan.SkipTrashItemChanged, m.TrashPath+" is not the object kipple moved there")
			continue
		}
		dst, d := sess.AuthorizeDestination(ruleByID[m.Rule], m.Path, kind)
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
		err = moveBack(tr.Fd(), name, dst, dev, ino, kind.IsDir())
		_ = dst.Close()
		if err != nil {
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
	return res, nil
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && name != "/"
}

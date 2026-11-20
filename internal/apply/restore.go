package apply

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/sizes"
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
	// Store saves the receipt's restored annotations. Leave it nil for a
	// receipt loaded from a file outside the store; it is then not
	// annotated.
	Store *receipt.Store
	// Only, when set, restores only these item IDs.
	Only []string
	// Apply moves items; without it Restore only reports what it would do.
	Apply bool
	// Trash is the Trash folder items are restored from (the home folder's
	// .Trash, opened through the pinned home descriptor, when nil). An
	// entry whose recorded Trash path is not directly inside it is skipped
	// with not_in_trash.
	Trash *trash.DirTrasher
	// Now is the clock (time.Now when nil).
	Now func() time.Time

	// beforeMove, in tests, runs immediately before the parent re-check.
	beforeMove func(plan.MovedItem)
	// openTrash, in tests, replaces opening the home .Trash.
	openTrash func() (*trash.DirTrasher, error)
}

// Restore moves entries recorded in a receipt (moved items, displaced
// objects, and pending items, which it finds in the Trash by identity)
// from the Trash back to their original paths. Receipts are
// untrusted, so for each entry:
//
//   - its ID must be the one its rule and path give (plan.ID);
//   - the Trash entry is opened only through the Trash folder's descriptor,
//     itself opened through the pinned home descriptor (never by path), and
//     must have the receipt's dev, ino, size and mtime and carry the tag
//     apply set when it moved it (tagValue: this receipt, this item, this
//     home); anything else is trash_item_mismatch;
//   - neither its name nor anything inside it may be protected
//     (contains_protected), so a forged receipt cannot launder a .env or a
//     key into a cache folder;
//   - the destination must pass AuthorizeDestination for the entry's own
//     rule (vacant, parent present, same lexical, walk and identity checks
//     as a move) and the parent re-check;
//   - the entry is renamed (RENAME_EXCL) to a random staging name in the
//     Trash and verified there by descriptor before it is renamed to the
//     destination's parent descriptor; nothing is ever replaced, and if the
//     object that arrives is not the entry it goes straight back into the
//     Trash and the receipt notes it.
//
// What these checks protect against: stale or mistaken receipts (a
// receipt for another home, an entry the user emptied or replaced, an
// entry that is not the object kipple moved, a destination the policy no
// longer allows). They do not stop a process running as the same user that
// can already edit kipple's receipts folder: it can compute the tag, which
// carries no secret (a Keychain-held MAC is on the roadmap). The CLI only
// restores from receipts inside that folder.
//
// Without access to the Trash folder (macOS TCC, no Full Disk Access),
// Restore returns ErrTrashAccess and moves nothing.
func Restore(ctx context.Context, o RestoreOptions) (plan.Result, error) {
	rc := o.Receipt.Receipt
	rc.Normalize()
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

	skip := func(m *plan.MovedItem, reason, detail string) {
		res.Skipped = append(res.Skipped, plan.SkippedItem{ID: m.ID, Rule: m.Rule, Path: m.Path, Reason: reason, Detail: detail})
	}
	var all []*plan.MovedItem
	for i := range rc.Moved {
		all = append(all, &rc.Moved[i])
	}
	for i := range rc.Displaced {
		all = append(all, &rc.Displaced[i])
	}
	pending := map[*plan.MovedItem]bool{}
	for i := range rc.Pending {
		all = append(all, &rc.Pending[i])
		pending[&rc.Pending[i]] = true
	}
	asked := all
	if len(o.Only) > 0 {
		want := map[string]bool{}
		for _, id := range o.Only {
			want[id] = true
		}
		asked = nil
		found := map[string]bool{}
		for _, m := range all {
			if want[m.ID] {
				asked = append(asked, m)
				found[m.ID] = true
			}
		}
		for _, id := range o.Only {
			if !found[id] {
				found[id] = true
				skip(&plan.MovedItem{ID: id}, plan.SkipNotFound, "not in receipt "+rc.ID)
			}
		}
	}
	ruleByID := map[string]rules.Rule{}
	for _, r := range o.Rules {
		ruleByID[r.ID] = r
	}
	var todo []*plan.MovedItem
	for _, m := range asked {
		r, ok := ruleByID[m.Rule]
		switch {
		case m.Permanent:
			skip(m, plan.SkipDeletedPermanently, "deleted with --permanent; nothing to restore")
		case m.RestoredAt != nil:
			skip(m, plan.SkipAlreadyRestored, "restored "+m.RestoredAt.Format(time.RFC3339))
		case !ok:
			skip(m, plan.SkipNotFound, "unknown rule "+m.Rule)
		case !strings.HasPrefix(m.Path, o.Env.Home+"/") || plan.ID(r.ID, strings.TrimPrefix(m.Path, o.Env.Home+"/")) != m.ID:
			skip(m, plan.SkipNotFound, "the receipt's item ID does not belong to its rule and path")
		default:
			todo = append(todo, m)
		}
	}
	if len(todo) == 0 {
		return res, nil
	}
	tr := o.Trash
	if tr == nil {
		open := o.openTrash
		if open == nil {
			open = func() (*trash.DirTrasher, error) { return openHomeTrash(sess, o.Env.Home) }
		}
		if tr, err = open(); err != nil {
			if isPermission(err) {
				return res, fmt.Errorf("%w: %v", ErrTrashAccess, err)
			}
			return res, fmt.Errorf("restore: %w", err)
		}
		defer func() { _ = tr.Close() }()
	} else if err := tr.Verify(); err != nil {
		return res, fmt.Errorf("restore: %w", err)
	}

	annotated := false
	for _, m := range todo {
		if err := ctx.Err(); err != nil {
			skip(m, plan.SkipMoveFailed, "not attempted: interrupted")
			continue
		}
		if pending[m] {
			// The run stopped between saving this item as pending and
			// recording its move. Find it in the Trash by its identity; the
			// tag and identity checks below still apply.
			name, ok := findInTrash(tr.Fd(), m.Dev, m.Ino)
			if !ok {
				if stillAt(m.Path, m.Dev, m.Ino) {
					skip(m, plan.SkipNotFound, "pending: the move did not happen; the item is still at its original path")
				} else {
					skip(m, plan.SkipTrashItemMissing, "pending: the item is not in the Trash")
				}
				continue
			}
			m.TrashPath = tr.Path() + "/" + name
		}
		reason, detail, done := restoreOne(sess, tr, ruleByID[m.Rule], m, tagValue(rc.ID, m.ID, o.Env.HomeIdentity), o)
		switch {
		case done:
			now := o.Now().UTC()
			m.RestoredAt = &now
			annotated = true
			res.Restored = append(res.Restored, plan.RestoredItem{ID: m.ID, Rule: m.Rule, Path: m.Path, TrashPath: m.TrashPath, Bytes: m.Bytes})
		case reason == "would":
			res.Restored = append(res.Restored, plan.RestoredItem{ID: m.ID, Rule: m.Rule, Path: m.Path, TrashPath: m.TrashPath, Bytes: m.Bytes})
		default:
			if reason == plan.SkipTrashItemMismatch && (detail == restoreSwapNote || strings.HasPrefix(detail, "a different object reached")) {
				m.RestoreNote = detail
				annotated = true
			}
			skip(m, reason, detail)
		}
	}
	if annotated && o.Store != nil && o.Apply {
		if err := o.Store.Save(rc); err != nil {
			res.Warnings = append(res.Warnings, "the receipt could not be annotated: "+err.Error())
		}
	}
	res.Normalize()
	return res, nil
}

// restoreOne checks and (with o.Apply) restores one entry. It returns
// done, or reason "would" in a dry run, or a skip reason and detail.
func restoreOne(sess *policy.Session, tr *trash.DirTrasher, r rules.Rule, m *plan.MovedItem, tag []byte, o RestoreOptions) (string, string, bool) {
	name := filepath.Base(m.TrashPath)
	if m.TrashPath == "" || filepath.Dir(m.TrashPath) != tr.Path() || name == "." || name == ".." || name == "/" {
		return plan.SkipNotInTrash, "the recorded Trash path is not directly inside " + tr.Path(), false
	}
	efd, info, err := openEntry(tr.Fd(), name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return plan.SkipTrashItemMissing, "nothing at " + m.TrashPath, false
	case errors.Is(err, syscall.ELOOP):
		return plan.SkipTrashItemMismatch, m.TrashPath + " is a symbolic link", false
	case err != nil:
		return classify(err), err.Error(), false
	}
	defer closeFd(efd)
	if m.Dev == 0 || info.dev != m.Dev || info.ino != m.Ino || info.size != m.Size || !info.mtime.Equal(m.Mtime) {
		return plan.SkipTrashItemMismatch, m.TrashPath + " is not the object kipple moved there (identity, size or modification time differ from the receipt)", false
	}
	if !tagMatches(efd, tag) {
		return plan.SkipTrashItemMismatch, m.TrashPath + " does not carry kipple's tag for this receipt and item", false
	}
	if reason, detail := protectedSource(sess, r, m.Path, name, efd, info); reason != "" {
		return reason, detail, false
	}
	dst, d := sess.AuthorizeDestination(r, m.Path, info.kind)
	if !d.Allowed {
		if d.Reason == policy.ReasonDestinationExists {
			return plan.SkipDestinationExists, "something is at " + m.Path, false
		}
		return plan.Refused(d.Reason), "the original path cannot take the item back", false
	}
	defer func() { _ = dst.Close() }()
	if !o.Apply {
		return "would", "", false
	}
	if o.beforeMove != nil {
		o.beforeMove(*m)
	}
	if d := sess.VerifyParent(dst, filepath.Dir(m.Path)); !d.Allowed {
		if d.Reason != policy.ReasonParentChanged {
			return plan.Refused(d.Reason), "the destination folder changed immediately before the move", false
		}
		return plan.SkipParentChanged, "the destination folder changed immediately before the move", false
	}
	if err := moveBack(tr.Fd(), name, dst, info, tag); err != nil {
		var ra *errRestoreArrived
		if errors.As(err, &ra) {
			return plan.SkipTrashItemMismatch, ra.note, false
		}
		return classify(err), err.Error(), false
	}
	return "", "", true
}

// protectedSource refuses a Trash entry whose own name, or anything inside
// it, is protected for the rule and destination (the same guard the scan
// uses), or that has another volume mounted inside.
func protectedSource(sess *policy.Session, r rules.Rule, dest, name string, efd int, info entryInfo) (string, string) {
	g := sess.Guard(r, dest)
	for _, n := range []string{name, filepath.Base(dest)} {
		if reason, hit := g.CheckMode(n, n, info.dev, info.ino, info.kind); hit {
			return plan.ReasonContainsProtected, "the Trash entry is protected (" + reason + ")"
		}
	}
	if !info.kind.IsDir() {
		return "", ""
	}
	f, err := dupFile(efd, name)
	if err != nil {
		return plan.SkipMoveFailed, err.Error()
	}
	defer func() { _ = f.Close() }()
	res, err := sizes.MeasureDir(context.Background(), f, sizes.Options{ProtectedMode: func(rel, n string, dev, ino uint64, mode fs.FileMode) bool {
		_, hit := g.CheckMode(rel, n, dev, ino, mode)
		return hit
	}})
	switch {
	case res.ProtectedCount > 0:
		return plan.ReasonContainsProtected, "the Trash entry contains protected files: " + strings.Join(res.Protected, ", ")
	case len(res.Mounts) > 0:
		return plan.ReasonContainsMount, "another volume is mounted inside the Trash entry"
	case err != nil || res.Status == sizes.StatusPartial:
		return plan.ReasonScanPartial, "the Trash entry could not be read completely"
	}
	return "", ""
}

// entryInfo is a Trash entry's type and identity.
type entryInfo struct {
	kind     fs.FileMode
	dev, ino uint64
	size     int64
	mtime    time.Time
}

// Package apply executes a plan (design D8, D16): it moves approved items to
// the Trash, or with Permanent deletes safe-tier items, and writes a
// receipt; Restore puts items from a receipt back.
//
// Nothing here decides that a path is movable. Every move is preceded,
// immediately, by policy.Session.AuthorizeFresh (a fresh, uncached walk from
// the pinned home descriptor that re-collects protected identities and
// re-checks the pinned home identity). Then, for that verified object:
//
//  1. its (dev, ino) must equal the plan's;
//  2. it is opened from the verified parent descriptor and measured again
//     with the protected-descendant guard, and the rule's conditions are
//     re-evaluated by scan.Checker (age, idle project, git state, partial,
//     contains_mount, contains_protected);
//  3. the owner app or process and, for project artifacts, agent working
//     directories are checked (procs);
//  4. policy.Session.VerifyParent re-checks with fcntl(F_GETPATH) that the
//     parent descriptor is still the planned parent;
//  5. the item moves with renameatx_np(RENAME_EXCL) from that parent
//     descriptor into the verified Trash descriptor (trash.DirTrasher), which
//     refuses unless the entry is still the approved (dev, ino). A different
//     device is skipped, never copied. When ~/.Trash cannot be opened (macOS
//     TCC: no Full Disk Access), Options.Finder supplies the NSFileManager
//     mover, which re-checks the approved object by descriptor immediately
//     before the call and trashes a file reference to it; when that mover
//     is unavailable, Run returns ErrTrashAccess. NSFileManager resolves the
//     reference to a path, so a swap at that instant can put another object
//     in the Trash: that object is recorded in the receipt as displaced
//     (restorable) and the item is skipped with identity_changed.
//
// Every item moved to the Trash first gets an extended attribute
// (tagValue) binding it to this receipt, item and home; restore requires
// it. Nothing that moved is ever left out of the receipt.
//
// Plan files are untrusted: items are matched by ID against a fresh scan,
// which re-derives each path from its rule; an item whose (dev, ino), or
// whose rule root's (dev, ino), differs from the plan file is refused.
//
// Any refusal is a skipped entry with a machine-readable reason, and the run
// is partial (exit 4). The receipt is created before the first move and
// saved after every item.
package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/procs"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/trash"
)

// Exit codes (design D11).
const (
	ExitOK      = 0
	ExitNothing = 3
	ExitPartial = 4
)

// ExitCode maps a clean or restore result to its exit code: 3 when nothing
// was selected, 4 when anything was skipped, otherwise 0.
func ExitCode(r plan.Result) int {
	switch {
	case len(r.Moved) == 0 && len(r.Restored) == 0 && len(r.Skipped) == 0:
		return ExitNothing
	case len(r.Skipped) > 0:
		return ExitPartial
	default:
		return ExitOK
	}
}

// Options configure Run.
type Options struct {
	// Env is the policy environment. HomeIdentity must be set: apply acts
	// only on the home directory cmd/ approved.
	Env policy.Env
	// Rules is the effective catalogue (scan.EffectiveRules).
	Rules []rules.Rule
	// Plan is what to act on: a fresh scan, or a plan file (then Fresh must
	// be a scan made now, at the plan's level).
	Plan plan.Plan
	// Fresh is a scan made now when Plan came from a file; nil when Plan is
	// itself that scan.
	Fresh *plan.Plan
	// Only, when set, names the item IDs to act on (manual and review items
	// included, never info). Otherwise the plan's selected items.
	Only []string
	// Permanent deletes safe-tier items instead of trashing them; every
	// other tier is skipped with permanent_not_allowed.
	Permanent bool
	// Trash is where items go (the home folder's .Trash, opened through the
	// session, when nil).
	Trash *trash.DirTrasher
	// Finder, when set, is the fallback mover used when the home .Trash
	// cannot be opened for lack of permission (macOS protects ~/.Trash:
	// only apps with Full Disk Access can open it). cmd/ sets it to
	// trash.NewFinderTrasher for the real home. When it is nil or cannot be
	// made, Run returns ErrTrashAccess; a refusal on one item is that item's
	// permission_denied.
	Finder func() (trash.ExpectTrasher, error)
	// ChecksSince, when set, is when the scan this run acts on started (cmd
	// sets it). A permanent delete refuses an item if anything inside it
	// changed (ctime) at or after the earlier of ChecksSince and the start
	// of its move-time checks, so a folder swapped in during the run is
	// never deleted.
	ChecksSince time.Time
	// DryRun runs every check, plan-level and move-time, and moves nothing:
	// Moved then lists what would be moved. No receipt is written.
	DryRun bool
	// Receipts stores the run's receipt.
	Receipts receipt.Store
	// KippleVersion is recorded in the receipt.
	KippleVersion string
	// Procs answers owner and agent checks (procs.New() when nil).
	Procs procs.Inspector
	// Git classifies repositories (scan's default when nil).
	Git scan.GitInspector
	// Now is the clock (time.Now when nil).
	Now func() time.Time
	// Progress receives short messages for stderr.
	Progress func(string)

	// beforeMove, in tests, runs after every check and immediately before
	// the parent re-check and the move.
	beforeMove func(plan.Item)
	// openTrash, in tests, replaces opening the home .Trash.
	openTrash func() (*trash.DirTrasher, error)
	// saveReceipt, in tests, replaces Receipts.Save (to fail on the Nth
	// save, as a full disk would).
	saveReceipt func(plan.Receipt) error
}

// ErrTrashAccess: the Trash folder cannot be opened (macOS protects
// ~/.Trash; only apps with Full Disk Access may open it) and no fallback
// mover is available (clean), or restore needs the Trash folder itself.
// A refusal on a single item is not ErrTrashAccess. cmd/ reports it
// with error code trash_access and how to grant Full Disk Access.
var ErrTrashAccess = errors.New("kipple cannot use your Trash folder")

// target is an item that passed the plan-level checks.
type target struct {
	item plan.Item // from the fresh scan
	rule rules.Rule
}

type applier struct {
	ctx       context.Context
	o         Options
	sess      *policy.Session
	check     *scan.Checker
	mover     trash.ExpectTrasher
	moverName string
	procs     procs.Inspector
	now       func() time.Time
	res       *plan.Result
	// receiptID names the run's receipt in the tags set on moved items.
	receiptID string
	// rc is the receipt, saved before and after every move (write-ahead).
	rc *plan.Receipt
	// pending holds the item whose move is in progress.
	pending []plan.MovedItem
	// saveErr is the first failure to save the receipt; no move follows it.
	saveErr error
}

// Run executes o.Plan and returns the result. It returns an error, having
// moved nothing, when home, the Trash or the receipt cannot be used.
func Run(ctx context.Context, o Options) (plan.Result, error) {
	res := plan.Result{SchemaVersion: plan.SchemaVersion, Command: "clean", DryRun: o.DryRun, Home: o.Env.Home}
	res.Normalize()
	if o.Env.HomeIdentity == nil {
		return res, errors.New("apply: the home folder's identity is not pinned")
	}
	if o.Permanent && !PermanentAvailable {
		return res, ErrPermanentUnavailable
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Procs == nil {
		o.Procs = procs.New()
	}
	sess, err := policy.NewSession(o.Env)
	if err != nil {
		_ = sess.Close()
		return res, fmt.Errorf("apply: %w", err)
	}
	defer func() { _ = sess.Close() }()
	a := &applier{ctx: ctx, o: o, sess: sess, procs: o.Procs, now: o.Now, res: &res}
	a.check = scan.NewChecker(ctx, sess, o.Rules, o.Git, o.Now())

	targets := a.selectTargets()
	if o.DryRun {
		for _, t := range targets {
			a.one(t)
		}
		res.Normalize()
		return res, nil
	}
	trashDir := o.Env.Home + "/.Trash"
	switch {
	case o.Permanent:
		a.moverName = plan.MoverDelete
	case len(targets) == 0:
		a.moverName = plan.MoverNone
	case o.Trash != nil:
		a.mover, a.moverName, trashDir = o.Trash, plan.MoverRename, o.Trash.Path()
	default:
		open := o.openTrash
		if open == nil {
			open = func() (*trash.DirTrasher, error) { return openHomeTrash(sess, o.Env.Home) }
		}
		tr, err := open()
		switch {
		case err == nil:
			defer func() { _ = tr.Close() }()
			a.mover, a.moverName, trashDir = tr, plan.MoverRename, tr.Path()
		case isPermission(err) && o.Finder != nil:
			ft, ferr := o.Finder()
			if ferr != nil {
				return res, fmt.Errorf("%w: %v; the NSFileManager fallback is unavailable: %v", ErrTrashAccess, err, ferr)
			}
			a.mover, a.moverName = ft, plan.MoverNSFileManager
			res.Warnings = append(res.Warnings, "the Trash folder cannot be opened without Full Disk Access; moving items with NSFileManager instead")
		case isPermission(err):
			return res, fmt.Errorf("%w: %v", ErrTrashAccess, err)
		default:
			return res, fmt.Errorf("apply: %w", err)
		}
	}

	rc := plan.Receipt{
		KippleVersion: o.KippleVersion,
		CreatedAt:     o.Now().UTC(),
		Home:          o.Env.Home,
		Level:         o.Plan.Level,
		Permanent:     o.Permanent,
		PlanGenerated: o.Plan.GeneratedAt,
		Skipped:       append([]plan.SkippedItem(nil), res.Skipped...),
	}
	if rc.Level == "" {
		rc.Level = rules.LevelSafe
	}
	rc.TrashDir, rc.Mover = trashDir, a.moverName
	path, err := o.Receipts.Create(&rc)
	if err != nil {
		return res, fmt.Errorf("apply: cannot write the receipt, so nothing was moved: %w", err)
	}
	res.ReceiptID, res.ReceiptPath = rc.ID, path
	a.receiptID = rc.ID

	a.rc = &rc
	for _, t := range targets {
		if a.saveErr != nil {
			a.skip(t.item, plan.SkipMoveFailed, "not attempted: the receipt could not be saved")
			continue
		}
		if err := ctx.Err(); err != nil {
			a.skip(t.item, plan.SkipMoveFailed, "not attempted: interrupted")
			continue
		}
		a.progress("checking " + t.item.Path)
		a.one(t)
		if a.saveErr == nil {
			a.save()
		}
	}
	if a.saveErr == nil {
		rc.Complete = true
		if a.save(); a.saveErr != nil {
			res.Warnings = append(res.Warnings, "the receipt could not be completed: "+a.saveErr.Error())
			a.saveErr = nil
		}
	}
	res.Normalize()
	if a.saveErr != nil {
		return res, fmt.Errorf("%w (%s, after %d moves): %v; any item it lists as pending can be recovered with kipple restore %s --apply",
			ErrReceipt, path, len(res.Moved), a.saveErr, rc.ID)
	}
	return res, nil
}

// ErrReceipt: the receipt could not be saved, so apply stopped. Items it
// was about to move are listed as pending in the receipt on disk.
var ErrReceipt = errors.New("the receipt could not be saved, so kipple stopped moving items")

// save writes the receipt with the current moved, skipped, displaced and
// pending lists, recording the first failure in a.saveErr.
func (a *applier) save() {
	a.rc.Moved, a.rc.Skipped, a.rc.Displaced = a.res.Moved, a.res.Skipped, a.res.Displaced
	a.rc.Pending = append([]plan.MovedItem(nil), a.pending...)
	write := a.o.saveReceipt
	if write == nil {
		write = a.o.Receipts.Save
	}
	// Saves hold the receipt's lock, so a concurrent restore of the same
	// receipt never interleaves with them.
	unlock, err := a.o.Receipts.Lock(a.rc.ID)
	if err == nil {
		defer unlock()
		err = write(*a.rc)
	}
	if err != nil && a.saveErr == nil {
		a.saveErr = err
	}
}

func (a *applier) progress(msg string) {
	if a.o.Progress != nil {
		a.o.Progress(msg)
	}
}

func (a *applier) warn(format string, args ...any) {
	a.res.Warnings = append(a.res.Warnings, fmt.Sprintf(format, args...))
}

func (a *applier) skip(it plan.Item, reason, detail string) {
	a.res.Skipped = append(a.res.Skipped, plan.SkippedItem{ID: it.ID, Rule: it.Rule, Path: it.Path, Reason: reason, Detail: detail})
}

// tierReasons are dropped from an item's reasons when it is named in Only:
// naming an item is consent to its tier, never to anything else.
var tierReasons = map[string]bool{plan.ReasonTierReview: true, plan.ReasonTierManual: true}

// selectTargets applies the plan-level checks: which items were asked for,
// whether a fresh scan still produces each one at the same identity and
// under the same root, and whether anything now blocks it.
func (a *applier) selectTargets() []target {
	o := a.o
	planByID := map[string]plan.Item{}
	for _, it := range o.Plan.Items {
		planByID[it.ID] = it
	}
	fresh := &o.Plan
	if o.Fresh != nil {
		fresh = o.Fresh
	}
	freshByID := map[string]plan.Item{}
	for _, it := range fresh.Items {
		freshByID[it.ID] = it
	}
	ruleByID := map[string]rules.Rule{}
	for _, r := range o.Rules {
		ruleByID[r.ID] = r
	}

	var asked []plan.Item
	explicit := len(o.Only) > 0
	if explicit {
		want := map[string]bool{}
		for _, id := range o.Only {
			if want[id] {
				continue
			}
			want[id] = true
			it, ok := planByID[id]
			if !ok {
				a.skip(plan.Item{ID: id}, plan.SkipNotFound, "not in the plan")
				continue
			}
			asked = append(asked, it)
		}
		for _, it := range o.Plan.Items {
			if !want[it.ID] {
				a.res.Untouched = append(a.res.Untouched, it.ID)
			}
		}
	} else {
		for _, it := range o.Plan.Items {
			if it.Selected {
				asked = append(asked, it)
			} else {
				a.res.Untouched = append(a.res.Untouched, it.ID)
			}
		}
	}

	var out []target
	for _, it := range asked {
		r, ok := ruleByID[it.Rule]
		if !ok {
			a.skip(it, plan.SkipNotFound, "unknown rule "+it.Rule)
			continue
		}
		if r.Tier == rules.TierInfo || it.Tier == string(rules.TierInfo) {
			a.skip(it, plan.SkipInfoTier, "info items are measured only; use: "+r.OwnerCommand)
			continue
		}
		f, ok := freshByID[it.ID]
		if !ok {
			a.skip(it, plan.SkipNotFound, "a scan now no longer finds this item")
			continue
		}
		if f.Path != it.Path || f.Rule != it.Rule {
			a.skip(it, plan.SkipIdentityChanged, "the plan's path or rule for this ID differs from a scan now")
			continue
		}
		if it.Dev == 0 || it.Dev != f.Dev || it.Ino != f.Ino {
			a.skip(f, plan.SkipIdentityChanged, "the item is not the object the plan recorded")
			continue
		}
		if o.Fresh != nil {
			pr, ok1 := rootFor(o.Plan, it)
			fr, ok2 := rootFor(*o.Fresh, f)
			if !ok1 || !ok2 || pr != fr {
				a.skip(f, plan.SkipRootChanged, "the rule's root folder is not the one the plan recorded")
				continue
			}
		}
		var blocking []string
		for _, reason := range f.Reasons {
			if explicit && tierReasons[reason] {
				continue
			}
			blocking = append(blocking, reason)
		}
		if len(blocking) > 0 {
			a.skip(f, blocking[0], strings.Join(blocking, ", "))
			continue
		}
		if o.Permanent && r.Tier != rules.TierSafe {
			a.skip(f, plan.SkipPermanentNotAllowed, "--permanent deletes only safe items; "+string(r.Tier)+" items go to the Trash without it")
			continue
		}
		out = append(out, target{item: f, rule: r})
	}
	return out
}

// rootFor returns the recorded root of it's rule that contains it (the
// longest match).
func rootFor(p plan.Plan, it plan.Item) (plan.RootIdentity, bool) {
	rel := strings.TrimPrefix(it.Path, p.Home+"/")
	var best plan.RootIdentity
	found := false
	for _, rs := range p.Rules {
		if rs.ID != it.Rule {
			continue
		}
		for _, root := range rs.Roots {
			if strings.HasPrefix(rel, root.Path+"/") && (!found || len(root.Path) > len(best.Path)) {
				best, found = root, true
			}
		}
	}
	return best, found
}

// one runs the move-time checks for t and moves it, recording the outcome.
func (a *applier) one(t target) {
	it, r := t.item, t.rule
	f, d := a.sess.AuthorizeFresh(r, it.Path)
	if !d.Allowed {
		if d.Reason == policy.ReasonPermissionDenied {
			a.skip(it, plan.SkipPermissionDenied, "the system refused access to the item or a folder above it")
			return
		}
		a.skip(it, plan.Refused(d.Reason), "refused by the safety policy immediately before the move")
		return
	}
	defer func() { _ = f.Close() }()
	dev, ino, ok := policy.Identity(f.Info)
	if !ok || dev != it.Dev || ino != it.Ino {
		a.skip(it, plan.SkipIdentityChanged, "the path now holds a different object than the plan recorded")
		return
	}
	var dir *os.File
	if f.Info.IsDir() {
		var err error
		if dir, err = openItem(f, dev, ino); err != nil {
			if isPermission(err) {
				a.skip(it, plan.SkipPermissionDenied, err.Error())
			} else {
				a.skip(it, plan.SkipIdentityChanged, err.Error())
			}
			return
		}
		defer func() { _ = dir.Close() }()
	}
	// Anything inside the item that changes from here on is refused by a
	// permanent delete (see removeItem).
	since := time.Now()
	if !a.o.ChecksSince.IsZero() && a.o.ChecksSince.Before(since) {
		since = a.o.ChecksSince
	}
	rc := a.check.Check(r, it.Path, dir, f.Info)
	if len(rc.Reasons) > 0 {
		a.skip(it, rc.Reasons[0], strings.Join(rc.Reasons, ", "))
		return
	}
	if reason, detail := a.running(r, it); reason != "" {
		a.skip(it, reason, detail)
		return
	}
	if a.o.beforeMove != nil {
		a.o.beforeMove(it)
	}
	if d := a.sess.VerifyParent(f, filepath.Dir(it.Path)); !d.Allowed {
		reason := plan.SkipParentChanged
		if d.Reason != policy.ReasonParentChanged {
			reason = plan.Refused(d.Reason)
		}
		a.skip(it, reason, "the parent folder changed immediately before the move")
		return
	}
	moved := plan.MovedItem{
		ID: it.ID, Rule: it.Rule, Path: it.Path,
		Bytes: rc.Measure.AllocatedBytes, ReclaimableBytes: rc.Measure.ReclaimableBytes,
		Dev: dev, Ino: ino, Size: f.Info.Size(), Mtime: f.Info.ModTime().UTC(), Mover: a.moverName,
	}
	if a.o.DryRun {
		moved.Mover = ""
		a.res.Moved = append(a.res.Moved, moved)
		return
	}
	// Write-ahead: the item is in the receipt as pending before it moves.
	// If that cannot be saved, it does not move.
	a.pending = []plan.MovedItem{moved}
	if a.save(); a.saveErr != nil {
		a.pending = nil
		a.skip(it, plan.SkipMoveFailed, "not moved: the receipt could not be saved first: "+a.saveErr.Error())
		return
	}
	defer func() { a.pending = nil }()
	a.progress("moving " + it.Path)
	var err error
	if a.o.Permanent {
		g := a.sess.Guard(r, it.Path)
		var rm removal
		rm, err = removeItem(f, dev, ino, g.CheckMode, since)
		moved.Permanent = true
		if err != nil && rm.count > 0 {
			a.res.Skipped = append(a.res.Skipped, plan.SkippedItem{
				ID: it.ID, Rule: it.Rule, Path: it.Path, Reason: classify(err),
				Detail:  "partly deleted before it stopped: " + err.Error(),
				Partial: true, RemovedCount: rm.count, RemovedBytes: rm.bytes,
			})
			return
		}
	} else {
		moved.TrashPath, err = a.trashTagged(f, it, dev, ino, dir)
	}
	if err != nil {
		a.skip(it, classify(err), err.Error())
		return
	}
	moved.MovedAt = a.now().UTC()
	a.res.Moved = append(a.res.Moved, moved)
}

// trashTagged tags the approved object (tagValue, required by restore),
// moves it, and handles what can go wrong after a move: when something
// other than the approved object reached the Trash (trash.DisplacedError),
// that object is recorded as displaced, with its Trash path and identity,
// so the receipt lists every object that moved; the item is skipped. The
// tag is removed again from an approved object that did not move.
func (a *applier) trashTagged(f *policy.Fresh, it plan.Item, dev, ino uint64, dir *os.File) (string, error) {
	tag := tagValue(a.receiptID, it.ID, a.o.Env.HomeIdentity)
	tfd, closeTfd, err := tagTarget(f, dev, ino, dir)
	if err != nil {
		return "", err
	}
	defer closeTfd()
	if err := setTag(tfd, tag); err != nil {
		return "", fmt.Errorf("could not tag the item for restore: %w", err)
	}
	dest, err := a.trashItem(f, dev, ino, dir)
	if err == nil {
		return dest, nil
	}
	var de *trash.DisplacedError
	if errors.As(err, &de) {
		d := plan.MovedItem{ID: it.ID, Rule: it.Rule, Path: it.Path, TrashPath: de.Dest, Mover: a.moverName, MovedAt: a.now().UTC()}
		if de.Known {
			d.Dev, d.Ino, d.Size, d.Mtime = de.Dev, de.Ino, de.Size, de.Mtime
			if terr := tagDisplaced(de.Dest, de.Dev, de.Ino, tag); terr != nil {
				a.warn("%s: the displaced object at %s could not be tagged for restore: %v", it.Path, de.Dest, terr)
			}
		} else {
			a.warn("%s: the identity of the displaced object at %s could not be read; restore will refuse it", it.Path, de.Dest)
		}
		a.res.Displaced = append(a.res.Displaced, d)
	}
	if cerr := clearTag(tfd); cerr != nil {
		a.warn("%s: its restore tag could not be removed: %v", it.Path, cerr)
	}
	return "", err
}

// trashItem moves the item, adding owner write permission to a read-only
// directory item for the duration of the rename when the system requires
// it (macOS needs write permission on a directory to move it to another
// folder; Go's module cache is read-only). Contents are never changed.
func (a *applier) trashItem(f *policy.Fresh, dev, ino uint64, dir *os.File) (string, error) {
	dest, err := a.mover.TrashExpect(f.ParentFd, f.Name, dev, ino)
	if err != nil && dir != nil && isPermission(err) {
		err = withOwnerWrite(dir, func() error {
			var e error
			dest, e = a.mover.TrashExpect(f.ParentFd, f.Name, dev, ino)
			return e
		})
	}
	return dest, err
}

// running checks the rule's owner and, for project artifacts, agent
// working directories. Unknown answers skip review and manual items and
// only warn for safe items (design D10).
func (a *applier) running(r rules.Rule, it plan.Item) (string, string) {
	unknown := func(reason, what string, err error) (string, string) {
		if r.Tier == rules.TierSafe {
			a.warn("%s: could not tell whether %s is running (%v); moved anyway", it.Path, what, err)
			return "", ""
		}
		return reason, fmt.Sprintf("could not tell whether %s is running: %v", what, err)
	}
	if r.Conditions.SkipIfOwnerRunning {
		if len(r.Owner.BundleIDs) > 0 {
			ids, err := a.procs.RunningBundleIDs(a.ctx)
			if err != nil {
				if reason, detail := unknown(plan.ReasonOwnerRunning, r.Owner.Name, err); reason != "" {
					return reason, detail
				}
			}
			for _, b := range r.Owner.BundleIDs {
				if ids[b] {
					return plan.ReasonOwnerRunning, r.Owner.Name + " is running"
				}
			}
		}
		if len(r.Owner.Processes) > 0 {
			names, err := a.procs.RunningNames(a.ctx, r.Owner.Processes)
			if err != nil {
				if reason, detail := unknown(plan.ReasonOwnerRunning, r.Owner.Name, err); reason != "" {
					return reason, detail
				}
			}
			ps := append([]string(nil), r.Owner.Processes...)
			sort.Strings(ps)
			for _, p := range ps {
				if names[p] {
					return plan.ReasonOwnerRunning, r.Owner.Name + " (" + p + ") is running"
				}
			}
		}
	}
	if r.Conditions.SkipIfAgentInProject {
		project := filepath.Dir(it.Path)
		cwds, err := a.procs.ProcessCwds(a.ctx, rules.AgentProcesses())
		if err != nil {
			if reason, detail := unknown(plan.SkipToolRunning, "an agent ("+strings.Join(rules.AgentProcesses(), ", ")+")", err); reason != "" {
				return reason, detail
			}
		}
		for _, c := range cwds {
			if procs.Within(c, project) {
				return plan.SkipToolRunning, "an agent process is working in " + c
			}
		}
	}
	return "", ""
}

// Preview returns p as kipple clean would act on it with only: named items
// are selected when nothing but their tier blocks them (never info), and
// every other item is unselected with reason not_requested. With no IDs it
// returns p unchanged. Unknown IDs become plan warnings.
func Preview(p plan.Plan, only []string) plan.Plan {
	if len(only) == 0 {
		return p
	}
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	out := p
	out.Items = make([]plan.Item, len(p.Items))
	out.Warnings = append([]string(nil), p.Warnings...)
	out.Totals.SelectedCount, out.Totals.SelectedBytes, out.Totals.ReclaimableBytes = 0, 0, 0
	seen := map[string]bool{}
	for i, it := range p.Items {
		it.Reasons = append([]string(nil), it.Reasons...)
		if want[it.ID] {
			seen[it.ID] = true
			var rest []string
			for _, r := range it.Reasons {
				if !tierReasons[r] {
					rest = append(rest, r)
				}
			}
			if it.Tier != string(rules.TierInfo) && len(rest) == 0 {
				it.Selected, it.Reasons = true, []string{}
			}
		} else {
			it.Selected = false
			it.Reasons = append(it.Reasons, plan.ReasonNotRequested)
		}
		if it.Selected {
			out.Totals.SelectedCount++
			out.Totals.SelectedBytes += it.AllocatedBytes
			out.Totals.ReclaimableBytes += it.ReclaimableBytes
		}
		out.Items[i] = it
	}
	for _, id := range only {
		if !seen[id] {
			out.Warnings = append(out.Warnings, "no item "+id+" in this plan")
			seen[id] = true
		}
	}
	return out
}

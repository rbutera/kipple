package guimodel

import (
	"context"
	"errors"
	"strings"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/words"
)

// Promise is the confirmation's fixed promise.
const Promise = "Nothing is deleted. Each item is checked again just before it moves, goes to your Trash, and is listed in a receipt you can use to put it back. The space comes back when you empty the Trash."

// Trash notes for the confirmation, from apply.ProbeTrash.
const (
	FallbackNote    = "kipple doesn't have Full Disk Access, so it will use macOS's own Trash command. Finder's Put Back works for these items."
	UnavailableNote = "kipple can't use the Trash folder here, so it can't move anything. macOS lets only apps with Full Disk Access open it."
)

// OpenConfirm opens the confirmation sheet, after asking apply how it would
// reach the Trash. With nothing selected it only explains.
func (m *Model) OpenConfirm() {
	if m.screen != ScreenReview || m.sheet != SheetNone {
		return
	}
	if m.sel.Len() == 0 {
		m.hint = "Nothing is selected. Click an item's box to select it."
		return
	}
	m.trashMode, m.trashErr = m.cfg.ProbeFunc(m.applyOptions(nil))
	m.sheet = SheetConfirm
	m.rescanWarn = false
}

// CancelConfirm closes the confirmation; nothing moves.
func (m *Model) CancelConfirm() {
	if m.sheet != SheetConfirm {
		return
	}
	m.sheet = SheetNone
	m.hint = "Cancelled. Nothing was moved."
}

// ConfirmView is the confirmation sheet.
type ConfirmView struct {
	Count string // "25 items" (amber in the title)
	Title string // "Move 25 items to the Trash?"
	Total string // allocated bytes of the selection
	Rules string // "from 6 rules: App caches, npm cache, …, and 2 more"
	// TierLine is set when review or manual items are selected.
	TierLine string
	// AtRisk lists up to three at-risk paths; AtRiskLine introduces them.
	AtRiskLine string
	AtRisk     []string
	Promise    string
	// Note is the Full Disk Access note (fallback) or the trash-access
	// message (unavailable); empty when items move straight into the Trash.
	Note string
	// CanMove is false when the Trash can't be used at all.
	CanMove bool
}

// Confirm describes the confirmation sheet.
func (m *Model) Confirm() ConfirmView {
	n, b := m.sel.Total()
	v := ConfirmView{Count: words.Items(n), Total: output.Bytes(b), Promise: Promise, CanMove: m.trashMode != apply.TrashUnavailable}
	v.Title = "Move " + v.Count + " to the Trash?"
	// Rule titles in the review list's order.
	var titles []string
	for _, g := range m.groups {
		for _, id := range g.ids {
			if m.sel.Selected(id) {
				titles = append(titles, g.title)
				break
			}
		}
	}
	review, manual := 0, 0
	var risky []string
	for _, it := range m.plan.Items {
		if !m.sel.Selected(it.ID) {
			continue
		}
		switch it.Tier {
		case string(rules.TierReview):
			review++
		case string(rules.TierManual):
			manual++
		}
		if it.AtRisk {
			risky = append(risky, m.Rel(it.Path))
		}
	}
	shown := titles
	if len(shown) > 4 {
		shown = shown[:4]
	}
	v.Rules = "from " + words.Plural(len(titles), "rule") + ": " + strings.Join(shown, ", ")
	if extra := len(titles) - len(shown); extra > 0 {
		v.Rules += ", and " + itoa(extra) + " more"
	}
	switch {
	case review > 0 && manual > 0:
		v.TierLine = "Includes " + words.Plural(review, "review item") + " and " + words.Plural(manual, "manual item") + " you chose."
	case review > 0:
		v.TierLine = "Includes " + words.Plural(review, "review item") + " you chose."
	case manual > 0:
		v.TierLine = "Includes " + words.Plural(manual, "manual item") + " you chose."
	}
	if len(risky) > 0 {
		v.AtRiskLine = words.Items(len(risky)) + " " + isAre(len(risky)) + " at risk:"
		if len(risky) > 3 {
			v.AtRisk = append(risky[:3:3], "and "+itoa(len(risky)-3)+" more")
		} else {
			v.AtRisk = risky
		}
	}
	switch m.trashMode {
	case apply.TrashFallback:
		v.Note = FallbackNote
	case apply.TrashUnavailable:
		// The Full Disk Access copy only when access was what failed;
		// otherwise say what the probe found.
		v.Note = UnavailableNote
		if m.trashErr != nil && !errors.Is(m.trashErr, apply.ErrTrashAccess) {
			v.Note = "kipple can't use the Trash folder here, so it can't move anything: " + output.SafeText(m.trashErr.Error()) + "."
		}
	}
	return v
}

// ---- Apply ----

// applyProgress is the applying sheet's state.
type applyProgress struct {
	total   int
	done    int
	bytes   int64
	current string
	sizes   map[string]int64 // path -> allocated bytes
	lastP   string
}

// ApplyJob is a move started by StartApply. Run it off the main goroutine.
type ApplyJob struct {
	ctx  context.Context
	opts apply.Options
	run  func(context.Context, apply.Options) (plan.Result, error)
}

// Run moves the selection, calling progress (from this goroutine) before
// each item.
func (j ApplyJob) Run(progress func(string)) (plan.Result, error) {
	o := j.opts
	o.Progress = progress
	return j.run(j.ctx, o)
}

// applyOptions are the launch apply options for the current selection, as
// kipple clean --apply --only <selection> builds them.
func (m *Model) applyOptions(progress func(string)) apply.Options {
	o := m.cfg.Apply
	o.Plan = m.plan
	o.Fresh = nil
	o.Only = m.sel.IDs()
	o.DryRun = false
	o.Permanent = false
	o.ChecksSince = m.started
	o.Progress = progress
	return o
}

// StartApply is the confirmation's Move to Trash. It returns the job, or
// false when nothing may move now (the sheet is not up, nothing is
// selected, or the Trash can't be used).
func (m *Model) StartApply() (ApplyJob, bool) {
	if m.sheet != SheetConfirm || m.sel.Len() == 0 || m.trashMode == apply.TrashUnavailable {
		return ApplyJob{}, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.sheet, m.stopping, m.hint = SheetApplying, false, ""
	m.progress = applyProgress{total: m.sel.Len(), sizes: map[string]int64{}}
	for _, it := range m.plan.Items {
		if m.sel.Selected(it.ID) {
			m.progress.sizes[it.Path] = it.AllocatedBytes
		}
	}
	return ApplyJob{ctx: ctx, opts: m.applyOptions(nil), run: m.cfg.ApplyFunc}, true
}

// ApplyProgress records apply's progress message ("checking <path>").
func (m *Model) ApplyProgress(msg string) {
	if m.sheet != SheetApplying {
		return
	}
	if p, ok := strings.CutPrefix(msg, "checking "); ok {
		if m.progress.lastP != "" {
			m.progress.done++
			m.progress.bytes += m.progress.sizes[m.progress.lastP]
		}
		m.progress.lastP = p
		m.progress.current = "Checking " + m.Rel(p)
		return
	}
	if p, ok := strings.CutPrefix(msg, "moving "); ok {
		m.progress.current = "Moving " + m.Rel(p)
		return
	}
	m.progress.current = output.SafeText(msg)
}

// ApplyingView is the applying sheet.
type ApplyingView struct {
	Title    string
	Fraction float64
	Count    string // "12 of 25 items"
	Bytes    string
	Current  string
	Stop     string // the stop button's label
	Stopping bool
}

// Applying describes the applying sheet.
func (m *Model) Applying() ApplyingView {
	p := m.progress
	v := ApplyingView{Title: "Moving to the Trash", Count: itoa(p.done) + " of " + words.Items(p.total), Bytes: output.Bytes(p.bytes), Current: p.current, Stop: "Stop after this item", Stopping: m.stopping}
	if p.total > 0 {
		v.Fraction = float64(p.done) / float64(p.total)
	}
	if m.stopping {
		v.Stop = "Stopping after this item…"
	}
	if m.sheet == SheetRestoring {
		v.Title, v.Count, v.Bytes, v.Fraction = "Putting items back", "", "", 0
	}
	return v
}

// StopApply asks apply to stop after the current item: it finishes or
// skips that item and records the rest as not attempted.
func (m *Model) StopApply() {
	if m.sheet != SheetApplying || m.stopping {
		return
	}
	m.stopping = true
	if m.cancel != nil {
		m.cancel()
	}
}

// ApplyDone records apply's result.
func (m *Model) ApplyDone(res plan.Result, err error) {
	if m.sheet != SheetApplying {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.result, m.applyErr, m.restored, m.restoreErr, m.showAllMove = res, err, nil, nil, false
	m.pending = nil
	m.finished = m.cfg.Now()
	if res.ReceiptID != "" {
		if e, lerr := m.cfg.Store.Load(res.ReceiptID); lerr == nil {
			m.pending = e.Receipt.Pending
		}
	}
	m.quitNow = m.quitAfter
	if err != nil {
		var card *ReceiptCard
		if res.ReceiptID != "" {
			c := m.receiptCard()
			card = &c
		}
		m.setProblem(applyProblem(err, res, card))
		return
	}
	m.screen, m.sheet, m.hint = ScreenDone, SheetNone, ""
}

// ApplyNow runs the confirmed move on this goroutine (tests, screenshots).
func (m *Model) ApplyNow() error {
	job, ok := m.StartApply()
	if !ok {
		return errors.New("guimodel: nothing to apply")
	}
	res, err := job.Run(m.ApplyProgress)
	m.ApplyDone(res, err)
	return err
}

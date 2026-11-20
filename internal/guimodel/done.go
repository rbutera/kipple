package guimodel

import (
	"context"
	"errors"
	"strings"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/words"
)

// Skip details apply writes for items it never reached.
const (
	notAttemptedInterrupted = "not attempted: interrupted"
	notAttemptedReceipt     = "not attempted: the receipt could not be saved"
)

// ReceiptCard is the receipt summary on the done and error screens.
type ReceiptCard struct {
	ID        string
	Path      string // the receipt file (for Open receipt; not escaped)
	TrashDir  string // the Trash folder items went to (for Open Trash; not escaped)
	Moved     string // "24 items · 28.1 GB"
	Skipped   int
	Displaced int
	Pending   int
	// PendingPath is the first pending item's path, when there is one.
	PendingPath string
	NotTried    int
	How         string
}

func (m *Model) receiptCard() ReceiptCard {
	r := m.result
	var bytes int64
	for _, mv := range r.Moved {
		bytes += mv.Bytes
	}
	c := ReceiptCard{
		ID:        output.SafeText(r.ReceiptID),
		Path:      r.ReceiptPath,
		Moved:     words.Items(len(r.Moved)) + " · " + output.Bytes(bytes),
		Displaced: len(r.Displaced),
		Pending:   len(m.pending),
	}
	if len(m.pending) > 0 {
		c.PendingPath = m.Rel(m.pending[0].Path)
	}
	for _, s := range r.Skipped {
		if s.Detail == notAttemptedReceipt || s.Detail == notAttemptedInterrupted {
			c.NotTried++
		} else {
			c.Skipped++
		}
	}
	movers := map[string]bool{}
	for _, mv := range r.Moved {
		movers[mv.Mover] = true
	}
	switch {
	case movers[plan.MoverNSFileManager] && movers[plan.MoverRename]:
		c.How = words.Mover(plan.MoverRename) + " " + words.Mover(plan.MoverNSFileManager)
	case movers[plan.MoverNSFileManager]:
		c.How = words.Mover(plan.MoverNSFileManager)
	case movers[plan.MoverRename]:
		c.How = words.Mover(plan.MoverRename)
	}
	c.TrashDir = m.cfg.Home + "/.Trash"
	if e, err := m.cfg.Store.Load(r.ReceiptID); err == nil && e.Receipt.TrashDir != "" {
		c.TrashDir = e.Receipt.TrashDir
	}
	return c
}

// Line is a path and its size or reason.
type Line struct {
	Path string
	Text string
	// Next is a next step for a skipped item.
	Next string
}

// DoneView is the done screen.
type DoneView struct {
	Title string
	// Total is the bytes moved (empty when nothing moved).
	Total string
	// SpaceLine says where the space is; SharedNote adds that emptying the
	// Trash frees less when files are shared (hard links).
	SpaceLine, SharedNote string
	Card                  ReceiptCard
	Moved                 []Line
	MovedMore             int // rows hidden behind Show
	Skipped               []Line
	NotTried              int
	Displaced             string
	Warnings              []string
	// ChangedMind is the restore guidance; Command the CLI command.
	ChangedMind string
	Command     string
	// CanPutBack is set when Put back is offered.
	CanPutBack bool
	// Restored summarises a put back, once it has run.
	Restored *RestoredView
}

// RestoredView is the result of Put back.
type RestoredView struct {
	Title   string
	Skipped []Line
}

// Done describes the done screen.
func (m *Model) Done() DoneView {
	r := m.result
	var bytes, reclaim int64
	for _, mv := range r.Moved {
		bytes += mv.Bytes
		reclaim += mv.ReclaimableBytes
	}
	v := DoneView{Card: m.receiptCard()}
	moved := len(r.Moved)
	tried := moved
	for _, s := range r.Skipped {
		tried++
		if s.Detail == notAttemptedInterrupted {
			v.NotTried++
		}
	}
	switch {
	case v.NotTried > 0 || m.stopping:
		v.Title = "Stopped. Moved " + itoa(moved) + " of " + words.Items(tried) + " to the Trash."
	case moved == 0:
		v.Title = "Nothing was moved."
	case len(r.Skipped) > 0:
		v.Title = "Moved " + itoa(moved) + " of " + words.Items(tried) + " to the Trash"
	default:
		v.Title = "Moved " + words.Items(moved) + " to the Trash"
	}
	if moved > 0 {
		v.Total = output.Bytes(bytes)
		v.SpaceLine = "is in your Trash now. Empty the Trash to get the space back."
		if reclaim < bytes {
			v.SharedNote = "Emptying it frees about " + output.Bytes(reclaim) + "; some files are shared with other folders."
		}
	}
	limit := len(r.Moved)
	if !m.showAllMove && limit > 6 {
		limit = 6
	}
	for _, mv := range r.Moved[:limit] {
		v.Moved = append(v.Moved, Line{Path: m.Rel(mv.Path), Text: output.Bytes(mv.Bytes)})
	}
	v.MovedMore = len(r.Moved) - limit
	for _, s := range r.Skipped {
		if s.Detail == notAttemptedInterrupted {
			continue
		}
		sentence, next := words.Skip(s.Reason)
		v.Skipped = append(v.Skipped, Line{Path: m.Rel(s.Path), Text: sentence, Next: next})
	}
	id := output.SafeText(r.ReceiptID)
	if len(r.Displaced) > 0 {
		v.Displaced = "Something else was at an item's name when macOS's Trash command ran, and it went to the Trash instead. kipple skipped the item and listed what moved in the receipt. Put it back with Finder's Put Back or kipple restore " + id + " --apply."
	}
	for _, w := range r.Warnings {
		v.Warnings = append(v.Warnings, output.SafeText(w))
	}
	if r.ReceiptID != "" && (moved > 0 || len(r.Displaced) > 0) {
		v.Command = "kipple restore " + id + " --apply"
		rename := false
		for _, mv := range r.Moved {
			rename = rename || mv.Mover == plan.MoverRename
		}
		if rename {
			v.ChangedMind = "Finder's Put Back doesn't know about items kipple moved straight into the Trash folder. Put them back here, run " + v.Command + " in Terminal, or drag them out of the Trash."
		} else {
			v.ChangedMind = "Open the Trash, select an item and choose Put Back, or put them all back here. " + v.Command + " also works."
		}
		v.CanPutBack = m.restored == nil
	}
	if m.restored != nil {
		rv := &RestoredView{}
		n := len(m.restored.Restored)
		switch {
		case n == 0:
			rv.Title = "Nothing was put back."
		case len(m.restored.Skipped) > 0:
			rv.Title = "Put back " + itoa(n) + " of " + words.Items(n+len(m.restored.Skipped)) + "."
		default:
			rv.Title = "Put back " + words.Items(n) + "."
		}
		for _, s := range m.restored.Skipped {
			sentence, next := words.Skip(s.Reason)
			rv.Skipped = append(rv.Skipped, Line{Path: m.Rel(s.Path), Text: sentence, Next: next})
		}
		v.Restored = rv
	}
	return v
}

// ShowAllMoved expands the moved list in place.
func (m *Model) ShowAllMoved() { m.showAllMove = true }

// Result returns the latest apply result.
func (m *Model) Result() plan.Result { return m.result }

// ---- Put back ----

// OpenPutBack opens the put-back confirmation for this run's receipt.
func (m *Model) OpenPutBack() {
	if m.screen != ScreenDone || m.sheet != SheetNone || m.restored != nil || m.result.ReceiptID == "" {
		return
	}
	m.sheet = SheetPutBack
}

// CancelPutBack closes the put-back confirmation.
func (m *Model) CancelPutBack() {
	if m.sheet != SheetPutBack {
		return
	}
	m.sheet = SheetNone
	m.hint = "Cancelled. Nothing was put back."
}

// PutBackView is the put-back confirmation.
type PutBackView struct {
	Title string
	Body  string
}

// PutBack describes the put-back confirmation.
func (m *Model) PutBack() PutBackView {
	n := len(m.result.Moved) + len(m.result.Displaced) + len(m.pending)
	return PutBackView{
		Title: "Put " + words.Items(n) + " back?",
		Body:  "Each item goes back to where it was, but only if that place is free and the item in the Trash is the one kipple moved there. Nothing is overwritten. This is what kipple restore " + output.SafeText(m.result.ReceiptID) + " --apply does.",
	}
}

// RestoreJob is a put back started by StartPutBack.
type RestoreJob struct {
	ctx  context.Context
	opts apply.RestoreOptions
	run  func(context.Context, apply.RestoreOptions) (plan.Result, error)
	err  error
}

// Run puts the receipt's items back.
func (j RestoreJob) Run() (plan.Result, error) {
	if j.err != nil {
		return plan.Result{}, j.err
	}
	return j.run(j.ctx, j.opts)
}

// StartPutBack is the put-back confirmation's Put Back. It restores
// through apply.Restore with the options kipple restore <receipt> --apply
// builds: the receipt from kipple's own store, every item, the same policy
// environment and rules, and the store so the receipt is annotated.
func (m *Model) StartPutBack() (RestoreJob, bool) {
	if m.sheet != SheetPutBack {
		return RestoreJob{}, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.sheet, m.hint = SheetRestoring, ""
	store := m.cfg.Store
	e, err := store.Load(m.result.ReceiptID)
	return RestoreJob{
		ctx: ctx, run: m.cfg.RestoreFunc, err: err,
		opts: apply.RestoreOptions{Env: m.cfg.Apply.Env, Rules: m.cfg.Apply.Rules, Receipt: e, Store: &store, Apply: true},
	}, true
}

// PutBackDone records the put back's result.
func (m *Model) PutBackDone(res plan.Result, err error) {
	if m.sheet != SheetRestoring {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.quitNow = m.quitAfter
	if err != nil {
		msg := output.SafeText(err.Error())
		body := []string{msg}
		if errors.Is(err, apply.ErrTrashAccess) {
			body = append(body, TrashAccessBody)
		}
		m.restoreErr = err
		m.setProblem(Problem{
			Kind: ProblemRestore, Heading: "kipple couldn't put the items back.", Body: body,
			Actions: []Action{ActionBack, ActionQuit}, BackTo: ScreenDone,
			Status: "Nothing was put back. The receipt still lists every item.",
		})
		return
	}
	m.restored = &res
	m.sheet = SheetNone
	m.hint = strings.TrimSuffix(m.Done().Restored.Title, ".") + ". The receipt records it."
}

// PutBackNow runs the confirmed put back on this goroutine (tests).
func (m *Model) PutBackNow() error {
	job, ok := m.StartPutBack()
	if !ok {
		return errors.New("guimodel: nothing to put back")
	}
	res, err := job.Run()
	m.PutBackDone(res, err)
	return err
}

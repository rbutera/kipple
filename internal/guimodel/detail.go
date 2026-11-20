package guimodel

import (
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/words"
)

// StatusKind is the detail pane's status field.
type StatusKind int

const (
	// StatusSelected: goes to the Trash on apply (amber).
	StatusSelected StatusKind = iota
	// StatusSelectable: may be selected.
	StatusSelectable
	// StatusManual: a manual item, never chosen for the person.
	StatusManual
	// StatusBlocked: something stops it.
	StatusBlocked
	// StatusInfo: measured only.
	StatusInfo
)

// Field is one labelled line of the detail pane.
type Field struct {
	Label string
	Value string
}

// DetailView is the detail pane for the cursor item.
type DetailView struct {
	ID         string
	Title      string
	Tier       string
	Size       string
	Path       string
	FullPath   string // the absolute path, for Copy path and Show in Finder (not escaped)
	Status     StatusKind
	StatusText string
	// Blockers are the reasons it can't be selected, as sentences.
	Blockers []string
	// Instead is the owner's own command (info items).
	Instead string
	AtRisk  bool
	What    string
	IfRem   string
	// Git is the repository state in words; GitNote explains that kipple
	// never fetches (unpushed and no upstream).
	Git, GitNote string
	Protected    []string
	Contents     string
	Warnings     []string
}

// Detail describes the cursor item, if there is one.
func (m *Model) Detail() (DetailView, bool) {
	it, ok := m.current()
	if !ok {
		return DetailView{}, false
	}
	r := m.ruleByID[it.Rule]
	d := DetailView{
		ID:       output.SafeText(it.ID),
		Title:    m.ruleTitle(it.Rule),
		Tier:     it.Tier,
		Size:     output.Bytes(it.AllocatedBytes),
		Path:     m.Rel(it.Path),
		FullPath: it.Path,
		AtRisk:   it.AtRisk,
		What:     output.SafeText(r.What),
		IfRem:    output.SafeText(r.IfRemoved),
	}
	switch {
	case it.Tier == string(rules.TierInfo):
		d.Status, d.StatusText = StatusInfo, "Measured only; kipple never moves it."
		d.Instead = m.ownerCommand(it)
	case m.sel.Selected(it.ID):
		d.Status, d.StatusText = StatusSelected, "Selected: goes to the Trash when you apply."
	case m.sel.Selectable(it.ID) && it.Tier == string(rules.TierManual):
		d.Status, d.StatusText = StatusManual, "Not selected: manual items are never chosen for you."
	case m.sel.Selectable(it.ID):
		d.Status, d.StatusText = StatusSelectable, "Not selected. Click its box or press Space to select it."
	default:
		d.Status, d.StatusText = StatusBlocked, "Can't be selected:"
		for _, reason := range it.Reasons {
			if words.IsTierReason(reason) {
				continue
			}
			s := words.Sentence(words.Reason(reason))
			if reason == plan.ReasonOwnerRunning && r.Owner.Name != "" {
				s += " Quit " + output.SafeText(r.Owner.Name) + " and scan again."
			}
			d.Blockers = append(d.Blockers, s)
		}
	}
	if it.Git != nil {
		d.Git = gitWords(it.Git.State)
		if it.Git.Detail != "" {
			d.Git += ": " + output.SafeText(it.Git.Detail)
		}
		if it.Git.State == plan.GitUnpushed || it.Git.State == plan.GitNoUpstream {
			d.GitNote = "kipple never fetches, so this compares with the last time the repository fetched."
		}
	}
	for i, p := range it.ProtectedPaths {
		if i == 5 {
			break
		}
		d.Protected = append(d.Protected, output.SafeText(p))
	}
	d.Contents = words.Plural(int(it.FileCount), "file")
	if !it.NewestMtime.IsZero() {
		d.Contents += ", newest " + it.NewestMtime.Format("2 Jan 2006")
	}
	for _, w := range it.Warnings {
		d.Warnings = append(d.Warnings, output.SafeText(w))
	}
	return d, true
}

// gitWords puts a git state into words.
func gitWords(state string) string {
	switch state {
	case plan.GitCleanPushed:
		return "Committed and pushed"
	case plan.GitDirty:
		return "Uncommitted changes"
	case plan.GitUnpushed:
		return "Commits not pushed"
	case plan.GitNoUpstream:
		return "No upstream branch"
	case plan.GitNotARepo:
		return "Not a git repository"
	case plan.GitUnavailable:
		return "git could not be run"
	}
	return output.SafeText(state)
}

// CursorPath returns the cursor item's absolute path (for Copy path).
func (m *Model) CursorPath() (string, bool) {
	it, ok := m.current()
	return it.Path, ok
}

// CursorID returns the cursor item's ID (for Copy ID).
func (m *Model) CursorID() (string, bool) {
	it, ok := m.current()
	return it.ID, ok
}

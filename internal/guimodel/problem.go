package guimodel

import (
	"errors"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/config"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
)

// ProblemKind is a refusal or an error that fills the window.
type ProblemKind int

const (
	ProblemNone ProblemKind = iota
	// ProblemRoot: kipple was started as root.
	ProblemRoot
	// ProblemHomeRefused: the KIPPLE_HOME guard refused the folder.
	ProblemHomeRefused
	// ProblemHomeUnreadable: the home folder could not be resolved.
	ProblemHomeUnreadable
	// ProblemConfig: the config file is invalid.
	ProblemConfig
	// ProblemScan: the scan failed.
	ProblemScan
	// ProblemTrashAccess: the Trash folder could not be used.
	ProblemTrashAccess
	// ProblemReceipt: the receipt could not be saved, so apply stopped.
	ProblemReceipt
	// ProblemApply: apply failed for another reason.
	ProblemApply
	// ProblemRestore: putting items back failed.
	ProblemRestore
	// ProblemState: the receipts folder can't be used.
	ProblemState
)

// Action is a button on a problem screen.
type Action int

const (
	ActionQuit Action = iota
	ActionScan
	ActionOpenPrivacy
	ActionBack
	ActionOpenTrash
	ActionOpenReceipt
)

// Problem is a full-window refusal or error.
type Problem struct {
	Kind    ProblemKind
	Heading string
	// Body is the explanation, as paragraphs; error text is escaped.
	Body    []string
	Actions []Action
	// Card is the receipt card, when a receipt exists.
	Card *ReceiptCard
	// Status is the action bar's text.
	Status string
	// CanScan allows Scan again (and ⌘R) from this screen.
	CanScan bool
	// BackTo is the screen Back returns to (0 when there is no Back).
	BackTo Screen
}

// Refuse returns a model that opens on a refusal, for a failure before the
// first scan (running as root, the home or config refused).
func Refuse(cfg Config, kind ProblemKind, err error) *Model {
	m := New(cfg)
	msg := ""
	if err != nil {
		msg = output.SafeText(err.Error())
	}
	p := Problem{Kind: kind, Actions: []Action{ActionQuit}, Status: "Nothing was scanned and nothing moved."}
	switch kind {
	case ProblemRoot:
		p.Heading = "kipple doesn't run as root."
		p.Body = []string{"Open it as yourself. kipple never uses sudo."}
	case ProblemHomeRefused:
		p.Heading = "kipple won't scan this folder."
		p.Body = []string{msg, "KIPPLE_HOME is for test folders. It can't be your real home folder, inside it, or around it."}
	case ProblemConfig:
		p.Heading = "kipple can't read its settings."
		p.Body = []string{msg, "The settings file is ~/" + config.RelPath + "."}
	case ProblemState:
		p.Heading = "kipple can't use its receipts folder."
		p.Body = []string{msg, "kipple writes a receipt for every move, so it won't start without one."}
	default:
		p.Kind = ProblemHomeUnreadable
		p.Heading = "kipple can't find your home folder."
		p.Body = []string{msg}
	}
	m.setProblem(p)
	return m
}

// scanProblem maps a scan error to its screen.
func scanProblem(err error) Problem {
	return Problem{
		Kind:    ProblemScan,
		Heading: "kipple couldn't finish the scan.",
		Body:    []string{output.SafeText(err.Error()), "Nothing was moved."},
		Actions: []Action{ActionScan, ActionQuit},
		Status:  "Nothing was moved.",
		CanScan: true,
	}
}

// TrashAccessBody is the trash-access explanation.
const TrashAccessBody = "macOS lets only apps with Full Disk Access open it. Allow kipple in System Settings > Privacy & Security > Full Disk Access, then try again."

// applyProblem maps an apply error to its screen. card is the receipt
// card when a receipt was written.
func applyProblem(err error, res plan.Result, card *ReceiptCard) Problem {
	switch {
	case errors.Is(err, apply.ErrTrashAccess) && !fullDiskAccess:
		// Not a Full Disk Access problem: say what is wrong with the Trash.
		return Problem{
			Kind:    ProblemTrashAccess,
			Heading: "kipple can't use your Trash folder, so nothing was moved.",
			Body:    []string{output.SafeText(err.Error())},
			Actions: []Action{ActionBack},
			Status:  "Nothing was moved.",
			BackTo:  ScreenReview,
		}
	case errors.Is(err, apply.ErrTrashAccess):
		return Problem{
			Kind:    ProblemTrashAccess,
			Heading: "kipple can't use your Trash folder, so nothing was moved.",
			Body:    []string{TrashAccessBody},
			Actions: []Action{ActionOpenPrivacy, ActionBack},
			Status:  "Nothing was moved.",
			BackTo:  ScreenReview,
		}
	case errors.Is(err, apply.ErrReceipt):
		moved := len(res.Moved)
		body := []string{"kipple never moves an item it can't record, so it stopped before the next one."}
		line := words2(moved, "item") + " moved before that and " + isAre(moved) + " in the Trash."
		if card != nil && card.Pending > 0 {
			line += " " + words2(card.Pending, "item") + " " + wasWere(card.Pending) + " about to move; it may or may not have moved. kipple restore looks for it by its tag."
		}
		body = append(body, line, "The disk may be full. Free some space, then put things back or carry on.")
		return Problem{
			Kind: ProblemReceipt, Heading: "kipple stopped: it couldn't save the receipt.", Body: body, Card: card,
			Actions: []Action{ActionOpenTrash, ActionOpenReceipt, ActionScan, ActionQuit},
			Status:  "The receipt on disk is the record of this run.", CanScan: true,
		}
	default:
		p := Problem{
			Kind: ProblemApply, Heading: "kipple stopped while moving items.", Body: []string{output.SafeText(err.Error())}, Card: card,
			Actions: []Action{ActionQuit}, Status: "The receipt on disk is the record of this run.",
		}
		if card != nil {
			p.Actions = []Action{ActionOpenTrash, ActionOpenReceipt, ActionQuit}
		}
		return p
	}
}

func words2(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

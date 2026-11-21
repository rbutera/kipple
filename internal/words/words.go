// Package words puts kipple's machine-readable codes into plain words for
// people: item reasons (why an item is not selected), the short chips the
// GUI shows on blocked rows, skip reasons (why apply or restore left an
// item alone, with a next step), and how a run moved items. The TUI and the
// GUI both use it, so the two front ends say the same thing.
//
// Every string that may carry a file-system name or an untrusted code is
// passed through output.SafeText.
package words

import (
	"fmt"
	"strings"

	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
)

// reasonText puts an item reason into words for a detail pane.
var reasonText = map[string]string{
	plan.ReasonTierReview:        "review tier: select it yourself if the cost of rebuilding is fine",
	plan.ReasonTierManual:        "manual tier: never selected for you; select it only if you are sure",
	plan.ReasonTierInfo:          "measured only; kipple never moves it",
	plan.ReasonTooRecent:         "changed too recently for this rule",
	plan.ReasonProjectActive:     "the project was worked on in the last 30 days",
	plan.ReasonGitDirty:          "the project has uncommitted changes",
	plan.ReasonGitUnpushed:       "the project has commits that are not pushed",
	plan.ReasonGitNoUpstream:     "the project's branch has no upstream to compare with",
	plan.ReasonGitNotARepo:       "the project is not a git repository, so kipple cannot tell what would be lost",
	plan.ReasonGitUnavailable:    "git could not be run to check the project",
	plan.ReasonOwnerRunning:      "the app that owns it is running",
	plan.ReasonScanPartial:       "part of it could not be read",
	plan.ReasonContainsMount:     "another volume is mounted inside it",
	plan.ReasonContainsProtected: "it holds protected files (keys, .env files, documents)",
	plan.ReasonNotRequested:      "not requested",
}

// chipText is the short label for a blocked row.
var chipText = map[string]string{
	plan.ReasonTierReview:        "review",
	plan.ReasonTierManual:        "manual",
	plan.ReasonTierInfo:          "info",
	plan.ReasonTooRecent:         "recent",
	plan.ReasonProjectActive:     "in use",
	plan.ReasonGitDirty:          "uncommitted",
	plan.ReasonGitUnpushed:       "unpushed",
	plan.ReasonGitNoUpstream:     "no upstream",
	plan.ReasonGitNotARepo:       "not in git",
	plan.ReasonGitUnavailable:    "no git",
	plan.ReasonOwnerRunning:      "app open",
	plan.ReasonScanPartial:       "unreadable",
	plan.ReasonContainsMount:     "mounted disk",
	plan.ReasonContainsProtected: "protected files",
	plan.ReasonNotRequested:      "not requested",
}

// IsTierReason reports whether r only names the item's tier: choosing the
// item yourself is consent to that, as with kipple clean --only.
func IsTierReason(r string) bool {
	return r == plan.ReasonTierReview || r == plan.ReasonTierManual
}

// Reason returns item reason r in words (lower case, no full stop).
func Reason(r string) string {
	if s, ok := reasonText[r]; ok {
		return s
	}
	if strings.HasPrefix(r, plan.RefusedPrefix) {
		return "refused by the safety policy (" + output.SafeText(strings.TrimPrefix(r, plan.RefusedPrefix)) + ")"
	}
	return output.SafeText(r)
}

// Chip returns the short row label for item reason r.
func Chip(r string) string {
	if s, ok := chipText[r]; ok {
		return s
	}
	if strings.HasPrefix(r, plan.RefusedPrefix) {
		return "refused"
	}
	return output.SafeText(r)
}

// Blockers returns the reasons other than tier reasons, in words.
func Blockers(reasons []string) []string {
	var out []string
	for _, r := range reasons {
		if !IsTierReason(r) {
			out = append(out, Reason(r))
		}
	}
	return out
}

// FirstBlocker returns the first reason other than a tier reason, or "".
func FirstBlocker(reasons []string) string {
	for _, r := range reasons {
		if !IsTierReason(r) {
			return r
		}
	}
	return ""
}

// skipText is a skipped entry's reason as a sentence, and a next step
// where there is a useful one.
var skipText = map[string][2]string{
	plan.SkipNotFound:            {"A scan now no longer finds it.", ""},
	plan.SkipInfoTier:            {"It is measured only; kipple never moves it.", ""},
	plan.SkipPermanentNotAllowed: {"Permanent deletion acts only on safe items.", ""},
	plan.SkipToolRunning:         {"An agent was running in this project, or kipple could not tell.", "Stop it and scan again."},
	plan.SkipIdentityChanged:     {"It changed after the scan, so it is not the item kipple checked.", "Scan again."},
	plan.SkipRootChanged:         {"Its rule's folder was replaced after the scan.", "Scan again."},
	plan.SkipParentChanged:       {"Its folder moved or was replaced just before the move.", "Scan again."},
	plan.SkipDifferentDevice:     {"It is on a different disk from the Trash, and kipple never copies.", ""},
	plan.SkipTrashChanged:        {"The Trash folder was replaced during the run.", "Scan again."},
	plan.SkipPermissionDenied:    {Here.SystemRefused + " kipple never asks for more permissions.", ""},
	plan.SkipMoveFailed:          {"The move failed.", ""},
	plan.SkipDestinationExists:   {"Something is already at its original path.", "Move that aside, then put it back."},
	plan.SkipTrashItemMissing:    {"It is no longer in the Trash.", ""},
	plan.SkipTrashItemMismatch:   {"The item in the Trash is not the one kipple moved there.", ""},
	plan.SkipNotInTrash:          {"The receipt's Trash path is not inside the Trash.", ""},
	plan.SkipAlreadyRestored:     {"It was already put back.", ""},
	plan.SkipDeletedPermanently:  {"It was deleted permanently, so there is nothing to put back.", ""},
	plan.ReasonOwnerRunning:      {"The app that owns it is running.", "Quit it and scan again."},
	plan.ReasonTooRecent:         {"It changed too recently for this rule.", ""},
	plan.ReasonProjectActive:     {"The project was worked on in the last 30 days.", ""},
	plan.ReasonGitDirty:          {"The project has uncommitted changes.", ""},
	plan.ReasonGitUnpushed:       {"The project has commits that are not pushed.", ""},
	plan.ReasonContainsProtected: {"It holds protected files (keys, .env files, documents).", ""},
}

// Skip returns a skip reason as a sentence and, where useful, a next step
// ("" when there is none). Every value of plan.SkipReasons has a sentence.
func Skip(reason string) (sentence, next string) {
	if s, ok := skipText[reason]; ok {
		return s[0], s[1]
	}
	return Sentence(Reason(reason)), ""
}

// Sentence capitalises s and ends it with a full stop.
func Sentence(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// Mover says how a run moved items, for the GUI's receipt card.
func Mover(m string) string {
	switch m {
	case plan.MoverRename:
		return "Moved straight into the Trash folder."
	case plan.MoverNSFileManager:
		return "Moved with macOS's Trash command (no Full Disk Access)."
	case plan.MoverFreedesktop:
		return "Moved into the Trash folder (" + PhrasesFor("linux").TrashFolder + "), where your file manager can restore it too."
	}
	return ""
}

// Plural returns "1 file", "2 files".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Items returns "1 item" or "N items".
func Items(n int) string { return Plural(n, "item") }

// TierLine is the one-sentence explanation of a tier on its section row.
func TierLine(tier string) string {
	switch tier {
	case "safe":
		return "Rebuilds itself. Selected for you."
	case "review":
		return "Rebuilds, but costs time. Your call."
	case "manual":
		return "Never selected for you. Only if you're sure."
	case "info":
		return "Measured only. kipple never moves these."
	}
	return ""
}

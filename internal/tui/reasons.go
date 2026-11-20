package tui

import (
	"strings"

	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
)

// reasonText puts an item reason into words for the detail pane.
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

// tierReason reports whether r only names the item's tier: choosing the
// item yourself is consent to that, as with kipple clean --only.
func tierReason(r string) bool {
	return r == plan.ReasonTierReview || r == plan.ReasonTierManual
}

// describeReason returns reason r in words.
func describeReason(r string) string {
	if s, ok := reasonText[r]; ok {
		return s
	}
	if strings.HasPrefix(r, plan.RefusedPrefix) {
		return "refused by the safety policy (" + output.SafeText(strings.TrimPrefix(r, plan.RefusedPrefix)) + ")"
	}
	return output.SafeText(r)
}

// blockers returns the reasons other than tier reasons, in words.
func blockers(reasons []string) []string {
	var out []string
	for _, r := range reasons {
		if !tierReason(r) {
			out = append(out, describeReason(r))
		}
	}
	return out
}

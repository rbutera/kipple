// Package output renders kipple documents for people. Machine output is the
// JSON from internal/plan; nothing here is parsed by anyone.
package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
)

// Bytes formats n with decimal units, as Finder does: 999 B, 1.0 kB, 2.5 MB.
func Bytes(n int64) string {
	const unit = 1000
	if n < 0 {
		return "-" + Bytes(-n)
	}
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), []string{"kB", "MB", "GB", "TB", "PB"}[exp])
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// tierBlurb explains each tier in one line.
var tierBlurb = map[rules.Tier]string{
	rules.TierSafe:   "regenerates by itself; selected at every level",
	rules.TierReview: "regenerates but costs time or needs a condition; selected with --level review",
	rules.TierManual: "never selected; cleaned only by naming the item ID",
	rules.TierInfo:   "measured only; kipple never moves it",
}

// NextStep is the command that would act on the plan.
func NextStep(level string) string {
	if level == rules.LevelReview {
		return "kipple clean --level review --apply"
	}
	return "kipple clean --apply"
}

// Plan writes the grouped table for p: one block per rule with items, each
// item with its selection marker, size, path and reasons, then warnings and
// a final line with the selected total, the statement that nothing was
// moved, and the next step. titles maps rule IDs to titles (may be nil).
func Plan(w io.Writer, p plan.Plan, titles map[string]string) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "Home:  %s\nLevel: %s\n", SafeText(p.Home), SafeText(p.Level))
	byRule := map[string][]plan.Item{}
	for _, it := range p.Items {
		byRule[it.Rule] = append(byRule[it.Rule], it)
	}
	var absent, errored []string
	for _, r := range p.Rules {
		items := byRule[r.ID]
		switch {
		case r.Status == plan.RuleError:
			errored = append(errored, r.ID)
		case len(items) == 0:
			absent = append(absent, r.ID)
			continue
		}
		if len(items) == 0 {
			continue
		}
		title := titles[r.ID]
		if title == "" {
			title = r.ID
		}
		fmt.Fprintf(b, "\n%s (%s, %s)  %s in %s\n", SafeText(title), SafeText(r.ID), SafeText(r.Tier), Bytes(r.Bytes), count(len(items), "item"))
		for _, it := range items {
			mark := "[ ]"
			if it.Selected {
				mark = "[x]"
			}
			fmt.Fprintf(b, "  %s %10s  %s  %s", mark, Bytes(it.AllocatedBytes), SafeText(it.ID), SafeText(it.Path))
			if len(it.Reasons) > 0 {
				fmt.Fprintf(b, "  (%s)", strings.Join(safeList(it.Reasons), ", "))
			}
			if it.ScanStatus == plan.ScanPartial {
				b.WriteString("  [partial]")
			}
			if it.AtRisk {
				b.WriteString("  [at risk: uncommitted or unpushed work]")
			}
			b.WriteString("\n")
			if it.OwnerCommand != "" {
				fmt.Fprintf(b, "      to clean, use: %s\n", SafeText(it.OwnerCommand))
			}
			for _, pp := range it.ProtectedPaths {
				fmt.Fprintf(b, "      protected inside: %s\n", SafeText(pp))
			}
		}
	}
	if len(errored) > 0 || len(p.Warnings) > 0 {
		b.WriteString("\nWarnings:\n")
		for _, w := range p.Warnings {
			fmt.Fprintf(b, "  %s\n", SafeText(w))
		}
	}
	if len(absent) > 0 {
		fmt.Fprintf(b, "\nNothing found for %s (not installed or empty).\n", count(len(absent), "rule"))
	}
	t := p.Totals
	fmt.Fprintf(b, "\nListed %s, %s. Selected %s, %s (%s reclaimable).\n",
		count(t.ListedCount, "item"), Bytes(t.ListedBytes), count(t.SelectedCount, "item"), Bytes(t.SelectedBytes), Bytes(t.ReclaimableBytes))
	fmt.Fprintf(b, "Selected total %s. Nothing was moved (dry run). Next step: %s\n", Bytes(t.SelectedBytes), NextStep(p.Level))
	return writeSafe(w, b)
}

// Rules writes the catalogue grouped by tier with explanations and roots.
func Rules(w io.Writer, rs []rules.Rule) error {
	b := &strings.Builder{}
	for i, tier := range rules.Tiers() {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "%s: %s\n", strings.ToUpper(string(tier)), tierBlurb[tier])
		for _, r := range rs {
			if r.Tier != tier {
				continue
			}
			fmt.Fprintf(b, "\n  %s  %s\n", r.ID, r.Title)
			writeRuleBody(b, r, "    ")
		}
	}
	return writeSafe(w, b)
}

func writeRuleBody(b *strings.Builder, r rules.Rule, indent string) {
	fmt.Fprintf(b, "%sWhat:       %s\n", indent, r.What)
	fmt.Fprintf(b, "%sIf removed: %s\n", indent, r.IfRemoved)
	fmt.Fprintf(b, "%sRoots:      %s\n", indent, strings.Join(safeList(prefixed(r.Roots)), ", "))
	if len(r.Exclude) > 0 {
		fmt.Fprintf(b, "%sExcludes:   %s\n", indent, strings.Join(safeList(r.Exclude), ", "))
	}
	if c := conditions(r); c != "" {
		fmt.Fprintf(b, "%sConditions: %s\n", indent, c)
	}
	fmt.Fprintf(b, "%sOwner:      %s\n", indent, r.Owner.Name)
	if r.OwnerCommand != "" {
		fmt.Fprintf(b, "%sTo clean:   %s\n", indent, r.OwnerCommand)
	}
}

func prefixed(roots []string) []string {
	out := make([]string, len(roots))
	for i, r := range roots {
		out[i] = "~/" + r
	}
	return out
}

func conditions(r rules.Rule) string {
	var parts []string
	c := r.Conditions
	if c.MinAgeDays > 0 {
		parts = append(parts, fmt.Sprintf("older than %d days", c.MinAgeDays))
	}
	if c.RequiresIdleProject {
		parts = append(parts, fmt.Sprintf("project idle for %d days", c.IdleDays))
	}
	if c.RequiresCleanGit {
		parts = append(parts, "git committed and pushed")
	}
	if c.SkipIfOwnerRunning {
		parts = append(parts, "owner not running")
	}
	if c.SkipIfAgentInProject {
		parts = append(parts, "no agent working in the project")
	}
	switch r.Match.Kind {
	case rules.MatchExtensions:
		parts = append(parts, "files ending "+strings.Join(r.Match.Extensions, ", "))
	case rules.MatchProjectArtifact:
		parts = append(parts, fmt.Sprintf("%s beside %s", strings.Join(r.Match.Names, " or "), strings.Join(r.Match.Markers, " or ")))
	}
	return strings.Join(parts, "; ")
}

// ExplainRule writes a rule's tier, roots and both explanation sentences.
func ExplainRule(w io.Writer, r rules.Rule) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%s  %s\nTier:       %s (%s)\n", r.ID, r.Title, r.Tier, tierBlurb[r.Tier])
	writeRuleBody(b, r, "")
	return writeSafe(w, b)
}

// ExplainItem writes an item's path, sizes, selection and reasons, then its
// rule.
func ExplainItem(w io.Writer, it plan.Item, r rules.Rule) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "Item %s\nPath:       %s\n", SafeText(it.ID), SafeText(it.Path))
	fmt.Fprintf(b, "Size:       %s on disk, %s logical, %s reclaimable, %d files\n",
		Bytes(it.AllocatedBytes), Bytes(it.SizeBytes), Bytes(it.ReclaimableBytes), it.FileCount)
	fmt.Fprintf(b, "Scan:       %s\n", SafeText(it.ScanStatus))
	sel := "no"
	if it.Selected {
		sel = "yes"
	}
	fmt.Fprintf(b, "Selected:   %s\n", sel)
	if len(it.Reasons) > 0 {
		fmt.Fprintf(b, "Reasons:    %s\n", strings.Join(safeList(it.Reasons), ", "))
	}
	if it.Git != nil {
		fmt.Fprintf(b, "Git:        %s", SafeText(it.Git.State))
		if it.Git.Detail != "" {
			fmt.Fprintf(b, " (%s)", SafeText(it.Git.Detail))
		}
		if it.AtRisk {
			b.WriteString(", at risk")
		}
		b.WriteString("\n")
	}
	for _, p := range it.ProtectedPaths {
		fmt.Fprintf(b, "Protected:  %s\n", SafeText(p))
	}
	for _, wn := range it.Warnings {
		fmt.Fprintf(b, "Warning:    %s\n", SafeText(wn))
	}
	b.WriteString("\n")
	if err := writeSafe(w, b); err != nil {
		return err
	}
	return ExplainRule(w, r)
}

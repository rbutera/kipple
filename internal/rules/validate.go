package rules

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rbutera/kipple/internal/policy"
)

var idPattern = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+(-[a-z0-9]+)*)+$`)

// Validate checks the catalogue invariants (specs/rule-catalogue "Catalogue
// invariants") and returns one error per violation. Every error starts with
// the offending rule's ID (or both IDs for a conflict between two rules).
//
// Info rule roots must be denylisted (so no other rule can move them; the
// policy also refuses info rules with info_tier), and project artifact rules
// may share roots with each other (they must have disjoint artifact names,
// and their roots must not overlap any other rule's root).
func Validate(rs []Rule) []error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	type ownedRoot struct {
		rule Rule
		segs []string
	}
	var all []ownedRoot
	ids := map[string]bool{}

	for _, r := range rs {
		if !idPattern.MatchString(r.ID) {
			fail("%s: ID does not match %s", r.ID, idPattern)
		}
		if ids[r.ID] {
			fail("%s: duplicate rule ID", r.ID)
		}
		ids[r.ID] = true
		if !r.Tier.Valid() {
			fail("%s: invalid tier %q", r.ID, r.Tier)
		}
		if strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.Owner.Name) == "" {
			fail("%s: empty title or owner name", r.ID)
		}
		for _, f := range []struct{ field, text string }{{"what", r.What}, {"if_removed", r.IfRemoved}} {
			s := strings.TrimSpace(f.text)
			switch {
			case s == "":
				fail("%s: empty %s", r.ID, f.field)
			case !strings.HasSuffix(s, "."):
				fail("%s: %s should be one sentence ending in a full stop: %q", r.ID, f.field, s)
			case strings.Contains(strings.TrimSuffix(s, "."), ". "):
				fail("%s: %s should be a single sentence: %q", r.ID, f.field, s)
			}
		}
		if r.Tier == TierInfo && strings.TrimSpace(r.OwnerCommand) == "" {
			fail("%s: info rules must name the owner's own command", r.ID)
		}
		switch r.Match.Kind {
		case MatchChildren:
		case MatchExtensions:
			if len(r.Match.Extensions) == 0 {
				fail("%s: extensions match without extensions", r.ID)
			}
			for _, e := range r.Match.Extensions {
				if !strings.HasPrefix(e, ".") || len(e) < 2 || strings.ContainsAny(e[1:], "./") {
					fail("%s: extension %q should look like .ext", r.ID, e)
				}
			}
		case MatchProjectArtifact:
			if len(r.Match.Names) == 0 || len(r.Match.Markers) == 0 {
				fail("%s: project artifact rule needs names and markers", r.ID)
			}
			for _, m := range append(append([]string(nil), r.Match.Names...), r.Match.Markers...) {
				if m == "" || m == "." || m == ".." || strings.ContainsAny(m, `/*?[\`) {
					fail("%s: artifact name or marker %q must be a plain file name", r.ID, m)
				}
			}
			c := r.Conditions
			if r.Tier != TierReview || !c.RequiresIdleProject || c.IdleDays < 30 || !c.RequiresCleanGit || !c.SkipIfAgentInProject {
				fail("%s: project artifact rules must be review tier with idle (30d), clean git and agent checks", r.ID)
			}
		default:
			fail("%s: unknown match kind %q", r.ID, r.Match.Kind)
		}
		for _, ex := range r.Exclude {
			if _, err := filepath.Match(ex, ""); err != nil || ex == "" {
				fail("%s: malformed exclusion %q", r.ID, ex)
			}
		}

		if len(r.Roots) == 0 {
			fail("%s: no roots", r.ID)
		}
		for _, root := range r.Roots {
			segs, ok := policy.ParseRoot(root)
			if !ok {
				fail("%s: root %q is not a valid home-relative root (absolute, .., confusable letters, a glob in the first segment, or more than one glob)", r.ID, root)
				continue
			}
			if r.Tier != TierInfo && policy.IsDenylisted(root) {
				fail("%s: root %q is on, under, or contains a denylisted path", r.ID, root)
			}
			if r.Tier == TierInfo && !policy.IsDenylisted(root) {
				fail("%s: info root %q is not on the denylist, so another rule could move it", r.ID, root)
			}
			all = append(all, ownedRoot{r, segs})
		}
	}

	// No two rules claim the same root, and a root nested inside another
	// rule's root must be excluded by that outer rule (so no path is offered
	// twice). Project artifact rules share the project roots by design.
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			sameRule := a.rule.ID == b.rule.ID
			aProj := a.rule.Match.Kind == MatchProjectArtifact
			bProj := b.rule.Match.Kind == MatchProjectArtifact
			if aProj != bProj {
				if i < j && segsOverlap(a.segs, b.segs) {
					fail("%s and %s: project root %q overlaps root %q", a.rule.ID, b.rule.ID, strings.Join(a.segs, "/"), strings.Join(b.segs, "/"))
				}
				continue
			}
			if len(a.segs) == len(b.segs) && segsMatch(a.segs, b.segs) {
				if aProj && bProj && !sameRule {
					continue
				}
				if i < j {
					fail("%s and %s: both claim root %q", a.rule.ID, b.rule.ID, strings.Join(a.segs, "/"))
				}
				continue
			}
			if len(a.segs) < len(b.segs) && segsMatch(a.segs, b.segs[:len(a.segs)]) {
				child := b.segs[len(a.segs)]
				switch {
				case sameRule:
					fail("%s: root %q is nested inside its own root %q", a.rule.ID, strings.Join(b.segs, "/"), strings.Join(a.segs, "/"))
				case aProj && bProj:
				case !excludes(a.rule, child):
					fail("%s: root %q lies inside root %q of %s, which does not exclude %q", b.rule.ID, strings.Join(b.segs, "/"), strings.Join(a.segs, "/"), a.rule.ID, child)
				}
			}
		}
	}

	projectNames := map[string]string{}
	for _, r := range rs {
		if r.Match.Kind != MatchProjectArtifact {
			continue
		}
		for _, n := range r.Match.Names {
			if other, dup := projectNames[n]; dup && other != r.ID {
				fail("%s and %s: both claim artifact name %q", other, r.ID, n)
			}
			projectNames[n] = r.ID
		}
	}
	return errs
}

// segMatch compares two root segments the way the policy refuses: folded,
// with either side allowed to be a glob.
func segMatch(a, b string) bool { return policy.Overlaps(a, b) }

func segsMatch(a, b []string) bool {
	for i := range a {
		if !segMatch(a[i], b[i]) {
			return false
		}
	}
	return true
}

// segsOverlap: equal, or one is a prefix of the other.
func segsOverlap(a, b []string) bool {
	n := min(len(a), len(b))
	return segsMatch(a[:n], b[:n])
}

func excludes(r Rule, name string) bool {
	for _, p := range r.Exclude {
		if policy.Overlaps(p, name) {
			return true
		}
	}
	return false
}

// Package rules holds kipple's catalogue: the allowlist of locations it knows
// how to clean, each with a risk tier and plain-language explanations.
//
// Rules are Go values, not runtime configuration, so the allowlist cannot be
// extended by a file on disk. Rules only describe; internal/policy is the
// gate. This package imports policy only so Validate can check roots against
// the gate's root grammar and denylist; policy never imports rules. Rule
// implements policy.Rule, so a Rule can be passed to policy.Authorize.
package rules

// Tier is a rule's risk tier.
type Tier string

const (
	// TierSafe regenerates itself without cost and is pre-selected at every level.
	TierSafe Tier = "safe"
	// TierReview regenerates but costs time or needs a condition; listed at
	// every level and pre-selected only at level review.
	TierReview Tier = "review"
	// TierManual is never pre-selected; movable only by explicit item ID.
	TierManual Tier = "manual"
	// TierInfo is measured and reported but never movable by kipple; the
	// owning tool's own command is shown instead.
	TierInfo Tier = "info"
)

// Tiers returns every tier from least to most restrictive.
func Tiers() []Tier { return []Tier{TierSafe, TierReview, TierManual, TierInfo} }

// Valid reports whether t is a known tier.
func (t Tier) Valid() bool {
	for _, x := range Tiers() {
		if t == x {
			return true
		}
	}
	return false
}

// Selection levels (D4). manual and info are never pre-selected, so they are
// not levels.
const (
	LevelSafe   = "safe"
	LevelReview = "review"
)

// Levels returns the valid values of --level and the config key level.
func Levels() []string { return []string{LevelSafe, LevelReview} }

// MatchKind says which entries under a root are candidate items.
type MatchKind string

const (
	// MatchChildren: every direct child of a root is an item.
	MatchChildren MatchKind = "children"
	// MatchExtensions: direct children that are regular files with one of
	// Match.Extensions (case-insensitive) are items.
	MatchExtensions MatchKind = "extensions"
	// MatchProjectArtifact: directories named one of Match.Names that sit
	// beside a file matching one of Match.Markers, found by a bounded walk
	// under the project roots.
	MatchProjectArtifact MatchKind = "project_artifact"
)

// Match describes how candidates are found under a rule's roots.
type Match struct {
	Kind       MatchKind `json:"kind"`
	Extensions []string  `json:"extensions,omitempty"`
	Names      []string  `json:"names,omitempty"`
	Markers    []string  `json:"markers,omitempty"`
}

// Conditions must hold for an item to be selectable, at scan time and again
// immediately before it is moved.
type Conditions struct {
	// MinAgeDays: the item's newest modification time must be at least this old.
	MinAgeDays int `json:"min_age_days,omitempty"`
	// RequiresIdleProject: no file in the project outside artifact
	// directories was modified within IdleDays.
	RequiresIdleProject bool `json:"requires_idle_project,omitempty"`
	IdleDays            int  `json:"idle_days,omitempty"`
	// RequiresCleanGit: the project's git state must be clean_pushed.
	RequiresCleanGit bool `json:"requires_clean_git,omitempty"`
	// SkipIfOwnerRunning: skip with owner_running while the owner app or
	// process (Owner.BundleIDs, Owner.Processes) is running.
	SkipIfOwnerRunning bool `json:"skip_if_owner_running,omitempty"`
	// SkipIfAgentInProject: skip with tool_running while one of AgentProcesses
	// has its working directory inside the project.
	SkipIfAgentInProject bool `json:"skip_if_agent_in_project,omitempty"`
}

// Owner is the tool that creates and owns a rule's files.
type Owner struct {
	Name      string   `json:"name"`
	BundleIDs []string `json:"bundle_ids,omitempty"`
	Processes []string `json:"processes,omitempty"`
}

// Rule is one catalogue entry.
type Rule struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Tier  Tier   `json:"tier"`
	// Roots are home-relative, slash-separated directories whose descendants
	// the rule may offer. One path segment may be a filepath.Match glob. The
	// roots themselves are never moved.
	Roots []string `json:"roots"`
	// Exclude lists name patterns for direct children of a root that the rule
	// never offers (policy.Authorize refuses them too).
	Exclude    []string   `json:"exclude,omitempty"`
	Match      Match      `json:"match"`
	Conditions Conditions `json:"conditions"`
	Owner      Owner      `json:"owner"`
	// OwnerCommand is the owning tool's own cleanup command. Required for info
	// rules, which kipple never moves.
	OwnerCommand string `json:"owner_command,omitempty"`
	// What and IfRemoved are one sentence each, written for a non-expert.
	What      string `json:"what"`
	IfRemoved string `json:"if_removed"`
}

// RuleID implements policy.Rule.
func (r Rule) RuleID() string { return r.ID }

// RuleRoots implements policy.Rule.
func (r Rule) RuleRoots() []string { return r.Roots }

// RuleExcludes implements policy.Rule.
func (r Rule) RuleExcludes() []string { return r.Exclude }

// Movable reports whether kipple may ever move items of this rule. An
// unknown or empty tier is not movable (fail closed).
func (r Rule) Movable() bool { return r.Tier.Valid() && r.Tier != TierInfo }

// RuleMovable implements policy.Rule: info rules are never authorised.
func (r Rule) RuleMovable() bool { return r.Movable() }

// RuleMatchKind implements policy.Rule.
func (r Rule) RuleMatchKind() string { return string(r.Match.Kind) }

// RuleMatchExtensions implements policy.Rule.
func (r Rule) RuleMatchExtensions() []string { return r.Match.Extensions }

// RuleMatchNames implements policy.Rule.
func (r Rule) RuleMatchNames() []string { return r.Match.Names }

// agentProcesses are the agent CLIs whose working directory blocks a project
// artifact (reason tool_running).
var agentProcesses = []string{"claude", "codex", "cursor-agent"}

// AgentProcesses returns the agent process names checked by
// Conditions.SkipIfAgentInProject.
func AgentProcesses() []string { return append([]string(nil), agentProcesses...) }

// DefaultProjectRoots are the home-relative folders searched for projects
// when config does not set project_roots. Missing ones are skipped.
var defaultProjectRoots = []string{"dev", "code", "projects", "src", "Developer", "repos"}

// DefaultProjectRoots returns a copy of the default project roots.
func DefaultProjectRoots() []string { return append([]string(nil), defaultProjectRoots...) }

// Catalogue returns a deep copy of every rule, in a stable order.
func Catalogue() []Rule {
	out := make([]Rule, len(catalogue))
	for i, r := range catalogue {
		out[i] = r.clone()
	}
	return out
}

// Lookup returns the rule with the given ID.
func Lookup(id string) (Rule, bool) {
	for _, r := range catalogue {
		if r.ID == id {
			return r.clone(), true
		}
	}
	return Rule{}, false
}

func (r Rule) clone() Rule {
	c := r
	c.Roots = cloneStrings(r.Roots)
	c.Exclude = cloneStrings(r.Exclude)
	c.Match.Extensions = cloneStrings(r.Match.Extensions)
	c.Match.Names = cloneStrings(r.Match.Names)
	c.Match.Markers = cloneStrings(r.Match.Markers)
	c.Owner.BundleIDs = cloneStrings(r.Owner.BundleIDs)
	c.Owner.Processes = cloneStrings(r.Owner.Processes)
	return c
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
}

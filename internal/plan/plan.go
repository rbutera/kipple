// Package plan defines the documents kipple emits: the scan plan (schema
// version 1), the rules listing, and the wave 3 receipt and apply result. The
// Go types are the single source of truth: JSON encoding and the published
// JSON Schema (kipple schema ...) are both generated from them.
package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
)

// SchemaVersion is the version of every document in this package. A change
// that removes or renames a field, or changes a field's meaning, bumps it.
const SchemaVersion = 1

// Item reasons: why an item is not selected. A closed set; see Reasons.
const (
	// ReasonTierReview: review-tier item at level safe.
	ReasonTierReview = "tier_review"
	// ReasonTierManual: manual-tier item, movable only by explicit ID.
	ReasonTierManual = "tier_manual"
	// ReasonTierInfo: info-tier item, measured only and never movable.
	ReasonTierInfo = "tier_info"
	// ReasonTooRecent: newest modification is younger than the rule's minimum age.
	ReasonTooRecent = "too_recent"
	// ReasonProjectActive: a project file outside artifact directories
	// changed within the idle window.
	ReasonProjectActive = "project_active"
	// Git states that block a project artifact (wave 3).
	ReasonGitDirty       = "git_dirty"
	ReasonGitUnpushed    = "git_unpushed"
	ReasonGitNoUpstream  = "git_no_upstream"
	ReasonGitNotARepo    = "git_not_a_repo"
	ReasonGitUnavailable = "git_unavailable"
	// ReasonGitUnchecked: the git condition has not been evaluated. Wave 2
	// scans never check git, so every project artifact carries it.
	// TODO(wave 3): gitstate.Inspect replaces it with a real state.
	ReasonGitUnchecked = "git_unchecked"
	// ReasonOwnerRunning: the owning app or process is running (wave 3).
	ReasonOwnerRunning = "owner_running"
	// ReasonScanPartial: part of the item could not be read or sat on another
	// volume, so its contents were not fully inspected. Never pre-selected.
	ReasonScanPartial = "scan_partial"
	// ReasonContainsProtected: something inside the item is denylisted or
	// user-protected (listed in protected_paths).
	ReasonContainsProtected = "contains_protected"
	// RefusedPrefix prefixes a policy refusal reason, for example
	// "refused:symlink_in_path".
	RefusedPrefix = "refused:"
)

// Refused returns the item reason for a policy refusal.
func Refused(policyReason string) string { return RefusedPrefix + policyReason }

// baseReasons are the item reasons other than refusals, in documentation order.
var baseReasons = []string{
	ReasonTierReview, ReasonTierManual, ReasonTierInfo, ReasonTooRecent,
	ReasonProjectActive, ReasonGitDirty, ReasonGitUnpushed, ReasonGitNoUpstream,
	ReasonGitNotARepo, ReasonGitUnavailable, ReasonGitUnchecked,
	ReasonOwnerRunning, ReasonScanPartial, ReasonContainsProtected,
}

// Reasons returns every item reason, including "refused:" plus each policy
// refusal reason.
func Reasons() []string {
	out := append([]string(nil), baseReasons...)
	for _, r := range policy.Reasons() {
		out = append(out, Refused(r))
	}
	return out
}

// Scan statuses.
const (
	ScanComplete = "complete"
	ScanPartial  = "partial"
)

// Rule statuses.
const (
	RulePresent = "present"
	RuleAbsent  = "absent"
	RuleError   = "error"
)

// Git states. Wave 2 emits only GitUnchecked.
const (
	GitCleanPushed = "clean_pushed"
	GitDirty       = "dirty"
	GitUnpushed    = "unpushed"
	GitNoUpstream  = "no_upstream"
	GitNotARepo    = "not_a_repo"
	GitUnavailable = "git_unavailable"
	GitUnchecked   = "unchecked"
)

// Plan is the scan result: what kipple would move, and why or why not.
type Plan struct {
	SchemaVersion int          `json:"schema_version" jsonschema:"description=Document version; 1 for kipple v1."`
	GeneratedAt   time.Time    `json:"generated_at" jsonschema:"description=When the scan finished (RFC 3339)."`
	KippleVersion string       `json:"kipple_version"`
	Home          string       `json:"home" jsonschema:"description=The effective home folder; every path is below it."`
	Level         string       `json:"level" jsonschema:"enum=safe,enum=review"`
	Totals        Totals       `json:"totals"`
	Rules         []RuleStatus `json:"rules"`
	Items         []Item       `json:"items"`
	Warnings      []string     `json:"warnings"`
}

// Totals sum the items. Byte totals are allocated bytes, with every
// hard-linked inode counted once across the plan.
type Totals struct {
	ListedCount      int   `json:"listed_count"`
	ListedBytes      int64 `json:"listed_bytes"`
	SelectedCount    int   `json:"selected_count"`
	SelectedBytes    int64 `json:"selected_bytes"`
	ReclaimableBytes int64 `json:"reclaimable_bytes" jsonschema:"description=Allocated bytes of selected items that moving them would actually free (excludes files with other hard links)."`
}

// RuleStatus reports one catalogue rule.
type RuleStatus struct {
	ID        string `json:"id"`
	Tier      string `json:"tier" jsonschema:"enum=safe,enum=review,enum=manual,enum=info"`
	Status    string `json:"status" jsonschema:"enum=present,enum=absent,enum=error"`
	ItemCount int    `json:"item_count"`
	Bytes     int64  `json:"bytes" jsonschema:"description=Allocated bytes of the rule's items."`
}

// Item is one candidate.
type Item struct {
	ID               string    `json:"id" jsonschema:"pattern=^[a-z2-7]{12}$"`
	Rule             string    `json:"rule"`
	Tier             string    `json:"tier" jsonschema:"enum=safe,enum=review,enum=manual,enum=info"`
	Path             string    `json:"path" jsonschema:"description=Absolute path."`
	SizeBytes        int64     `json:"size_bytes" jsonschema:"description=Logical bytes (st_size)."`
	AllocatedBytes   int64     `json:"allocated_bytes" jsonschema:"description=Bytes on disk (st_blocks*512)."`
	ReclaimableBytes int64     `json:"reclaimable_bytes" jsonschema:"description=Allocated bytes excluding files with other hard links."`
	FileCount        int64     `json:"file_count"`
	NewestMtime      time.Time `json:"newest_mtime"`
	ScanStatus       string    `json:"scan_status" jsonschema:"enum=complete,enum=partial"`
	Selected         bool      `json:"selected"`
	Reasons          []string  `json:"reasons" jsonschema:"description=Why the item is not selected; empty when selected."`
	ProtectedPaths   []string  `json:"protected_paths,omitempty" jsonschema:"description=Up to 5 item-relative paths of protected entries inside the item."`
	Warnings         []string  `json:"warnings,omitempty"`
	OwnerCommand     string    `json:"owner_command,omitempty" jsonschema:"description=For info items: the owning tool's own cleanup command."`
	Git              *Git      `json:"git,omitempty" jsonschema:"description=Project artifacts only."`
	AtRisk           bool      `json:"at_risk,omitempty"`
}

// Git is a project artifact's repository state.
type Git struct {
	State string `json:"state" jsonschema:"enum=clean_pushed,enum=dirty,enum=unpushed,enum=no_upstream,enum=not_a_repo,enum=git_unavailable,enum=unchecked"`
}

// JSONSchemaExtend pins schema_version.
func (Plan) JSONSchemaExtend(s *jsonschema.Schema) { pinVersion(s) }

// JSONSchemaExtend closes the reasons enumeration.
func (Item) JSONSchemaExtend(s *jsonschema.Schema) {
	if p, ok := s.Properties.Get("reasons"); ok && p.Items != nil {
		for _, r := range Reasons() {
			p.Items.Enum = append(p.Items.Enum, r)
		}
	}
}

func pinVersion(s *jsonschema.Schema) {
	if p, ok := s.Properties.Get("schema_version"); ok {
		p.Const = SchemaVersion
	}
}

// ID returns the stable item ID (design D6): the first 12 characters of the
// lower-case, unpadded base32 encoding of sha256(ruleID + "\x00" + rel),
// where rel is the item's home-relative slash path.
func ID(ruleID, rel string) string {
	sum := sha256.Sum256([]byte(ruleID + "\x00" + rel))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return strings.ToLower(enc[:12])
}

// Encode writes v as indented JSON followed by a newline.
func Encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Decode reads a plan, rejecting unknown fields, trailing data and other
// schema versions.
func Decode(r io.Reader) (Plan, error) {
	var p Plan
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Plan{}, fmt.Errorf("plan: %w", err)
	}
	if dec.More() {
		return Plan{}, fmt.Errorf("plan: trailing data after the document")
	}
	if p.SchemaVersion != SchemaVersion {
		return Plan{}, fmt.Errorf("plan: schema_version %d is not supported (want %d)", p.SchemaVersion, SchemaVersion)
	}
	return p, nil
}

// RulesDocument is the output of kipple rules --json.
type RulesDocument struct {
	SchemaVersion int          `json:"schema_version"`
	Home          string       `json:"home,omitempty" jsonschema:"description=The effective home whose config set the project roots."`
	Rules         []rules.Rule `json:"rules"`
}

// JSONSchemaExtend pins schema_version.
func (RulesDocument) JSONSchemaExtend(s *jsonschema.Schema) { pinVersion(s) }

// Schemas lists the documents kipple schema can print.
func Schemas() []string { return []string{"plan", "rules", "receipt", "result"} }

// Schema returns the JSON Schema for the named document.
func Schema(name string) ([]byte, error) {
	var v any
	switch name {
	case "plan":
		v = &Plan{}
	case "rules":
		v = &RulesDocument{}
	case "receipt":
		v = &Receipt{}
	case "result":
		v = &Result{}
	default:
		return nil, fmt.Errorf("unknown schema %q (want one of %s)", name, strings.Join(Schemas(), ", "))
	}
	r := &jsonschema.Reflector{}
	s := r.Reflect(v)
	s.ID = jsonschema.ID("https://github.com/rbutera/kipple/schema/v1/" + name + ".json")
	s.Title = "kipple " + name
	var buf bytes.Buffer
	if err := Encode(&buf, s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Normalize replaces nil slices with empty ones so the plan encodes arrays,
// never null, as the schema requires.
func (p *Plan) Normalize() {
	if p.Rules == nil {
		p.Rules = []RuleStatus{}
	}
	if p.Items == nil {
		p.Items = []Item{}
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
	for i := range p.Items {
		if p.Items[i].Reasons == nil {
			p.Items[i].Reasons = []string{}
		}
	}
}

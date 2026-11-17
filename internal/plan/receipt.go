package plan

import (
	"time"

	"github.com/invopop/jsonschema"
)

// TODO(wave 3): Receipt and Result are minimal shapes so that
// `kipple schema receipt|result` exists and validates. Wave 3 (internal/apply,
// internal/receipt) owns them and may extend them; adding fields is
// compatible, renaming or removing them bumps SchemaVersion.

// Receipt records one --apply run (specs/clean-apply "Receipts").
type Receipt struct {
	SchemaVersion int           `json:"schema_version"`
	KippleVersion string        `json:"kipple_version"`
	CreatedAt     time.Time     `json:"created_at"`
	Home          string        `json:"home"`
	PlanGenerated time.Time     `json:"plan_generated_at"`
	Moved         []MovedItem   `json:"moved"`
	Skipped       []SkippedItem `json:"skipped"`
}

// MovedItem is an item that went to the Trash (or was deleted with
// --permanent, in which case TrashPath is empty).
type MovedItem struct {
	ID        string `json:"id"`
	Rule      string `json:"rule"`
	Path      string `json:"path"`
	TrashPath string `json:"trash_path"`
	Bytes     int64  `json:"bytes"`
}

// SkippedItem is a selected item that was not moved, with a machine-readable
// reason (an item reason, a refused:<policy reason>, or an apply reason such
// as permanent_not_allowed).
type SkippedItem struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Result is the JSON output of kipple clean --apply.
type Result struct {
	SchemaVersion int           `json:"schema_version"`
	ReceiptPath   string        `json:"receipt_path"`
	Moved         []MovedItem   `json:"moved"`
	Skipped       []SkippedItem `json:"skipped"`
	Untouched     []string      `json:"untouched" jsonschema:"description=IDs of plan items that were not selected."`
}

// JSONSchemaExtend pins schema_version.
func (Receipt) JSONSchemaExtend(s *jsonschema.Schema) { pinVersion(s) }

// JSONSchemaExtend pins schema_version.
func (Result) JSONSchemaExtend(s *jsonschema.Schema) { pinVersion(s) }

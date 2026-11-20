package plan

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/invopop/jsonschema"
)

// Skip reasons that only apply and restore produce, in addition to the item
// reasons (an item that is no longer selectable is skipped with the reason
// a scan would give it, such as git_dirty). A closed set; see SkipReasons.
const (
	// SkipNotFound: an ID passed to --only is not in the plan, or a fresh
	// scan no longer produces that item.
	SkipNotFound = "not_found"
	// SkipInfoTier: an info-tier item was named; kipple never moves those.
	SkipInfoTier = "info_tier"
	// SkipPermanentNotAllowed: --permanent acts only on safe-tier items.
	SkipPermanentNotAllowed = "permanent_not_allowed"
	// SkipToolRunning: an agent process (claude, codex, cursor-agent) has
	// its working directory inside the project.
	SkipToolRunning = "tool_running"
	// SkipIdentityChanged: the item's (dev, ino) differs from the plan, or
	// the object changed while it was being moved.
	SkipIdentityChanged = "identity_changed"
	// SkipRootChanged: the rule root's (dev, ino) differs from the plan.
	SkipRootChanged = "root_changed"
	// SkipParentChanged: immediately before the move, the verified parent
	// directory was no longer at the planned path.
	SkipParentChanged = "parent_changed"
	// SkipDifferentDevice: the item and the Trash (or, for restore, the
	// destination) are on different devices; kipple never copies instead.
	SkipDifferentDevice = "different_device"
	// SkipTrashChanged: the Trash folder no longer names the directory
	// verified when the run started.
	SkipTrashChanged = "trash_changed"
	// SkipPermissionDenied: the system refused the move (EPERM, EACCES,
	// for example a SIP- or TCC-protected folder). kipple never elevates.
	SkipPermissionDenied = "permission_denied"
	// SkipMoveFailed: the move failed for another reason (see detail).
	SkipMoveFailed = "move_failed"
	// SkipDestinationExists: restore found something at the original path.
	SkipDestinationExists = "destination_exists"
	// SkipTrashItemMissing: restore found nothing at the recorded Trash path.
	SkipTrashItemMissing = "trash_item_missing"
	// SkipTrashItemChanged: the object at the recorded Trash path is not the
	// one kipple moved there.
	SkipTrashItemChanged = "trash_item_changed"
	// SkipNotInTrash: the receipt's Trash path is not directly inside the
	// Trash folder kipple uses (receipts are untrusted input).
	SkipNotInTrash = "not_in_trash"
	// SkipAlreadyRestored: the receipt says the item was already restored.
	SkipAlreadyRestored = "already_restored"
	// SkipDeletedPermanently: the item was deleted with --permanent, so
	// there is nothing to restore.
	SkipDeletedPermanently = "deleted_permanently"
)

var skipOnly = []string{
	SkipNotFound, SkipInfoTier, SkipPermanentNotAllowed, SkipToolRunning,
	SkipIdentityChanged, SkipRootChanged, SkipParentChanged, SkipDifferentDevice,
	SkipTrashChanged, SkipPermissionDenied, SkipMoveFailed,
	SkipDestinationExists, SkipTrashItemMissing, SkipTrashItemChanged,
	SkipNotInTrash, SkipAlreadyRestored, SkipDeletedPermanently,
}

// SkipReasons returns every reason a skipped entry can carry: the item
// reasons (Reasons) and the apply and restore reasons above.
func SkipReasons() []string {
	return append(Reasons(), skipOnly...)
}

// Receipt records one --apply run (specs/clean-apply "Receipts"). Apply
// writes it before the first move and rewrites it after every item, so a
// run that is interrupted still leaves a record of what moved.
type Receipt struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id" jsonschema:"pattern=^[0-9]{8}T[0-9]{6}Z-[a-z0-9]{6}$,description=Receipt ID; the file is <id>.json in the receipts folder."`
	KippleVersion string    `json:"kipple_version"`
	CreatedAt     time.Time `json:"created_at"`
	// Complete is false while the run is in progress, and stays false if it
	// was interrupted.
	Complete      bool          `json:"complete"`
	Home          string        `json:"home"`
	Level         string        `json:"level" jsonschema:"enum=safe,enum=review"`
	Permanent     bool          `json:"permanent" jsonschema:"description=The run used --permanent: safe items were deleted, not moved to the Trash."`
	TrashDir      string        `json:"trash_dir" jsonschema:"description=The Trash folder items were moved into."`
	PlanGenerated time.Time     `json:"plan_generated_at"`
	Moved         []MovedItem   `json:"moved"`
	Skipped       []SkippedItem `json:"skipped"`
}

// MovedItem is an item that went to the Trash, or was deleted with
// --permanent (then TrashPath is empty and Permanent is set).
type MovedItem struct {
	ID        string `json:"id"`
	Rule      string `json:"rule"`
	Path      string `json:"path" jsonschema:"description=The original absolute path."`
	TrashPath string `json:"trash_path" jsonschema:"description=Where the item is in the Trash; empty when deleted permanently."`
	Bytes     int64  `json:"bytes" jsonschema:"description=Allocated bytes measured immediately before the move."`
	// ReclaimableBytes excludes files with other hard links, which moving
	// this item does not free.
	ReclaimableBytes int64 `json:"reclaimable_bytes" jsonschema:"description=Allocated bytes excluding files with other hard links: what emptying the Trash frees."`
	// Dev and Ino identify the moved object; a rename keeps them, so
	// restore checks the Trash entry is the same object.
	Dev        uint64     `json:"dev"`
	Ino        uint64     `json:"ino"`
	MovedAt    time.Time  `json:"moved_at"`
	Permanent  bool       `json:"permanent,omitempty"`
	RestoredAt *time.Time `json:"restored_at,omitempty" jsonschema:"description=Set when kipple restore put the item back."`
}

// SkippedItem is a selected item that was not moved (or, in a restore
// result, not restored), with a machine-readable reason.
type SkippedItem struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	// Partial is set when part of the item was already removed before the
	// run stopped on it (a --permanent delete that met something protected
	// or failed partway). RemovedCount and RemovedBytes say how much.
	Partial      bool  `json:"partial,omitempty"`
	RemovedCount int64 `json:"removed_count,omitempty" jsonschema:"description=Entries (files, links and folders) already deleted when a partial delete stopped."`
	RemovedBytes int64 `json:"removed_bytes,omitempty" jsonschema:"description=Allocated bytes already deleted when a partial delete stopped."`
}

// RestoredItem is an item kipple restore moved back (or, in a dry run,
// would move back).
type RestoredItem struct {
	ID        string `json:"id"`
	Rule      string `json:"rule"`
	Path      string `json:"path"`
	TrashPath string `json:"trash_path"`
	Bytes     int64  `json:"bytes"`
}

// Result is the JSON output of kipple clean --apply and kipple restore.
type Result struct {
	SchemaVersion int    `json:"schema_version"`
	Command       string `json:"command" jsonschema:"enum=clean,enum=restore"`
	// DryRun is set for kipple restore without --apply: restored then lists
	// what would be put back, and nothing moved.
	DryRun      bool           `json:"dry_run"`
	Home        string         `json:"home"`
	ReceiptID   string         `json:"receipt_id,omitempty"`
	ReceiptPath string         `json:"receipt_path,omitempty"`
	Moved       []MovedItem    `json:"moved"`
	Restored    []RestoredItem `json:"restored"`
	Skipped     []SkippedItem  `json:"skipped"`
	Untouched   []string       `json:"untouched" jsonschema:"description=IDs of plan items that were not selected and were left alone."`
	Warnings    []string       `json:"warnings"`
}

// Normalize replaces nil slices with empty ones.
func (r *Result) Normalize() {
	if r.Moved == nil {
		r.Moved = []MovedItem{}
	}
	if r.Restored == nil {
		r.Restored = []RestoredItem{}
	}
	if r.Skipped == nil {
		r.Skipped = []SkippedItem{}
	}
	if r.Untouched == nil {
		r.Untouched = []string{}
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
}

// Normalize replaces nil slices with empty ones.
func (r *Receipt) Normalize() {
	if r.Moved == nil {
		r.Moved = []MovedItem{}
	}
	if r.Skipped == nil {
		r.Skipped = []SkippedItem{}
	}
}

// DecodeReceipt reads a receipt of at most MaxDocumentSize bytes, rejecting
// unknown fields, trailing data and other schema versions. Receipts are
// untrusted input: restore re-checks every path in them.
func DecodeReceipt(r io.Reader) (Receipt, error) {
	var rc Receipt
	lr := &io.LimitedReader{R: r, N: MaxDocumentSize + 1}
	dec := json.NewDecoder(lr)
	dec.DisallowUnknownFields()
	err := dec.Decode(&rc)
	more := err == nil && dec.More()
	if lr.N <= 0 {
		return Receipt{}, fmt.Errorf("receipt: document larger than %d bytes", MaxDocumentSize)
	}
	if err != nil {
		return Receipt{}, fmt.Errorf("receipt: %w", err)
	}
	if more {
		return Receipt{}, fmt.Errorf("receipt: trailing data after the document")
	}
	if rc.SchemaVersion != SchemaVersion {
		return Receipt{}, fmt.Errorf("receipt: schema_version %d is not supported (want %d)", rc.SchemaVersion, SchemaVersion)
	}
	rc.Normalize()
	return rc, nil
}

// JSONSchemaExtend pins schema_version.
func (Receipt) JSONSchemaExtend(s *jsonschema.Schema) { pinVersion(s) }

// JSONSchemaExtend pins schema_version.
func (Result) JSONSchemaExtend(s *jsonschema.Schema) { pinVersion(s) }

// JSONSchemaExtend closes the reason enumeration.
func (SkippedItem) JSONSchemaExtend(s *jsonschema.Schema) {
	if p, ok := s.Properties.Get("reason"); ok {
		for _, r := range SkipReasons() {
			p.Enum = append(p.Enum, r)
		}
	}
}

// History is the JSON output of kipple history: every receipt, newest first.
type History struct {
	SchemaVersion int       `json:"schema_version"`
	Home          string    `json:"home"`
	ReceiptsDir   string    `json:"receipts_dir"`
	Receipts      []Receipt `json:"receipts"`
	Warnings      []string  `json:"warnings" jsonschema:"description=Receipt files that could not be read."`
}

// JSONSchemaExtend pins schema_version.
func (History) JSONSchemaExtend(s *jsonschema.Schema) { pinVersion(s) }

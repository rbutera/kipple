package output

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rbutera/kipple/internal/plan"
)

// DryRunLine ends the human output of kipple clean without --apply.
const DryRunLine = "This was a dry run: nothing was moved. Add --apply to move the selected items to the Trash."

// CleanResult writes what kipple clean --apply did.
func CleanResult(w io.Writer, r plan.Result) error {
	b := &strings.Builder{}
	var bytes, reclaim int64
	deleted := 0
	for _, m := range r.Moved {
		bytes += m.Bytes
		reclaim += m.ReclaimableBytes
		if m.Permanent {
			deleted++
		}
	}
	switch {
	case r.DryRun:
		fmt.Fprintf(b, "Would move %s to the Trash (%s), after every check apply runs.\n", count(len(r.Moved), "item"), Bytes(bytes))
		for _, m := range r.Moved {
			fmt.Fprintf(b, "  would move  %s  %s\n", m.ID, m.Path)
		}
		writeSkipped(b, r.Skipped, "Would skip")
		writeWarnings(b, r.Warnings)
		b.WriteString(DryRunLine + "\n")
		_, err := io.WriteString(w, b.String())
		return err
	case len(r.Moved) == 0 && len(r.Skipped) == 0:
		b.WriteString("Nothing was selected, so nothing was moved.\n")
	case deleted == len(r.Moved) && deleted > 0:
		fmt.Fprintf(b, "Deleted %s permanently (%s, %s freed).\n", count(len(r.Moved), "item"), Bytes(bytes), Bytes(reclaim))
	default:
		fmt.Fprintf(b, "Moved %s to the Trash (%s; emptying the Trash frees %s).\n", count(len(r.Moved), "item"), Bytes(bytes), Bytes(reclaim))
	}
	for _, m := range r.Moved {
		if m.Permanent {
			fmt.Fprintf(b, "  deleted  %s  %s\n", m.ID, m.Path)
		} else {
			fmt.Fprintf(b, "  moved    %s  %s\n      -> %s\n", m.ID, m.Path, m.TrashPath)
		}
	}
	writeSkipped(b, r.Skipped, "Skipped")
	if len(r.Displaced) > 0 {
		fmt.Fprintf(b, "Moved to the Trash instead of the approved item (another program swapped it in): %s. kipple restore puts it back at the item's path when that path is free; Finder's Put Back may return it to where it came from.\n", count(len(r.Displaced), "object"))
		for _, d := range r.Displaced {
			fmt.Fprintf(b, "  %s  taken from %s\n      -> %s\n", d.ID, d.Path, d.TrashPath)
		}
	}
	writeWarnings(b, r.Warnings)
	if r.ReceiptID != "" {
		fmt.Fprintf(b, "\nReceipt %s (%s).\n", r.ReceiptID, r.ReceiptPath)
		if len(r.Moved) > deleted || len(r.Displaced) > 0 {
			fmt.Fprintf(b, "To put the items back: kipple restore %s --apply\n", r.ReceiptID)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// RestoreResult writes what kipple restore did, or would do in a dry run.
func RestoreResult(w io.Writer, r plan.Result) error {
	b := &strings.Builder{}
	verb := "Restored"
	if r.DryRun {
		verb = "Would restore"
	}
	kinds := map[string]int{}
	for _, it := range r.Restored {
		k := it.Kind
		if k == "" {
			k = "moved"
		}
		kinds[k]++
	}
	fmt.Fprintf(b, "Receipt %s\n%s %s (moved %d, pending %d, displaced %d), skipped %d:\n",
		r.ReceiptID, verb, count(len(r.Restored), "item"), kinds["moved"], kinds["pending"], kinds["displaced"], len(r.Skipped))
	for _, it := range r.Restored {
		fmt.Fprintf(b, "  %s  %s\n      <- %s\n", it.ID, it.Path, it.TrashPath)
	}
	writeSkipped(b, r.Skipped, "Skipped")
	writeWarnings(b, r.Warnings)
	if r.DryRun {
		fmt.Fprintf(b, "\nThis was a dry run: nothing was moved. Add --apply to put the items back: kipple restore %s --apply\n", r.ReceiptID)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// History writes one line per receipt, newest first.
func History(w io.Writer, h plan.History) error {
	b := &strings.Builder{}
	if len(h.Receipts) == 0 {
		b.WriteString("Nothing has been cleaned yet.\n")
	}
	for _, r := range h.Receipts {
		var bytes int64
		restored := 0
		for _, m := range r.Moved {
			bytes += m.Bytes
			if m.RestoredAt != nil {
				restored++
			}
		}
		fmt.Fprintf(b, "%s  %s  moved %d (%s), pending %d, displaced %d, skipped %d", r.ID, r.CreatedAt.Local().Format(time.DateTime), len(r.Moved), Bytes(bytes), len(r.Pending), len(r.Displaced), len(r.Skipped))
		if restored > 0 {
			fmt.Fprintf(b, ", restored %d", restored)
		}
		if r.Permanent {
			b.WriteString(", permanent")
		}
		if !r.Complete {
			b.WriteString(", interrupted")
		}
		b.WriteString("\n")
	}
	writeWarnings(b, h.Warnings)
	_, err := io.WriteString(w, b.String())
	return err
}

func writeSkipped(b *strings.Builder, skipped []plan.SkippedItem, title string) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintf(b, "%s %s:\n", title, count(len(skipped), "item"))
	for _, s := range skipped {
		path := s.Path
		if path == "" {
			path = "(unknown item)"
		}
		fmt.Fprintf(b, "  %s  %s  %s\n", s.Reason, s.ID, path)
		if s.Detail != "" {
			fmt.Fprintf(b, "      %s\n", s.Detail)
		}
		if s.Partial {
			fmt.Fprintf(b, "      partly deleted already: %d entries, %s; the rest is still there\n", s.RemovedCount, Bytes(s.RemovedBytes))
		}
	}
}

func writeWarnings(b *strings.Builder, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	b.WriteString("Warnings:\n")
	for _, w := range warnings {
		fmt.Fprintf(b, "  %s\n", w)
	}
}

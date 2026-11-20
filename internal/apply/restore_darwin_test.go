//go:build darwin

package apply

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
)

// Should-fix 9: if the destination is swapped right after a restore's
// rename, nothing else is moved and the receipt records what happened.
func TestRestoreNotesAReplacedDestination(t *testing.T) {
	f, e, _ := cleaned(t)
	m := e.Receipt.Moved[1] // Library/Logs/app
	if err := os.MkdirAll(f.abs("decoy"), 0o755); err != nil {
		t.Fatal(err)
	}
	afterRestoreRename = func() {
		_ = unix.RenameatxNp(unix.AT_FDCWD, m.Path, unix.AT_FDCWD, f.abs("decoy"), unix.RENAME_SWAP)
	}
	defer func() { afterRestoreRename = nil }()
	res := f.restore(e, true, m.ID)
	if len(res.Restored) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipTrashItemMismatch {
		t.Fatalf("%+v", res)
	}
	after, err := f.store.Load(e.Receipt.ID)
	if err != nil || after.Receipt.Moved[1].RestoreNote == "" {
		t.Fatalf("receipt not annotated: %+v %v", after.Receipt.Moved[1], err)
	}
}

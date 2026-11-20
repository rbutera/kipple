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

// precious makes a folder outside every rule with a file in it.
func precious(t *testing.T, f *fx) string {
	t.Helper()
	p := f.abs("precious")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+"/thesis.txt", []byte("irreplaceable"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func swapPaths(t *testing.T, a, b string) {
	t.Helper()
	if err := unix.RenameatxNp(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_SWAP); err != nil {
		t.Fatalf("RENAME_SWAP: %v", err)
	}
}

// Must-fix 3 (F3): a foreign folder swapped in at the Trash name right
// before the restore's move is caught at the staging name and put back in
// the Trash; it never reaches the destination.
func TestRestoreSwapAtTheTrashName(t *testing.T) {
	f, e, _ := cleaned(t)
	m := e.Receipt.Moved[0]
	pr := precious(t, f)
	beforeStage = func() { swapPaths(t, m.TrashPath, pr) }
	defer func() { beforeStage = nil }()
	res := f.restore(e, true, m.ID)
	if len(res.Restored) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Detail != restoreSwapNote {
		t.Fatalf("%+v", res)
	}
	if exists(m.Path) {
		t.Fatal("something reached the destination")
	}
	if b, err := os.ReadFile(m.TrashPath + "/thesis.txt"); err != nil || string(b) != "irreplaceable" {
		t.Fatal("the foreign folder is not back at the Trash name")
	}
	after, _ := f.store.Load(e.Receipt.ID)
	if after.Receipt.Moved[0].RestoreNote != restoreSwapNote {
		t.Fatalf("receipt note %q", after.Receipt.Moved[0].RestoreNote)
	}
}

// A swap at the staging name after it was verified: the foreign object
// that reaches the destination is renamed straight back into the Trash.
func TestRestoreSwapAtTheStagingName(t *testing.T) {
	f, e, _ := cleaned(t)
	m := e.Receipt.Moved[0]
	pr := precious(t, f)
	afterStage = func() {
		entries, _ := os.ReadDir(f.abs(".Trash"))
		for _, de := range entries {
			if len(de.Name()) > 16 && de.Name()[:16] == ".kipple-restore-" {
				swapPaths(t, f.abs(".Trash/"+de.Name()), pr)
				return
			}
		}
		t.Error("no staging entry found")
	}
	defer func() { afterStage = nil }()
	res := f.restore(e, true, m.ID)
	if len(res.Restored) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Detail != restoreSwapNote {
		t.Fatalf("%+v", res)
	}
	if exists(m.Path) {
		t.Fatal("the foreign folder stayed at the destination")
	}
	found := false
	entries, _ := os.ReadDir(f.abs(".Trash"))
	for _, de := range entries {
		if b, err := os.ReadFile(f.abs(".Trash/" + de.Name() + "/thesis.txt")); err == nil && string(b) == "irreplaceable" {
			found = true
		}
	}
	if !found {
		t.Fatal("the foreign folder was not put back in the Trash")
	}
}

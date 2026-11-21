//go:build linux

package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

// pipCleaned moves Library/Caches/pip/http (and its sibling) to the
// fixture's freedesktop Trash and returns the receipt and the http item.
func pipCleaned(t *testing.T) (*fx, receipt.Entry, plan.MovedItem) {
	t.Helper()
	f := newFx(t, pipFixture())
	res := f.run(f.opts(f.scan("")))
	m, ok := movedFor(res, f.abs("Library/Caches/pip/http"))
	if !ok || m.TrashPath != f.abs(".local/share/Trash/files/http") {
		t.Fatalf("fixture clean: %+v", res)
	}
	e, err := f.store.Load(res.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	return f, e, m
}

// specs/history-restore scenarios on Linux.
func TestFreedesktopRestore(t *testing.T) {
	t.Run("Restore removes the info file", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		res := f.restore(e, true, m.ID)
		if len(res.Restored) != 1 || len(res.Warnings) != 0 || !exists(m.Path) || exists(m.TrashPath) {
			t.Fatalf("%+v", res)
		}
		if exists(f.abs(".local/share/Trash/info/http.trashinfo")) {
			t.Fatal("the info file was not removed")
		}
		got, err := f.store.Load(e.Receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, mm := range got.Receipt.Moved {
			if mm.ID == m.ID && mm.RestoredAt == nil {
				t.Fatal("the receipt was not annotated")
			}
		}
		// The other item's info file is untouched.
		if !exists(f.abs(".local/share/Trash/info/com.example.a.trashinfo")) {
			t.Fatal("another item's info file was removed")
		}
	})
	t.Run("Info file describes something else", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		info := f.abs(".local/share/Trash/info/http.trashinfo")
		forged := "[Trash Info]\nPath=/somewhere/else/http\nDeletionDate=2026-11-21T10:30:00\n"
		if err := os.WriteFile(info, []byte(forged), 0o600); err != nil {
			t.Fatal(err)
		}
		res := f.restore(e, true, m.ID)
		if len(res.Restored) != 1 || !exists(m.Path) {
			t.Fatalf("%+v", res)
		}
		if b, _ := os.ReadFile(info); string(b) != forged {
			t.Fatal("an info file describing something else was removed or changed")
		}
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], info) || !strings.Contains(res.Warnings[0], "/somewhere/else/http") {
			t.Fatalf("warnings %q", res.Warnings)
		}
	})
	t.Run("Info file that is a symlink is left", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		info := f.abs(".local/share/Trash/info/http.trashinfo")
		target := f.abs("decoy.trashinfo")
		if err := os.Rename(info, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, info); err != nil {
			t.Fatal(err)
		}
		res := f.restore(e, true, m.ID)
		if len(res.Restored) != 1 || len(res.Warnings) != 1 || !exists(target) {
			t.Fatalf("%+v", res)
		}
		if fi, err := os.Lstat(info); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatal("the symlinked info file was removed")
		}
	})
	t.Run("Restore dry run on Linux", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		before := testutil.Checksum(t, f.home)
		res := f.restore(e, false)
		if len(res.Restored) != 2 || !res.DryRun || exists(m.Path) {
			t.Fatalf("%+v", res)
		}
		if testutil.Checksum(t, f.home) != before {
			t.Fatal("a restore dry run changed the fixture or its Trash")
		}
	})
	t.Run("Restored in the file manager first", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		// What GNOME's and KDE's Restore do: rename the entry back and
		// remove its info file.
		if err := os.Rename(m.TrashPath, m.Path); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(f.abs(".local/share/Trash/info/http.trashinfo")); err != nil {
			t.Fatal(err)
		}
		before := testutil.Checksum(t, f.home)
		res := f.restore(e, true, m.ID)
		if len(res.Restored) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipAlreadyRestored {
			t.Fatalf("%+v", res)
		}
		if testutil.Checksum(t, f.home) != before {
			t.Fatal("something moved")
		}
	})
	t.Run("An untagged object at the original path is not already restored", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		if err := os.RemoveAll(m.TrashPath); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(m.Path, 0o755); err != nil {
			t.Fatal(err)
		}
		res := f.restore(e, true, m.ID)
		if len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipTrashItemMissing {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("Trash emptied in the file manager", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		for _, d := range []string{"files", "info"} {
			entries, _ := os.ReadDir(f.abs(".local/share/Trash/" + d))
			for _, de := range entries {
				if err := os.RemoveAll(filepath.Join(f.abs(".local/share/Trash/"+d), de.Name())); err != nil {
					t.Fatal(err)
				}
			}
		}
		res := f.restore(e, true, m.ID)
		if len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipTrashItemMissing {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("Entry renamed in the Trash: the info file of the name found is removed", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		renamed := f.abs(".local/share/Trash/files/http renamed")
		if err := os.Rename(m.TrashPath, renamed); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(f.abs(".local/share/Trash/info/http.trashinfo"), f.abs(".local/share/Trash/info/http renamed.trashinfo")); err != nil {
			t.Fatal(err)
		}
		res := f.restore(e, true, m.ID)
		if len(res.Restored) != 1 || len(res.Warnings) != 0 || !exists(m.Path) {
			t.Fatalf("%+v", res)
		}
		if exists(f.abs(".local/share/Trash/info/http renamed.trashinfo")) {
			t.Fatal("the info file of the name it was found under was not removed")
		}
	})
	t.Run("An info file kipple did not write is left", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		if m.InfoIno == 0 {
			t.Fatal("the receipt does not record the info file's inode")
		}
		info := f.abs(".local/share/Trash/info/http.trashinfo")
		b, err := os.ReadFile(info)
		if err != nil {
			t.Fatal(err)
		}
		// Same name, same Path, another file (another inode). It is
		// written under a temporary name first and renamed over the
		// original, so it cannot get the original's inode number back:
		// ext4 and xfs reuse a freed inode number at once, and the recorded
		// inode alone cannot tell such a recreated file apart (a limit of
		// the check; the decoded Path is checked too).
		tmp := info + ".new"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, info); err != nil {
			t.Fatal(err)
		}
		res := f.restore(e, true, m.ID)
		if len(res.Restored) != 1 || len(res.Warnings) != 1 || !exists(info) {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("Entry renamed in the Trash: its own info file is still found", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		if err := os.Rename(m.TrashPath, f.abs(".local/share/Trash/files/http renamed")); err != nil {
			t.Fatal(err)
		}
		res := f.restore(e, true, m.ID)
		if len(res.Restored) != 1 || len(res.Warnings) != 0 || !exists(m.Path) {
			t.Fatalf("%+v", res)
		}
		if exists(f.abs(".local/share/Trash/info/http.trashinfo")) {
			t.Fatal("the info file kipple wrote was not removed")
		}
	})
	t.Run("Another entry's info under the recorded name is left", func(t *testing.T) {
		f, e, m := pipCleaned(t)
		// The entry is renamed and another item takes its old name, with
		// an info file of the same Path (the cache came back and was
		// trashed again from the file manager).
		if err := os.Rename(m.TrashPath, f.abs(".local/share/Trash/files/http renamed")); err != nil {
			t.Fatal(err)
		}
		info := f.abs(".local/share/Trash/info/http.trashinfo")
		if err := os.Rename(info, f.abs(".local/share/Trash/info/http renamed.trashinfo")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(m.TrashPath, 0o700); err != nil {
			t.Fatal(err)
		}
		other := "[Trash Info]\nPath=" + m.Path + "\nDeletionDate=2026-11-21T10:30:00\n"
		if err := os.WriteFile(info, []byte(other), 0o600); err != nil {
			t.Fatal(err)
		}
		res := f.restore(e, true, m.ID)
		if len(res.Restored) != 1 {
			t.Fatalf("%+v", res)
		}
		if b, _ := os.ReadFile(info); string(b) != other {
			t.Fatal("the other entry's info file was removed")
		}
		if exists(f.abs(".local/share/Trash/info/http renamed.trashinfo")) {
			t.Fatal("the restored entry's own info file was left")
		}
	})
	t.Run("Missing Trash is an error, never created", func(t *testing.T) {
		f, e, _ := pipCleaned(t)
		if err := os.Rename(f.abs(".local/share/Trash"), f.abs("moved-trash")); err != nil {
			t.Fatal(err)
		}
		if err := f.store.Save(e.Receipt); err != nil {
			t.Fatal(err)
		}
		store := f.store
		_, err := Restore(context.Background(), RestoreOptions{Env: f.env, Rules: rules.Catalogue(), Receipt: e, Store: &store, Apply: true})
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("err = %v, want the missing Trash reported", err)
		}
		if exists(f.abs(".local/share/Trash")) {
			t.Fatal("restore created the Trash")
		}
	})
}

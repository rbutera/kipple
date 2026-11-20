package apply

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/trash"
)

// cleaned runs a clean of threeSafe and returns the fixture, the receipt,
// and each item's checksum before the clean.
func cleaned(t *testing.T) (*fx, receipt.Entry, map[string]string) {
	t.Helper()
	f := newFx(t, threeSafe())
	p := f.scan("")
	sums := map[string]string{}
	for _, it := range p.Items {
		sums[it.Path] = testutil.Checksum(t, it.Path)
	}
	res := f.run(f.opts(p))
	if len(res.Moved) != 3 {
		t.Fatalf("fixture clean moved %d", len(res.Moved))
	}
	e, err := f.store.Load(res.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	return f, e, sums
}

func (f *fx) restore(e receipt.Entry, apply bool, only ...string) plan.Result {
	f.t.Helper()
	store := f.store
	res, err := Restore(context.Background(), RestoreOptions{Env: f.env, Rules: rules.Catalogue(), Receipt: e, Store: &store, Only: only, Apply: apply, Now: func() time.Time { return now }})
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

// Spec scenario "Restore to vacant path" (history-restore), plus the dry
// run that precedes it.
func TestRestoreToVacantPath(t *testing.T) {
	f, e, sums := cleaned(t)
	dry := f.restore(e, false)
	if !dry.DryRun || len(dry.Restored) != 3 || len(dry.Skipped) != 0 {
		t.Fatalf("dry run %+v", dry)
	}
	for _, m := range e.Receipt.Moved {
		if exists(m.Path) || !exists(m.TrashPath) {
			t.Fatalf("the dry run moved %s", m.Path)
		}
	}
	res := f.restore(e, true)
	if len(res.Restored) != 3 || ExitCode(res) != ExitOK {
		t.Fatalf("%+v", res)
	}
	for path, sum := range sums {
		if !exists(path) || testutil.Checksum(t, path) != sum {
			t.Errorf("%s not restored intact", path)
		}
	}
	after, err := f.store.Load(e.Receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range after.Receipt.Moved {
		if m.RestoredAt == nil {
			t.Errorf("%s not annotated as restored", m.Path)
		}
	}
	again := f.restore(after, true)
	if len(again.Restored) != 0 || len(again.Skipped) != 3 || again.Skipped[0].Reason != plan.SkipAlreadyRestored {
		t.Fatalf("second restore %+v", again)
	}
}

// Spec scenario "Original path occupied" (history-restore).
func TestRestoreOriginalPathOccupied(t *testing.T) {
	f, e, _ := cleaned(t)
	target := e.Receipt.Moved[0]
	if err := os.MkdirAll(target.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	res := f.restore(e, true, target.ID)
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipDestinationExists || ExitCode(res) != ExitPartial {
		t.Fatalf("%+v", res)
	}
	if !exists(target.TrashPath) {
		t.Fatal("the Trash item moved")
	}
}

// Spec scenario "Trash emptied" (history-restore).
func TestRestoreTrashEmptied(t *testing.T) {
	f, e, _ := cleaned(t)
	target := e.Receipt.Moved[1]
	if err := os.RemoveAll(target.TrashPath); err != nil {
		t.Fatal(err)
	}
	res := f.restore(e, true, target.ID)
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipTrashItemMissing {
		t.Fatalf("%+v", res)
	}
}

// Receipts are untrusted: every path is re-authorised, the Trash entry must
// be the very object kipple moved, and the receipt must be for this home.
func TestRestoreUntrustedReceipt(t *testing.T) {
	f, e, _ := cleaned(t)
	if err := os.MkdirAll(f.abs("Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := e
	bad.Receipt.Moved = append([]plan.MovedItem(nil), e.Receipt.Moved...)
	bad.Receipt.Moved[0].Path = f.abs("Documents/stolen")
	bad.Receipt.Moved[1].TrashPath = f.abs("Library/Caches/elsewhere")
	bad.Receipt.Moved[2].Ino++
	res := f.restore(bad, true)
	want := map[string]string{
		bad.Receipt.Moved[0].ID: plan.SkipNotFound, // its ID no longer matches the rule and path
		bad.Receipt.Moved[1].ID: plan.SkipNotInTrash,
		bad.Receipt.Moved[2].ID: plan.SkipTrashItemMismatch,
	}
	for _, s := range res.Skipped {
		if want[s.ID] != s.Reason {
			t.Errorf("%s: %s, want %s", s.ID, s.Reason, want[s.ID])
		}
	}
	if len(res.Restored) != 0 || exists(f.abs("Documents/stolen")) {
		t.Fatalf("restored %+v", res.Restored)
	}
	other := e
	other.Receipt.Home = "/somewhere/else"
	store := f.store
	if _, err := Restore(context.Background(), RestoreOptions{Env: f.env, Rules: rules.Catalogue(), Receipt: other, Store: &store, Apply: true}); err == nil {
		t.Fatal("restored a receipt for another home")
	}
}

// The destination's parent swapped for a symlink just before the move is
// refused by the parent re-check.
func TestRestoreParentSwapped(t *testing.T) {
	f, e, _ := cleaned(t)
	target := e.Receipt.Moved[1] // Library/Logs/app
	store := f.store
	o := RestoreOptions{Env: f.env, Rules: rules.Catalogue(), Receipt: e, Store: &store, Only: []string{target.ID}, Apply: true}
	o.beforeMove = func(plan.MovedItem) {
		if err := os.Rename(f.abs("Library/Logs"), f.abs("Library/Logs.real")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(f.abs("decoy"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(f.abs("decoy"), f.abs("Library/Logs")); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Restore(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipParentChanged || !exists(target.TrashPath) {
		t.Fatalf("%+v", res)
	}
	if entries, _ := os.ReadDir(f.abs("decoy")); len(entries) != 0 {
		t.Fatal("restored through the swapped parent")
	}
}

// Red-team finding: a forged receipt cannot resurrect a Trash entry kipple
// never created. The entry must match the receipt's dev, ino, size and
// mtime, read through the verified Trash descriptor.
func TestRestoreForgedReceiptForeignTrashEntry(t *testing.T) {
	f, e, _ := cleaned(t)
	foreign := f.abs(".Trash/foreign-secret")
	if err := os.WriteFile(foreign, []byte("not kipple's"), 0o600); err != nil {
		t.Fatal(err)
	}
	forged := e
	forged.Receipt.Moved = append([]plan.MovedItem(nil), e.Receipt.Moved...)
	forged.Receipt.Moved[0].TrashPath = foreign
	stale := forged.Receipt.Moved[1]
	stale.Mtime = stale.Mtime.Add(time.Second)
	forged.Receipt.Moved[1] = stale
	res := f.restore(forged, true, forged.Receipt.Moved[0].ID, stale.ID)
	if len(res.Restored) != 0 || len(res.Skipped) != 2 {
		t.Fatalf("%+v", res)
	}
	for _, s := range res.Skipped {
		if s.Reason != plan.SkipTrashItemMismatch {
			t.Errorf("%s: %s, want %s", s.ID, s.Reason, plan.SkipTrashItemMismatch)
		}
	}
	if !exists(foreign) || exists(forged.Receipt.Moved[0].Path) {
		t.Fatal("the foreign Trash entry was moved")
	}
}

// Must-fix 2: without Full Disk Access the Trash folder cannot be opened;
// restore never falls back to paths, so it fails with ErrTrashAccess and
// moves nothing.
func TestRestoreWithoutTrashDescriptor(t *testing.T) {
	f, e, _ := cleaned(t)
	store := f.store
	o := RestoreOptions{Env: f.env, Rules: rules.Catalogue(), Receipt: e, Store: &store, Apply: true,
		openTrash: func() (*trash.DirTrasher, error) {
			return nil, &fs.PathError{Op: "open", Path: f.abs(".Trash"), Err: syscall.EPERM}
		}}
	res, err := Restore(context.Background(), o)
	if !errors.Is(err, ErrTrashAccess) || len(res.Restored) != 0 {
		t.Fatalf("err %v result %+v", err, res)
	}
	for _, m := range e.Receipt.Moved {
		if !exists(m.TrashPath) || exists(m.Path) {
			t.Fatalf("%s moved", m.Path)
		}
	}
}

// A Trash folder that cannot be opened (here mode 000) is ErrTrashAccess.
func TestRestoreTrashAccessDenied(t *testing.T) {
	f, e, _ := cleaned(t)
	if err := os.Chmod(f.abs(".Trash"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.abs(".Trash"), 0o700) })
	store := f.store
	res, err := Restore(context.Background(), RestoreOptions{Env: f.env, Rules: rules.Catalogue(), Receipt: e, Store: &store, Apply: true})
	if !errors.Is(err, ErrTrashAccess) || len(res.Restored) != 0 {
		t.Fatalf("err %v result %+v", err, res)
	}
}

// Must-fix 3a: a forged receipt that copies a foreign Trash entry's dev,
// ino, size and mtime is refused: the entry lacks kipple's tag.
func TestRestoreForgedReceiptCopiedIdentity(t *testing.T) {
	f, e, _ := cleaned(t)
	foreign := f.abs(".Trash/foreign-secret")
	if err := os.WriteFile(foreign, []byte("not kipple's"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(foreign)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	forged := e
	forged.Receipt.Moved = append([]plan.MovedItem(nil), e.Receipt.Moved...)
	m := &forged.Receipt.Moved[0]
	m.TrashPath, m.Dev, m.Ino, m.Size = foreign, uint64(st.Dev), st.Ino, st.Size
	m.Mtime = fi.ModTime().UTC()
	res := f.restore(forged, true, m.ID)
	if len(res.Restored) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipTrashItemMismatch || !exists(foreign) {
		t.Fatalf("%+v", res)
	}
}

// Must-fix 3b: a Trash entry with something protected inside it is never
// restored, even when its identity and tag match (a .env planted in a
// subfolder leaves the entry's own size and mtime unchanged).
func TestRestoreRefusesProtectedContents(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Files: []testutil.File{{Path: "Library/Caches/com.example.n/sub/blob", Size: 10, MTime: old}},
		Dirs:  []testutil.Dir{{Path: "Library/Caches/com.example.n/sub", MTime: old}, {Path: "Library/Caches/com.example.n", MTime: old}},
	})
	res := f.run(f.opts(f.scan("")))
	if len(res.Moved) != 1 {
		t.Fatalf("%+v", res)
	}
	e, err := f.store.Load(res.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	m := e.Receipt.Moved[0]
	if err := os.WriteFile(m.TrashPath+"/sub/.env", []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	rr := f.restore(e, true, m.ID)
	if len(rr.Skipped) != 1 || rr.Skipped[0].Reason != plan.ReasonContainsProtected || exists(m.Path) {
		t.Fatalf("%+v", rr)
	}
}

// displacingTrasher stands in for NSFileManager losing the swap race: it
// moves a decoy into the Trash instead of the approved item.
type displacingTrasher struct{ decoy, dest string }

func (d displacingTrasher) Trash(int, string) (string, error) { return "", errors.New("unused") }
func (d displacingTrasher) TrashExpect(int, string, uint64, uint64) (string, error) {
	if err := os.Rename(d.decoy, d.dest); err != nil {
		return "", err
	}
	fi, err := os.Lstat(d.dest)
	if err != nil {
		return "", err
	}
	st := fi.Sys().(*syscall.Stat_t)
	return d.dest, &trash.DisplacedError{Dest: d.dest, Dev: uint64(st.Dev), Ino: st.Ino, Size: st.Size, Mtime: fi.ModTime().UTC(), Known: true, Why: "test"}
}

// Must-fix 4 and 6: an object displaced into the Trash is recorded in the
// receipt with its Trash path and identity, the item is skipped with
// identity_changed (exit 4), and restore can put the object back.
func TestDisplacedObjectIsRecordedAndRestorable(t *testing.T) {
	spec := threeSafe()
	spec.Files = append(spec.Files, testutil.File{Path: "decoy/keep.txt", Content: "keep"})
	f := newFx(t, spec)
	p := f.scan("")
	item := itemByRel(t, p, "Library/Caches/com.example.a")
	o := f.opts(p)
	o.Only = []string{item.ID}
	o.openTrash = errTrashEPERM(f.home)
	o.Finder = func() (trash.ExpectTrasher, error) {
		return displacingTrasher{decoy: f.abs("decoy"), dest: f.abs(".Trash/com.example.a")}, nil
	}
	res := f.run(o)
	if len(res.Moved) != 0 || len(res.Displaced) != 1 || len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipIdentityChanged || ExitCode(res) != ExitPartial {
		t.Fatalf("%+v", res)
	}
	e, err := f.store.Load(res.ReceiptID)
	if err != nil || len(e.Receipt.Displaced) != 1 || e.Receipt.Displaced[0].TrashPath != f.abs(".Trash/com.example.a") || e.Receipt.Displaced[0].Ino == 0 {
		t.Fatalf("receipt %+v %v", e.Receipt, err)
	}
	// The approved item never moved; once it is gone, the displaced object
	// can go back where it was taken from.
	if err := os.RemoveAll(item.Path); err != nil {
		t.Fatal(err)
	}
	rr := f.restore(e, true, item.ID)
	if len(rr.Restored) != 1 {
		t.Fatalf("restore %+v", rr)
	}
	if b, err := os.ReadFile(item.Path + "/keep.txt"); err != nil || string(b) != "keep" {
		t.Fatal("the displaced object was not restored")
	}
}

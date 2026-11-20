//go:build kipple_permanent

package apply

import (
	"os"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

// Spec scenario "Permanent on review item" (clean-apply).
func TestPermanentOnReviewItem(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/com.example.a/blob", Size: 4096, MTime: old},
		{Path: "go/pkg/mod/github.com/x/y.go", Size: 1024, MTime: old},
	}})
	o := f.opts(f.scan(rules.LevelReview))
	o.Permanent = true
	res := f.run(o)
	safe, review := f.abs("Library/Caches/com.example.a"), f.abs("go/pkg/mod/github.com")
	if m, ok := movedFor(res, safe); !ok || !m.Permanent || m.TrashPath != "" || exists(safe) {
		t.Fatalf("safe item not deleted: %+v", res)
	}
	if s, ok := skipFor(res, review); !ok || s.Reason != plan.SkipPermanentNotAllowed || !exists(review) {
		t.Fatalf("review item: %+v", res.Skipped)
	}
	if ExitCode(res) != ExitPartial {
		t.Fatalf("exit %d, want %d", ExitCode(res), ExitPartial)
	}
	if n := trashEntries(t, f.home); len(n) != 0 {
		t.Fatalf("--permanent put something in the Trash: %v", n)
	}
}

// --permanent never follows a symlink inside the item.
func TestPermanentDoesNotFollowSymlinks(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Files:    []testutil.File{{Path: "Library/Caches/com.example.a/blob", Size: 10, MTime: old}, {Path: "outside/keep.txt", Content: "keep"}},
		Symlinks: []testutil.Symlink{{Path: "Library/Caches/com.example.a/link", Target: "~/outside"}},
		Dirs:     []testutil.Dir{{Path: "Library/Caches/com.example.a", MTime: old}},
	})
	o := f.opts(f.scan(""))
	o.Permanent = true
	res := f.run(o)
	if len(res.Moved) != 1 || exists(f.abs("Library/Caches/com.example.a")) {
		t.Fatalf("%+v", res)
	}
	if b, err := os.ReadFile(f.abs("outside/keep.txt")); err != nil || string(b) != "keep" {
		t.Fatalf("symlink target disturbed: %v", err)
	}
}

// permanentFx is a safe cache item with two subfolders, for --permanent.
func permanentFx(t *testing.T) (*fx, Options, string) {
	f := newFx(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.example.a/aaa/blob", Size: 4096, MTime: old},
			{Path: "Library/Caches/com.example.a/zzz/data", Size: 4096, MTime: old},
		},
		Dirs: []testutil.Dir{
			{Path: "Library/Caches/com.example.a/aaa", MTime: old},
			{Path: "Library/Caches/com.example.a/zzz", MTime: old},
			{Path: "Library/Caches/com.example.a", MTime: old},
		},
	})
	o := f.opts(f.scan(""))
	o.Permanent = true
	return f, o, f.abs("Library/Caches/com.example.a")
}

// Red-team finding: a protected file planted after the move-time measure
// is never deleted by --permanent.
func TestPermanentStopsAtProtectedEntry(t *testing.T) {
	t.Run("planted before the delete starts: nothing is deleted", func(t *testing.T) {
		f, o, item := permanentFx(t)
		o.beforeMove = func(plan.Item) {
			if err := os.WriteFile(item+"/zzz/.env", []byte("SECRET=1"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		res := f.run(o)
		s, ok := skipFor(res, item)
		if !ok || s.Reason != plan.ReasonContainsProtected || s.Partial || ExitCode(res) != ExitPartial {
			t.Fatalf("skipped %+v", res.Skipped)
		}
		if !exists(item+"/zzz/.env") || !exists(item+"/aaa/blob") {
			t.Fatal("something was deleted")
		}
	})
	t.Run("planted while deleting: stops, reports partial", func(t *testing.T) {
		f, o, item := permanentFx(t)
		afterPrecheck = func() {
			if err := os.WriteFile(item+"/zzz/.env", []byte("SECRET=1"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		defer func() { afterPrecheck = nil }()
		res := f.run(o)
		s, ok := skipFor(res, item)
		if !ok || s.Reason != plan.ReasonContainsProtected || !s.Partial || s.RemovedCount < 2 || s.RemovedBytes == 0 || ExitCode(res) != ExitPartial {
			t.Fatalf("skipped %+v", res.Skipped)
		}
		if b, err := os.ReadFile(item + "/zzz/.env"); err != nil || string(b) != "SECRET=1" {
			t.Fatal("the protected file was deleted")
		}
		if exists(item + "/aaa") {
			t.Fatal("test bug: nothing was deleted before the protected file")
		}
		e, err := f.store.Load(res.ReceiptID)
		if err != nil || len(e.Receipt.Skipped) != 1 || !e.Receipt.Skipped[0].Partial || e.Receipt.Skipped[0].RemovedCount != s.RemovedCount {
			t.Fatalf("receipt %+v %v", e.Receipt.Skipped, err)
		}
	})
}

// Standards finding: a permanent delete that fails partway is marked
// partial with what it removed, in the result and the receipt.
func TestPermanentFailingPartwayIsPartial(t *testing.T) {
	f, _, item := permanentFx(t)
	if err := os.Chmod(item+"/zzz", 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(item+"/zzz", 0o755) })
	// Scan after the chmod: a change after the scan would be refused as
	// identity_changed before anything is deleted.
	o := f.opts(f.scan(""))
	o.Permanent = true
	res := f.run(o)
	s, ok := skipFor(res, item)
	if !ok || s.Reason != plan.SkipPermissionDenied || !s.Partial || s.RemovedCount == 0 {
		t.Fatalf("skipped %+v moved %+v", res.Skipped, res.Moved)
	}
	if _, ok := movedFor(res, item); ok {
		t.Fatal("a partial delete was reported as moved")
	}
}

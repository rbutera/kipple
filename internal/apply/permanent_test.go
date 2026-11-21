//go:build kipple_permanent

package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

// Tagged builds only (kipple_permanent): safe items are deleted and review
// items are skipped with permanent_not_allowed.
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

// Standards addendum C: with a snapshot mount oracle (mountinfo, statx
// denied), the delete pass reads the mount table again before deleting in
// each folder. A bind made over zzz after the check pass is in the table
// only once it is read again; it must stop the delete before zzz is
// entered.
func TestPermanentRereadsMountsPerFolder(t *testing.T) {
	f, o, item := permanentFx(t)
	zzz := filepath.Join(item, "zzz")
	fi, err := os.Lstat(zzz)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	id := policy.FileIdentity{Dev: uint64(st.Dev), Ino: uint64(st.Ino)} //nolint:unconvert // Dev is int32 on darwin
	roots := map[policy.FileIdentity]bool{}
	armed, refreshes := false, 0
	o.Env.Mounts = policy.StubMounts{Roots: roots, OnRefresh: func() error {
		if armed {
			refreshes++
			roots[id] = true // the snapshot now sees the bind
		}
		return nil
	}}
	afterPrecheck = func() { armed = true }
	defer func() { afterPrecheck = nil }()
	res := f.run(o)
	s, ok := skipFor(res, item)
	if !ok || s.Reason != plan.ReasonContainsMount || !s.Partial || !exists(filepath.Join(zzz, "data")) || exists(filepath.Join(item, "aaa")) {
		t.Fatalf("skipped %+v moved %+v", res.Skipped, res.Moved)
	}
	if refreshes < 2 { // the item folder, then aaa, before zzz
		t.Fatalf("mount table read again %d times during the delete, want one per folder", refreshes)
	}
}

// Standards addendum C: a mount table that cannot be read again during the
// delete stops it before anything in that folder is unlinked, and stops the
// run.
func TestPermanentStopsWhenMountTableUnreadable(t *testing.T) {
	_, o, item := permanentFx(t)
	armed := false
	o.Env.Mounts = policy.StubMounts{OnRefresh: func() error {
		if armed {
			return fmt.Errorf("%w: mountinfo vanished", policy.ErrMountsUnavailable)
		}
		return nil
	}}
	afterPrecheck = func() { armed = true }
	defer func() { afterPrecheck = nil }()
	res, err := Run(context.Background(), o)
	if !errors.Is(err, policy.ErrMountsUnavailable) {
		t.Fatalf("err = %v", err)
	}
	s, ok := skipFor(res, item)
	if !ok || s.Reason != plan.ReasonContainsMount || !exists(filepath.Join(item, "aaa", "blob")) || !exists(filepath.Join(item, "zzz", "data")) {
		t.Fatalf("skipped %+v moved %+v", res.Skipped, res.Moved)
	}
}

// Wave 2 confirmation G2: a run that stops on a mount-table failure in the
// middle of a permanent delete says what was deleted, names the receipt,
// never says "nothing was moved", and never offers restore for what was
// deleted permanently.
func TestPermanentStopSaysWhatWasDeleted(t *testing.T) {
	tree := func(item string) []testutil.File {
		return []testutil.File{
			{Path: item + "/aaa/blob", Size: 4096, MTime: old},
			{Path: item + "/zzz/data", Size: 4096, MTime: old},
		}
	}
	for _, tc := range []struct {
		name  string
		items []string
		want  []string
	}{
		{"partly deleted only", []string{"Library/Caches/com.example.a"}, []string{"after partly deleting 1 item (receipt "}},
		{"deleted and partly deleted", []string{"Library/Caches/com.example.a", "Library/Caches/com.example.b"}, []string{"deleting 1 item permanently, partly deleting 1 item (receipt "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var spec testutil.Spec
			for _, it := range tc.items {
				spec.Files = append(spec.Files, tree(it)...)
				spec.Dirs = append(spec.Dirs, testutil.Dir{Path: it + "/aaa", MTime: old}, testutil.Dir{Path: it + "/zzz", MTime: old}, testutil.Dir{Path: it, MTime: old})
			}
			f := newFx(t, spec)
			o := f.opts(f.scan(""))
			o.Permanent = true
			prechecks, armed := 0, 0
			o.Env.Mounts = policy.StubMounts{OnRefresh: func() error {
				if prechecks == len(tc.items) {
					// Last item: the folder, aaa (blob and aaa go), then zzz.
					if armed++; armed == 3 {
						return fmt.Errorf("%w: mountinfo vanished", policy.ErrMountsUnavailable)
					}
				}
				return nil
			}}
			afterPrecheck = func() { prechecks++ }
			defer func() { afterPrecheck = nil }()
			res, err := Run(context.Background(), o)
			if !errors.Is(err, ErrStoppedPartway) || !errors.Is(err, policy.ErrMountsUnavailable) {
				t.Fatalf("err = %v", err)
			}
			msg := err.Error()
			for _, w := range append(tc.want, res.ReceiptID, "what was deleted permanently cannot be put back") {
				if !strings.Contains(msg, w) {
					t.Fatalf("%q lacks %q", msg, w)
				}
			}
			if strings.Contains(msg, "restore") || strings.Contains(msg, "nothing was moved") || strings.Contains(msg, "to the Trash") {
				t.Fatalf("%q", msg)
			}
		})
	}
}

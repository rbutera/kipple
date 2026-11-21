//go:build unix

package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/testutil"
)

func fileIdentity(t *testing.T, p string) policy.FileIdentity {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, ok := policy.Identity(fi)
	if !ok {
		t.Fatal("no identity")
	}
	return policy.FileIdentity{Dev: dev, Ino: ino}
}

// Spec scenario "Bind mount inside a cache folder" (safety-policy), with an
// injected mount oracle standing in for a same-device bind mount of
// Documents: the item holding it is contains_mount, never selected, and
// apply skips it even when named; nothing under it or Documents moves.
func TestBindMountInsideACacheFolder(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/foo/docs/letter.txt", Content: "dear", MTime: old},
		{Path: "Library/Caches/foo/blob", Content: "x", MTime: old},
		{Path: "Documents/letter.txt", Content: "dear"},
	}})
	f.env.Mounts = policy.StubMounts{Roots: map[policy.FileIdentity]bool{fileIdentity(t, f.abs("Library/Caches/foo/docs")): true}}
	before := testutil.Checksum(t, f.home)
	p := f.scan("")
	it := itemByRel(t, p, "Library/Caches/foo")
	if it.Selected || !slices.Contains(it.Reasons, plan.ReasonContainsMount) {
		t.Fatalf("item %+v", it)
	}
	o := f.opts(p)
	o.Only = []string{it.ID}
	res := f.run(o)
	if s, ok := skipFor(res, it.Path); !ok || s.Reason != plan.ReasonContainsMount || len(res.Moved) != 0 {
		t.Fatalf("%+v", res)
	}
	if testutil.Checksum(t, f.home) != before {
		// Only the Trash folders (created for the run on Linux) may differ.
		for _, e := range []string{"Library/Caches/foo/docs/letter.txt", "Library/Caches/foo/blob", "Documents/letter.txt"} {
			if !exists(f.abs(e)) {
				t.Fatalf("%s moved", e)
			}
		}
	}
}

// Spec scenario "Mount table unavailable": scan warns and marks every item
// contains_mount; apply returns ErrMountsUnavailable and nothing changes.
func TestMountTableUnavailable(t *testing.T) {
	f := newFx(t, threeSafe())
	f.env.Mounts = policy.StubMounts{Err: policy.ErrMountsUnavailable}
	before := testutil.Checksum(t, f.home)
	p := f.scan("")
	if len(p.Warnings) == 0 {
		t.Fatal("no warning")
	}
	for _, it := range p.Items {
		if it.Selected || !slices.Contains(it.Reasons, plan.ReasonContainsMount) {
			t.Errorf("item %+v", it)
		}
	}
	o := f.opts(p)
	var ids []string
	for _, it := range p.Items {
		ids = append(ids, it.ID)
	}
	o.Only = ids
	if _, err := Run(context.Background(), o); !errors.Is(err, ErrMountsUnavailable) {
		t.Fatalf("err = %v, want ErrMountsUnavailable", err)
	}
	if testutil.Checksum(t, f.home) != before {
		t.Fatal("the fixture changed")
	}
	if es, _ := f.store.List(); len(es) != 0 {
		t.Fatal("a receipt was written")
	}
}

// Wave 2 red-team R1: a mount made inside an item after its last check
// rides into the Trash with it. The post-move check finds it and puts the
// item back (contains_mount); if that cannot happen, the item is recorded
// as moved and skipped with a detail saying not to empty the Trash.
func TestMountAppearsDuringTheMove(t *testing.T) {
	setup := func(t *testing.T) (*fx, plan.Item, map[policy.FileIdentity]bool) {
		f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/foo/m/x", Content: "x", MTime: old}, {Path: "Library/Caches/foo/blob", Content: "x", MTime: old}}})
		roots := map[policy.FileIdentity]bool{}
		f.env.Mounts = policy.StubMounts{Roots: roots}
		p := f.scan("")
		return f, itemByRel(t, p, "Library/Caches/foo"), roots
	}
	t.Run("put back", func(t *testing.T) {
		f, it, roots := setup(t)
		o := f.opts(f.scan(""))
		o.Only = []string{it.ID}
		m := fileIdentity(t, f.abs("Library/Caches/foo/m"))
		o.afterMove = func(plan.Item) { roots[m] = true }
		res := f.run(o)
		s, ok := skipFor(res, it.Path)
		if !ok || s.Reason != plan.ReasonContainsMount || len(res.Moved) != 0 || !exists(it.Path) || !strings.Contains(s.Detail, "put back") {
			t.Fatalf("%+v", res)
		}
		if n := trashEntries(t, f.home); len(n) != 0 {
			t.Fatalf("left in the Trash: %v", n)
		}
		if info := f.abs(".local/share/Trash/info"); exists(info) {
			if es, _ := os.ReadDir(info); len(es) != 0 {
				t.Fatalf("info files left: %v", es)
			}
		}
	})
	t.Run("cannot be put back", func(t *testing.T) {
		f, it, roots := setup(t)
		o := f.opts(f.scan(""))
		o.Only = []string{it.ID}
		m := fileIdentity(t, f.abs("Library/Caches/foo/m"))
		o.afterMove = func(plan.Item) {
			roots[m] = true
			if err := os.Mkdir(it.Path, 0o755); err != nil { // something took the name
				t.Fatal(err)
			}
		}
		res := f.run(o)
		mv, moved := movedFor(res, it.Path)
		s, skipped := skipFor(res, it.Path)
		if !moved || !skipped || s.Reason != plan.ReasonContainsMount || !strings.Contains(s.Detail, "do not empty the Trash") || len(res.Warnings) == 0 || ExitCode(res) != ExitPartial {
			t.Fatalf("%+v", res)
		}
		if !exists(mv.TrashPath) {
			t.Fatal("the item is not where the result says")
		}
		e, err := f.store.Load(res.ReceiptID)
		if err != nil || len(e.Receipt.Moved) != 1 || len(e.Receipt.Skipped) != 1 || !strings.Contains(e.Receipt.Skipped[0].Detail, "do not empty the Trash") {
			t.Fatalf("receipt %+v %v", e.Receipt, err)
		}
	})
}

// Wave 2 re-attack F1: a post-move walk that cannot see everything (here
// a folder made unreadable after the move) is not clean: the item is put
// back, as for a found mount.
func TestPartialWalkAfterTheMoveIsNotClean(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/foo/d/m/x", Content: "x", MTime: old}, {Path: "Library/Caches/foo/blob", Content: "x", MTime: old}}})
	p := f.scan("")
	it := itemByRel(t, p, "Library/Caches/foo")
	o := f.opts(p)
	o.Only = []string{it.ID}
	var d string
	o.afterMove = func(plan.Item) {
		entries := trashEntries(t, f.home)
		d = filepath.Join(f.abs(testutil.TrashRel), entries[0], "d")
		if err := os.Chmod(d, 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(f.abs("Library/Caches/foo/d"), 0o755) })
	res := f.run(o)
	s, ok := skipFor(res, it.Path)
	if !ok || s.Reason != plan.ReasonContainsMount || len(res.Moved) != 0 || !exists(it.Path) || !strings.Contains(s.Detail, "could not be checked completely") {
		t.Fatalf("%+v", res)
	}
	if n := trashEntries(t, f.home); len(n) != 0 {
		t.Fatalf("left in the Trash: %v", n)
	}
}

// Wave 2 re-attack F2: the move-back checks identity. If something else
// sits at the Trash name (the entry was exchanged), nothing is "put back"
// blindly: the item stays recorded as moved, with the loud warning.
func TestMoveBackChecksIdentity(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/foo/m/x", Content: "x", MTime: old}, {Path: "decoy/DECOY", Content: "d"}}})
	roots := map[policy.FileIdentity]bool{}
	f.env.Mounts = policy.StubMounts{Roots: roots}
	p := f.scan("")
	it := itemByRel(t, p, "Library/Caches/foo")
	o := f.opts(p)
	o.Only = []string{it.ID}
	m := fileIdentity(t, f.abs("Library/Caches/foo/m"))
	o.afterMove = func(plan.Item) {
		roots[m] = true
		// Exchange the Trash entry with a decoy (swap by renames).
		tr := f.abs(testutil.TrashRel)
		entry := filepath.Join(tr, trashEntries(t, f.home)[0])
		for _, r := range [][2]string{{entry, entry + ".x"}, {f.abs("decoy"), entry}, {entry + ".x", filepath.Join(tr, "real")}} {
			if err := os.Rename(r[0], r[1]); err != nil {
				t.Fatal(err)
			}
		}
	}
	res := f.run(o)
	_, moved := movedFor(res, it.Path)
	s, skipped := skipFor(res, it.Path)
	if !moved || !skipped || s.Reason != plan.ReasonContainsMount || !strings.Contains(s.Detail, "do not empty the Trash") || len(res.Warnings) == 0 {
		t.Fatalf("%+v", res)
	}
	if exists(f.abs("Library/Caches/foo/DECOY")) {
		t.Fatal("the decoy was put back as the item")
	}
	if !exists(filepath.Join(f.abs(testutil.TrashRel), "real", "m", "x")) {
		t.Fatal("the real item is not where it was left")
	}
}

// Wave 2 re-attack F6: a run that stops partway (mount points can no longer
// be read at item N) says how many items moved and names the receipt.
func TestStopPartwayReportsMoves(t *testing.T) {
	spec := testutil.Spec{}
	for i := 0; i < 5; i++ {
		spec.Files = append(spec.Files, testutil.File{Path: fmt.Sprintf("Library/Caches/c%d/blob", i), Content: "x", MTime: old})
	}
	f := newFx(t, spec)
	calls := 0
	f.env.Mounts = policy.StubMounts{OnRefresh: func() error {
		calls++
		if calls > 6 { // two refreshes per moved item: before and after
			return fmt.Errorf("%w: mountinfo vanished", policy.ErrMountsUnavailable)
		}
		return nil
	}}
	res, err := Run(context.Background(), f.opts(f.scan("")))
	if !errors.Is(err, ErrStoppedPartway) || !errors.Is(err, ErrMountsUnavailable) || !strings.Contains(err.Error(), res.ReceiptID) {
		t.Fatalf("err = %v", err)
	}
	if len(res.Moved) != 3 || !strings.Contains(err.Error(), "after 3 moves") {
		t.Fatalf("moved %d: %v", len(res.Moved), err)
	}
	e, lerr := f.store.Load(res.ReceiptID)
	if lerr != nil || len(e.Receipt.Moved) != 3 {
		t.Fatalf("receipt %+v %v", e.Receipt, lerr)
	}
}

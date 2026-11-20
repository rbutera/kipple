package apply

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/procs"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

// threeSafe has three selected safe items; Xcode owns the DerivedData one.
func threeSafe() testutil.Spec {
	return testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/com.example.a/blob", Size: 4096, MTime: old},
		{Path: "Library/Logs/app/log.txt", Size: 2048, MTime: old},
		{Path: "Library/Developer/Xcode/DerivedData/Proj-abc/build.o", Size: 8192, MTime: old},
	}}
}

// Spec scenario "Apply to Trash" (clean-apply).
func TestApplyToTrash(t *testing.T) {
	f := newFx(t, threeSafe())
	p := f.scan("")
	before := map[string]string{}
	for _, it := range p.Items {
		before[it.Path] = testutil.Checksum(t, it.Path)
	}
	res := f.run(f.opts(p))
	if len(res.Moved) != 3 || len(res.Skipped) != 0 || ExitCode(res) != ExitOK {
		t.Fatalf("moved %d skipped %+v exit %d", len(res.Moved), res.Skipped, ExitCode(res))
	}
	e, err := f.store.Load(res.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Receipt.Complete || e.Receipt.TrashDir != f.abs(".Trash") || len(e.Receipt.Moved) != 3 {
		t.Fatalf("receipt %+v", e.Receipt)
	}
	for _, m := range e.Receipt.Moved {
		if exists(m.Path) {
			t.Errorf("%s still at its original path", m.Path)
		}
		if filepath.Dir(m.TrashPath) != f.abs(".Trash") || !exists(m.TrashPath) {
			t.Errorf("%s not at its recorded Trash path %s", m.Path, m.TrashPath)
			continue
		}
		if got := testutil.Checksum(t, m.TrashPath); got != before[m.Path] {
			t.Errorf("%s changed on its way to the Trash", m.Path)
		}
		if m.Bytes == 0 || m.Dev == 0 || m.Ino == 0 || m.MovedAt.IsZero() {
			t.Errorf("incomplete receipt entry %+v", m)
		}
	}
}

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

// Spec scenario "Project became dirty" (clean-apply).
func TestProjectBecameDirty(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Repos: []testutil.Repo{gitProject("dev/app")},
		Files: []testutil.File{
			{Path: "dev/app/node_modules/x/i.js", Content: "x"},
			{Path: "Library/Caches/com.example.a/blob", Size: 10, MTime: old},
		},
	})
	p := f.scan(rules.LevelReview)
	nm := itemByRel(t, p, "dev/app/node_modules")
	if !nm.Selected {
		t.Fatalf("fixture: project artifact not selected: %+v", nm)
	}
	if err := os.WriteFile(f.abs("dev/app/src/index.js"), []byte("edited by an agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := f.run(f.opts(p))
	if s, ok := skipFor(res, nm.Path); !ok || s.Reason != plan.ReasonGitDirty || !exists(nm.Path) {
		t.Fatalf("skipped %+v", res.Skipped)
	}
	if _, ok := movedFor(res, f.abs("Library/Caches/com.example.a")); !ok {
		t.Fatal("the rest of the plan did not proceed")
	}
}

// Spec scenario "Agent writes to project mid-run" (project-artifacts).
func TestAgentWritesToProjectMidRun(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Repos: []testutil.Repo{gitProject("dev/app")},
		Files: []testutil.File{{Path: "dev/app/node_modules/x/i.js", Content: "x"}},
	})
	p := f.scan(rules.LevelReview)
	nm := itemByRel(t, p, "dev/app/node_modules")
	if err := os.WriteFile(f.abs("dev/app/src/new.js"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := f.run(f.opts(p))
	s, ok := skipFor(res, nm.Path)
	if !ok || (s.Reason != plan.ReasonProjectActive && s.Reason != plan.ReasonGitDirty) || !exists(nm.Path) {
		t.Fatalf("skipped %+v", res.Skipped)
	}
}

// Spec scenario "Claude Code open in project" (project-artifacts).
func TestClaudeCodeOpenInProject(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Repos: []testutil.Repo{gitProject("dev/app")},
		Files: []testutil.File{{Path: "dev/app/node_modules/x/i.js", Content: "x"}},
	})
	p := f.scan(rules.LevelReview)
	o := f.opts(p)
	o.Procs = fakeProcs{cwds: []string{"/elsewhere", f.abs("dev/app/src")}}
	res := f.run(o)
	nm := f.abs("dev/app/node_modules")
	if s, ok := skipFor(res, nm); !ok || s.Reason != plan.SkipToolRunning || !exists(nm) {
		t.Fatalf("skipped %+v", res.Skipped)
	}
	t.Run("unknown agent state skips a review item", func(t *testing.T) {
		o.Procs = fakeProcs{err: procs.ErrUnknown}
		res := f.run(o)
		if s, ok := skipFor(res, nm); !ok || s.Reason != plan.SkipToolRunning {
			t.Fatalf("skipped %+v", res.Skipped)
		}
	})
	t.Run("an agent elsewhere does not block", func(t *testing.T) {
		o.Procs = fakeProcs{cwds: []string{f.abs("dev/other")}}
		res := f.run(o)
		if _, ok := movedFor(res, nm); !ok {
			t.Fatalf("not moved: %+v", res.Skipped)
		}
	})
}

// Spec scenarios "Receipt written" and "Partial run" (clean-apply).
func TestReceiptWrittenAndPartialRun(t *testing.T) {
	f := newFx(t, threeSafe())
	o := f.opts(f.scan(""))
	o.Procs = fakeProcs{bundles: map[string]bool{"com.apple.dt.Xcode": true}}
	res := f.run(o)
	dd := f.abs("Library/Developer/Xcode/DerivedData/Proj-abc")
	t.Run("Partial run", func(t *testing.T) {
		s, ok := skipFor(res, dd)
		if !ok || s.Reason != plan.ReasonOwnerRunning || ExitCode(res) != ExitPartial || !exists(dd) {
			t.Fatalf("exit %d skipped %+v", ExitCode(res), res.Skipped)
		}
	})
	t.Run("Receipt written", func(t *testing.T) {
		e, err := f.store.Load(res.ReceiptID)
		if err != nil {
			t.Fatal(err)
		}
		if len(e.Receipt.Moved) != 2 || len(e.Receipt.Skipped) != 1 || e.Receipt.Skipped[0].Reason != plan.ReasonOwnerRunning {
			t.Fatalf("receipt moved %d skipped %+v", len(e.Receipt.Moved), e.Receipt.Skipped)
		}
	})
	t.Run("unknown owner state warns for a safe item", func(t *testing.T) {
		f := newFx(t, threeSafe())
		o := f.opts(f.scan(""))
		o.Procs = fakeProcs{err: procs.ErrUnknown}
		res := f.run(o)
		if len(res.Moved) != 3 || len(res.Warnings) == 0 {
			t.Fatalf("moved %d warnings %v", len(res.Moved), res.Warnings)
		}
	})
}

// Spec scenario "Agent approves a subset" (clean-apply), with the plan read
// back from JSON as a plan file would be.
func TestAgentApprovesASubset(t *testing.T) {
	f := newFx(t, threeSafe())
	file := roundTrip(t, f.scan(""))
	pick := itemByRel(t, file, "Library/Logs/app")
	o := f.opts(file)
	fresh := f.scan(file.Level)
	o.Fresh, o.Only = &fresh, []string{pick.ID}
	res := f.run(o)
	if len(res.Moved) != 1 || res.Moved[0].ID != pick.ID || len(res.Skipped) != 0 {
		t.Fatalf("moved %+v skipped %+v", res.Moved, res.Skipped)
	}
	var others []string
	for _, it := range file.Items {
		if it.ID != pick.ID {
			others = append(others, it.ID)
		}
	}
	slices.Sort(others)
	got := append([]string(nil), res.Untouched...)
	slices.Sort(got)
	if !slices.Equal(got, others) {
		t.Fatalf("untouched %v, want %v", got, others)
	}
}

// --only overrides tier review and manual, never info, never anything else.
func TestOnlyTiers(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{
		{Path: "Downloads/Old.dmg", Size: 4096, MTime: old},
		{Path: "Downloads/New.dmg", Size: 4096, MTime: now},
		{Path: "go/pkg/mod/github.com/x/y.go", Size: 1024, MTime: old},
		{Path: ".ollama/models/blobs/sha", Size: 4096, MTime: old},
	}})
	p := f.scan("")
	ids := map[string]string{}
	for _, rel := range []string{"Downloads/Old.dmg", "Downloads/New.dmg", "go/pkg/mod/github.com", ".ollama/models/blobs"} {
		ids[rel] = itemByRel(t, p, rel).ID
	}
	o := f.opts(p)
	o.Only = []string{ids["Downloads/Old.dmg"], ids["Downloads/New.dmg"], ids["go/pkg/mod/github.com"], ids[".ollama/models/blobs"], "zzzzzzzzzzzz"}
	res := f.run(o)
	for _, rel := range []string{"Downloads/Old.dmg", "go/pkg/mod/github.com"} {
		if _, ok := movedFor(res, f.abs(rel)); !ok {
			t.Errorf("%s (named) not moved", rel)
		}
	}
	want := map[string]string{"Downloads/New.dmg": plan.ReasonTooRecent, ".ollama/models/blobs": plan.SkipInfoTier}
	for rel, reason := range want {
		if s, ok := skipFor(res, f.abs(rel)); !ok || s.Reason != reason {
			t.Errorf("%s: %+v, want %s", rel, s, reason)
		}
	}
	found := false
	for _, s := range res.Skipped {
		found = found || (s.ID == "zzzzzzzzzzzz" && s.Reason == plan.SkipNotFound)
	}
	if !found {
		t.Errorf("unknown ID not reported: %+v", res.Skipped)
	}
}

// Spec scenario "Apply re-authorises before moving" (safety-policy), and
// the TOCTOU case of task 3.6: a directory swapped for a symlink between
// scan and apply is refused and nothing behind the symlink moves.
func TestApplyReauthorisesBeforeMoving(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/com.example.a/blob", Size: 10, MTime: old},
		{Path: "Library/Caches/com.example.b/blob", Size: 10, MTime: old},
		{Path: "Documents/important/report.txt", Content: "precious"},
	}})
	p := f.scan("")
	item := f.abs("Library/Caches/com.example.a")
	if err := os.Rename(item, item+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.abs("Documents/important"), item); err != nil {
		t.Fatal(err)
	}
	sum := testutil.Checksum(t, f.abs("Documents"))
	res := f.run(f.opts(p))
	if s, ok := skipFor(res, item); !ok || s.Reason != plan.Refused(policy.ReasonSymlinkInPath) {
		t.Fatalf("skipped %+v", res.Skipped)
	}
	if fi, err := os.Lstat(item); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink itself was moved")
	}
	if testutil.Checksum(t, f.abs("Documents")) != sum {
		t.Fatal("the symlink target changed")
	}
	if _, ok := movedFor(res, f.abs("Library/Caches/com.example.b")); !ok {
		t.Fatal("the other item did not move")
	}
}

// An ancestor swapped for a symlink to a look-alike tree is refused.
func TestAncestorSwappedForSymlink(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/com.example.a/blob", Size: 10, MTime: old},
		{Path: "decoy/com.example.a/precious.txt", Content: "precious"},
	}})
	p := f.scan("")
	if err := os.Rename(f.abs("Library/Caches"), f.abs("Library/Caches.real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.abs("decoy"), f.abs("Library/Caches")); err != nil {
		t.Fatal(err)
	}
	res := f.run(f.opts(p))
	if s, ok := skipFor(res, f.abs("Library/Caches/com.example.a")); !ok || s.Reason != plan.Refused(policy.ReasonSymlinkInPath) {
		t.Fatalf("skipped %+v", res.Skipped)
	}
	if !exists(f.abs("decoy/com.example.a/precious.txt")) {
		t.Fatal("the decoy was moved")
	}
}

// The parent moved after every check and before the rename: VerifyParent
// (F_GETPATH) refuses it.
func TestParentMovedBeforeRename(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Logs/app/log.txt", Size: 10, MTime: old}}})
	o := f.opts(f.scan(""))
	o.beforeMove = func(plan.Item) {
		if err := os.Rename(f.abs("Library/Logs"), f.abs("Library/Logs.moved")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(f.abs("Library/Logs"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	res := f.run(o)
	if s, ok := skipFor(res, f.abs("Library/Logs/app")); !ok || s.Reason != plan.SkipParentChanged {
		t.Fatalf("skipped %+v moved %+v", res.Skipped, res.Moved)
	}
	if !exists(f.abs("Library/Logs.moved/app/log.txt")) || len(trashEntries(t, f.home)) != 0 {
		t.Fatal("the item moved")
	}
}

// An item replaced by another object of the same name is refused.
func TestItemReplacedAfterScan(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Logs/app/log.txt", Size: 10, MTime: old}}})
	p := f.scan("")
	if err := os.Rename(f.abs("Library/Logs/app"), f.abs("Library/Logs/app.orig")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.abs("Library/Logs/app"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := f.run(f.opts(p))
	if s, ok := skipFor(res, f.abs("Library/Logs/app")); !ok || s.Reason != plan.SkipIdentityChanged {
		t.Fatalf("skipped %+v", res.Skipped)
	}
}

// Plan files are untrusted: the rule root's identity, the item's identity
// and its path are checked against a fresh scan.
func TestPlanFileIsUntrusted(t *testing.T) {
	t.Run("rule root replaced", func(t *testing.T) {
		f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Logs/app/log.txt", Size: 10, MTime: old}}})
		file := roundTrip(t, f.scan(""))
		if err := os.Rename(f.abs("Library/Logs"), f.abs("Library/Logs.old")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(f.abs("Library/Logs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(f.abs("Library/Logs.old/app"), f.abs("Library/Logs/app")); err != nil {
			t.Fatal(err)
		}
		fresh := f.scan(file.Level)
		o := f.opts(file)
		o.Fresh = &fresh
		res := f.run(o)
		if s, ok := skipFor(res, f.abs("Library/Logs/app")); !ok || s.Reason != plan.SkipRootChanged {
			t.Fatalf("skipped %+v", res.Skipped)
		}
	})
	t.Run("tampered path and identity", func(t *testing.T) {
		spec := threeSafe()
		spec.Files = append(spec.Files, testutil.File{Path: "Documents/x", Content: "x"})
		f := newFx(t, spec)
		file := roundTrip(t, f.scan(""))
		fresh := f.scan(file.Level)
		for i := range file.Items {
			switch strings.TrimPrefix(file.Items[i].Path, f.home+"/") {
			case "Library/Logs/app":
				file.Items[i].Path = f.abs("Documents")
			case "Library/Caches/com.example.a":
				file.Items[i].Ino++
			}
		}
		file.Items = append(file.Items, plan.Item{ID: "aaaaaaaaaaaa", Rule: "caches.user", Tier: "safe", Path: f.abs("Library/Caches"), Selected: true, Dev: 1, Ino: 1, Reasons: []string{}})
		o := f.opts(file)
		o.Fresh = &fresh
		res := f.run(o)
		reasons := map[string]string{}
		for _, s := range res.Skipped {
			reasons[s.ID] = s.Reason
		}
		if reasons[itemByRel(t, fresh, "Library/Logs/app").ID] != plan.SkipIdentityChanged ||
			reasons[itemByRel(t, fresh, "Library/Caches/com.example.a").ID] != plan.SkipIdentityChanged ||
			reasons["aaaaaaaaaaaa"] != plan.SkipNotFound {
			t.Fatalf("skipped %+v", res.Skipped)
		}
		if !exists(f.abs("Documents")) || !exists(f.abs("Library/Caches")) || len(res.Moved) != 1 {
			t.Fatalf("moved %+v", res.Moved)
		}
	})
}

// Go's module cache is read-only: the item's own directory gets owner
// write for the rename and its mode is restored; contents are untouched.
func TestReadOnlyDirectoryItem(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Files: []testutil.File{{Path: "go/pkg/mod/github.com/x/y.go", Size: 10, MTime: old, Mode: 0o444}},
		Dirs: []testutil.Dir{
			{Path: "go/pkg/mod/github.com", MTime: old},
			{Path: "go/pkg/mod/github.com/x", MTime: old},
		},
	})
	t.Cleanup(func() {
		_ = filepath.Walk(f.home, func(p string, _ os.FileInfo, _ error) error { return os.Chmod(p, 0o755) })
	})
	for _, d := range []string{"go/pkg/mod/github.com/x", "go/pkg/mod/github.com"} {
		if err := os.Chmod(f.abs(d), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	res := f.run(f.opts(f.scan(rules.LevelReview)))
	m, ok := movedFor(res, f.abs("go/pkg/mod/github.com"))
	if !ok {
		t.Fatalf("not moved: %+v", res.Skipped)
	}
	fi, err := os.Lstat(m.TrashPath)
	if err != nil || fi.Mode().Perm() != 0o555 {
		t.Fatalf("mode after the move: %v %v", fi.Mode(), err)
	}
}

// Nothing selected: exit 3, and the receipt is still written.
func TestNothingSelected(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "Downloads/Old.dmg", Size: 10, MTime: old}}})
	res := f.run(f.opts(f.scan("")))
	if ExitCode(res) != ExitNothing || res.ReceiptID == "" || len(res.Untouched) != 1 {
		t.Fatalf("%+v", res)
	}
}

// Apply refuses a home whose pinned identity no longer matches.
func TestHomeIdentityMismatch(t *testing.T) {
	f := newFx(t, threeSafe())
	p := f.scan("")
	o := f.opts(p)
	o.Env.HomeIdentity = &policy.FileIdentity{Dev: o.Env.HomeIdentity.Dev, Ino: o.Env.HomeIdentity.Ino + 1}
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("apply ran against an unpinned home")
	}
	o.Env.HomeIdentity = nil
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("apply ran without a pinned identity")
	}
	for _, it := range p.Items {
		if !exists(it.Path) {
			t.Fatalf("%s moved", it.Path)
		}
	}
}

func TestPreview(t *testing.T) {
	f := newFx(t, threeSafe())
	p := f.scan("")
	pick := itemByRel(t, p, "Library/Logs/app")
	out := Preview(p, []string{pick.ID, "zzzzzzzzzzzz"})
	for _, it := range out.Items {
		if it.ID == pick.ID {
			if !it.Selected || len(it.Reasons) != 0 {
				t.Fatalf("named item %+v", it)
			}
		} else if it.Selected || !slices.Contains(it.Reasons, plan.ReasonNotRequested) {
			t.Fatalf("other item %+v", it)
		}
	}
	if out.Totals.SelectedCount != 1 || len(out.Warnings) != 1 {
		t.Fatalf("totals %+v warnings %v", out.Totals, out.Warnings)
	}
}

// Items that became partial or gained protected contents after the scan
// are skipped; the re-measure at move time sees them.
func TestRemeasuredAtMoveTime(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/com.example.a/blob", Size: 10, MTime: old},
		{Path: "Library/Caches/com.example.b/blob", Size: 10, MTime: old},
		{Path: "Library/Caches/com.example.c/sub/blob", Size: 10, MTime: old},
	}})
	p := f.scan("")
	if err := os.WriteFile(f.abs("Library/Caches/com.example.b/.env"), []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.abs("Library/Caches/com.example.c/sub"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.abs("Library/Caches/com.example.c/sub"), 0o755) })
	res := f.run(f.opts(p))
	if _, ok := movedFor(res, f.abs("Library/Caches/com.example.a")); !ok {
		t.Fatalf("control item not moved: %+v", res.Skipped)
	}
	want := map[string]string{"Library/Caches/com.example.b": plan.ReasonContainsProtected, "Library/Caches/com.example.c": plan.ReasonScanPartial}
	for rel, reason := range want {
		if s, ok := skipFor(res, f.abs(rel)); !ok || s.Reason != reason || !exists(f.abs(rel)) {
			t.Errorf("%s: %+v, want %s", rel, s, reason)
		}
	}
}

// The system refusing a move (EACCES, as for a folder in a read-only
// parent) is a skip with permission_denied, never an elevation.
func TestPermissionDenied(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Logs/app/log.txt", Size: 10, MTime: old}}})
	p := f.scan("")
	if err := os.Chmod(f.abs("Library/Logs"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.abs("Library/Logs"), 0o755) })
	res := f.run(f.opts(p))
	if s, ok := skipFor(res, f.abs("Library/Logs/app")); !ok || s.Reason != plan.SkipPermissionDenied || ExitCode(res) != ExitPartial {
		t.Fatalf("skipped %+v", res.Skipped)
	}
	if fi, _ := os.Stat(f.abs("Library/Logs")); fi.Mode().Perm() != 0o555 {
		t.Fatal("kipple changed the parent's permissions")
	}
}

// Moving one link of a hard-linked file frees nothing: the receipt counts
// it in bytes but not in reclaimable_bytes.
func TestHardlinkedBytesAreNotReclaimed(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Files:     []testutil.File{{Path: "Library/Caches/com.example.a/own", Size: 8192, MTime: old}, {Path: "elsewhere/shared", Size: 65536, MTime: old}},
		Hardlinks: []testutil.Hardlink{{Path: "Library/Caches/com.example.a/shared", Target: "elsewhere/shared"}},
		Dirs:      []testutil.Dir{{Path: "Library/Caches/com.example.a", MTime: old}},
	})
	res := f.run(f.opts(f.scan("")))
	m, ok := movedFor(res, f.abs("Library/Caches/com.example.a"))
	if !ok || m.ReclaimableBytes >= m.Bytes || m.Bytes-m.ReclaimableBytes < 65536 {
		t.Fatalf("moved %+v", m)
	}
}

// Red-team finding: a filter driver configured in the project's repository
// never runs during scan or apply.
func TestGitFiltersNeverRunDuringScanOrApply(t *testing.T) {
	repo := gitProject("dev/app")
	repo.Commits[0].Files = append(repo.Commits[0].Files, testutil.File{Path: ".gitattributes", Content: "* filter=canary\n"})
	f := newFx(t, testutil.Spec{Repos: []testutil.Repo{repo}, Files: []testutil.File{{Path: "dev/app/node_modules/x/i.js", Content: "x"}}})
	canary := filepath.Join(filepath.Dir(f.home), "canary")
	app := f.abs("dev/app")
	testutil.Git(t, app, "config", "filter.canary.clean", "touch "+canary+"; cat")
	testutil.Git(t, app, "config", "filter.canary.process", "touch "+canary)
	_ = os.Chtimes(f.abs("dev/app/.git/config"), old, old) // keep the project idle
	p := f.scan(rules.LevelReview)
	nm := itemByRel(t, p, "dev/app/node_modules")
	if !nm.Selected {
		t.Fatalf("fixture: %+v", nm)
	}
	res := f.run(f.opts(p))
	if _, ok := movedFor(res, nm.Path); !ok {
		t.Fatalf("not moved: %+v", res.Skipped)
	}
	if _, err := os.Stat(canary); err == nil {
		t.Fatal("a git filter ran during scan or apply")
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
	f, o, item := permanentFx(t)
	if err := os.Chmod(item+"/zzz", 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(item+"/zzz", 0o755) })
	res := f.run(o)
	s, ok := skipFor(res, item)
	if !ok || s.Reason != plan.SkipPermissionDenied || !s.Partial || s.RemovedCount == 0 {
		t.Fatalf("skipped %+v moved %+v", res.Skipped, res.Moved)
	}
	if _, ok := movedFor(res, item); ok {
		t.Fatal("a partial delete was reported as moved")
	}
}

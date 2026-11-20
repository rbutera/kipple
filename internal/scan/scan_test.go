package scan

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/sizes"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

const kib = 1024

var (
	now    = time.Now()
	old    = now.Add(-200 * 24 * time.Hour).Truncate(time.Second)
	recent = now.Add(-2 * 24 * time.Hour).Truncate(time.Second)
)

// fixtureSpec is a home with at least one item of every tier and every
// listing outcome. Old items have old mtimes on every file and directory.
func fixtureSpec() testutil.Spec {
	var spec testutil.Spec
	file := func(p string, size int64, mt time.Time) {
		spec.Files = append(spec.Files, testutil.File{Path: p, Size: size, MTime: mt})
	}
	dir := func(p string, mt time.Time) { spec.Dirs = append(spec.Dirs, testutil.Dir{Path: p, MTime: mt}) }

	// safe
	file("Library/Caches/com.example.app/blob", 64*kib, old)
	dir("Library/Caches/com.example.app", old)
	file("Library/Caches/com.example.keep/blob", kib, old) // user-protected
	file("Library/Caches/com.apple.bird/state", kib, old)  // excluded
	file("Library/Caches/withenv/.env", 10, old)           // contains_protected
	file("Library/Caches/withenv/data", kib, old)
	file("Library/Caches/partial/ok", 2*kib, old) // partial: locked subdir
	file("Library/Caches/partial/locked/x", kib, old)
	spec.Dirs = append(spec.Dirs, testutil.Dir{Path: "Library/Caches/partial/locked", Unreadable: true})
	// An unreadable subfolder hiding a .env, and one inside an aged rule.
	file("Library/Caches/hiddenenv/ok", kib, old)
	file("Library/Caches/hiddenenv/locked/.env", 10, old)
	spec.Dirs = append(spec.Dirs, testutil.Dir{Path: "Library/Caches/hiddenenv/locked", Unreadable: true})
	file(".claude/debug/partialold/ok.txt", kib, old)
	file(".claude/debug/partialold/locked/x.txt", kib, old)
	spec.Dirs = append(spec.Dirs, testutil.Dir{Path: ".claude/debug/partialold/locked", Unreadable: true})
	dir(".claude/debug/partialold", old)
	file("Library/Logs/app/log.txt", 3*kib, old)
	file("elsewhere/big", 256*kib, old)
	spec.Symlinks = append(spec.Symlinks, testutil.Symlink{Path: "Library/Caches/link", Target: "~/elsewhere"})
	file(".claude/debug/old.txt", kib, old) // min age 7 days
	file(".claude/debug/new.txt", kib, recent)
	// Hard-linked store shared by the pnpm store and a project.
	file("Library/pnpm/store/v3/pkg", 512*kib, old)
	dir("Library/pnpm/store/v3", old)

	// Certificates (*.pem) inside package caches and dependency installs are
	// regenerable and do not block them.
	file(".cache/uv/archive-v0/abc/certifi/cacert.pem", kib, old)
	dir(".cache/uv/archive-v0/abc/certifi", old)
	dir(".cache/uv/archive-v0/abc", old)
	file("dev/py/pyproject.toml", 10, old)
	file("dev/py/.venv/lib/python3.12/site-packages/pip/_vendor/certifi/cacert.pem", kib, old)
	// A worktree's .git is a file. codex.worktrees exempts exactly that
	// top-level pointer file, and requires a clean, pushed checkout (this
	// one is not a real worktree, so it is blocked by its git state).
	file(".codex/worktrees/wt1/.git", 40, old)
	file(".codex/worktrees/wt1/main.go", 10, old)

	// review
	file("go/pkg/mod/github.com/x/testdata/key.pem", 100, old)
	file("go/pkg/mod/github.com/x/y.go", 4*kib, old)
	dir("go/pkg/mod/github.com/x/testdata", old)
	dir("go/pkg/mod/github.com/x", old)
	dir("go/pkg/mod/github.com", old)

	// manual
	file("Downloads/Old.dmg", 8*kib, old)
	file("Downloads/new.pkg", 8*kib, recent)
	file("Downloads/taxes-2025.pdf", kib, old)

	// info
	file(".ollama/models/blobs/sha256-1", 16*kib, old)

	// projects
	file("dev/idle/package.json", 10, old)
	file("dev/idle/src/index.js", 10, old)
	file("dev/idle/node_modules/left-pad/index.js", 2*kib, old)
	file("dev/active/package.json", 10, old)
	file("dev/active/src/index.js", 10, recent)
	file("dev/active/node_modules/x/index.js", kib, old)
	file("dev/withgit/package.json", 10, old)
	file("dev/withgit/node_modules/dep/.git/HEAD", 10, old)
	file("dev/notes/target/f", 10, old)              // no Cargo.toml beside it
	file("dev/node_modules/stray/index.js", 10, old) // artifact directly in a project root
	file("dev/mono/package.json", 10, old)
	file("dev/mono/packages/a/package.json", 10, old)
	file("dev/mono/packages/a/node_modules/z/i.js", 10, old)
	file("Desktop/app/package.json", 10, old)
	file("Desktop/app/node_modules/x/i.js", 10, old)
	spec.Hardlinks = append(spec.Hardlinks, testutil.Hardlink{Path: "dev/idle/node_modules/left-pad/pkg", Target: "Library/pnpm/store/v3/pkg"})
	for _, d := range []string{"dev/idle/src", "dev/idle/node_modules/left-pad", "dev/idle/node_modules", "dev/idle"} {
		dir(d, old)
	}
	return spec
}

func runScan(t *testing.T, home, level string, protect ...string) plan.Plan {
	t.Helper()
	env := policy.Env{Home: home}
	for _, p := range protect {
		env.Protect = append(env.Protect, home+"/"+p)
	}
	p, err := Run(context.Background(), Options{Env: env, Level: level, KippleVersion: "test", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func byRel(t *testing.T, p plan.Plan, home string) map[string]plan.Item {
	t.Helper()
	out := map[string]plan.Item{}
	for _, it := range p.Items {
		out[strings.TrimPrefix(it.Path, home+"/")] = it
	}
	return out
}

func ruleStatus(p plan.Plan, id string) plan.RuleStatus {
	for _, r := range p.Rules {
		if r.ID == id {
			return r
		}
	}
	return plan.RuleStatus{}
}

// Spec scenario "Default level".
func scenarioDefaultLevelSelectsOnlySafe(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	p := runScan(t, home, "")
	if p.Level != rules.LevelSafe {
		t.Fatalf("level = %q", p.Level)
	}
	tierReason := map[string]string{"review": plan.ReasonTierReview, "manual": plan.ReasonTierManual, "info": plan.ReasonTierInfo}
	for _, it := range p.Items {
		if it.Selected && it.Tier != "safe" {
			t.Errorf("%s (%s) selected at level safe", it.Path, it.Tier)
		}
		if want, ok := tierReason[it.Tier]; ok && (it.Selected || !slices.Contains(it.Reasons, want)) {
			t.Errorf("%s: selected=%v reasons=%v, want unselected with %s", it.Path, it.Selected, it.Reasons, want)
		}
	}
	items := byRel(t, p, home)
	if it := items["Library/Caches/com.example.app"]; !it.Selected || len(it.Reasons) != 0 {
		t.Fatalf("safe cache item: %+v", it)
	}
	if p.Totals.SelectedCount == 0 || p.Totals.SelectedBytes == 0 {
		t.Fatalf("totals: %+v", p.Totals)
	}
}

// Spec "Selection levels": review selects review items whose conditions pass.
func TestReviewLevel(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	items := byRel(t, runScan(t, home, rules.LevelReview), home)
	if it := items["go/pkg/mod/github.com"]; !it.Selected {
		t.Fatalf("review item with no conditions should be selected: %+v", it)
	}
	idle := items["dev/idle/node_modules"]
	if idle.Selected || !slices.Equal(idle.Reasons, []string{plan.ReasonGitNotARepo}) || idle.Git == nil || idle.Git.State != plan.GitNotARepo {
		t.Fatalf("idle project artifact: selected=%v reasons=%v git=%+v", idle.Selected, idle.Reasons, idle.Git)
	}
	active := items["dev/active/node_modules"]
	if active.Selected || !slices.Contains(active.Reasons, plan.ReasonProjectActive) {
		t.Fatalf("active project: %+v", active.Reasons)
	}
	if it := items["Downloads/Old.dmg"]; it.Selected || !slices.Equal(it.Reasons, []string{plan.ReasonTierManual}) {
		t.Fatalf("manual item: %+v", it.Reasons)
	}
}

func TestInfoNeverSelected(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	for _, level := range rules.Levels() {
		it := byRel(t, runScan(t, home, level), home)[".ollama/models/blobs"]
		if it.Selected || !slices.Equal(it.Reasons, []string{plan.ReasonTierInfo}) || it.OwnerCommand == "" {
			t.Fatalf("level %s: info item %+v", level, it)
		}
		if it.AllocatedBytes < 16*kib {
			t.Fatalf("info item not measured: %d", it.AllocatedBytes)
		}
	}
}

// Spec scenario "No Xcode installed".
func scenarioAbsentRoots(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	p := runScan(t, home, "")
	for _, id := range []string{"xcode.deriveddata", "xcode.devicesupport", "xcode.archives", "simulator.runtimes", "docker.raw"} {
		if st := ruleStatus(p, id); st.Status != plan.RuleAbsent {
			t.Errorf("%s: status %q, want absent", id, st.Status)
		}
	}
	if st := ruleStatus(p, "caches.user"); st.Status != plan.RulePresent || st.ItemCount == 0 {
		t.Errorf("caches.user: %+v", st)
	}
	if len(p.Rules) != len(rules.Catalogue()) {
		t.Fatalf("rules: %d, want every catalogue rule", len(p.Rules))
	}
	// An empty home is all absent and still succeeds.
	empty := testutil.NewHome(t, testutil.Spec{})
	for _, r := range runScan(t, empty, "").Rules {
		if r.Status != plan.RuleAbsent {
			t.Errorf("empty home: %s is %s", r.ID, r.Status)
		}
	}
}

// Spec scenario "Fixture unchanged".
func scenarioScanIsReadOnly(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	// The locked directory cannot be snapshotted; checksum everything else
	// plus the locked directory's own metadata.
	before := testutil.Checksum(t, home+"/dev") + testutil.Checksum(t, home+"/Library/Logs") + testutil.Checksum(t, home+"/.codex")
	lockedBefore, _ := os.Lstat(home + "/Library/Caches/partial/locked")
	runScan(t, home, rules.LevelReview)
	after := testutil.Checksum(t, home+"/dev") + testutil.Checksum(t, home+"/Library/Logs") + testutil.Checksum(t, home+"/.codex")
	lockedAfter, _ := os.Lstat(home + "/Library/Caches/partial/locked")
	if before != after || !lockedBefore.ModTime().Equal(lockedAfter.ModTime()) {
		t.Fatal("scan changed the fixture")
	}
}

func TestScanIsReadOnly(t *testing.T) { t.Run("Fixture unchanged", scenarioScanIsReadOnly) }

func scenarioScanIsReadOnlyWholeHome(t *testing.T) {
	spec := fixtureSpec()
	spec.Dirs = slices.DeleteFunc(spec.Dirs, func(d testutil.Dir) bool { return d.Unreadable })
	home := testutil.NewHome(t, spec)
	before := testutil.Checksum(t, home)
	runScan(t, home, rules.LevelReview)
	if testutil.Checksum(t, home) != before {
		t.Fatal("scan changed the fixture")
	}
}

// Spec scenario "Two scans, same IDs".
func scenarioStableIDs(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	ids := func() []string {
		var out []string
		for _, it := range runScan(t, home, "").Items {
			out = append(out, it.ID)
			if it.ID != plan.ID(it.Rule, strings.TrimPrefix(it.Path, home+"/")) {
				t.Fatalf("%s: ID is not plan.ID(rule, rel)", it.Path)
			}
			refused := len(it.Reasons) > 0 && strings.HasPrefix(it.Reasons[0], plan.RefusedPrefix)
			if fi, err := os.Lstat(it.Path); err == nil && !refused {
				dev, ino, _ := policy.Identity(fi)
				if it.Dev != dev || it.Ino != ino || ino == 0 {
					t.Fatalf("%s: recorded identity %d/%d, on disk %d/%d", it.Path, it.Dev, it.Ino, dev, ino)
				}
			}
		}
		return out
	}
	if a, b := ids(), ids(); !slices.Equal(a, b) {
		t.Fatalf("IDs differ between scans:\n%v\n%v", a, b)
	}
}

func TestListingOutcomes(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	p := runScan(t, home, "", "Library/Caches/com.example.keep")
	items := byRel(t, p, home)
	for _, rel := range []string{
		"Library/Caches/com.example.keep", // user-protected: listed nowhere
		"Library/Caches/com.apple.bird",   // excluded
		"Downloads/taxes-2025.pdf",        // not an item of the rule
		"dev/notes/target",                // no Cargo.toml beside it
		"dev/node_modules",                // directly in a project root
		"Desktop/app/node_modules",        // outside project roots
	} {
		if _, ok := items[rel]; ok {
			t.Errorf("%s should not be listed", rel)
		}
	}
	link := items["Library/Caches/link"]
	if link.Selected || !slices.Equal(link.Reasons, []string{plan.Refused(policy.ReasonSymlinkInPath)}) || link.AllocatedBytes != 0 {
		t.Errorf("symlink item: %+v", link)
	}
	env := items["Library/Caches/withenv"]
	if env.Selected || !slices.Contains(env.Reasons, plan.ReasonContainsProtected) || !slices.Equal(env.ProtectedPaths, []string{".env"}) {
		t.Errorf("contains .env: %+v", env)
	}
	git := items["dev/withgit/node_modules"]
	if !slices.Contains(git.Reasons, plan.ReasonContainsProtected) || !slices.Equal(git.ProtectedPaths, []string{"dep/.git"}) {
		t.Errorf(".git inside an artifact: %+v", git)
	}
	if _, ok := items["dev/mono/packages/a/node_modules"]; !ok {
		t.Error("nested monorepo package artifact not found")
	}
	if it := items[".claude/debug/new.txt"]; it.Selected || !slices.Equal(it.Reasons, []string{plan.ReasonTooRecent}) {
		t.Errorf("recent debug file: %+v", it)
	}
	if it := items[".claude/debug/old.txt"]; !it.Selected {
		t.Errorf("old debug file: %+v", it)
	}
	if it := items["Downloads/new.pkg"]; !slices.Equal(it.Reasons, []string{plan.ReasonTierManual, plan.ReasonTooRecent}) {
		t.Errorf("recent installer: %+v", it.Reasons)
	}
}

// Spec scenario "Partial scan recorded".
func scenarioPartialItem(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	it := byRel(t, runScan(t, home, ""), home)["Library/Caches/partial"]
	if it.ScanStatus != plan.ScanPartial || it.SizeBytes != 2*kib || len(it.Warnings) == 0 {
		t.Fatalf("partial item: %+v", it)
	}
	if it.Selected || !slices.Equal(it.Reasons, []string{plan.ReasonScanPartial}) {
		t.Fatalf("a partial item must never be pre-selected: selected=%v reasons=%v", it.Selected, it.Reasons)
	}
}

// Spec scenario "Hardlinked store", across two items.
func scenarioHardlinkCountedOncePerScan(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	items := byRel(t, runScan(t, home, rules.LevelReview), home)
	store, proj := items["Library/pnpm/store/v3"], items["dev/idle/node_modules"]
	if store.SizeBytes != 512*kib {
		t.Fatalf("store logical = %d", store.SizeBytes)
	}
	if proj.SizeBytes != 2*kib {
		t.Fatalf("project logical = %d, the shared inode must be counted once (in the store)", proj.SizeBytes)
	}
	if store.ReclaimableBytes >= 512*kib || proj.ReclaimableBytes >= 512*kib {
		t.Fatalf("hard-linked bytes counted as reclaimable: %d %d", store.ReclaimableBytes, proj.ReclaimableBytes)
	}
}

func TestHardlinkCountedOncePerScan(t *testing.T) {
	t.Run("Hardlinked store", scenarioHardlinkCountedOncePerScan)
}

// Spec scenario "Plan validates against schema".
func scenarioPlanValidatesAgainstSchema(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	for _, level := range rules.Levels() {
		var buf bytes.Buffer
		if err := plan.Encode(&buf, runScan(t, home, level)); err != nil {
			t.Fatal(err)
		}
		if err := plantest.Validate("plan", buf.Bytes()); err != nil {
			t.Fatalf("level %s: %v", level, err)
		}
	}
}

func TestPlanValidatesAgainstSchema(t *testing.T) {
	t.Run("Plan validates against schema", scenarioPlanValidatesAgainstSchema)
}

func TestEffectiveProjectRoots(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	rs := EffectiveRules(rules.Catalogue(), []string{"Desktop"})
	p, err := Run(context.Background(), Options{Env: policy.Env{Home: home}, Rules: rs, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	items := byRel(t, p, home)
	if _, ok := items["Desktop/app/node_modules"]; ok {
		t.Fatal("Desktop is denylisted; configured as a project root it must still list nothing")
	}
	if _, ok := items["dev/idle/node_modules"]; ok {
		t.Fatal("dev is no longer a project root")
	}
}

func TestHomeErrors(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{})
	if _, err := Run(context.Background(), Options{Env: policy.Env{Home: home + "-missing"}}); err == nil {
		t.Fatal("missing home must fail")
	}
	if _, err := Run(context.Background(), Options{Env: policy.Env{Home: home}, Level: "everything"}); err == nil {
		t.Fatal("unknown level must fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Options{Env: policy.Env{Home: home}}); err == nil {
		t.Fatal("cancelled scan must fail")
	}
}

// Review finding: regenerable descendants such as certifi's cacert.pem, Go
// test keys and a worktree's .git file must not make an item unselectable.
// Review findings (wave 2): inside package caches and dependency installs
// every protected name except *.pem certificates and .git blocks an item
// (signing keys and SSH keys left in a build folder included); inside every
// other rule's item the whole deny-component list does.
func TestProtectedDescendantsPerRule(t *testing.T) {
	spec := fixtureSpec()
	spec.Files = append(spec.Files,
		testutil.File{Path: "Library/Caches/withenvrc/.envrc", Size: 10, MTime: old},
		testutil.File{Path: "Library/Caches/withkey/certs/server.p12", Size: 10, MTime: old},
		testutil.File{Path: ".npm/_cacache/withkey/fixtures/id_rsa", Size: 10, MTime: old},
		testutil.File{Path: ".npm/_cacache/withenv/config/prod.env", Size: 10, MTime: old},
		testutil.File{Path: "dev/signed/package.json", Size: 10, MTime: old},
		testutil.File{Path: "dev/signed/node_modules/app/build/release.p12", Size: 10, MTime: old},
	)
	home := testutil.NewHome(t, spec)
	items := byRel(t, runScan(t, home, rules.LevelReview), home)
	venv := items["dev/py/.venv"]
	if !slices.Equal(venv.Reasons, []string{plan.ReasonGitNotARepo}) || len(venv.ProtectedPaths) != 0 {
		t.Errorf("venv with cacert.pem: reasons %v protected %v, want only git_not_a_repo", venv.Reasons, venv.ProtectedPaths)
	}
	if uv := items[".cache/uv/archive-v0"]; !uv.Selected {
		t.Errorf("uv cache with cacert.pem not selected: %+v", uv.Reasons)
	}
	if gomod := items["go/pkg/mod/github.com"]; !gomod.Selected {
		t.Errorf("go module cache with testdata/key.pem not selected at review: %+v", gomod.Reasons)
	}
	// Package cache and project artifact: keys and env files still refuse.
	for rel, want := range map[string]string{
		".npm/_cacache/withkey":   "fixtures/id_rsa",
		".npm/_cacache/withenv":   "config/prod.env",
		"dev/signed/node_modules": "app/build/release.p12",
	} {
		it := items[rel]
		if it.Path == "" || it.Selected || !slices.Contains(it.Reasons, plan.ReasonContainsProtected) || !slices.Equal(it.ProtectedPaths, []string{want}) {
			t.Errorf("%s: selected=%v reasons=%v protected=%v", rel, it.Selected, it.Reasons, it.ProtectedPaths)
		}
	}
	// codex.worktrees: the top-level .git pointer file is exempt, but the
	// checkout must be clean and pushed, which this fake one is not.
	wt := items[".codex/worktrees/wt1"]
	if wt.Selected || slices.Contains(wt.Reasons, plan.ReasonContainsProtected) || len(wt.ProtectedPaths) != 0 || wt.Git == nil || wt.Git.State == plan.GitCleanPushed {
		t.Errorf("codex worktree with a .git file: %+v", wt)
	}
	// Not package caches: .envrc and *.p12 inside refuse the item.
	for rel, want := range map[string]string{"Library/Caches/withenvrc": ".envrc", "Library/Caches/withkey": "certs/server.p12"} {
		it := items[rel]
		if it.Selected || !slices.Contains(it.Reasons, plan.ReasonContainsProtected) || !slices.Equal(it.ProtectedPaths, []string{want}) {
			t.Errorf("%s: selected=%v reasons=%v protected=%v", rel, it.Selected, it.Reasons, it.ProtectedPaths)
		}
	}
}

func TestScanIsReadOnlyWholeHome(t *testing.T) {
	t.Run("Fixture unchanged", scenarioScanIsReadOnlyWholeHome)
}

func TestPartialItem(t *testing.T) { t.Run("Partial scan recorded", scenarioPartialItem) }

func TestStableIDs(t *testing.T) { t.Run("Two scans, same IDs", scenarioStableIDs) }

func TestDefaultLevelSelectsOnlySafe(t *testing.T) {
	t.Run("Default level", scenarioDefaultLevelSelectsOnlySafe)
}

func TestAbsentRoots(t *testing.T) { t.Run("No Xcode installed", scenarioAbsentRoots) }

// Red-team finding: any partial walk leaves the item unselected, even when
// the unread part hides a .env, and cannot prove an age condition.
func TestPartialWalksAreNeverSelected(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	items := byRel(t, runScan(t, home, rules.LevelReview), home)
	env := items["Library/Caches/hiddenenv"]
	if env.Selected || env.ScanStatus != plan.ScanPartial || !slices.Equal(env.Reasons, []string{plan.ReasonScanPartial}) {
		t.Errorf("unreadable folder hiding .env: selected=%v status=%s reasons=%v", env.Selected, env.ScanStatus, env.Reasons)
	}
	aged := items[".claude/debug/partialold"]
	if aged.Selected || !slices.Equal(aged.Reasons, []string{plan.ReasonTooRecent, plan.ReasonScanPartial}) {
		t.Errorf("partial walk must not satisfy min age: selected=%v reasons=%v", aged.Selected, aged.Reasons)
	}
}

// Red-team finding: a volume mounted inside an item gets contains_mount and
// is never selected. A real mount is impossible in a test, so the measure
// hook reports one.
func TestMountInsideItem(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	orig := measureDir
	t.Cleanup(func() { measureDir = orig })
	measureDir = func(ctx context.Context, dir *os.File, o sizes.Options) (sizes.Result, error) {
		r, err := orig(ctx, dir, o)
		if strings.HasSuffix(dir.Name(), "/Library/Caches/com.example.app") {
			r.Mounts = append(r.Mounts, "mnt")
			r.Status = sizes.StatusPartial
		}
		return r, err
	}
	it := byRel(t, runScan(t, home, ""), home)["Library/Caches/com.example.app"]
	if it.Selected || !slices.Equal(it.Reasons, []string{plan.ReasonScanPartial, plan.ReasonContainsMount}) {
		t.Fatalf("item with a mount inside: selected=%v reasons=%v", it.Selected, it.Reasons)
	}
}

// Red-team finding: conditions computed from an interrupted walk are not
// satisfied. A mount inside an old debug item fails its minimum age, and a
// project whose source cannot be fully read is not idle.
func TestInterruptedWalksFailConditions(t *testing.T) {
	spec := fixtureSpec()
	spec.Files = append(spec.Files,
		testutil.File{Path: "dev/lockedsrc/package.json", Content: "{}", MTime: old},
		testutil.File{Path: "dev/lockedsrc/src/a.js", Content: "x", MTime: old},
		testutil.File{Path: "dev/lockedsrc/node_modules/x/i.js", Content: "x", MTime: old},
		testutil.File{Path: ".claude/debug/mountedold/log.txt", Content: "x", MTime: old},
	)
	spec.Dirs = append(spec.Dirs, testutil.Dir{Path: ".claude/debug/mountedold", MTime: old})
	spec.Dirs = append(spec.Dirs, testutil.Dir{Path: "dev/lockedsrc/src", Unreadable: true})
	home := testutil.NewHome(t, spec)
	orig := measureDir
	t.Cleanup(func() { measureDir = orig })
	measureDir = func(ctx context.Context, dir *os.File, o sizes.Options) (sizes.Result, error) {
		r, err := orig(ctx, dir, o)
		if strings.HasSuffix(dir.Name(), "/.claude/debug/mountedold") || strings.HasSuffix(dir.Name(), "/Library/Logs/app") {
			r.Mounts = append(r.Mounts, "mnt")
			r.Status = sizes.StatusPartial
		}
		return r, err
	}
	items := byRel(t, runScan(t, home, rules.LevelReview), home)
	logs := items["Library/Logs/app"]
	if logs.Selected || !slices.Equal(logs.Reasons, []string{plan.ReasonScanPartial, plan.ReasonContainsMount}) {
		t.Errorf("mount in a logs item: selected=%v reasons=%v", logs.Selected, logs.Reasons)
	}
	proj := items["dev/lockedsrc/node_modules"]
	if proj.Selected || !slices.Contains(proj.Reasons, plan.ReasonProjectActive) {
		t.Errorf("partly unreadable project must not count as idle: %v", proj.Reasons)
	}
	// An old item under a 7-day minimum age, interrupted by a mount.
	aged := items[".claude/debug/mountedold"]
	if aged.Selected || !slices.Equal(aged.Reasons, []string{plan.ReasonTooRecent, plan.ReasonScanPartial, plan.ReasonContainsMount}) {
		t.Errorf("mount-interrupted walk satisfied min age: selected=%v reasons=%v", aged.Selected, aged.Reasons)
	}
}

// Spec scenario "Recently edited", review finding (wave 2): the idle check
// skipped every folder named like an artifact at any depth, so a recent
// edit inside an unrelated target/ left the project looking idle. Only
// marker-matched artifact directories are skipped now.
func TestIdleCheckSkipsOnlyArtifacts(t *testing.T) {
	f := func(p string, mt time.Time) testutil.File { return testutil.File{Path: p, Content: "x", MTime: mt} }
	spec := testutil.Spec{Files: []testutil.File{
		// A folder merely named target (no Cargo.toml beside it) holds recent work.
		f("dev/plain/package.json", old), f("dev/plain/src/a.js", old),
		f("dev/plain/node_modules/x/i.js", old),
		f("dev/plain/docs/target/notes.md", recent),
		// The same at the project root.
		f("dev/roottarget/package.json", old), f("dev/roottarget/node_modules/x/i.js", old),
		f("dev/roottarget/target/plan.md", recent),
		// A sibling artifact with its marker (a fresh .venv) does not count.
		f("dev/sibling/package.json", old), f("dev/sibling/requirements.txt", old),
		f("dev/sibling/node_modules/x/i.js", old),
		f("dev/sibling/.venv/lib/site.py", recent),
		// A nested install beside its own manifest does not count.
		f("dev/nested/package.json", old), f("dev/nested/node_modules/x/i.js", old),
		f("dev/nested/examples/demo/package.json", old),
		f("dev/nested/examples/demo/node_modules/y/i.js", recent),
		// A marker that is a symlink does not make a sibling an artifact.
		f("dev/symmarker/package.json", old), f("dev/symmarker/node_modules/x/i.js", old),
		f("dev/symmarker/real.toml", old),
		f("dev/symmarker/.venv/lib/site.py", recent),
		// Nor does a marker whose stored name differs in case.
		f("dev/casemarker/package.json", old), f("dev/casemarker/node_modules/x/i.js", old),
		f("dev/casemarker/Cargo.TOML", old),
		f("dev/casemarker/target/notes.md", recent),
	},
		Symlinks: []testutil.Symlink{{Path: "dev/symmarker/pyproject.toml", Target: "~/dev/symmarker/real.toml"}},
	}
	var dirs []testutil.Dir
	for _, d := range []string{
		"dev/plain/src", "dev/plain/node_modules/x", "dev/plain/node_modules", "dev/plain/docs/target", "dev/plain/docs", "dev/plain",
		"dev/roottarget/node_modules/x", "dev/roottarget/node_modules", "dev/roottarget/target", "dev/roottarget",
		"dev/sibling/node_modules/x", "dev/sibling/node_modules", "dev/sibling/.venv/lib", "dev/sibling/.venv", "dev/sibling",
		"dev/nested/node_modules/x", "dev/nested/node_modules", "dev/nested/examples/demo/node_modules/y",
		"dev/nested/examples/demo/node_modules", "dev/nested/examples/demo", "dev/nested/examples", "dev/nested",
		"dev/symmarker/node_modules/x", "dev/symmarker/node_modules", "dev/symmarker/.venv/lib", "dev/symmarker/.venv", "dev/symmarker",
		"dev/casemarker/node_modules/x", "dev/casemarker/node_modules", "dev/casemarker/target", "dev/casemarker",
	} {
		mt := old
		if strings.HasPrefix(d, "dev/plain/docs") || strings.HasPrefix(d, "dev/roottarget/target") || strings.HasPrefix(d, "dev/sibling/.venv") || strings.HasPrefix(d, "dev/nested/examples/demo/node_modules") ||
			strings.HasPrefix(d, "dev/symmarker/.venv") || strings.HasPrefix(d, "dev/casemarker/target") {
			mt = recent
		}
		dirs = append(dirs, testutil.Dir{Path: d, MTime: mt})
	}
	spec.Dirs = dirs
	home := testutil.NewHome(t, spec)
	// The symlink's own mtime is now; age it so only the marker decides.
	tv := []unix.Timeval{unix.NsecToTimeval(old.UnixNano()), unix.NsecToTimeval(old.UnixNano())}
	if err := unix.Lutimes(home+"/dev/symmarker/pyproject.toml", tv); err != nil {
		t.Fatal(err)
	}
	items := byRel(t, runScan(t, home, rules.LevelReview), home)
	for _, tc := range []struct {
		name, rel string
		active    bool
	}{
		{"Recently edited inside a nested folder merely named target", "dev/plain/node_modules", true},
		{"Recently edited inside a root folder merely named target", "dev/roottarget/node_modules", true},
		{"Fresh sibling artifact beside its marker is skipped", "dev/sibling/node_modules", false},
		{"Fresh nested install beside its own manifest is skipped", "dev/nested/node_modules", false},
		{"Recently edited beside a symlinked marker", "dev/symmarker/node_modules", true},
		{"Recently edited beside a wrong-case marker", "dev/casemarker/node_modules", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it, ok := items[tc.rel]
			if !ok {
				t.Fatalf("%s not listed", tc.rel)
			}
			if got := slices.Contains(it.Reasons, plan.ReasonProjectActive); got != tc.active {
				t.Fatalf("%s: project_active=%v, want %v (reasons %v)", tc.rel, got, tc.active, it.Reasons)
			}
		})
	}
}

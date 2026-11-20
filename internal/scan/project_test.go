package scan

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/gitstate"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

// pushedProject is a clean, pushed repository whose node_modules is ignored,
// aged so the project is idle.
func pushedProject(path string, local ...testutil.Commit) testutil.Repo {
	return testutil.Repo{
		Path: path,
		Commits: []testutil.Commit{{Files: []testutil.File{
			{Path: "package.json", Content: "{}"},
			{Path: ".gitignore", Content: "node_modules/\ntarget/\n"},
			{Path: "src/index.js", Content: "x"},
		}}},
		Upstream:     true,
		Pushed:       true,
		LocalCommits: local,
		MTime:        old,
	}
}

func projectHome(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Repos: []testutil.Repo{
			pushedProject("dev/clean"),
			pushedProject("dev/unpushed", testutil.Commit{Files: []testutil.File{{Path: "b.js", Content: "b"}}}),
			pushedProject("dev/dirty"),
			pushedProject("dev/recent"),
		},
		Files: []testutil.File{
			{Path: "dev/clean/node_modules/a/i.js", Content: "a"},
			{Path: "dev/unpushed/node_modules/a/i.js", Content: "a"},
			{Path: "dev/dirty/node_modules/a/i.js", Content: "a"},
			{Path: "dev/recent/node_modules/a/i.js", Content: "a"},
			{Path: "dev/notes/target/f", Content: "f", MTime: old},
			{Path: "dev/rust/Cargo.toml", Content: "[package]", MTime: old},
			{Path: "dev/rust/target/debug/app", Content: "bin", MTime: old},
			{Path: "Desktop/app/package.json", Content: "{}", MTime: old},
			{Path: "Desktop/app/node_modules/x/i.js", Content: "x", MTime: old},
		},
	})
}

// Every scenario of specs/project-artifacts that a scan decides; the
// re-check and running-tool scenarios are apply's (internal/apply).
func TestProjectArtifacts(t *testing.T) {
	home := projectHome(t)
	if err := os.WriteFile(testutil.Abs(home, "dev/dirty/src/index.js"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(testutil.Abs(home, "dev/dirty/src/index.js"), old, old)
	if err := os.Chtimes(testutil.Abs(home, "dev/recent/src/index.js"), recent, recent); err != nil {
		t.Fatal(err)
	}
	items := byRel(t, runScan(t, home, rules.LevelReview), home)

	t.Run("Folder merely named target", func(t *testing.T) {
		if _, ok := items["dev/notes/target"]; ok {
			t.Fatal("a target folder with no Cargo.toml beside it was listed")
		}
		if _, ok := items["dev/rust/target"]; !ok {
			t.Fatal("control: target beside Cargo.toml was not listed")
		}
	})
	t.Run("Project outside roots", func(t *testing.T) {
		for rel := range items {
			if len(rel) >= 8 && rel[:8] == "Desktop/" {
				t.Fatalf("%s listed although Desktop is not a project root", rel)
			}
		}
	})
	t.Run("Recently edited", func(t *testing.T) {
		it := items["dev/recent/node_modules"]
		if it.Selected || !slices.Contains(it.Reasons, plan.ReasonProjectActive) {
			t.Fatalf("selected=%v reasons=%v", it.Selected, it.Reasons)
		}
	})
	t.Run("Unpushed commits", func(t *testing.T) {
		it := items["dev/unpushed/node_modules"]
		if it.Path == "" || it.Selected || it.Git == nil || it.Git.State != plan.GitUnpushed || !it.AtRisk || !slices.Contains(it.Reasons, plan.ReasonGitUnpushed) {
			t.Fatalf("%+v git=%+v", it, it.Git)
		}
	})
	t.Run("Dirty project is at risk", func(t *testing.T) {
		it := items["dev/dirty/node_modules"]
		if it.Selected || it.Git == nil || it.Git.State != plan.GitDirty || !it.AtRisk || !slices.Contains(it.Reasons, plan.ReasonGitDirty) {
			t.Fatalf("%+v git=%+v", it, it.Git)
		}
	})
	t.Run("Idle, clean and pushed project is selected at review", func(t *testing.T) {
		it := items["dev/clean/node_modules"]
		if !it.Selected || len(it.Reasons) != 0 || it.Git == nil || it.Git.State != plan.GitCleanPushed || it.AtRisk {
			t.Fatalf("%+v git=%+v", it, it.Git)
		}
	})
	t.Run("Not a repository", func(t *testing.T) {
		it := items["dev/rust/target"]
		if it.Selected || it.Git == nil || it.Git.State != plan.GitNotARepo || it.AtRisk {
			t.Fatalf("%+v git=%+v", it, it.Git)
		}
	})
}

// Spec scenario "No git": with no git binary (kipple looks only on
// gitstate.FixedPath) every project item is git_unavailable and unselected.
func TestNoGit(t *testing.T) {
	home := projectHome(t)
	noGit := gitstate.Inspector{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }}
	p, err := Run(context.Background(), Options{Env: policy.Env{Home: home}, Level: rules.LevelReview, Now: func() time.Time { return now }, Git: noGit})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range p.Items {
		if it.Tier != string(rules.TierReview) || it.Git == nil {
			continue
		}
		n++
		if it.Selected || it.Git.State != plan.GitUnavailable || !slices.Contains(it.Reasons, plan.ReasonGitUnavailable) {
			t.Errorf("%s: selected=%v git=%+v reasons=%v", it.Path, it.Selected, it.Git, it.Reasons)
		}
	}
	if n < 4 {
		t.Fatalf("only %d project items checked", n)
	}
}

// codex.worktrees: the top-level .git pointer file is exempt only for that
// rule; a .git directory, a nested .git, or a checkout that is not clean
// and pushed still blocks the item.
func TestCodexWorktrees(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Repos: []testutil.Repo{
			{Path: "dev/main", Commits: []testutil.Commit{{Files: []testutil.File{{Path: "a.txt", Content: "a"}}}}, Upstream: true, Pushed: true},
			{Path: ".codex/worktrees/fullclone", Commits: []testutil.Commit{{Files: []testutil.File{{Path: "a.txt", Content: "a"}}}}},
		},
	})
	main := testutil.Abs(home, "dev/main")
	for _, w := range []string{"clean", "dirty", "nested"} {
		wt := testutil.Abs(home, ".codex/worktrees/"+w)
		testutil.Git(t, main, "worktree", "add", "-q", "-b", w, wt)
		testutil.Git(t, wt, "push", "-q", "-u", "origin", w)
	}
	if err := os.WriteFile(testutil.Abs(home, ".codex/worktrees/dirty/a.txt"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(testutil.Abs(home, ".codex/worktrees/nested/vendor/lib/.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	items := byRel(t, runScan(t, home, rules.LevelReview), home)

	t.Run("clean and pushed worktree: pointer file exempt", func(t *testing.T) {
		it := items[".codex/worktrees/clean"]
		if !slices.Equal(it.Reasons, []string{plan.ReasonTierManual}) || len(it.ProtectedPaths) != 0 || it.Git == nil || it.Git.State != plan.GitCleanPushed {
			t.Fatalf("reasons=%v protected=%v git=%+v", it.Reasons, it.ProtectedPaths, it.Git)
		}
	})
	t.Run("dirty worktree is blocked and at risk", func(t *testing.T) {
		it := items[".codex/worktrees/dirty"]
		if !slices.Contains(it.Reasons, plan.ReasonGitDirty) || !it.AtRisk || slices.Contains(it.Reasons, plan.ReasonContainsProtected) {
			t.Fatalf("reasons=%v at_risk=%v", it.Reasons, it.AtRisk)
		}
	})
	t.Run("nested .git still blocks", func(t *testing.T) {
		it := items[".codex/worktrees/nested"]
		if !slices.Contains(it.Reasons, plan.ReasonContainsProtected) || !slices.Equal(it.ProtectedPaths, []string{"vendor/lib/.git"}) {
			t.Fatalf("reasons=%v protected=%v", it.Reasons, it.ProtectedPaths)
		}
	})
	t.Run("a .git directory at the top still blocks", func(t *testing.T) {
		it := items[".codex/worktrees/fullclone"]
		if !slices.Contains(it.Reasons, plan.ReasonContainsProtected) || !slices.Equal(it.ProtectedPaths, []string{".git"}) {
			t.Fatalf("reasons=%v protected=%v", it.Reasons, it.ProtectedPaths)
		}
	})
	t.Run("the exemption is for codex.worktrees only", func(t *testing.T) {
		h2 := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
			{Path: "Library/Caches/gitfile/.git", Content: "gitdir: /elsewhere", MTime: old},
			{Path: "Library/Caches/gitfile/x", Content: "x", MTime: old},
		}})
		it := byRel(t, runScan(t, h2, ""), h2)["Library/Caches/gitfile"]
		if it.Selected || !slices.Equal(it.ProtectedPaths, []string{".git"}) {
			t.Fatalf("caches.user item with a .git file: %+v", it)
		}
	})
}

// Rule roots are recorded with their identity so apply can refuse an item
// whose root was replaced (wave 2 red-team note).
func TestRuleRootIdentities(t *testing.T) {
	home := testutil.NewHome(t, fixtureSpec())
	p := runScan(t, home, "")
	fi, err := os.Lstat(testutil.Abs(home, "Library/Caches"))
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, _ := policy.Identity(fi)
	if got := ruleStatus(p, "caches.user").Roots; !slices.Equal(got, []plan.RootIdentity{{Path: "Library/Caches", Dev: dev, Ino: ino}}) {
		t.Fatalf("caches.user roots %+v, want Library/Caches (%d, %d)", got, dev, ino)
	}
	if got := ruleStatus(p, "project.node-modules").Roots; len(got) != 1 || got[0].Path != "dev" {
		t.Fatalf("project roots %+v", got)
	}
	if got := ruleStatus(p, "xcode.deriveddata").Roots; len(got) != 0 {
		t.Fatalf("absent rule has roots %+v", got)
	}
}

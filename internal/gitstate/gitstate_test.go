package gitstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

func commit(files ...string) testutil.Commit {
	var c testutil.Commit
	for _, f := range files {
		c.Files = append(c.Files, testutil.File{Path: f, Content: f})
	}
	return c
}

func fixture(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Repos: []testutil.Repo{
			{Path: "dev/clean", Commits: []testutil.Commit{commit("a.txt", "sub/b.txt")}, Upstream: true, Pushed: true},
			{Path: "dev/dirty", Commits: []testutil.Commit{commit("a.txt")}, Upstream: true, Pushed: true},
			{Path: "dev/untracked", Commits: []testutil.Commit{commit("a.txt")}, Upstream: true, Pushed: true},
			{Path: "dev/unpushed", Commits: []testutil.Commit{commit("a.txt")}, Upstream: true, Pushed: true, LocalCommits: []testutil.Commit{commit("b.txt")}},
			{Path: "dev/noupstream", Commits: []testutil.Commit{commit("a.txt")}},
			{Path: "dev/detached", Commits: []testutil.Commit{commit("a.txt"), commit("b.txt")}, Upstream: true, Pushed: true},
		},
		Files: []testutil.File{
			{Path: "dev/plain/file.txt", Content: "x"},
			{Path: "dev/untracked/new.txt", Content: "new"},
		},
	})
}

func inspector(home string) Inspector { return Inspector{Ceiling: filepath.Dir(home)} }

// One test per git state (task 3.3), including a bare upstream for
// clean_pushed and a PATH without git for git_unavailable.
func TestInspectStates(t *testing.T) {
	home := fixture(t)
	if err := os.WriteFile(filepath.Join(home, "dev/dirty/a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Join(home, "dev/detached"), "checkout", "-q", "--detach", "HEAD~1")
	in := inspector(home)
	cases := []struct {
		name, dir, want string
		ahead           int
	}{
		{"clean and pushed", "dev/clean", StateCleanPushed, 0},
		{"modified file", "dev/dirty", StateDirty, 0},
		{"untracked file", "dev/untracked", StateDirty, 0},
		{"one commit ahead", "dev/unpushed", StateUnpushed, 1},
		{"no upstream", "dev/noupstream", StateNoUpstream, 0},
		{"detached head", "dev/detached", StateNoUpstream, 0},
		{"not a repo", "dev/plain", StateNotARepo, 0},
		{"subfolder of a clean repo", "dev/clean/sub", StateCleanPushed, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := in.Inspect(context.Background(), filepath.Join(home, tc.dir))
			if r.State != tc.want || r.Ahead != tc.ahead {
				t.Fatalf("Inspect(%s) = %+v, want %s ahead %d", tc.dir, r, tc.want, tc.ahead)
			}
		})
	}
	t.Run("git not on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		r := in.Inspect(context.Background(), filepath.Join(home, "dev/clean"))
		if r.State != StateUnavailable {
			t.Fatalf("state %+v, want %s", r, StateUnavailable)
		}
	})
}

// The ceiling keeps git from finding a repository above home.
func TestCeilingStopsDiscovery(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "dev/p/f", Content: "x"}}})
	testutil.Git(t, filepath.Dir(home), "init", "-q")
	if r := inspector(home).Inspect(context.Background(), filepath.Join(home, "dev/p")); r.State != StateNotARepo {
		t.Fatalf("state %+v, want %s", r, StateNotARepo)
	}
}

// Inspect never runs a repository's fsmonitor or hooks and never writes
// to the repository (GIT_OPTIONAL_LOCKS=0).
func TestInspectRunsNothingAndWritesNothing(t *testing.T) {
	home := fixture(t)
	repo := filepath.Join(home, "dev/clean")
	marker := filepath.Join(filepath.Dir(home), "ran")
	hook := filepath.Join(filepath.Dir(home), "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "config", "core.fsmonitor", hook)
	testutil.Git(t, repo, "config", "core.hooksPath", filepath.Dir(hook))
	// Make files racily clean so status would want to refresh the index.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(repo, "a.txt"), now, now)
	before := testutil.Checksum(t, repo)
	r := inspector(home).Inspect(context.Background(), repo)
	if r.State != StateCleanPushed {
		t.Fatalf("state %+v", r)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git ran the repository's fsmonitor or hook")
	}
	if after := testutil.Checksum(t, repo); after != before {
		t.Fatal("Inspect changed the repository")
	}
}

func TestTimeout(t *testing.T) {
	in := Inspector{
		LookPath: func(string) (string, error) { return "/usr/bin/git", nil },
		Timeout:  20 * time.Millisecond,
		Run: func(ctx context.Context, _ string, _ []string, _ ...string) ([]byte, []byte, error) {
			<-ctx.Done()
			return nil, nil, ctx.Err()
		},
	}
	r := in.Inspect(context.Background(), "/nowhere")
	if r.State != StateUnavailable {
		t.Fatalf("state %+v, want %s", r, StateUnavailable)
	}
}

func TestSafetyArgumentsAndEnvironment(t *testing.T) {
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	var gotArgs, gotEnv []string
	in := Inspector{
		LookPath: func(string) (string, error) { return "/usr/bin/git", nil },
		Ceiling:  "/fx",
		Run: func(_ context.Context, _ string, env []string, args ...string) ([]byte, []byte, error) {
			gotArgs, gotEnv = args, env
			return []byte("# branch.oid abc\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n"), nil, nil
		},
	}
	if r := in.Inspect(context.Background(), "/fx/home/dev/p"); r.State != StateCleanPushed || r.Branch != "main" {
		t.Fatalf("state %+v", r)
	}
	want := []string{"-c", "core.fsmonitor=", "-c", "core.hooksPath=/dev/null"}
	for i, w := range want {
		if gotArgs[i] != w {
			t.Fatalf("args %v do not start with %v", gotArgs, want)
		}
	}
	has := map[string]bool{}
	for _, kv := range gotEnv {
		has[kv] = true
		for _, bad := range []string{"GIT_DIR=", "GIT_CONFIG_COUNT=", "GIT_CONFIG_KEY_0="} {
			if len(kv) >= len(bad) && kv[:len(bad)] == bad {
				t.Errorf("environment kept %s", kv)
			}
		}
	}
	for _, w := range []string{"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_CEILING_DIRECTORIES=/fx"} {
		if !has[w] {
			t.Errorf("environment lacks %s", w)
		}
	}
}

func TestParseStatus(t *testing.T) {
	cases := map[string]struct{ out, want string }{
		"upstream gone":   {"# branch.oid a\n# branch.head main\n# branch.upstream origin/main\n", StateNoUpstream},
		"behind only":     {"# branch.oid a\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -3\n", StateCleanPushed},
		"no commits":      {"# branch.oid (initial)\n# branch.head main\n", StateNoUpstream},
		"rename entry":    {"# branch.head main\n# branch.upstream o/m\n# branch.ab +0 -0\n2 R. N... 100644 100644 100644 a b R100 new\told\n", StateDirty},
		"ahead and dirty": {"# branch.head main\n# branch.upstream o/m\n# branch.ab +2 -0\n? x\n", StateDirty},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if r := parseStatus([]byte(tc.out)); r.State != tc.want {
				t.Fatalf("parseStatus = %+v, want %s", r, tc.want)
			}
		})
	}
}

func TestWorktrees(t *testing.T) {
	home := fixture(t)
	repo := filepath.Join(home, "dev/clean")
	wt := filepath.Join(home, ".codex/worktrees/w1")
	testutil.Git(t, repo, "worktree", "add", "-q", "-b", "task", wt)
	list, err := inspector(home).Worktrees(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Path != repo || list[1].Path != wt || list[1].Branch != "task" {
		t.Fatalf("worktrees %+v", list)
	}
	// A linked worktree with a fresh branch has no upstream yet.
	if r := inspector(home).Inspect(context.Background(), wt); r.State != StateNoUpstream {
		t.Fatalf("worktree state %+v", r)
	}
	if _, err := inspector(home).Worktrees(context.Background(), filepath.Join(home, "dev/plain")); !errors.Is(err, errNotARepo) {
		t.Fatalf("err = %v, want not a repo", err)
	}
}

package gitstate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
			{Path: "dev/staged", Commits: []testutil.Commit{commit("a.txt")}, Upstream: true, Pushed: true},
			{Path: "dev/deleted", Commits: []testutil.Commit{commit("a.txt", "b.txt")}, Upstream: true, Pushed: true},
			{Path: "dev/unpushed", Commits: []testutil.Commit{commit("a.txt")}, Upstream: true, Pushed: true, LocalCommits: []testutil.Commit{commit("b.txt")}},
			{Path: "dev/noupstream", Commits: []testutil.Commit{commit("a.txt")}},
			{Path: "dev/detached", Commits: []testutil.Commit{commit("a.txt"), commit("b.txt")}, Upstream: true, Pushed: true},
			{Path: "dev/empty"},
		},
		Files: []testutil.File{
			{Path: "dev/plain/file.txt", Content: "x"},
			{Path: "dev/untracked/new.txt", Content: "new"},
		},
	})
}

func inspector(home string) Inspector { return Inspector{Ceiling: filepath.Dir(home)} }

// One test per git state (task 3.3), including a bare upstream for
// clean_pushed and no git for git_unavailable.
func TestInspectStates(t *testing.T) {
	home := fixture(t)
	if err := os.WriteFile(filepath.Join(home, "dev/dirty/a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "dev/staged/c.txt"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Join(home, "dev/staged"), "add", "c.txt")
	if err := os.Remove(filepath.Join(home, "dev/deleted/b.txt")); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Join(home, "dev/detached"), "checkout", "-q", "--detach", "HEAD~1")
	in := inspector(home)
	cases := []struct {
		name, dir, want string
		ahead           int
	}{
		{"clean and pushed", "dev/clean", StateCleanPushed, 0},
		{"subfolder of a clean repo", "dev/clean/sub", StateCleanPushed, 0},
		{"modified file", "dev/dirty", StateDirty, 0},
		{"untracked file", "dev/untracked", StateDirty, 0},
		{"staged file", "dev/staged", StateDirty, 0},
		{"deleted file", "dev/deleted", StateDirty, 0},
		{"one commit ahead", "dev/unpushed", StateUnpushed, 1},
		{"no upstream", "dev/noupstream", StateNoUpstream, 0},
		{"detached head", "dev/detached", StateNoUpstream, 0},
		{"no commits yet", "dev/empty", StateNoUpstream, 0},
		{"not a repo", "dev/plain", StateNotARepo, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := in.Inspect(context.Background(), filepath.Join(home, tc.dir))
			if r.State != tc.want || r.Ahead != tc.ahead {
				t.Fatalf("Inspect(%s) = %+v, want %s ahead %d", tc.dir, r, tc.want, tc.ahead)
			}
		})
	}
	t.Run("git not available", func(t *testing.T) {
		in := in
		in.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
		if r := in.Inspect(context.Background(), filepath.Join(home, "dev/clean")); r.State != StateUnavailable {
			t.Fatalf("state %+v, want %s", r, StateUnavailable)
		}
	})
	t.Run("upstream gone", func(t *testing.T) {
		testutil.Git(t, filepath.Join(home, "dev/clean"), "update-ref", "-d", "refs/remotes/origin/main")
		if r := in.Inspect(context.Background(), filepath.Join(home, "dev/clean")); r.State != StateNoUpstream {
			t.Fatalf("state %+v, want %s", r, StateNoUpstream)
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

// armFilters configures, after the repository was built, a clean/smudge
// filter and a process filter that each create a canary file, and makes
// every tracked file's timestamp differ from the index so git would have to
// read (and filter) contents to compare them.
func armFilters(t *testing.T, repo, canary string) {
	t.Helper()
	cmd := "touch " + canary + "; cat"
	testutil.Git(t, repo, "config", "filter.canary.clean", cmd)
	testutil.Git(t, repo, "config", "filter.canary.smudge", cmd)
	testutil.Git(t, repo, "config", "filter.canary.required", "true")
	testutil.Git(t, repo, "config", "filter.proc.process", "touch "+canary)
	later := time.Now().Add(time.Minute)
	_ = filepath.Walk(repo, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() && !strings.Contains(p, "/.git/") {
			_ = os.Chtimes(p, later, later)
		}
		return nil
	})
}

// Red-team finding: a repository-configured filter driver must never run,
// whatever the state.
func TestFiltersNeverRun(t *testing.T) {
	attrs := testutil.File{Path: ".gitattributes", Content: "*.txt filter=canary\n*.js filter=proc\n"}
	repo := func(path string, local bool) testutil.Repo {
		r := testutil.Repo{Path: path, Commits: []testutil.Commit{{Files: []testutil.File{attrs, {Path: "a.txt", Content: "a"}, {Path: "b.js", Content: "b"}}}}, Upstream: path != "dev/noupstream", Pushed: path != "dev/noupstream"}
		if local {
			r.LocalCommits = []testutil.Commit{commit("c.txt")}
		}
		return r
	}
	home := testutil.NewHome(t, testutil.Spec{Repos: []testutil.Repo{
		repo("dev/clean", false), repo("dev/dirty", false), repo("dev/unpushed", true), repo("dev/noupstream", false),
	}})
	canary := filepath.Join(filepath.Dir(home), "canary")
	for _, r := range []string{"dev/clean", "dev/dirty", "dev/unpushed", "dev/noupstream"} {
		armFilters(t, filepath.Join(home, r), canary)
	}
	if err := os.WriteFile(filepath.Join(home, "dev/dirty/a.txt"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Control: plain git status runs the filter, so the fixture is armed.
	_ = exec.Command("git", "-C", filepath.Join(home, "dev/clean"), "status", "--porcelain").Run()
	if _, err := os.Stat(canary); err != nil {
		t.Fatal("control failed: git status did not run the planted filter")
	}
	if err := os.Remove(canary); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"dev/clean": StateCleanPushed, "dev/dirty": StateDirty, "dev/unpushed": StateUnpushed, "dev/noupstream": StateNoUpstream}
	for dir, state := range want {
		if r := inspector(home).Inspect(context.Background(), filepath.Join(home, dir)); r.State != state {
			t.Errorf("%s: %+v, want %s", dir, r, state)
		}
		if _, err := os.Stat(canary); err == nil {
			t.Fatalf("classifying %s ran a filter", dir)
		}
	}
	if _, err := inspector(home).Worktrees(context.Background(), filepath.Join(home, "dev/clean")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(canary); err == nil {
		t.Fatal("listing worktrees ran a filter")
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
	now := time.Now()
	_ = os.Chtimes(filepath.Join(repo, "a.txt"), now, now)
	before := testutil.Checksum(t, repo)
	if r := inspector(home).Inspect(context.Background(), repo); r.State != StateCleanPushed {
		t.Fatalf("state %+v", r)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git ran the repository's fsmonitor or hook")
	}
	if after := testutil.Checksum(t, repo); after != before {
		t.Fatal("Inspect changed the repository")
	}
}

// A program named git earlier on the caller's PATH is never run.
func TestFakeGitOnPATHIsIgnored(t *testing.T) {
	home := fixture(t)
	dir := t.TempDir()
	canary := filepath.Join(dir, "ran")
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\ntouch "+canary+"\necho true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	if r := inspector(home).Inspect(context.Background(), filepath.Join(home, "dev/clean")); r.State != StateCleanPushed {
		t.Fatalf("state %+v", r)
	}
	if _, err := os.Stat(canary); err == nil {
		t.Fatal("the git found first on PATH was run")
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
	if r := in.Inspect(context.Background(), "/nowhere"); r.State != StateUnavailable {
		t.Fatalf("state %+v, want %s", r, StateUnavailable)
	}
}

// git runs with the safety flags and an environment built from nothing.
func TestSafetyArgumentsAndEnvironment(t *testing.T) {
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("SECRET_TOKEN", "x")
	var calls [][]string
	var env []string
	in := Inspector{
		LookPath: func(string) (string, error) { return "/usr/bin/git", nil },
		Ceiling:  "/fx",
		Run: func(_ context.Context, _ string, e []string, args ...string) ([]byte, []byte, error) {
			calls, env = append(calls, args), e
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "--is-inside-work-tree"):
				return []byte("true\n"), nil, nil
			case strings.Contains(joined, "--get-regexp"):
				return []byte("filter.lfs.clean git-lfs clean -- %f\nfilter.lfs.process git-lfs filter-process\n"), nil, nil
			case strings.Contains(joined, "symbolic-ref"):
				return []byte("main\n"), nil, nil
			case strings.Contains(joined, "@{upstream}") && strings.Contains(joined, "rev-list"):
				return []byte("0\n"), nil, nil
			case strings.Contains(joined, "@{upstream}"):
				return []byte("origin/main\n"), nil, nil
			}
			return nil, nil, nil
		},
	}
	if r := in.Inspect(context.Background(), "/fx/home/dev/p"); r.State != StateCleanPushed || r.Branch != "main" {
		t.Fatalf("state %+v", r)
	}
	for _, args := range calls {
		if args[0] != "-c" || args[1] != "core.fsmonitor=" || args[3] != "core.hooksPath=/dev/null" {
			t.Fatalf("args %v lack the safety flags", args)
		}
	}
	lsFiles := strings.Join(calls[2], " ")
	for _, want := range []string{"filter.lfs.clean=", "filter.lfs.smudge=", "filter.lfs.process=", "filter.lfs.required=false", "ls-files"} {
		if !strings.Contains(lsFiles, want) {
			t.Errorf("ls-files call %q lacks %q", lsFiles, want)
		}
	}
	allowed := map[string]bool{"PATH": true, "HOME": true, "XDG_CONFIG_HOME": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_NOSYSTEM": true,
		"GIT_OPTIONAL_LOCKS": true, "GIT_TERMINAL_PROMPT": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true, "LC_ALL": true, "LANG": true, "GIT_CEILING_DIRECTORIES": true}
	has := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if !allowed[k] {
			t.Errorf("environment carries %s", kv)
		}
		has[k] = v
	}
	if has["PATH"] != FixedPath || has["GIT_CONFIG_GLOBAL"] != os.DevNull || has["GIT_OPTIONAL_LOCKS"] != "0" || has["GIT_CEILING_DIRECTORIES"] != "/fx" {
		t.Errorf("environment %v", env)
	}
	if fi, err := os.Stat(has["HOME"]); err == nil || !os.IsNotExist(err) {
		t.Errorf("temporary HOME %s was not removed (%v)", has["HOME"], fi)
	}
}

func TestUnsafeFilterNameIsUnavailable(t *testing.T) {
	in := Inspector{
		LookPath: func(string) (string, error) { return "/usr/bin/git", nil },
		Run: func(_ context.Context, _ string, _ []string, args ...string) ([]byte, []byte, error) {
			if strings.Contains(strings.Join(args, " "), "--get-regexp") {
				return []byte("filter.a=b.clean evil\n"), nil, nil
			}
			return []byte("true\n"), nil, nil
		},
	}
	if r := in.Inspect(context.Background(), "/fx"); r.State != StateUnavailable {
		t.Fatalf("state %+v", r)
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
	if r := inspector(home).Inspect(context.Background(), wt); r.State != StateNoUpstream {
		t.Fatalf("worktree state %+v", r)
	}
	if _, err := inspector(home).Worktrees(context.Background(), filepath.Join(home, "dev/plain")); !errors.Is(err, errNotARepo) {
		t.Fatalf("err = %v, want not a repo", err)
	}
}

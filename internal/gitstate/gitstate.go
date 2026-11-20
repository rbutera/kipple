// Package gitstate classifies a project's git state (design D10) by running
// git read-only, with plumbing commands only:
//
//	git rev-parse --is-inside-work-tree           is it a repository
//	git config --get-regexp ^filter\.             which filter drivers exist
//	git ls-files -m -d -o --exclude-standard      worktree changes, untracked
//	git diff-index --cached --quiet HEAD          staged changes (index vs tree)
//	git symbolic-ref -q --short HEAD              branch, or detached
//	git rev-parse ... @{upstream}                 upstream configured
//	git rev-list --count @{upstream}..HEAD        unpushed commits
//
// Every command runs with core.fsmonitor and core.hooksPath disabled, and
// with every filter driver the repository's config defines (clean, smudge
// and process) set to empty, which git treats as no filter: ls-files may
// compare file contents when timestamps changed, and must never run a
// configured filter to do it. No command here produces a diff, so no
// textconv or external diff driver is invoked. Hooks, fsmonitor, filters
// and lazy fetch are disabled; global and system config are not read;
// PATH and the environment are fixed.
//
// Partial clones never fetch: GIT_NO_LAZY_FETCH=1 and protocol.allow=never
// turn a missing object into an error, and a repository configured as a
// partial clone (extensions.partialClone or a promisor remote) is
// git_unavailable outright, since its state cannot be proven without
// fetching.
//
// git runs from a fixed PATH (FixedPath, never the caller's), with an
// environment built from nothing: HOME and XDG_CONFIG_HOME point at an
// empty temporary folder, GIT_CONFIG_GLOBAL=/dev/null and
// GIT_CONFIG_NOSYSTEM=1 (so only the repository's own config is read),
// GIT_OPTIONAL_LOCKS=0 (the index is never rewritten), no prompts, and an
// optional GIT_CEILING_DIRECTORIES. The whole classification has a 5-second
// timeout. Only StateCleanPushed satisfies a rule's RequiresCleanGit
// condition; every other state, including errors and timeouts, blocks.
//
// Limits: changes inside a submodule's own working tree are not seen (a
// submodule whose checked-out commit differs is), and ignoring files
// through a global excludes file no longer applies, so such files count as
// untracked (dirty), which errs towards keeping the project.
package gitstate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// States. The values are the plan's git.state values.
const (
	StateCleanPushed = "clean_pushed"
	StateDirty       = "dirty"
	StateUnpushed    = "unpushed"
	StateNoUpstream  = "no_upstream"
	StateNotARepo    = "not_a_repo"
	StateUnavailable = "git_unavailable"
)

// States returns every state.
func States() []string {
	return []string{StateCleanPushed, StateDirty, StateUnpushed, StateNoUpstream, StateNotARepo, StateUnavailable}
}

// DefaultTimeout bounds one classification (every git command it runs).
const DefaultTimeout = 5 * time.Second

// FixedPath is where git is looked up and the PATH git runs with. The
// caller's PATH is never used, so a program named git earlier on it cannot
// answer for the repository.
const FixedPath = "/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin"

// Result is a repository's state.
type Result struct {
	State string
	// Ahead counts commits on HEAD that its upstream does not have.
	Ahead int
	// Branch is the checked-out branch ("" when detached or unknown).
	Branch string
	// Detail explains a non-clean state for people (stderr, "timed out").
	Detail string
}

// Runner runs git with args in env and returns its stdout and stderr. A
// non-zero exit returns an error with an ExitCode method.
type Runner func(ctx context.Context, git string, env []string, args ...string) (stdout, stderr []byte, err error)

// Inspector runs git. The zero value finds git on FixedPath and runs it
// with DefaultTimeout.
type Inspector struct {
	// LookPath finds the git binary (searching FixedPath when nil).
	LookPath func(string) (string, error)
	// Run executes git (os/exec when nil).
	Run Runner
	// Timeout bounds each classification (DefaultTimeout when zero).
	Timeout time.Duration
	// Ceiling, when set, is passed as GIT_CEILING_DIRECTORIES so git never
	// discovers a repository above it (kipple sets it to home's parent).
	Ceiling string
}

// baseArgs disable everything in a repository's config that would run a
// program during these commands, apart from filter drivers, which are
// disabled by name (filterOverrides).
//
// Partial clones fetch missing objects on demand ("lazy fetch") through
// the repository's remote, which can run its core.sshCommand or reach the
// network: GIT_NO_LAZY_FETCH=1 and protocol.allow=never forbid every
// transport, so a missing object is an error (git_unavailable), never a
// fetch.
var baseArgs = []string{
	"-c", "core.fsmonitor=",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.untrackedCache=false",
	"-c", "protocol.allow=never",
	"-c", "fetch.recurseSubmodules=no",
	"-c", "uploadpack.allowAnySHA1InWant=false",
	"--no-pager",
}

// lookFixed finds name in FixedPath.
func lookFixed(name string) (string, error) {
	for _, dir := range filepath.SplitList(FixedPath) {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in %s: %w", name, FixedPath, exec.ErrNotFound)
}

// ErrUnavailable: git is not available or could not run.
var ErrUnavailable = errors.New("git is not available")

// errNotARepo: git says dir is not inside a repository.
var errNotARepo = errors.New("not a git repository")

// gitCall runs git commands for one classification.
type gitCall struct {
	in        Inspector
	ctx       context.Context
	bin       string
	env       []string
	dir       string
	overrides []string
}

// exitError is implemented by *exec.ExitError.
type exitError interface{ ExitCode() int }

// start prepares a classification of dir: finds git, creates the empty
// HOME, and builds the environment. done removes the HOME.
func (in Inspector) start(ctx context.Context, dir string) (*gitCall, func(), error) {
	look := in.LookPath
	if look == nil {
		look = lookFixed
	}
	bin, err := look("git")
	if err != nil {
		return nil, func() {}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	home, err := os.MkdirTemp("", "kipple-git-home-")
	if err != nil {
		return nil, func() {}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	env := []string{
		"PATH=" + FixedPath,
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + home,
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"LC_ALL=C",
		"LANG=C",
	}
	if in.Ceiling != "" {
		env = append(env, "GIT_CEILING_DIRECTORIES="+in.Ceiling)
	}
	return &gitCall{in: in, ctx: ctx, bin: bin, env: env, dir: dir}, func() { _ = os.RemoveAll(home) }, nil
}

// run runs one git command and returns stdout and the exit code. A timeout,
// a failure to start, or an unexpected exit code is an error.
func (g *gitCall) run(okCodes []int, args ...string) ([]byte, int, error) {
	run := g.in.Run
	if run == nil {
		run = execRunner
	}
	full := append(append(append(append([]string(nil), baseArgs...), g.overrides...), "-C", g.dir), args...)
	out, stderr, err := run(g.ctx, g.bin, g.env, full...)
	if g.ctx.Err() != nil {
		return nil, -1, fmt.Errorf("%w: timed out", ErrUnavailable)
	}
	code := 0
	if err != nil {
		var ee exitError
		if !errors.As(err, &ee) {
			return nil, -1, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		code = ee.ExitCode()
	}
	for _, c := range append([]int{0}, okCodes...) {
		if c == code {
			return out, code, nil
		}
	}
	msg := strings.TrimSpace(string(stderr))
	if strings.Contains(msg, "not a git repository") {
		return nil, code, fmt.Errorf("%w: %s", errNotARepo, msg)
	}
	if msg == "" {
		msg = fmt.Sprintf("git %s exited %d", args[0], code)
	}
	return nil, code, fmt.Errorf("%w: %s", ErrUnavailable, msg)
}

func execRunner(ctx context.Context, git string, env []string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = "/"
	cmd.Env = env
	cmd.WaitDelay = time.Second
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	return out.Bytes(), stderr.Bytes(), err
}

// filterOverrides reads the filter drivers the repository's config defines
// and returns -c arguments that set each one's clean, smudge and process
// commands to empty (no filter) and required to false.
func (g *gitCall) filterOverrides() error {
	out, _, err := g.run([]int{1}, "config", "--get-regexp", `^filter\.`)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, _, _ := strings.Cut(sc.Text(), " ")
		rest := strings.TrimPrefix(key, "filter.")
		i := strings.LastIndex(rest, ".")
		if i <= 0 {
			continue
		}
		name := rest[:i]
		if seen[name] {
			continue
		}
		seen[name] = true
		if strings.ContainsAny(name, "=\n\x00") {
			return fmt.Errorf("%w: the repository defines a filter %q that cannot be disabled safely", ErrUnavailable, name)
		}
		for _, k := range []string{"clean", "smudge", "process"} {
			g.overrides = append(g.overrides, "-c", "filter."+name+"."+k+"=")
		}
		g.overrides = append(g.overrides, "-c", "filter."+name+".required=false")
	}
	return sc.Err()
}

// Inspect classifies the repository containing dir.
func (in Inspector) Inspect(ctx context.Context, dir string) Result {
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	g, done, err := in.start(ctx, dir)
	defer done()
	if err != nil {
		return failed(err)
	}
	out, _, err := g.run(nil, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return failed(err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return Result{State: StateNotARepo, Detail: "not inside a working tree"}
	}
	// A partial clone may be missing objects that only a fetch could
	// supply, so its state cannot be proven; it is never clean_pushed.
	if out, _, err := g.run([]int{1}, "config", "--get-regexp", `^extensions\.partialclone$|^remote\..*\.promisor$`); err != nil {
		return failed(err)
	} else if len(bytes.TrimSpace(out)) > 0 {
		return Result{State: StateUnavailable, Detail: "partial clone: kipple cannot prove its state without fetching missing objects"}
	}
	if err := g.filterOverrides(); err != nil {
		return failed(err)
	}
	out, _, err = g.run(nil, "ls-files", "-z", "-m", "-d", "-o", "--exclude-standard", "--directory", "--no-empty-directory")
	if err != nil {
		return failed(err)
	}
	if n := bytes.Count(out, []byte{0}); n > 0 {
		return Result{State: StateDirty, Detail: fmt.Sprintf("%d modified, deleted or untracked paths", n)}
	}
	if _, code, err := g.run([]int{1}, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		return failed(err)
	} else if code == 1 {
		staged, _, err := g.run(nil, "ls-files", "-z")
		if err != nil {
			return failed(err)
		}
		if len(staged) > 0 {
			return Result{State: StateDirty, Detail: "files staged before the first commit"}
		}
		return Result{State: StateNoUpstream, Detail: "no commits yet"}
	}
	if _, code, err := g.run([]int{1}, "diff-index", "--cached", "--quiet", "HEAD", "--"); err != nil {
		return failed(err)
	} else if code == 1 {
		return Result{State: StateDirty, Detail: "staged changes not committed"}
	}
	out, code, err := g.run([]int{1}, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return failed(err)
	}
	if code == 1 {
		return Result{State: StateNoUpstream, Detail: "HEAD is detached"}
	}
	r := Result{Branch: strings.TrimSpace(string(out))}
	out, code, err = g.run([]int{128}, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return failed(err)
	}
	if code != 0 {
		r.State, r.Detail = StateNoUpstream, "branch "+r.Branch+" has no upstream, or its upstream is gone"
		return r
	}
	upstream := strings.TrimSpace(string(out))
	out, _, err = g.run(nil, "rev-list", "--count", "@{upstream}..HEAD")
	if err != nil {
		return failed(err)
	}
	if r.Ahead, err = strconv.Atoi(strings.TrimSpace(string(out))); err != nil {
		return Result{State: StateUnavailable, Detail: "git rev-list printed " + strconv.Quote(string(out))}
	}
	if r.Ahead > 0 {
		r.State, r.Detail = StateUnpushed, fmt.Sprintf("%d commits not pushed to %s", r.Ahead, upstream)
		return r
	}
	r.State = StateCleanPushed
	return r
}

func failed(err error) Result {
	if errors.Is(err, errNotARepo) {
		return Result{State: StateNotARepo, Detail: err.Error()}
	}
	return Result{State: StateUnavailable, Detail: err.Error()}
}

// Worktree is one entry of git worktree list --porcelain.
type Worktree struct {
	Path     string
	Head     string
	Branch   string
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// Worktrees lists the worktrees of the repository containing dir.
func (in Inspector) Worktrees(ctx context.Context, dir string) ([]Worktree, error) {
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	g, done, err := in.start(ctx, dir)
	defer done()
	if err != nil {
		return nil, err
	}
	out, _, err := g.run(nil, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, val, _ := strings.Cut(sc.Text(), " ")
		switch key {
		case "worktree":
			list = append(list, Worktree{Path: val})
			cur = &list[len(list)-1]
		case "HEAD":
			if cur != nil {
				cur.Head = val
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	return list, sc.Err()
}

// Inspect classifies dir with the default Inspector.
func Inspect(ctx context.Context, dir string) Result { return Inspector{}.Inspect(ctx, dir) }

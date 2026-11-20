// Package gitstate classifies a project's git state (design D10) by running
// git read-only:
//
//	git -c core.fsmonitor= -c core.hooksPath=/dev/null ... -C <dir> status --porcelain=v2 --branch
//
// with a 5-second timeout, GIT_OPTIONAL_LOCKS=0 (git never rewrites the
// index), repo-local hooks and fsmonitor disabled, and every environment
// variable that could redirect git to another repository or inject config
// removed. Only StateCleanPushed satisfies a rule's RequiresCleanGit
// condition; every other state, including errors and timeouts, blocks.
package gitstate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// DefaultTimeout bounds every git invocation.
const DefaultTimeout = 5 * time.Second

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

// Runner runs git with args in env and returns its stdout and stderr.
type Runner func(ctx context.Context, git string, env []string, args ...string) (stdout, stderr []byte, err error)

// Inspector runs git. The zero value looks git up on PATH and runs it with
// DefaultTimeout.
type Inspector struct {
	// LookPath finds the git binary (exec.LookPath when nil).
	LookPath func(string) (string, error)
	// Run executes git (os/exec when nil).
	Run Runner
	// Timeout bounds each invocation (DefaultTimeout when zero).
	Timeout time.Duration
	// Ceiling, when set, is passed as GIT_CEILING_DIRECTORIES so git never
	// discovers a repository above it (kipple sets it to home's parent).
	Ceiling string
}

// baseArgs disable everything in a repository's own config that would run
// a program during status: fsmonitor and hooks. Untracked files are always
// listed, whatever status.showUntrackedFiles says, since they count as
// uncommitted work.
var baseArgs = []string{
	"-c", "core.fsmonitor=",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.untrackedCache=false",
	"-c", "status.showUntrackedFiles=normal",
	"--no-pager",
}

// strippedEnv are variables that would point git at another repository,
// index or object store, or inject configuration.
var strippedEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM",
	"GIT_OPTIONAL_LOCKS", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_EXTERNAL_DIFF", "GIT_PAGER", "PAGER", "GIT_EDITOR", "GIT_TRACE",
}

func (in Inspector) env() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		drop := strings.HasPrefix(k, "GIT_CONFIG_KEY_") || strings.HasPrefix(k, "GIT_CONFIG_VALUE_") || k == "LC_ALL" || k == "LANG"
		for _, s := range strippedEnv {
			drop = drop || k == s
		}
		if !drop {
			out = append(out, kv)
		}
	}
	out = append(out, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "LANG=C")
	if in.Ceiling != "" {
		out = append(out, "GIT_CEILING_DIRECTORIES="+in.Ceiling)
	}
	return out
}

// ErrUnavailable: git is not on PATH or could not run.
var ErrUnavailable = errors.New("git is not available")

// errNotARepo: git says dir is not inside a repository.
var errNotARepo = errors.New("not a git repository")

// git runs git -C dir args with the safety flags and timeout.
func (in Inspector) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	look := in.LookPath
	if look == nil {
		look = exec.LookPath
	}
	bin, err := look("git")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run := in.Run
	if run == nil {
		run = execRunner
	}
	full := append(append(append([]string(nil), baseArgs...), "-C", dir), args...)
	out, stderr, err := run(ctx, bin, in.env(), full...)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w: timed out after %s", ErrUnavailable, timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if strings.Contains(msg, "not a git repository") {
			return nil, fmt.Errorf("%w: %s", errNotARepo, msg)
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, msg)
	}
	return out, nil
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

// Inspect classifies the repository containing dir.
func (in Inspector) Inspect(ctx context.Context, dir string) Result {
	out, err := in.git(ctx, dir, "status", "--porcelain=v2", "--branch", "--untracked-files=normal", "--ignore-submodules=none")
	switch {
	case errors.Is(err, errNotARepo):
		return Result{State: StateNotARepo, Detail: err.Error()}
	case err != nil:
		return Result{State: StateUnavailable, Detail: err.Error()}
	}
	return parseStatus(out)
}

// parseStatus classifies porcelain v2 output with branch headers.
func parseStatus(out []byte) Result {
	var r Result
	var head, upstream string
	haveAB := false
	dirty := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "# ") {
			dirty++
			continue
		}
		f := strings.Fields(line[2:])
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "branch.head":
			head = f[1]
		case "branch.upstream":
			upstream = f[1]
		case "branch.ab":
			if n, err := strconv.Atoi(strings.TrimPrefix(f[1], "+")); err == nil {
				r.Ahead, haveAB = n, true
			}
		}
	}
	if head != "(detached)" {
		r.Branch = head
	}
	switch {
	case sc.Err() != nil:
		r.State, r.Detail = StateUnavailable, "git status output could not be read: "+sc.Err().Error()
	case dirty > 0:
		r.State, r.Detail = StateDirty, fmt.Sprintf("%d uncommitted or untracked paths", dirty)
	case head == "(detached)":
		r.State, r.Detail = StateNoUpstream, "HEAD is detached"
	case upstream == "":
		r.State, r.Detail = StateNoUpstream, "branch has no upstream"
	case !haveAB:
		r.State, r.Detail = StateNoUpstream, "upstream "+upstream+" is gone"
	case r.Ahead > 0:
		r.State, r.Detail = StateUnpushed, fmt.Sprintf("%d commits not pushed to %s", r.Ahead, upstream)
	default:
		r.State = StateCleanPushed
	}
	return r
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
	out, err := in.git(ctx, dir, "worktree", "list", "--porcelain")
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

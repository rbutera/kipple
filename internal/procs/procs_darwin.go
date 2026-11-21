//go:build darwin

package procs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// System is the macOS Inspector: lsappinfo for GUI apps, pgrep -x for
// process names, lsof for working directories, each run by absolute path
// (toolPaths) with a fixed environment.
type System struct {
	// Run executes a command (os/exec with Timeout when nil).
	Run Runner
	// Timeout bounds each command (DefaultTimeout when zero).
	Timeout time.Duration
}

// New returns the platform Inspector.
func New() Inspector { return System{} }

func (s System) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run := s.Run
	if run == nil {
		run = execRunner
	}
	out, err := run(ctx, name, args...)
	if ctx.Err() != nil {
		return out, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	return out, err
}

// toolPaths are the absolute paths of the system tools System runs. They
// are never looked up on PATH, so a program named pgrep earlier on PATH
// cannot answer "not running". A tool missing at its path is an unknown
// answer.
var toolPaths = map[string]string{
	"lsappinfo": "/usr/bin/lsappinfo",
	"pgrep":     "/usr/bin/pgrep",
	"lsof":      "/usr/sbin/lsof",
}

// toolEnv is the environment the tools run with: nothing inherited.
var toolEnv = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C", "LANG=C"}

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, ok := toolPaths[name]
	if !ok {
		return nil, fmt.Errorf("%s is not a known system tool", name)
	}
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("%s is not available: %w", path, exec.ErrNotFound)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = "/"
	cmd.Env = toolEnv
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}

// RunningBundleIDs implements Inspector with `lsappinfo list`.
func (s System) RunningBundleIDs(ctx context.Context) (map[string]bool, error) {
	out, err := s.run(ctx, "lsappinfo", "list")
	if err != nil {
		return nil, fmt.Errorf("%w: lsappinfo: %v", ErrUnknown, err)
	}
	ids, err := parseLsappinfo(out)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnknown, err)
	}
	return ids, nil
}

// pids returns the PIDs of processes named exactly name (pgrep -x). pgrep
// exits 1 when none match.
func (s System) pids(ctx context.Context, name string) ([]int, error) {
	out, err := s.run(ctx, "pgrep", "-x", regexp.QuoteMeta(name))
	if err != nil {
		if exitCode(err) == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: pgrep %s: %v", ErrUnknown, name, err)
	}
	pids, err := parsePIDs(out)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnknown, err)
	}
	return pids, nil
}

// RunningNames implements Inspector with `pgrep -x <name>` per name.
func (s System) RunningNames(ctx context.Context, names []string) (map[string]bool, error) {
	running := map[string]bool{}
	for _, n := range names {
		pids, err := s.pids(ctx, n)
		if err != nil {
			return nil, err
		}
		running[n] = len(pids) > 0
	}
	return running, nil
}

// ProcessCwds implements Inspector: pgrep -x for each name, then
// `lsof -a -d cwd -Fn -p <pids>`.
func (s System) ProcessCwds(ctx context.Context, names []string) ([]string, error) {
	var all []string
	for _, n := range names {
		pids, err := s.pids(ctx, n)
		if err != nil {
			return nil, err
		}
		for _, p := range pids {
			all = append(all, strconv.Itoa(p))
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	out, err := s.run(ctx, "lsof", "-a", "-d", "cwd", "-Fn", "-p", strings.Join(all, ","))
	cwds := parseLsofCwds(out)
	// lsof exits 1 when a listed process exited meanwhile; what it printed
	// for the others still stands.
	if err != nil && (exitCode(err) != 1 || len(cwds) == 0) {
		return nil, fmt.Errorf("%w: lsof: %v", ErrUnknown, err)
	}
	return cwds, nil
}

// exitCode returns err's exit code, or -1 when err did not come from an exit.
func exitCode(err error) int {
	var ec ExitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

// Package procs answers whether an owning app, a process, or an agent with
// its working directory in a project is running (design D10). Apply asks it
// immediately before moving an item whose rule says to skip while its owner
// runs (reason owner_running) or while an agent works in the project
// (reason tool_running).
//
// Every answer can be unknown: the command is missing, failed, or timed
// out. Methods then return an error, and apply treats unknown as running for
// review and manual items (skip) and as a warning for safe items.
//
// The Inspector interface keeps the platform seam (design D17): the macOS
// implementation (procs_darwin.go) runs lsappinfo, pgrep and lsof through an
// injectable Runner; other platforms get an Inspector that always answers
// ErrUnsupported until a port reads /proc.
package procs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrUnsupported: no implementation on this platform; every answer is unknown.
var ErrUnsupported = errors.New("procs: not supported on this platform")

// ErrUnknown wraps every failure to get an answer.
var ErrUnknown = errors.New("procs: could not determine running processes")

// DefaultTimeout bounds each command.
const DefaultTimeout = 5 * time.Second

// Inspector reports running apps and processes.
type Inspector interface {
	// RunningBundleIDs returns the bundle IDs of running GUI apps.
	RunningBundleIDs(ctx context.Context) (map[string]bool, error)
	// RunningNames returns which of names have a process with exactly that
	// name.
	RunningNames(ctx context.Context, names []string) (map[string]bool, error)
	// ProcessCwds returns the working directories of every process named
	// one of names.
	ProcessCwds(ctx context.Context, names []string) ([]string, error)
}

// Runner runs a command and returns its stdout. A non-zero exit returns an
// error that ExitCode can read.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExitCoder is implemented by *exec.ExitError.
type ExitCoder interface{ ExitCode() int }

var bundleIDPattern = regexp.MustCompile(`bundleID="([^"]+)"`)

// parseLsappinfo extracts bundle IDs from `lsappinfo list`. Output without
// any is treated as unknown: a GUI session always runs some app.
func parseLsappinfo(out []byte) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, m := range bundleIDPattern.FindAllSubmatch(out, -1) {
		ids[string(m[1])] = true
	}
	if len(ids) == 0 {
		return nil, errors.New("lsappinfo listed no apps")
	}
	return ids, nil
}

// parsePIDs reads pgrep output, one PID per line.
func parsePIDs(out []byte) ([]int, error) {
	var pids []int
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.TrimSpace(sc.Text())
		if f == "" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil || n <= 0 {
			return nil, errors.New("pgrep printed " + strconv.Quote(f))
		}
		pids = append(pids, n)
	}
	return pids, sc.Err()
}

// parseLsofCwds reads `lsof -Fn` output: lines starting with "n" name a
// file, here each process's working directory.
func parseLsofCwds(out []byte) []string {
	var cwds []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "n") && len(line) > 1 {
			cwds = append(cwds, line[1:])
		}
	}
	return cwds
}

// Within reports whether dir is project or below it, comparing ASCII case
// insensitively as APFS does. Apply uses it to match agent working
// directories against a project.
func Within(dir, project string) bool {
	d, p := strings.ToLower(strings.TrimSuffix(dir, "/")), strings.ToLower(strings.TrimSuffix(project, "/"))
	return p != "" && (d == p || strings.HasPrefix(d, p+"/"))
}

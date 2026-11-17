// Package testutil builds fixture home folders and guards every kipple test
// against touching the real home folder of the machine it runs on.
//
// This file is the only file under internal/ allowed to resolve the real home
// directory (tools/checkhome enforces that), and it does so only to refuse it.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realHomeDir returns the real home directory of the user running the tests.
func realHomeDir() (string, error) {
	return os.UserHomeDir()
}

// GuardRealHome fails the test unless KIPPLE_HOME and KIPPLE_STATE_DIR are set
// to locations that are neither the real home folder nor inside it.
func GuardRealHome(t testing.TB) {
	t.Helper()
	if err := checkEnv(); err != nil {
		t.Fatalf("testutil.GuardRealHome: %v", err)
	}
}

// GuardedMain is the TestMain body every kipple test package uses:
//
//	func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }
//
// It points KIPPLE_HOME and KIPPLE_STATE_DIR at a fresh empty sandbox (so code
// that reads them never falls back to the real home), verifies the guard, runs
// the tests and removes the sandbox. Tests that need content call NewHome,
// which replaces both variables for that test.
func GuardedMain(m *testing.M) int {
	base, err := os.MkdirTemp("", "kipple-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testutil.GuardedMain:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(base) }()
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	home := filepath.Join(base, "home")
	state := filepath.Join(base, "state")
	for _, d := range []string{home, state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "testutil.GuardedMain:", err)
			return 1
		}
	}
	if err := os.Setenv("KIPPLE_HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, "testutil.GuardedMain:", err)
		return 1
	}
	if err := os.Setenv("KIPPLE_STATE_DIR", state); err != nil {
		fmt.Fprintln(os.Stderr, "testutil.GuardedMain:", err)
		return 1
	}
	if err := checkEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "testutil.GuardedMain:", err)
		return 1
	}
	return m.Run()
}

// checkEnv fails closed: if the real home cannot be determined, nothing runs.
func checkEnv() error {
	realHome, err := realHomeDir()
	if err != nil || realHome == "" {
		return fmt.Errorf("cannot determine the real home directory to guard against it: %v", err)
	}
	for _, name := range []string{"KIPPLE_HOME", "KIPPLE_STATE_DIR"} {
		v := os.Getenv(name)
		if v == "" {
			return fmt.Errorf("%s is not set; build a fixture with testutil.NewHome or use testutil.GuardedMain", name)
		}
		if !filepath.IsAbs(v) {
			return fmt.Errorf("%s=%q is not absolute", name, v)
		}
		if within(v, realHome) {
			return fmt.Errorf("%s=%q is the real home folder or inside it (%q); tests must use a fixture", name, v, realHome)
		}
	}
	return nil
}

// within reports whether p is root or descends from it, comparing both the
// lexical paths and their symlink-resolved forms, case-insensitively (APFS is
// case-insensitive by default).
func within(p, root string) bool {
	cands := []string{filepath.Clean(p)}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		cands = append(cands, r)
	}
	roots := []string{filepath.Clean(root)}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		roots = append(roots, r)
	}
	for _, c := range cands {
		for _, r := range roots {
			cl, rl := strings.ToLower(c), strings.ToLower(r)
			if cl == rl || strings.HasPrefix(cl, strings.TrimSuffix(rl, string(filepath.Separator))+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

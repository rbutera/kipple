// Package testutil builds fixture home folders and guards every kipple test
// against touching the real home folder of the machine it runs on.
//
// This file is the only file under internal/ allowed to resolve the real home
// directory, through os.UserHomeDir and os/user (tools/checkhome enforces
// that), and it does so only to refuse it.
package testutil

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/homeguard"
)

// accountHomeDir returns the home directory recorded in the user database
// for the user running the tests. Unlike os.UserHomeDir it ignores $HOME, so
// a test that overrides HOME cannot hide the real home from the guard.
func accountHomeDir() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.HomeDir == "" {
		return "", errors.New("user database has no home directory for the current user")
	}
	return u.HomeDir, nil
}

// realHomeDirs returns every location the real home might be: $HOME (via
// os.UserHomeDir) and the user database entry. It fails only when neither
// can be determined.
func realHomeDirs() ([]string, error) {
	var homes []string
	var errs []error
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		homes = append(homes, h)
	} else {
		errs = append(errs, fmt.Errorf("os.UserHomeDir: %v", err))
	}
	if h, err := accountHomeDir(); err == nil {
		if len(homes) == 0 || homes[0] != h {
			homes = append(homes, h)
		}
	} else {
		errs = append(errs, fmt.Errorf("os/user: %w", err))
	}
	if len(homes) == 0 {
		return nil, errors.Join(errs...)
	}
	return homes, nil
}

// RealHomePaths joins each home-relative path onto every location the real
// home might be ($HOME and the user database entry), for tests that must
// prove, by stat alone, that a run left those files untouched. It returns
// nil when the real home cannot be determined.
func RealHomePaths(rels ...string) []string {
	homes, err := realHomeDirs()
	if err != nil {
		return nil
	}
	var out []string
	for _, h := range homes {
		for _, r := range rels {
			out = append(out, filepath.Join(h, filepath.FromSlash(r)))
		}
	}
	return out
}

// GuardRealHome fails the test unless KIPPLE_HOME and KIPPLE_STATE_DIR are set
// to locations that are neither the real home folder, inside it, nor an
// ancestor of it (such as / or /Users).
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
	// git run by the code under test (internal/gitstate) must not depend
	// on the developer's own git configuration.
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1"} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "testutil.GuardedMain:", err)
			return 1
		}
	}
	if err := checkEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "testutil.GuardedMain:", err)
		return 1
	}
	return m.Run()
}

// checkEnv fails closed: if the real home cannot be determined, nothing runs.
func checkEnv() error {
	realHomes, err := realHomeDirs()
	if err != nil {
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
		for _, realHome := range realHomes {
			if within(v, realHome) {
				return fmt.Errorf("%s=%q is the real home folder or inside it (%q); tests must use a fixture", name, v, realHome)
			}
			if within(realHome, v) {
				return fmt.Errorf("%s=%q contains the real home folder (%q); tests must use a fixture", name, v, realHome)
			}
			if err := checkIdentity(name, v, realHome); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkIdentity refuses v when it is, lies inside, or contains realHome by
// (st_dev, st_ino), which catches spellings the string check misses: APFS
// folds case and Unicode (a long s, an NFD name) and firmlinks alias
// /System/Volumes/Data/Users/<me> to /Users/<me>. A path that does not
// exist yet (a state folder created lazily) is checked through its nearest
// existing ancestor, which must not be the real home or inside it. Any
// lookup error refuses.
func checkIdentity(name, v, realHome string) error {
	p, exact := filepath.Clean(v), true
	for {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			return fmt.Errorf("%s=%q has no existing ancestor to check against the real home folder", name, v)
		}
		p, exact = parent, false
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	rel, err := homeguard.Relate(homeguard.System(), p, realHome)
	if err != nil {
		return fmt.Errorf("%s=%q cannot be checked against the real home folder (%q), so it is refused: %v", name, v, realHome, err)
	}
	if rel == homeguard.Same || rel == homeguard.Inside || (exact && rel == homeguard.Contains) {
		return fmt.Errorf("%s=%q %s (%q); tests must use a fixture", name, v, rel, realHome)
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

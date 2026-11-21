//go:build darwin

// APFS folds case and Unicode when it looks a name up, and firmlinks make
// /System/Volumes/Data contain the real home. Linux file systems compare
// bytes and have no firmlinks, so the spellings below name nothing there
// and these checks are macOS-only. The portable halves of the same tests
// are in main_test.go.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/plan"
)

// An upper-case spelling of KIPPLE_HOME reaches the same folder on APFS and
// is respelled to the stored name (the case half of TestHomeResolution).
func TestHomeResolutionCaseVariant(t *testing.T) {
	home := fixture(t)
	upper := filepath.Join(filepath.Dir(home), "HOME")
	if fi, err := os.Stat(upper); err != nil || !fi.IsDir() {
		t.Skip("case-sensitive APFS volume")
	}
	t.Setenv("KIPPLE_HOME", upper)
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if p.Home != home || len(p.Items) == 0 {
		t.Fatalf("case variant: home %q items %d, want %q", p.Home, len(p.Items), home)
	}
}

// KIPPLE_HOME inside the real home with different case is refused (the
// case half of TestKippleHomeInsideRealHomeRefused).
func TestKippleHomeInsideRealHomeCaseVariantRefused(t *testing.T) {
	fixture(t)
	_, real := mkRealHome(t, "me")
	old := filepath.Join(real, "Documents", "OldMac")
	alias := strings.ToUpper(old[:len(old)-len("OldMac")]) + "OldMac"
	if fi, err := os.Stat(alias); err != nil || !fi.IsDir() {
		t.Skip("case-sensitive APFS volume")
	}
	refusedHome(t, withRealHome(t, real), alias)
}

// The APFS alias cases of TestKippleHomeAliasOfRealHomeRefused.
func TestKippleHomeAPFSAliasOfRealHomeRefused(t *testing.T) {
	fixture(t)
	mkReal, withReal := mkRealHome, withRealHome
	refused := func(t *testing.T, rt runtimeEnv, home string) {
		t.Helper()
		if fi, err := os.Stat(home); err != nil || !fi.IsDir() {
			t.Skipf("spelling does not resolve on this volume (case-sensitive APFS): %v", err)
		}
		refusedHome(t, rt, home)
	}
	t.Run("Long s spelling inside the real home", func(t *testing.T) {
		base, real := mkReal(t, "me")
		refused(t, withReal(t, real), filepath.Join(base, "U\u017fers", "me", "Documents", "OldMac"))
	})
	t.Run("Long s spelling of the real home", func(t *testing.T) {
		base, real := mkReal(t, "me")
		refused(t, withReal(t, real), filepath.Join(base, "U\u017fers", "me"))
	})
	t.Run("Long s spelling of an ancestor", func(t *testing.T) {
		base, real := mkReal(t, "me")
		refused(t, withReal(t, real), filepath.Join(base, "U\u017fers"))
	})
	t.Run("NFD spelling inside the real home", func(t *testing.T) {
		base, real := mkReal(t, "m\u00e9") // stored precomposed (NFC)
		refused(t, withReal(t, real), filepath.Join(base, "Users", "me\u0301", "Documents", "OldMac"))
	})
}

// Review finding (wave 2 re-review): /System and /System/Volumes contain
// the real home through the /System/Volumes/Data firmlink. This checks the
// machine's real home read-only (stat, open and statfs only; kipple does
// not run against it).
func TestSystemFoldersContainRealHome(t *testing.T) {
	if fi, err := os.Stat("/System/Volumes/Data"); err != nil || !fi.IsDir() {
		t.Skip("no /System/Volumes/Data on this machine")
	}
	reals, err := realHomes()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, real := range reals {
		if seen[real] {
			continue
		}
		seen[real] = true
		for _, cand := range []string{"/System", "/System/Volumes", "/System/Volumes/Data", "/"} {
			t.Run(cand, func(t *testing.T) {
				rel, err := homeguard.Relate(homeguard.System(), cand, real)
				if err != nil {
					t.Fatal(err)
				}
				if rel != homeguard.Contains {
					t.Fatalf("Relate(%q, %q) = %v, want contains", cand, real, rel)
				}
			})
		}
	}
}

package rules_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/testutil"
)

// Task 3.6: CONTRIBUTING.md's "Adding a rule" steps for a rule whose
// location differs per platform. A scratch rule shared by both catalogues,
// with a Linux override of its roots, passes both catalogues' validation
// and invariants, resolves to the right roots on each platform, and a scan
// on the running platform lists its item from that platform's root. (This
// package's TestMain pins Catalogue() to macOS, so the scan names the
// running platform's catalogue explicitly.)
func TestAddARuleWithALinuxOverride(t *testing.T) {
	r := rules.Rule{
		ID:        "example.cache",
		Title:     "Example tool cache",
		Tier:      rules.TierSafe,
		Roots:     []string{".example/cache"},
		Match:     rules.Match{Kind: rules.MatchChildren},
		Owner:     rules.Owner{Name: "Example"},
		What:      "Downloads the example tool keeps so its next run starts faster.",
		IfRemoved: "The example tool downloads them again the next time it needs them.",
	}
	roots := map[string]string{rules.PlatformDarwin: ".example/cache", rules.PlatformLinux: ".local/share/example/cache"}
	undo := rules.AddForTest(r, []string{roots[rules.PlatformLinux]})
	defer undo()

	for _, goos := range rules.Platforms() {
		cat := rules.CatalogueFor(goos)
		if errs := rules.ValidateFor(goos, cat); len(errs) != 0 {
			t.Fatalf("%s catalogue with the scratch rule: %v", goos, errs)
		}
		got, ok := rules.LookupFor(goos, r.ID)
		if !ok || len(got.Roots) != 1 || got.Roots[0] != roots[goos] {
			t.Fatalf("%s: %+v", goos, got.Roots)
		}
	}
	if p := rules.PlatformsOf(r.ID); strings.Join(p, ",") != "darwin,linux" {
		t.Fatalf("PlatformsOf = %v", p)
	}
	// A Linux root inside the denylist fails the Linux catalogue.
	undo()
	undo = rules.AddForTest(r, []string{".local/share/keyrings/example"})
	if errs := rules.ValidateFor(rules.PlatformLinux, rules.CatalogueFor(rules.PlatformLinux)); len(errs) == 0 {
		t.Fatal("a Linux override rooted in the denylist validated")
	}
	undo()
	undo = rules.AddForTest(r, []string{roots[rules.PlatformLinux]})

	now := time.Now()
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{{Path: roots[runtime.GOOS] + "/blob-1/data", Size: 4096, MTime: now.Add(-48 * time.Hour)}},
	})
	p, err := scan.Run(context.Background(), scan.Options{Env: policy.Env{Home: home}, Rules: rules.CatalogueFor(runtime.GOOS), KippleVersion: "test", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range p.Items {
		if it.Rule == r.ID {
			if it.Path != home+"/"+roots[runtime.GOOS]+"/blob-1" || !it.Selected {
				t.Fatalf("scratch item %+v", it)
			}
			return
		}
	}
	t.Fatalf("the scan did not list the scratch rule's item on %s: %+v", runtime.GOOS, p.Items)
}

package scan

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

// scratchRule is the example rule CONTRIBUTING.md's "Adding a rule" steps
// walk through.
func scratchRule() rules.Rule {
	return rules.Rule{
		ID:        "example.cache",
		Title:     "Example tool cache",
		Tier:      rules.TierSafe,
		Roots:     []string{".example/cache"},
		Match:     rules.Match{Kind: rules.MatchChildren},
		Owner:     rules.Owner{Name: "Example"},
		What:      "Downloads the example tool keeps so its next run starts faster.",
		IfRemoved: "The example tool downloads them again the next time it needs them.",
	}
}

// Task 4.4: the steps in CONTRIBUTING.md for adding a rule work, on a
// scratch rule: (1) the rule value passes rules.Validate alongside the
// catalogue, and a rule missing its What/IfRemoved sentences or reaching
// into a denylisted folder does not; (2) the policy authorises a child of
// its root but not the root itself; (3) a scan of a fixture home lists the
// item under the new rule with its tier and selection.
func TestAddARuleSteps(t *testing.T) {
	r := scratchRule()
	cat := append(rules.Catalogue(), r)

	// Step 1: validation.
	if errs := rules.Validate(cat); len(errs) != 0 {
		t.Fatalf("the scratch rule does not validate: %v", errs)
	}
	noWords := r
	noWords.What, noWords.IfRemoved = "", ""
	if errs := rules.Validate(append(rules.Catalogue(), noWords)); len(errs) == 0 {
		t.Error("a rule without What and IfRemoved validated")
	}
	denied := r
	denied.Roots = []string{".ssh/example"}
	if errs := rules.Validate(append(rules.Catalogue(), denied)); len(errs) == 0 {
		t.Error("a rule rooted in a denylisted folder validated")
	}
	clash := r
	clash.Roots = []string{"Library/Caches/com.example"}
	if errs := rules.Validate(append(rules.Catalogue(), clash)); len(errs) == 0 {
		t.Error("a rule overlapping another rule's root validated")
	}

	// Step 2 and 3: a fixture home, the policy, and a scan.
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{{Path: ".example/cache/blob-1/data", Size: 4096, MTime: now.Add(-48 * time.Hour)}},
	})
	env := policy.Env{Home: home}
	if d := policy.Authorize(env, r, home+"/.example/cache/blob-1"); !d.Allowed {
		t.Fatalf("policy refused the scratch rule's item: %s", d.Reason)
	}
	if d := policy.Authorize(env, r, home+"/.example/cache"); d.Allowed {
		t.Fatal("policy allowed the rule root itself")
	}
	p, err := Run(context.Background(), Options{Env: env, Rules: cat, KippleVersion: "test", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range p.Items {
		if it.Rule != r.ID {
			continue
		}
		found = true
		if !strings.HasSuffix(it.Path, "/.example/cache/blob-1") || it.Tier != string(rules.TierSafe) || !it.Selected {
			t.Fatalf("scratch item %+v", it)
		}
	}
	if !found {
		t.Fatalf("the scan did not list the scratch rule's item: %+v", p.Items)
	}
}

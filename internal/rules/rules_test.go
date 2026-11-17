package rules_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

var update = flag.Bool("update", false, "rewrite testdata golden files")

// Compile-time: a catalogue rule can be handed straight to the policy.
var _ policy.Rule = rules.Rule{}

// The policy's match kinds mirror this package's.
func TestMatchKindsAgreeWithPolicy(t *testing.T) {
	pairs := map[rules.MatchKind]string{
		rules.MatchChildren:        policy.MatchChildren,
		rules.MatchExtensions:      policy.MatchExtensions,
		rules.MatchProjectArtifact: policy.MatchProjectArtifact,
	}
	for k, v := range pairs {
		if string(k) != v {
			t.Errorf("rules.%s = %q, policy has %q", k, k, v)
		}
	}
}

var idPattern = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+(-[a-z0-9]+)*)+$`)

func hasMeta(s string) bool { return strings.ContainsAny(s, `*?[\`) }

func segMatch(a, b string) bool {
	if a == b {
		return true
	}
	if hasMeta(a) {
		if ok, _ := filepath.Match(a, b); ok {
			return true
		}
	}
	if hasMeta(b) {
		if ok, _ := filepath.Match(b, a); ok {
			return true
		}
	}
	return false
}

func excludes(r rules.Rule, name string) bool {
	for _, p := range r.Exclude {
		if ok, _ := filepath.Match(strings.ToLower(p), strings.ToLower(name)); ok {
			return true
		}
	}
	return false
}

// TestCatalogueInvariants is the spec scenario "Invariant test": every failure
// names the rule.
func TestCatalogueInvariants(t *testing.T) {
	cat := rules.Catalogue()
	if len(cat) == 0 {
		t.Fatal("catalogue is empty")
	}
	ids := map[string]bool{}
	type ownedRoot struct {
		rule rules.Rule
		segs []string
	}
	var all []ownedRoot

	for _, r := range cat {
		if !idPattern.MatchString(r.ID) {
			t.Errorf("%s: ID does not match %s", r.ID, idPattern)
		}
		if ids[r.ID] {
			t.Errorf("%s: duplicate rule ID", r.ID)
		}
		ids[r.ID] = true
		if !r.Tier.Valid() {
			t.Errorf("%s: invalid tier %q", r.ID, r.Tier)
		}
		if strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.Owner.Name) == "" {
			t.Errorf("%s: empty title or owner name", r.ID)
		}
		for field, s := range map[string]string{"what": r.What, "if_removed": r.IfRemoved} {
			s = strings.TrimSpace(s)
			switch {
			case s == "":
				t.Errorf("%s: empty %s", r.ID, field)
			case !strings.HasSuffix(s, "."):
				t.Errorf("%s: %s should be one sentence ending in a full stop: %q", r.ID, field, s)
			case strings.Contains(strings.TrimSuffix(s, "."), ". "):
				t.Errorf("%s: %s should be a single sentence: %q", r.ID, field, s)
			}
		}
		if r.Tier == rules.TierInfo {
			if r.OwnerCommand == "" {
				t.Errorf("%s: info rules must name the owner's own command", r.ID)
			}
			if r.Movable() {
				t.Errorf("%s: info rule reports movable", r.ID)
			}
		}
		switch r.Match.Kind {
		case rules.MatchChildren:
		case rules.MatchExtensions:
			if len(r.Match.Extensions) == 0 {
				t.Errorf("%s: extensions match without extensions", r.ID)
			}
		case rules.MatchProjectArtifact:
			if len(r.Match.Names) == 0 || len(r.Match.Markers) == 0 {
				t.Errorf("%s: project artifact rule needs names and markers", r.ID)
			}
			c := r.Conditions
			if r.Tier != rules.TierReview || !c.RequiresIdleProject || c.IdleDays < 30 || !c.RequiresCleanGit || !c.SkipIfAgentInProject {
				t.Errorf("%s: project artifact rules must be review tier with idle (30d), clean git and agent checks", r.ID)
			}
		default:
			t.Errorf("%s: unknown match kind %q", r.ID, r.Match.Kind)
		}

		if len(r.Roots) == 0 {
			t.Errorf("%s: no roots", r.ID)
		}
		for _, root := range r.Roots {
			segs, ok := policy.ParseRoot(root)
			if !ok {
				t.Errorf("%s: root %q is not a valid home-relative root (absolute, .., or more than one glob)", r.ID, root)
				continue
			}
			if r.Tier != rules.TierInfo && policy.IsDenylisted(root) {
				t.Errorf("%s: root %q is on, under, or contains a denylisted path", r.ID, root)
			}
			all = append(all, ownedRoot{r, segs})
		}
	}

	// No two rules claim the same root, and a root nested inside another
	// rule's root must be excluded by that outer rule (so no path is offered
	// twice). Project artifact rules share the project roots by design and
	// must have disjoint artifact names instead.
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			sameRule := a.rule.ID == b.rule.ID
			bothProjects := a.rule.Match.Kind == rules.MatchProjectArtifact && b.rule.Match.Kind == rules.MatchProjectArtifact
			if len(a.segs) == len(b.segs) && segsMatch(a.segs, b.segs) {
				if bothProjects && !sameRule {
					continue
				}
				if i < j {
					t.Errorf("%s and %s: both claim root %q", a.rule.ID, b.rule.ID, strings.Join(a.segs, "/"))
				}
				continue
			}
			if len(a.segs) < len(b.segs) && segsMatch(a.segs, b.segs[:len(a.segs)]) {
				child := b.segs[len(a.segs)]
				if sameRule {
					t.Errorf("%s: root %q is nested inside its own root %q", a.rule.ID, strings.Join(b.segs, "/"), strings.Join(a.segs, "/"))
				} else if !excludes(a.rule, child) && !(bothProjects) {
					t.Errorf("%s: root %q lies inside root %q of %s, which does not exclude %q", b.rule.ID, strings.Join(b.segs, "/"), strings.Join(a.segs, "/"), a.rule.ID, child)
				}
			}
		}
	}

	projectNames := map[string]string{}
	for _, r := range cat {
		if r.Match.Kind != rules.MatchProjectArtifact {
			continue
		}
		for _, n := range r.Match.Names {
			if other, dup := projectNames[n]; dup {
				t.Errorf("%s and %s: both claim artifact name %q", other, r.ID, n)
			}
			projectNames[n] = r.ID
		}
	}
}

func segsMatch(a, b []string) bool {
	for i := range a {
		if !segMatch(a[i], b[i]) {
			return false
		}
	}
	return true
}

// TestCatalogueContents is the spec scenario "Catalogue count": every rule
// named in "v1 catalogue contents" is present with the stated tier.
func TestCatalogueContents(t *testing.T) {
	want := map[string]rules.Tier{
		"caches.user": rules.TierSafe, "logs.user": rules.TierSafe, "xcode.deriveddata": rules.TierSafe,
		"simulator.caches": rules.TierSafe, "swiftpm.cache": rules.TierSafe, "homebrew.cache": rules.TierSafe,
		"npm.cache": rules.TierSafe, "pnpm.store": rules.TierSafe, "yarn.cache": rules.TierSafe,
		"bun.cache": rules.TierSafe, "pip.cache": rules.TierSafe, "uv.cache": rules.TierSafe,
		"poetry.cache": rules.TierSafe, "cargo.registry": rules.TierSafe, "go.build-cache": rules.TierSafe,
		"gradle.caches": rules.TierSafe, "cocoapods.cache": rules.TierSafe, "editor.cursor": rules.TierSafe,
		"editor.vscode": rules.TierSafe, "editor.windsurf": rules.TierSafe,
		"claude.shell-snapshots": rules.TierSafe, "claude.debug": rules.TierSafe, "codex.log": rules.TierSafe,

		"go.mod-cache": rules.TierReview, "xcode.devicesupport": rules.TierReview,
		"project.node-modules": rules.TierReview, "project.target": rules.TierReview,
		"project.venv": rules.TierReview, "project.next": rules.TierReview,

		"xcode.archives": rules.TierManual, "huggingface.hub": rules.TierManual,
		"codex.worktrees": rules.TierManual, "downloads.installers": rules.TierManual,

		"ollama.models": rules.TierInfo, "claude.projects": rules.TierInfo, "codex.sessions": rules.TierInfo,
		"docker.raw": rules.TierInfo, "simulator.runtimes": rules.TierInfo,
	}
	got := map[string]rules.Rule{}
	for _, r := range rules.Catalogue() {
		got[r.ID] = r
	}
	for id, tier := range want {
		r, ok := got[id]
		if !ok {
			t.Errorf("%s: missing from the catalogue", id)
			continue
		}
		if r.Tier != tier {
			t.Errorf("%s: tier %q, want %q", id, r.Tier, tier)
		}
	}
	for id := range got {
		if _, ok := want[id]; !ok {
			t.Errorf("%s: in the catalogue but not in this test's list; add it here and to the spec", id)
		}
	}

	has := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	for _, ex := range []string{"com.apple.bird", "CloudKit", "com.apple.cloudkit", "com.apple.icloud*", "Homebrew"} {
		if !has(got["caches.user"].Exclude, ex) {
			t.Errorf("caches.user: missing exclusion %q", ex)
		}
	}
	for _, id := range []string{"editor.cursor", "editor.vscode", "editor.windsurf"} {
		for _, leaf := range []string{"/Cache", "/CachedData", "/Code Cache", "/GPUCache"} {
			found := false
			for _, root := range got[id].Roots {
				found = found || strings.HasSuffix(root, leaf)
			}
			if !found {
				t.Errorf("%s: no root ending in %q", id, leaf)
			}
		}
	}
	if r := got["cargo.registry"]; !has(r.Roots, ".cargo/registry/cache") || !has(r.Roots, ".cargo/registry/src") {
		t.Errorf("cargo.registry: want both registry cache and src roots, got %v", r.Roots)
	}
	if r := got["xcode.devicesupport"]; r.Conditions.MinAgeDays != 90 {
		t.Errorf("xcode.devicesupport: MinAgeDays %d, want 90", r.Conditions.MinAgeDays)
	}
	if r := got["downloads.installers"]; r.Conditions.MinAgeDays != 14 || r.Match.Kind != rules.MatchExtensions ||
		!has(r.Match.Extensions, ".dmg") || !has(r.Match.Extensions, ".pkg") || !has(r.Match.Extensions, ".iso") {
		t.Errorf("downloads.installers: want .dmg/.pkg/.iso older than 14 days, got %+v %+v", r.Match, r.Conditions)
	}
	projects := map[string][2][]string{
		"project.node-modules": {{"node_modules"}, {"package.json"}},
		"project.target":       {{"target"}, {"Cargo.toml"}},
		"project.venv":         {{".venv", "venv"}, {"pyproject.toml", "requirements.txt"}},
		"project.next":         {{".next"}, {"next.config.*"}},
	}
	for id, nm := range projects {
		r := got[id]
		if strings.Join(r.Match.Names, ",") != strings.Join(nm[0], ",") || strings.Join(r.Match.Markers, ",") != strings.Join(nm[1], ",") {
			t.Errorf("%s: names %v markers %v, want %v %v", id, r.Match.Names, r.Match.Markers, nm[0], nm[1])
		}
		if strings.Join(r.Roots, ",") != strings.Join(rules.DefaultProjectRoots(), ",") {
			t.Errorf("%s: roots %v, want the default project roots", id, r.Roots)
		}
	}
}

// concreteRoot replaces a glob segment with a name it matches.
func concreteRoot(root string) string {
	segs := strings.Split(root, "/")
	for i, s := range segs {
		if hasMeta(s) {
			segs[i] = strings.ReplaceAll(s, "*", "iOS")
		}
	}
	return strings.Join(segs, "/")
}

// TestPolicyAgreesWithCatalogue builds a fixture with a sample item under
// every root of every movable rule and checks the gate allows the item and
// refuses the root itself.
func TestPolicyAgreesWithCatalogue(t *testing.T) {
	var spec testutil.Spec
	type probe struct {
		rule       rules.Rule
		root, item string
	}
	var probes []probe
	for _, r := range rules.Catalogue() {
		if !r.Movable() {
			continue
		}
		for _, root := range r.Roots {
			cr := concreteRoot(root)
			var item string
			switch r.Match.Kind {
			case rules.MatchExtensions:
				item = cr + "/old" + r.Match.Extensions[0]
				spec.Files = append(spec.Files, testutil.File{Path: item, Size: 16})
			case rules.MatchProjectArtifact:
				proj := cr + "/app-" + strings.TrimPrefix(r.ID, "project.")
				item = proj + "/" + r.Match.Names[0]
				marker := strings.ReplaceAll(r.Match.Markers[0], "*", "js")
				spec.Files = append(spec.Files,
					testutil.File{Path: proj + "/" + marker, Content: "{}"},
					testutil.File{Path: item + "/f", Size: 16})
			default:
				item = cr + "/sample-item"
				spec.Files = append(spec.Files, testutil.File{Path: item + "/f", Size: 16})
			}
			probes = append(probes, probe{r, cr, item})
		}
	}
	home := testutil.NewHome(t, spec)
	env := policy.Env{Home: home}
	for _, p := range probes {
		if d := policy.Authorize(env, p.rule, home+"/"+p.item); !d.Allowed {
			t.Errorf("%s: sample item %q refused: %s", p.rule.ID, p.item, d.Reason)
		}
		if d := policy.Authorize(env, p.rule, home+"/"+p.root); d.Reason != policy.ReasonIsRuleRoot {
			t.Errorf("%s: root %q should be refused as is_rule_root, got %+v", p.rule.ID, p.root, d)
		}
	}

	// caches.user must not reach into children owned by more specific rules.
	caches, _ := rules.Lookup("caches.user")
	for _, child := range []string{"Homebrew", "org.swift.swiftpm", "Yarn", "pip", "pypoetry", "go-build", "CocoaPods"} {
		if d := policy.Authorize(env, caches, home+"/Library/Caches/"+child); d.Allowed {
			t.Errorf("caches.user: authorises %s, which belongs to another rule", child)
		}
	}
}

// TestInfoRuleNeverAuthorised: the spec says info rules SHALL never be
// movable, and Authorize is the only approver, so the gate itself refuses a
// real item under every root of every info rule.
func TestInfoRuleNeverAuthorised(t *testing.T) {
	var spec testutil.Spec
	type probe struct {
		rule rules.Rule
		item string
	}
	var probes []probe
	for _, r := range rules.Catalogue() {
		if r.Tier != rules.TierInfo {
			continue
		}
		if r.RuleMovable() {
			t.Errorf("%s: info rule reports RuleMovable", r.ID)
		}
		for _, root := range r.Roots {
			item := concreteRoot(root) + "/sample-item"
			spec.Files = append(spec.Files, testutil.File{Path: item + "/f", Size: 16})
			probes = append(probes, probe{r, item})
		}
	}
	if len(probes) == 0 {
		t.Fatal("no info rules in the catalogue")
	}
	home := testutil.NewHome(t, spec)
	for _, p := range probes {
		if d := policy.Authorize(policy.Env{Home: home}, p.rule, home+"/"+p.item); d.Allowed || d.Reason != policy.ReasonInfoTier {
			t.Errorf("%s: item %q: want %q, got %+v", p.rule.ID, p.item, policy.ReasonInfoTier, d)
		}
	}
}

func TestCatalogueIsACopy(t *testing.T) {
	a := rules.Catalogue()
	a[0].Roots[0] = "Documents"
	a[0].Exclude = nil
	b, ok := rules.Lookup(a[0].ID)
	if !ok {
		t.Fatal("lookup failed")
	}
	if b.Roots[0] == "Documents" || len(b.Exclude) == 0 {
		t.Fatal("mutating Catalogue() leaked into the package catalogue")
	}
	if _, ok := rules.Lookup("no.such-rule"); ok {
		t.Fatal("lookup of an unknown ID succeeded")
	}
}

func TestLevels(t *testing.T) {
	if strings.Join(rules.Levels(), ",") != "safe,review" {
		t.Fatalf("levels = %v", rules.Levels())
	}
	if rules.Tier("bogus").Valid() {
		t.Fatal("bogus tier is valid")
	}
}

// TestGoldenRulesJSON pins the JSON encoding of the catalogue. Regenerate
// with: go test ./internal/rules -run TestGoldenRulesJSON -update
func TestGoldenRulesJSON(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rules.Catalogue()); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "rules.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("catalogue JSON differs from %s; if intended, rerun with -update and review the diff", golden)
	}

	// Every rule in the golden carries the fields the spec requires.
	var decoded []map[string]any
	if err := json.Unmarshal(want, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, r := range decoded {
		for _, k := range []string{"id", "title", "tier", "roots", "what", "if_removed", "owner"} {
			if _, ok := r[k]; !ok {
				t.Errorf("%v: golden JSON lacks %q", r["id"], k)
			}
		}
	}
}

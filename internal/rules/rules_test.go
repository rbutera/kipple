package rules_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
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

func hasMeta(s string) bool { return strings.ContainsAny(s, `*?[\`) }

// TestCatalogueInvariants is the spec scenario "Invariant test": every failure
// names the rule.
func TestCatalogueInvariants(t *testing.T) {
	cat := rules.Catalogue()
	if len(cat) == 0 {
		t.Fatal("catalogue is empty")
	}
	for _, err := range rules.Validate(cat) {
		t.Error(err)
	}
	for _, r := range cat {
		if (r.Tier == rules.TierInfo) == r.Movable() {
			t.Errorf("%s: Movable() = %v for tier %s", r.ID, r.Movable(), r.Tier)
		}
	}
}

// goodRule is a valid rule that each TestValidate case breaks in one way.
func goodRule(id, root string) rules.Rule {
	return rules.Rule{
		ID: id, Title: "Title", Tier: rules.TierSafe, Roots: []string{root},
		Match: rules.Match{Kind: rules.MatchChildren}, Owner: rules.Owner{Name: "Owner"},
		What: "One sentence.", IfRemoved: "Another sentence.",
	}
}

func goodProject(id string, names ...string) rules.Rule {
	r := goodRule(id, "dev")
	r.Tier = rules.TierReview
	r.Match = rules.Match{Kind: rules.MatchProjectArtifact, Names: names, Markers: []string{"marker"}}
	r.Conditions = rules.Conditions{RequiresIdleProject: true, IdleDays: 30, RequiresCleanGit: true, SkipIfAgentInProject: true}
	return r
}

// TestValidate feeds synthetic bad rules to Validate; each must produce an
// error that starts with the offending rule's ID.
func TestValidate(t *testing.T) {
	if errs := rules.Validate([]rules.Rule{goodRule("good.one", "Library/Caches"), goodProject("project.a", "a"), goodProject("project.b", "b")}); len(errs) != 0 {
		t.Fatalf("valid rules rejected: %v", errs)
	}
	mod := func(id, root string, f func(*rules.Rule)) rules.Rule {
		r := goodRule(id, root)
		f(&r)
		return r
	}
	cases := []struct {
		name  string
		rules []rules.Rule
		want  string // the error must start with this and contain the rest
		frag  string
	}{
		{"Bad ID", []rules.Rule{goodRule("BadID", "Library/Caches")}, "BadID:", "ID does not match"},
		{"Duplicate ID", []rules.Rule{goodRule("dup.id", "Library/Caches"), goodRule("dup.id", "Library/Logs")}, "dup.id:", "duplicate rule ID"},
		{"Invalid tier", []rules.Rule{mod("bad.tier", "Library/Caches", func(r *rules.Rule) { r.Tier = "bogus" })}, "bad.tier:", "invalid tier"},
		{"Empty title", []rules.Rule{mod("no.title", "Library/Caches", func(r *rules.Rule) { r.Title = " " })}, "no.title:", "empty title"},
		{"Empty what", []rules.Rule{mod("no.what", "Library/Caches", func(r *rules.Rule) { r.What = "" })}, "no.what:", "empty what"},
		{"Two sentences", []rules.Rule{mod("two.sentences", "Library/Caches", func(r *rules.Rule) { r.IfRemoved = "One. Two." })}, "two.sentences:", "single sentence"},
		{"No full stop", []rules.Rule{mod("no.stop", "Library/Caches", func(r *rules.Rule) { r.What = "No stop" })}, "no.stop:", "full stop"},
		{"Info without owner command", []rules.Rule{mod("info.nocmd", "Library/Caches", func(r *rules.Rule) { r.Tier = rules.TierInfo })}, "info.nocmd:", "owner's own command"},
		{"Unknown match kind", []rules.Rule{mod("bad.kind", "Library/Caches", func(r *rules.Rule) { r.Match.Kind = "everything" })}, "bad.kind:", "unknown match kind"},
		{"Extensions without extensions", []rules.Rule{mod("no.ext", "Downloads", func(r *rules.Rule) { r.Match.Kind = rules.MatchExtensions })}, "no.ext:", "without extensions"},
		{"Malformed extension", []rules.Rule{mod("bad.ext", "Downloads", func(r *rules.Rule) {
			r.Match = rules.Match{Kind: rules.MatchExtensions, Extensions: []string{"dmg"}}
		})}, "bad.ext:", "should look like .ext"},
		{"Project rule without markers", []rules.Rule{func() rules.Rule { r := goodProject("project.nomarker", "x"); r.Match.Markers = nil; return r }()}, "project.nomarker:", "needs names and markers"},
		{"Glob marker", []rules.Rule{func() rules.Rule {
			r := goodProject("project.globmarker", "x")
			r.Match.Markers = []string{"next.config.*"}
			return r
		}()}, "project.globmarker:", "plain file name"},
		{"Project rule in safe tier", []rules.Rule{func() rules.Rule { r := goodProject("project.safe", "x"); r.Tier = rules.TierSafe; return r }()}, "project.safe:", "review tier"},
		{"Malformed exclusion", []rules.Rule{mod("bad.exclude", "Library/Caches", func(r *rules.Rule) { r.Exclude = []string{"["} })}, "bad.exclude:", "malformed exclusion"},
		{"No roots", []rules.Rule{mod("no.roots", "x", func(r *rules.Rule) { r.Roots = nil })}, "no.roots:", "no roots"},
		{"Absolute root", []rules.Rule{goodRule("abs.root", "/Library/Caches")}, "abs.root:", "not a valid home-relative root"},
		{"Bare glob root", []rules.Rule{goodRule("star.root", "*")}, "star.root:", "first segment"},
		{"Glob in the first segment", []rules.Rule{goodRule("star.cache", "*/Cache")}, "star.cache:", "first segment"},
		{"Info root not on the denylist", []rules.Rule{mod("info.open", "Library/Caches/big", func(r *rules.Rule) { r.Tier = rules.TierInfo; r.OwnerCommand = "tool clean" })}, "info.open:", "not on the denylist"},
		{"Confusable root", []rules.Rule{goodRule("long.s", "Documentſ")}, "long.s:", "not a valid home-relative root"},
		{"Denylisted root", []rules.Rule{goodRule("deny.root", "Library/Mail/V10")}, "deny.root:", "denylisted"},
		{"Root containing a denylisted path", []rules.Rule{goodRule("deny.ancestor", "Library")}, "deny.ancestor:", "denylisted"},
		{"Same root twice", []rules.Rule{goodRule("a.one", "Library/Logs"), goodRule("b.two", "Library/Logs")}, "a.one and b.two:", "both claim root"},
		{"Same root in another case", []rules.Rule{goodRule("a.one", "Library/Logs"), goodRule("b.two", "Library/logs")}, "a.one and b.two:", "both claim root"},
		{"Nested root not excluded", []rules.Rule{goodRule("outer.rule", "Library/Caches"), goodRule("inner.rule", "Library/Caches/inner")}, "inner.rule:", "does not exclude"},
		{"Root nested in its own root", []rules.Rule{mod("self.nest", "Library/Caches", func(r *rules.Rule) { r.Roots = append(r.Roots, "Library/Caches/x") })}, "self.nest:", "nested inside its own root"},
		{"Project names overlap", []rules.Rule{goodProject("project.a", "node_modules"), goodProject("project.b", "node_modules")}, "project.a and project.b:", "artifact name"},
		{"Project root overlaps another rule's root", []rules.Rule{goodRule("go.thing", "dev/cache"), goodProject("project.a", "x")}, "go.thing and project.a:", "overlaps root"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := rules.Validate(tc.rules)
			if len(errs) == 0 {
				t.Fatal("want an error, got none")
			}
			for _, err := range errs {
				if strings.HasPrefix(err.Error(), tc.want) && strings.Contains(err.Error(), tc.frag) {
					return
				}
			}
			t.Fatalf("no error starts with %q and contains %q: %v", tc.want, tc.frag, errs)
		})
	}

	// Exemptions the spec allows.
	info := mod("info.ok", ".claude/projects", func(r *rules.Rule) { r.Tier = rules.TierInfo; r.OwnerCommand = "tool clean" })
	if errs := rules.Validate([]rules.Rule{info}); len(errs) != 0 {
		t.Errorf("info rule on a denylisted root rejected: %v", errs)
	}
}

// TestCatalogueCount is the spec scenario "Catalogue count": every rule
// named in "v1 catalogue contents" is present with the stated tier.
func TestCatalogueCount(t *testing.T) {
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
		"project.next":         {{".next"}, {"next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs"}},
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
				marker := r.Match.Markers[0]
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

// An unknown or empty tier fails closed: not movable, and refused by the gate.
func TestUnknownTierIsNotMovable(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/x/f", Content: "x"}}})
	for _, tier := range []rules.Tier{"", "bogus", "SAFE"} {
		r := goodRule("odd.tier", "Library/Caches")
		r.Tier = tier
		if r.Movable() || r.RuleMovable() {
			t.Errorf("tier %q: reported movable", tier)
		}
		if d := policy.Authorize(policy.Env{Home: home}, r, home+"/Library/Caches/x"); d.Allowed || d.Reason != policy.ReasonInfoTier {
			t.Errorf("tier %q: want %q, got %+v", tier, policy.ReasonInfoTier, d)
		}
	}
	if r := goodRule("safe.tier", "Library/Caches"); !r.Movable() {
		t.Error("safe tier reported not movable")
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

// Only package-manager caches and dependency installs get the narrow
// protected-descendant check; every other rule gets the full one.
func TestPackageCacheRules(t *testing.T) {
	want := map[string]bool{
		"swiftpm.cache": true, "homebrew.cache": true, "npm.cache": true, "pnpm.store": true,
		"yarn.cache": true, "bun.cache": true, "pip.cache": true, "uv.cache": true,
		"poetry.cache": true, "cargo.registry": true, "go.build-cache": true, "go.mod-cache": true,
		"gradle.caches": true, "cocoapods.cache": true,
	}
	for _, r := range rules.Catalogue() {
		exp := want[r.ID] || r.Match.Kind == rules.MatchProjectArtifact
		if r.PackageCache != exp || r.RulePackageCache() != exp {
			t.Errorf("%s: PackageCache %v, want %v", r.ID, r.PackageCache, exp)
		}
	}
}

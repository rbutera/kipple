package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

// testEnv reads KIPPLE_HOME from the test environment (set by NewHome) and
// fails if anything asks for the real home.
func testEnv(t *testing.T) runtimeEnv {
	return runtimeEnv{
		getenv:  os.Getenv,
		geteuid: func() int { return 501 },
		userHome: func() (string, error) {
			t.Error("the real home was requested in a test")
			return "", errors.New("no real home in tests")
		},
		// A stand-in real home in its own temp folder, never the machine's.
		realHomes: func() ([]string, error) { return []string{fakeRealHome(t)}, nil },
		isTTY:     func(io.Writer) bool { return false },
	}
}

// fakeRealHome creates a stand-in for the real home in a temp folder.
func fakeRealHome(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := filepath.Join(base, "Users", "me")
	if err := os.MkdirAll(h, 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func cli(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return cliWith(t, testEnv(t), args...)
}

func cliWith(t *testing.T, rt runtimeEnv, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, rt)
	return code, out.String(), errb.String()
}

var old = time.Now().Add(-200 * 24 * time.Hour)

func fixture(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.example.app/blob", Size: 64 << 10, MTime: old},
			{Path: "Library/Logs/app/log.txt", Size: 1024, MTime: old},
			{Path: "go/pkg/mod/github.com/x/y.go", Size: 1024, MTime: old},
			{Path: "Downloads/Old.dmg", Size: 4096, MTime: old},
			{Path: ".ollama/models/blobs/sha", Size: 4096, MTime: old},
			{Path: "dev/app/package.json", Content: "{}", MTime: old},
			{Path: "dev/app/node_modules/x/i.js", Content: "x", MTime: old},
		},
		Dirs: []testutil.Dir{{Path: "Library/Caches/com.example.app", MTime: old}},
	})
}

// exactlyOneJSON decodes stdout as exactly one JSON document.
func exactlyOneJSON(t *testing.T, stdout string, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout holds more than one JSON document (%v):\n%s", err, stdout)
	}
}

// Spec scenarios "Scan JSON is parseable", "JSON plan", "Fixture home" and
// "Fixture unchanged", through the CLI.
func TestScanJSON(t *testing.T) {
	t.Run("Scan JSON is parseable", func(t *testing.T) {
		fixture(t)
		code, out, _ := cli(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if code != exitOK || len(p.Items) == 0 {
			t.Fatalf("exit %d, %d items", code, len(p.Items))
		}
	})
	t.Run("JSON plan", func(t *testing.T) {
		fixture(t)
		_, out, errOut := cli(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if p.SchemaVersion != plan.SchemaVersion || p.KippleVersion != version {
			t.Fatalf("plan header: schema=%d version=%q", p.SchemaVersion, p.KippleVersion)
		}
		_, schema, _ := cli(t, "schema", "plan")
		if err := plantest.ValidateWith([]byte(schema), "plan", []byte(out)); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(errOut, "{") {
			t.Fatalf("JSON leaked to stderr: %q", errOut)
		}
	})
	t.Run("Fixture home", func(t *testing.T) {
		home := fixture(t)
		_, out, _ := cli(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if p.Home != home {
			t.Fatalf("plan home %q, want %q", p.Home, home)
		}
		for _, it := range p.Items {
			if !strings.HasPrefix(it.Path, home+"/") {
				t.Errorf("path outside the fixture home: %s", it.Path)
			}
		}
	})
	t.Run("Fixture unchanged", func(t *testing.T) {
		home := fixture(t)
		before := testutil.Checksum(t, home)
		if code, _, _ := cli(t, "scan", "--json", "--level", "review"); code != exitOK {
			t.Fatalf("exit %d", code)
		}
		if testutil.Checksum(t, home) != before {
			t.Fatal("scan changed the fixture")
		}
	})
}

// Spec scenario "Unknown flag".
func TestUnknownFlag(t *testing.T) {
	t.Run("Unknown flag", func(t *testing.T) {
		fixture(t)
		if code, out, _ := cli(t, "scan", "--bogus"); code != exitUsage || out != "" {
			t.Fatalf("exit %d stdout %q", code, out)
		}
	})
}

// Spec scenario "Plan validates against schema", using kipple schema plan.
func scenarioPlanValidatesAgainstPrintedSchema(t *testing.T) {
	fixture(t)
	_, out, _ := cli(t, "scan", "--json", "--level", "review")
	code, schema, _ := cli(t, "schema", "plan")
	if code != exitOK {
		t.Fatalf("schema exit %d", code)
	}
	if err := plantest.ValidateWith([]byte(schema), "plan", []byte(out)); err != nil {
		t.Fatal(err)
	}
}

func TestPlanValidatesAgainstPrintedSchema(t *testing.T) {
	t.Run("Plan validates against schema", scenarioPlanValidatesAgainstPrintedSchema)
}

// Spec scenario "Unknown flag" and the JSON error envelope.
func TestUsageErrors(t *testing.T) {
	fixture(t)
	cases := [][]string{
		{"scan", "--bogus"},
		{"scan", "--bogus", "--json"},
		{"bogus"},
		{"scan", "extra"},
		{"--level", "everything", "scan"},
		{"schema", "bogus", "--json"},
		{"schema"},
		{"explain"},
		// Review finding: valid --json spellings other than the bare flag.
		{"scan", "--bogus", "--json=1"},
		{"scan", "--json=1", "--bogus"},
		{"--json=t", "bogus"},
		{"--json=TRUE", "scan", "extra"},
		{"--level", "everything", "--json=1", "scan"},
		{"scan", "--bogus", "--json=false"},
		{"scan", "--bogus", "--", "--json"},
		// Review finding: unknown help topics are usage errors.
		{"help", "bogus"},
		{"help", "bogus", "--json"},
		{"help", "scan", "extra"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, out, errOut := cli(t, args...)
			if code != exitUsage {
				t.Fatalf("exit %d, want %d (stderr %q)", code, exitUsage, errOut)
			}
			if errOut == "" {
				t.Fatal("no message on stderr")
			}
			if jsonRequested(args) {
				var env errorEnvelope
				exactlyOneJSON(t, out, &env)
				if env.Error.Code != codeUsage || env.Error.Message == "" {
					t.Fatalf("envelope: %+v", env)
				}
			} else if out != "" {
				t.Fatalf("stdout should be empty, got %q", out)
			}
		})
	}
}

// With --json, every command prints exactly one JSON document on stdout.
func TestStdoutIsOnlyJSON(t *testing.T) {
	home := fixture(t)
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	cases := [][]string{
		{"scan", "--json"},
		{"--json", "scan", "--level", "review"},
		{"rules", "--json"},
		{"explain", "xcode.deriveddata", "--json"},
		{"explain", p.Items[0].ID, "--json"},
		{"schema", "plan", "--json"},
		{"schema", "rules"},
		{"version", "--json"},
		{"explain", "nonexistent.rule", "--json"},
		{"--json"},
		{"scan", "--json", "--help"},
		{"--json", "--help"},
		{"help", "scan", "--json"},
		{"scan", "--json=1"},
		{"rules", "--json=t"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, out, _ := cli(t, args...)
			var v any
			exactlyOneJSON(t, out, &v)
			if !strings.HasSuffix(out, "}\n") {
				t.Fatalf("stdout does not end with the document: %q", out[max(0, len(out)-20):])
			}
		})
	}
	_ = home
}

func TestHelpUnderJSON(t *testing.T) {
	fixture(t)
	code, out, errOut := cli(t, "--json")
	if errOut == "" {
		t.Fatal("bare --json: no message on stderr")
	}
	var env errorEnvelope
	exactlyOneJSON(t, out, &env)
	if code != exitUsage || env.Error.Code != codeUsage {
		t.Fatalf("bare --json: exit %d %+v", code, env)
	}
	code, out, errOut = cli(t, "scan", "--json", "--help")
	var h helpDocument
	exactlyOneJSON(t, out, &h)
	if code != exitOK || h.Command != "kipple scan" || !strings.Contains(h.Help, "Usage:") || !strings.Contains(errOut, "Usage:") {
		t.Fatalf("scan --json --help: exit %d doc %+v stderr %q", code, h, errOut)
	}
	if code, out, _ := cli(t); code != exitOK || !strings.Contains(out, "Exit codes") {
		t.Fatalf("bare kipple: exit %d", code)
	}
}

// Spec scenario "Explain a rule".
func scenarioExplainRule(t *testing.T) {
	fixture(t)
	code, out, _ := cli(t, "explain", "xcode.deriveddata")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	r, _ := rules.Lookup("xcode.deriveddata")
	for _, want := range []string{"safe", "~/Library/Developer/Xcode/DerivedData", r.What, r.IfRemoved} {
		if !strings.Contains(out, want) {
			t.Errorf("explain output lacks %q:\n%s", want, out)
		}
	}
}

func TestExplainRule(t *testing.T) { t.Run("Explain a rule", scenarioExplainRule) }

func TestExplainItem(t *testing.T) {
	home := fixture(t)
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	var target plan.Item
	for _, it := range p.Items {
		if it.Path == home+"/Library/Caches/com.example.app" {
			target = it
		}
	}
	code, out, _ := cli(t, "explain", target.ID)
	if code != exitOK || !strings.Contains(out, target.Path) || !strings.Contains(out, "Selected:   yes") || !strings.HasPrefix(out, "Home: "+home+"\n") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	_, out, _ = cli(t, "explain", target.ID, "--json")
	var doc explainDocument
	exactlyOneJSON(t, out, &doc)
	if doc.Item == nil || doc.Item.ID != target.ID || doc.Rule.ID != "caches.user" || doc.Home != home {
		t.Fatalf("explain JSON: %+v", doc)
	}
	code, out, _ = cli(t, "explain", "aaaaaaaaaaaa", "--json")
	var env errorEnvelope
	exactlyOneJSON(t, out, &env)
	if code != exitError || env.Error.Code != codeNotFound {
		t.Fatalf("unknown item: exit %d %+v", code, env)
	}
}

// Spec scenario "Version JSON".
func scenarioVersion(t *testing.T) {
	home := fixture(t)
	code, out, _ := cli(t, "version", "--json")
	var v map[string]any
	exactlyOneJSON(t, out, &v)
	if code != exitOK || v["version"] != version || v["commit"] != commit || v["schema_version"] != float64(plan.SchemaVersion) || v["home"] != home {
		t.Fatalf("version: exit %d %v", code, v)
	}
	_, out, _ = cli(t, "version")
	if !strings.Contains(out, "kipple "+version) || !strings.Contains(out, "schema_version 1") {
		t.Fatalf("version text: %q", out)
	}
}

// Spec scenario "Run as root".
func scenarioRunAsRoot(t *testing.T) {
	fixture(t)
	rt := testEnv(t)
	rt.geteuid = func() int { return 0 }
	for _, args := range [][]string{{"scan"}, {"version"}, {"scan", "--json"}} {
		code, out, errOut := cliWith(t, rt, args...)
		if code != exitError || !strings.Contains(errOut, "kipple will not run as root") {
			t.Fatalf("%v: exit %d stderr %q", args, code, errOut)
		}
		if jsonRequested(args) {
			var env errorEnvelope
			exactlyOneJSON(t, out, &env)
			if env.Error.Code != codeRoot || env.Error.Message != "kipple will not run as root" {
				t.Fatalf("envelope: %+v", env)
			}
		}
	}
}

func TestRunAsRoot(t *testing.T) { t.Run("Run as root", scenarioRunAsRoot) }

// Spec scenario "Dry-run footer".
func scenarioHumanScan(t *testing.T) {
	home := fixture(t)
	code, out, _ := cli(t, "scan")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "Selected total") || !strings.Contains(last, "Nothing was moved (dry run).") || !strings.Contains(last, "kipple clean --apply") {
		t.Fatalf("last line: %q", last)
	}
	if !strings.Contains(out, "Home:  "+home) || !strings.Contains(out, home+"/Library/Caches/com.example.app") {
		t.Fatalf("output lacks home or item path:\n%s", out)
	}
}

// Spec scenario "No Xcode installed": absent roots exit 0.
func scenarioEmptyHomeScan(t *testing.T) {
	testutil.NewHome(t, testutil.Spec{})
	code, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if code != exitOK || len(p.Items) != 0 {
		t.Fatalf("exit %d items %d", code, len(p.Items))
	}
	for _, r := range p.Rules {
		if r.Status != plan.RuleAbsent {
			t.Errorf("%s: %s", r.ID, r.Status)
		}
	}
}

// Spec scenarios "Catalogue listing" and "Human listing".
func TestRules(t *testing.T) {
	t.Run("Catalogue listing", func(t *testing.T) {
		home := fixture(t)
		_, out, _ := cli(t, "rules", "--json")
		var doc struct {
			SchemaVersion int              `json:"schema_version"`
			Home          string           `json:"home"`
			Rules         []map[string]any `json:"rules"`
		}
		exactlyOneJSON(t, out, &doc)
		if doc.SchemaVersion != 1 || doc.Home != home || len(doc.Rules) != len(rules.Catalogue()) {
			t.Fatalf("rules doc: %d %q %d", doc.SchemaVersion, doc.Home, len(doc.Rules))
		}
		for _, r := range doc.Rules {
			for _, k := range []string{"id", "title", "tier", "roots", "what", "if_removed", "owner"} {
				if _, ok := r[k]; !ok {
					t.Fatalf("rule %v lacks %s", r["id"], k)
				}
			}
		}
		if err := plantest.Validate("rules", []byte(out)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Human listing", func(t *testing.T) {
		home := fixture(t)
		code, text, _ := cli(t, "rules")
		if code != exitOK || !strings.Contains(text, "SAFE:") || !strings.Contains(text, "INFO:") || !strings.Contains(text, "Home: "+home) {
			t.Fatalf("human rules:\n%s", text)
		}
	})
}

func TestConfiguredLevelAndProjectRoots(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
		{Path: ".config/kipple/config.toml", Content: "level = \"review\"\nproject_roots = [\"~/work\"]\n"},
		{Path: "go/pkg/mod/github.com/x/y.go", Content: "x", MTime: old},
		{Path: "work/app/package.json", Content: "{}", MTime: old},
		{Path: "work/app/node_modules/x/i.js", Content: "x", MTime: old},
	}})
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	got := map[string]plan.Item{}
	for _, it := range p.Items {
		got[strings.TrimPrefix(it.Path, home+"/")] = it
	}
	if p.Level != rules.LevelReview || !got["go/pkg/mod/github.com"].Selected {
		t.Fatalf("config level not applied: %s %+v", p.Level, got["go/pkg/mod/github.com"])
	}
	if _, ok := got["work/app/node_modules"]; !ok {
		t.Fatal("configured project root not scanned")
	}
	_, out, _ = cli(t, "scan", "--json", "--level", "safe")
	exactlyOneJSON(t, out, &p)
	if p.Level != rules.LevelSafe {
		t.Fatal("--level must override config")
	}
}

func TestBadConfigIsAnError(t *testing.T) {
	testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: ".config/kipple/config.toml", Content: "bogus_key = 1\n"}}})
	code, out, _ := cli(t, "scan", "--json")
	var env errorEnvelope
	exactlyOneJSON(t, out, &env)
	if code != exitError || env.Error.Code != codeConfig {
		t.Fatalf("exit %d %+v", code, env)
	}
}

func TestHomeResolution(t *testing.T) {
	home := fixture(t)
	link := filepath.Join(filepath.Dir(home), "linked")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIPPLE_HOME", link)
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if p.Home != home {
		t.Fatalf("home %q, want the resolved %q", p.Home, home)
	}
	if len(p.Items) == 0 {
		t.Fatal("no items through a symlinked KIPPLE_HOME")
	}

	upper := filepath.Join(filepath.Dir(home), "HOME")
	if fi, err := os.Stat(upper); err != nil || !fi.IsDir() {
		t.Skip("case-sensitive volume")
	}
	t.Setenv("KIPPLE_HOME", upper)
	_, out, _ = cli(t, "scan", "--json")
	exactlyOneJSON(t, out, &p)
	if p.Home != home || len(p.Items) == 0 {
		t.Fatalf("case variant: home %q items %d, want %q", p.Home, len(p.Items), home)
	}

	for _, bad := range []string{"relative/home", home + "-missing", home + "/Library/Logs/app/log.txt"} {
		t.Setenv("KIPPLE_HOME", bad)
		code, out, _ := cli(t, "scan", "--json")
		var env errorEnvelope
		exactlyOneJSON(t, out, &env)
		if code != exitError || env.Error.Code != codeHome {
			t.Fatalf("%s: exit %d %+v", bad, code, env)
		}
	}
}

func TestHelpDocumentsExitCodes(t *testing.T) {
	code, out, _ := cli(t, "--help")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Exit codes", "0  success", "1  error", "2  usage", "3  nothing to do", "4  partial"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestHumanScan(t *testing.T) { t.Run("Dry-run footer", scenarioHumanScan) }

func TestVersion(t *testing.T) { t.Run("Version JSON", scenarioVersion) }

func TestEmptyHomeScan(t *testing.T) { t.Run("No Xcode installed", scenarioEmptyHomeScan) }

// Red-team finding: KIPPLE_HOME pointed at, into, or above the real home
// is refused (exit 1, code home); a separate temp fixture is accepted.
func TestKippleHomeInsideRealHomeRefused(t *testing.T) {
	fixture(t) // sets KIPPLE_HOME and KIPPLE_STATE_DIR to a fixture
	real := fakeRealHome(t)
	old := filepath.Join(real, "Documents", "OldMac")
	if err := os.MkdirAll(filepath.Join(old, "Library", "Caches", "com.example.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "Library", "Caches", "com.example.app", "blob"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(filepath.Dir(real)), "link-to-oldmac")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	rt := testEnv(t)
	rt.realHomes = func() ([]string, error) { return []string{real}, nil }
	cases := []struct{ name, home string }{
		{"Real home itself", real},
		{"Inside the real home", old},
		{"Inside the real home through a symlink", link},
		{"Inside the real home with different case", strings.ToUpper(old[:len(old)-len("OldMac")]) + "OldMac"},
		{"Ancestor of the real home", filepath.Dir(real)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := os.Stat(tc.home); err != nil {
				t.Skipf("spelling does not resolve on this volume: %v", err)
			}
			t.Setenv("KIPPLE_HOME", tc.home)
			code, out, errOut := cliWith(t, rt, "scan", "--json")
			var env errorEnvelope
			exactlyOneJSON(t, out, &env)
			if code != exitError || env.Error.Code != codeHome || !strings.Contains(errOut, "real home") {
				t.Fatalf("exit %d %+v stderr %q", code, env, errOut)
			}
		})
	}
	t.Run("Separate temp fixture", func(t *testing.T) {
		home := fixture(t)
		code, out, _ := cliWith(t, rt, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if code != exitOK || p.Home != home {
			t.Fatalf("exit %d home %q", code, p.Home)
		}
	})
	t.Run("Real home unknown fails closed", func(t *testing.T) {
		fixture(t)
		rt := testEnv(t)
		rt.realHomes = func() ([]string, error) { return nil, errors.New("unknown") }
		if code, _, _ := cliWith(t, rt, "scan"); code != exitError {
			t.Fatalf("exit %d", code)
		}
	})
}

// Review finding (wave 2): the KIPPLE_HOME guard compared spellings, so
// APFS aliases of the real home passed it and were then respelled to the
// real folder. Identity decides now. Each case below names a directory in
// the stand-in real home through an alias that the old string comparison
// accepted.
func TestKippleHomeAliasOfRealHomeRefused(t *testing.T) {
	fixture(t)
	refused := func(t *testing.T, rt runtimeEnv, home string) {
		t.Helper()
		if fi, err := os.Stat(home); err != nil || !fi.IsDir() {
			t.Skipf("spelling does not resolve on this volume: %v", err)
		}
		t.Setenv("KIPPLE_HOME", home)
		for _, args := range [][]string{{"scan", "--json"}, {"rules", "--json"}, {"version", "--json"}} {
			code, out, errOut := cliWith(t, rt, args...)
			var env errorEnvelope
			exactlyOneJSON(t, out, &env)
			if code != exitError || env.Error.Code != codeHome || !strings.Contains(errOut, "real home") {
				t.Fatalf("%v: exit %d %+v stderr %q", args, code, env, errOut)
			}
		}
	}
	mkReal := func(t *testing.T, name string) (string, string) {
		t.Helper()
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		real := filepath.Join(base, "Users", name)
		if err := os.MkdirAll(filepath.Join(real, "Documents", "OldMac", "Library", "Caches", "com.example.app"), 0o755); err != nil {
			t.Fatal(err)
		}
		return base, real
	}
	withReal := func(t *testing.T, real string) runtimeEnv {
		rt := testEnv(t)
		rt.realHomes = func() ([]string, error) { return []string{real}, nil }
		return rt
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

	// Firmlinks (/System/Volumes/Data/Users/<me> is /Users/<me>) cannot be
	// made in a temp folder, so a stub identity function models them over
	// real directories.
	firmlinked := func(t *testing.T, alias func(sys homeguard.FS, fixture, real string) homeguard.FS) {
		t.Helper()
		_, real := mkReal(t, "me")
		home := fixture(t)
		rt := withReal(t, real)
		rt.guardFS = alias(homeguard.System(), home, real)
		refused(t, rt, home)
	}
	t.Run("Firmlink spelling inside the real home", func(t *testing.T) {
		firmlinked(t, func(sys homeguard.FS, fx, real string) homeguard.FS {
			chain := sys.Chain
			sys.Chain = func(dir string, nofollow bool) ([]homeguard.ID, error) {
				if dir != fx {
					return chain(dir, nofollow)
				}
				r, err := chain(real, false)
				if err != nil {
					return nil, err
				}
				return append([]homeguard.ID{{Dev: r[0].Dev, Ino: 1 << 40}}, r...), nil
			}
			return sys
		})
	})
	t.Run("Firmlink spelling of the real home", func(t *testing.T) {
		firmlinked(t, func(sys homeguard.FS, fx, real string) homeguard.FS {
			chain := sys.Chain
			sys.Chain = func(dir string, nofollow bool) ([]homeguard.ID, error) {
				if dir == fx {
					dir, nofollow = real, false
				}
				return chain(dir, nofollow)
			}
			return sys
		})
	})
	t.Run("Data volume root reaching the real home by a firmlink", func(t *testing.T) {
		firmlinked(t, func(sys homeguard.FS, fx, real string) homeguard.FS {
			stat, chain := sys.Stat, sys.Chain
			sys.Stat = func(p string) (homeguard.ID, bool, error) {
				if p == filepath.Join(fx, "Users", "me") {
					r, err := chain(real, false)
					if err != nil {
						return homeguard.ID{}, false, err
					}
					return r[0], true, nil
				}
				return stat(p)
			}
			return sys
		})
	})
	t.Run("Identity lookup failure fails closed", func(t *testing.T) {
		firmlinked(t, func(sys homeguard.FS, _, _ string) homeguard.FS {
			sys.Chain = func(string, bool) ([]homeguard.ID, error) { return nil, errors.New("no identity") }
			return sys
		})
	})
	t.Run("Separate fixture with identities is accepted", func(t *testing.T) {
		_, real := mkReal(t, "me")
		home := fixture(t)
		code, out, _ := cliWith(t, withReal(t, real), "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if code != exitOK || p.Home != home {
			t.Fatalf("exit %d home %q", code, p.Home)
		}
	})
}

// Review finding: with KIPPLE_HOME set, version reports a refused home as
// an error (exit 1, code home) like every other command, and prints the
// home when it is accepted; without KIPPLE_HOME it needs no home.
func TestVersionHome(t *testing.T) {
	home := fixture(t)
	if code, out, _ := cli(t, "version"); code != exitOK || !strings.Contains(out, "home "+home) {
		t.Fatalf("accepted home: exit %d %q", code, out)
	}
	for _, bad := range []string{fakeRealHome(t), home + "-missing", "relative"} {
		rt := testEnv(t)
		rt.realHomes = func() ([]string, error) { return []string{bad}, nil }
		t.Setenv("KIPPLE_HOME", bad)
		code, out, errOut := cliWith(t, rt, "version", "--json")
		var env errorEnvelope
		exactlyOneJSON(t, out, &env)
		if code != exitError || env.Error.Code != codeHome || errOut == "" {
			t.Fatalf("%s: exit %d %+v", bad, code, env)
		}
		if code, out, _ := cliWith(t, rt, "version"); code != exitError || out != "" {
			t.Fatalf("%s human: exit %d stdout %q", bad, code, out)
		}
	}
	t.Setenv("KIPPLE_HOME", "")
	rt := testEnv(t)
	rt.userHome = func() (string, error) {
		t.Error("version without KIPPLE_HOME must not resolve a home")
		return "", errors.New("no")
	}
	if code, out, _ := cliWith(t, rt, "version"); code != exitOK || strings.Contains(out, "home ") {
		t.Fatalf("no KIPPLE_HOME: exit %d %q", code, out)
	}
}

// help with a known topic, or none, still prints help and exits 0.
func TestHelpTopics(t *testing.T) {
	fixture(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help"}, "Exit codes"},
		{[]string{"help", "scan"}, "kipple scan"},
		{[]string{"help", "schema"}, "kipple schema"},
	} {
		code, out, _ := cli(t, tc.args...)
		if code != exitOK || !strings.Contains(out, tc.want) {
			t.Fatalf("%v: exit %d\n%s", tc.args, code, out)
		}
	}
}

// Spec scenario "Partial scan recorded", through the CLI: an item whose walk
// could not finish is listed with scan_partial, never selected, and the
// scan itself still succeeds (exit 0).
func scenarioPartialScanCLI(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/partial/ok", Size: 2048, MTime: old},
			{Path: "Library/Caches/partial/locked/x", Size: 1024, MTime: old},
			{Path: "Library/Caches/whole/blob", Size: 1024, MTime: old},
		},
		Dirs: []testutil.Dir{{Path: "Library/Caches/partial/locked", Unreadable: true}},
	})
	for _, level := range []string{rules.LevelSafe, rules.LevelReview} {
		code, out, _ := cli(t, "scan", "--json", "--level", level)
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if code != exitOK {
			t.Fatalf("level %s: exit %d, want %d", level, code, exitOK)
		}
		var partial, whole *plan.Item
		for i := range p.Items {
			switch p.Items[i].Path {
			case home + "/Library/Caches/partial":
				partial = &p.Items[i]
			case home + "/Library/Caches/whole":
				whole = &p.Items[i]
			}
		}
		if partial == nil || whole == nil {
			t.Fatalf("level %s: items missing: partial=%v whole=%v", level, partial, whole)
		}
		if partial.Selected || partial.ScanStatus != plan.ScanPartial || !slices.Contains(partial.Reasons, plan.ReasonScanPartial) {
			t.Fatalf("level %s: partial item %+v", level, *partial)
		}
		if !whole.Selected {
			t.Fatalf("level %s: complete item not selected: %+v", level, whole.Reasons)
		}
	}
}

func TestPartialScanCLI(t *testing.T) { t.Run("Partial scan recorded", scenarioPartialScanCLI) }

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

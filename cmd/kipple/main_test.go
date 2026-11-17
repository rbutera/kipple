package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		isTTY: func(io.Writer) bool { return false },
	}
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
func TestPlanValidatesAgainstPrintedSchema(t *testing.T) {
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
			if wantsJSON(args) {
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
func TestExplainRule(t *testing.T) {
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
	if code != exitOK || !strings.Contains(out, target.Path) || !strings.Contains(out, "Selected:   yes") {
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
func TestRunAsRoot(t *testing.T) {
	fixture(t)
	rt := testEnv(t)
	rt.geteuid = func() int { return 0 }
	for _, args := range [][]string{{"scan"}, {"version"}, {"scan", "--json"}} {
		code, out, errOut := cliWith(t, rt, args...)
		if code != exitError || !strings.Contains(errOut, "kipple will not run as root") {
			t.Fatalf("%v: exit %d stderr %q", args, code, errOut)
		}
		if wantsJSON(args) {
			var env errorEnvelope
			exactlyOneJSON(t, out, &env)
			if env.Error.Code != codeRoot || env.Error.Message != "kipple will not run as root" {
				t.Fatalf("envelope: %+v", env)
			}
		}
	}
}

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

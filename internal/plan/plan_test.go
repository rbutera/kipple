package plan_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	. "github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

func TestIDDeterminism(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z2-7]{12}$`)
	a := ID("caches.user", "Library/Caches/com.example.app")
	if a != ID("caches.user", "Library/Caches/com.example.app") {
		t.Fatal("ID is not deterministic")
	}
	if !pattern.MatchString(a) {
		t.Fatalf("ID %q does not match %s", a, pattern)
	}
	// Pinned: changing the ID function breaks agents' saved plans.
	if want := "2sqzl6oahyvx"; a != want { // computed independently with Python hashlib
		t.Fatalf("ID = %q, want %q (the ID function changed)", a, want)
	}
	distinct := map[string]bool{
		a: true,
		ID("logs.user", "Library/Caches/com.example.app"):   true,
		ID("caches.user", "Library/Caches/com.example.ap"):  true,
		ID("caches.use", "rLibrary/Caches/com.example.app"): true,
	}
	if len(distinct) != 4 {
		t.Fatal("distinct inputs share an ID")
	}
}

func samplePlan() Plan {
	at := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return Plan{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   at,
		KippleVersion: "0.0.0-test",
		Home:          "/tmp/fx/home",
		Level:         rules.LevelSafe,
		Totals:        Totals{ListedCount: 3, ListedBytes: 12288, SelectedCount: 1, SelectedBytes: 8192, ReclaimableBytes: 8192},
		Rules: []RuleStatus{
			{ID: "caches.user", Tier: "safe", Status: RulePresent, ItemCount: 1, Bytes: 8192},
			{ID: "xcode.deriveddata", Tier: "safe", Status: RuleAbsent},
			{ID: "project.node-modules", Tier: "review", Status: RulePresent, ItemCount: 1, Bytes: 4096},
			{ID: "ollama.models", Tier: "info", Status: RulePresent, ItemCount: 1},
		},
		Items: []Item{
			{
				ID: ID("caches.user", "Library/Caches/com.example.app"), Rule: "caches.user", Tier: "safe",
				Path: "/tmp/fx/home/Library/Caches/com.example.app", SizeBytes: 5000, AllocatedBytes: 8192,
				ReclaimableBytes: 8192, FileCount: 2, NewestMtime: old, ScanStatus: ScanComplete, Selected: true, Reasons: []string{},
			},
			{
				ID: ID("project.node-modules", "dev/app/node_modules"), Rule: "project.node-modules", Tier: "review",
				Path: "/tmp/fx/home/dev/app/node_modules", SizeBytes: 10, AllocatedBytes: 4096, ReclaimableBytes: 4096,
				FileCount: 1, NewestMtime: old, ScanStatus: ScanComplete,
				Reasons: []string{ReasonTierReview, ReasonGitUnpushed}, Git: &Git{State: GitUnpushed, Detail: "1 commits not pushed to origin/main"}, AtRisk: true,
			},
			{
				ID: ID("ollama.models", ".ollama/models/blobs"), Rule: "ollama.models", Tier: "info",
				Path: "/tmp/fx/home/.ollama/models/blobs", FileCount: 0, NewestMtime: old, ScanStatus: ScanPartial,
				Reasons: []string{ReasonTierInfo, ReasonScanPartial, ReasonContainsProtected}, ProtectedPaths: []string{"x/.env"},
				Warnings: []string{"locked: cannot be read"}, OwnerCommand: "ollama rm <model>",
			},
		},
		Warnings: []string{},
	}
}

func TestGoldenPlan(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, samplePlan()); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "plan.golden.json")
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("plan JSON differs from %s (run go test ./internal/plan -update):\n%s", golden, buf.String())
	}
	p, err := Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := Encode(&again, p); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.Bytes(), want) {
		t.Fatal("decode/encode does not round-trip")
	}
}

func TestPlanValidatesAgainstSchema(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, samplePlan()); err != nil {
		t.Fatal(err)
	}
	if err := plantest.Validate("plan", buf.Bytes()); err != nil {
		t.Fatalf("golden plan does not validate: %v", err)
	}
	bad := []struct{ name, from, to string }{
		{"Unknown reason", `"tier_review"`, `"looks_old"`},
		{"Wrong schema version", `"schema_version": 1`, `"schema_version": 2`},
		{"Unknown tier", `"tier": "info"`, `"tier": "whatever"`},
		{"Bad ID", `"id": "` + samplePlan().Items[0].ID, `"id": "UPPER` + samplePlan().Items[0].ID[5:]},
		{"Unknown field", `"selected": true`, `"selected": true, "deleted": true`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			doc := strings.Replace(buf.String(), tc.from, tc.to, 1)
			if doc == buf.String() {
				t.Fatalf("mutation %q not applied", tc.from)
			}
			if err := plantest.Validate("plan", []byte(doc)); err == nil {
				t.Fatal("invalid plan validated")
			}
		})
	}
}

func TestEverySchemaCompilesAndValidates(t *testing.T) {
	docs := map[string]any{
		"plan":    samplePlan(),
		"rules":   RulesDocument{SchemaVersion: SchemaVersion, Rules: rules.Catalogue()},
		"receipt": Receipt{SchemaVersion: SchemaVersion, ID: "20261120T093000Z-abc123", KippleVersion: "x", Home: "/h", Level: "safe", Moved: []MovedItem{}, Skipped: []SkippedItem{}},
		"result": func() Result {
			r := Result{SchemaVersion: SchemaVersion, Command: "clean", Home: "/h"}
			r.Normalize()
			return r
		}(),
		"history": History{SchemaVersion: SchemaVersion, Home: "/h", ReceiptsDir: "/s/receipts", Receipts: []Receipt{}, Warnings: []string{}},
	}
	for _, name := range Schemas() {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Encode(&buf, docs[name]); err != nil {
				t.Fatal(err)
			}
			if err := plantest.Validate(name, buf.Bytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := Schema("bogus"); err == nil {
		t.Fatal("unknown schema accepted")
	}
}

func TestDecodeRejects(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, samplePlan()); err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]string{
		"unknown field": strings.Replace(buf.String(), `"level"`, `"bogus": 1, "level"`, 1),
		"other version": strings.Replace(buf.String(), `"schema_version": 1`, `"schema_version": 7`, 1),
		"trailing data": buf.String() + "{}",
		"not json":      "nope",
		"too large":     buf.String()[:len(buf.String())-2] + `, "pad": "` + strings.Repeat("x", MaxDocumentSize) + `"}`,
	} {
		if _, err := Decode(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func TestReasonsClosedSet(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Reasons() {
		if seen[r] {
			t.Fatalf("duplicate reason %q", r)
		}
		seen[r] = true
	}
	for _, r := range []string{"tier_review", "tier_manual", "tier_info", "too_recent", "project_active", "git_dirty",
		"git_unpushed", "git_no_upstream", "git_not_a_repo", "git_unavailable", "owner_running",
		"scan_partial", "contains_mount", "contains_protected", "not_requested", "refused:symlink_in_path", "refused:denylisted"} {
		if !seen[r] {
			t.Errorf("missing reason %q", r)
		}
	}
}

func TestNormalizeEncodesEmptyArrays(t *testing.T) {
	p := Plan{SchemaVersion: SchemaVersion, Level: "safe", Items: []Item{{ID: "aaaaaaaaaaaa", Tier: "safe", ScanStatus: ScanComplete}}}
	p.Normalize()
	var buf bytes.Buffer
	if err := Encode(&buf, p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "null") {
		t.Fatalf("null in normalised plan:\n%s", buf.String())
	}
	if err := plantest.Validate("plan", buf.Bytes()); err != nil {
		t.Fatal(err)
	}
}

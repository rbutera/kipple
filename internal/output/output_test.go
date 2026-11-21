package output

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite golden files")

// These tests are written against the macOS catalogue's fixtures
// (Library/Caches, Library/Logs, Xcode) and test mechanics, not catalogue
// contents, so they use that catalogue on every host. The Linux catalogue
// has its own tests (internal/rules, and the Linux scenario tests).
func TestMain(m *testing.M) {
	rules.UseCatalogueOf(rules.PlatformDarwin)
	os.Exit(testutil.GuardedMain(m))
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 B", 999: "999 B", 1000: "1.0 kB", 1500: "1.5 kB", 999_949: "999.9 kB",
		1_000_000: "1.0 MB", 2_500_000_000: "2.5 GB", 3_000_000_000_000: "3.0 TB", -2000: "-2.0 kB",
	} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func fixturePlan(level string) plan.Plan {
	mt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	home := "/tmp/fx/home"
	item := func(rule, tier, rel string, alloc int64, selected bool, reasons ...string) plan.Item {
		return plan.Item{ID: plan.ID(rule, rel), Rule: rule, Tier: tier, Path: home + "/" + rel, SizeBytes: alloc - 100,
			AllocatedBytes: alloc, ReclaimableBytes: alloc, FileCount: 3, NewestMtime: mt, ScanStatus: plan.ScanComplete,
			Selected: selected, Reasons: append([]string{}, reasons...)}
	}
	partial := item("caches.user", "safe", "Library/Caches/partial", 4096, false, plan.ReasonScanPartial)
	partial.ScanStatus = plan.ScanPartial
	env := item("caches.user", "safe", "Library/Caches/withenv", 8192, false, plan.ReasonContainsProtected)
	env.ProtectedPaths = []string{".env"}
	info := item("ollama.models", "info", ".ollama/models/blobs", 4_200_000_000, false, plan.ReasonTierInfo)
	info.OwnerCommand = "ollama rm <model>"
	nm := item("project.node-modules", "review", "dev/app/node_modules", 120_000_000, false, plan.ReasonTierReview, plan.ReasonGitUnpushed)
	nm.Git = &plan.Git{State: plan.GitUnpushed}
	nm.AtRisk = true
	return plan.Plan{
		SchemaVersion: 1, GeneratedAt: mt, KippleVersion: "test", Home: home, Level: level,
		Totals: plan.Totals{ListedCount: 5, ListedBytes: 4_320_120_000 + 12288, SelectedCount: 1, SelectedBytes: 1_500_000, ReclaimableBytes: 1_500_000},
		Rules: []plan.RuleStatus{
			{ID: "caches.user", Tier: "safe", Status: plan.RulePresent, ItemCount: 3, Bytes: 1_512_288},
			{ID: "xcode.deriveddata", Tier: "safe", Status: plan.RuleAbsent},
			{ID: "logs.user", Tier: "safe", Status: plan.RuleError},
			{ID: "project.node-modules", Tier: "review", Status: plan.RulePresent, ItemCount: 1, Bytes: 120_000_000},
			{ID: "ollama.models", Tier: "info", Status: plan.RulePresent, ItemCount: 1, Bytes: 4_200_000_000},
		},
		Items: []plan.Item{
			item("caches.user", "safe", "Library/Caches/com.example.app", 1_500_000, true), partial, env, nm, info,
		},
		Warnings: []string{"rule logs.user: root Library/Logs cannot be scanned: symlink_in_path"},
	}
}

func titles() map[string]string {
	out := map[string]string{}
	for _, r := range rules.Catalogue() {
		out[r.ID] = r.Title
	}
	return out
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs (go test ./internal/output -update):\n%s", path, got)
	}
}

// Spec scenario "Dry-run footer" and "Human output shows paths and sizes".
func scenarioPlanGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Plan(&buf, fixturePlan(rules.LevelSafe), titles()); err != nil {
		t.Fatal(err)
	}
	golden(t, "scan.golden.txt", buf.Bytes())
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	for _, want := range []string{"Selected total 1.5 MB", "Nothing was moved (dry run).", "kipple clean --apply"} {
		if !strings.Contains(last, want) {
			t.Errorf("last line %q lacks %q", last, want)
		}
	}
	for _, it := range fixturePlan(rules.LevelSafe).Items {
		if !strings.Contains(out, it.Path) || !strings.Contains(out, Bytes(it.AllocatedBytes)) {
			t.Errorf("item %s missing path or size", it.Path)
		}
	}
}

func TestReviewNextStep(t *testing.T) {
	var buf bytes.Buffer
	if err := Plan(&buf, fixturePlan(rules.LevelReview), titles()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(buf.String(), "Next step: kipple clean --level review --apply\n") {
		t.Fatalf("footer: %q", buf.String())
	}
}

// Spec scenario "Human listing".
func scenarioRulesGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Rules(&buf, rules.Catalogue()); err != nil {
		t.Fatal(err)
	}
	golden(t, "rules.golden.txt", buf.Bytes())
	out := buf.String()
	prev := -1
	for _, tier := range rules.Tiers() {
		i := strings.Index(out, strings.ToUpper(string(tier))+":")
		if i <= prev {
			t.Fatalf("tier %s out of order", tier)
		}
		prev = i
	}
}

func TestExplain(t *testing.T) {
	r, _ := rules.Lookup("xcode.deriveddata")
	var buf bytes.Buffer
	if err := ExplainRule(&buf, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{r.What, r.IfRemoved, "~/Library/Developer/Xcode/DerivedData", "Tier:       safe"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("explain lacks %q", want)
		}
	}
	buf.Reset()
	p := fixturePlan(rules.LevelSafe)
	cr, _ := rules.Lookup("caches.user")
	if err := ExplainItem(&buf, p.Items[2], cr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{p.Items[2].Path, "contains_protected", "Protected:  .env", cr.What} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("explain item lacks %q", want)
		}
	}
}

func TestPlanGolden(t *testing.T) { t.Run("Dry-run footer", scenarioPlanGolden) }

func TestRulesGolden(t *testing.T) { t.Run("Human listing", scenarioRulesGolden) }

// F4: history and the restore result show pending and displaced counts.
func TestPendingAndDisplacedCounts(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	h := plan.History{Receipts: []plan.Receipt{{
		ID: "20261120T090000Z-abc123", CreatedAt: at,
		Moved:     []plan.MovedItem{{ID: "a"}, {ID: "b"}},
		Pending:   []plan.MovedItem{{ID: "c"}},
		Displaced: []plan.MovedItem{{ID: "d"}},
	}}}
	var b strings.Builder
	if err := History(&b, h); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "moved 2 (0 B), pending 1, displaced 1, skipped 0") {
		t.Fatalf("history line: %q", b.String())
	}
	r := plan.Result{ReceiptID: "20261120T090000Z-abc123", Restored: []plan.RestoredItem{{Kind: "moved"}, {Kind: "moved"}, {Kind: "pending"}, {Kind: "displaced"}}}
	b.Reset()
	if err := RestoreResult(&b, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Restored 4 items (moved 2, pending 1, displaced 1), skipped 0") {
		t.Fatalf("restore line: %q", b.String())
	}
}

// Task 2.8: the CLI's human result names the Trash the items went to:
// macOS's ~/.Trash wording is unchanged, and a freedesktop run (Linux)
// names ~/.local/share/Trash, the file manager and kipple restore. Goldens
// per mover in testdata/result_<mover>.golden.txt.
func TestResultGoldenPerPlatform(t *testing.T) {
	for _, mover := range []string{plan.MoverRename, plan.MoverFreedesktop} {
		t.Run(mover, func(t *testing.T) {
			trash := "/fx/home/.Trash"
			if mover == plan.MoverFreedesktop {
				trash = "/fx/home/.local/share/Trash/files"
			}
			r := plan.Result{Command: "clean", Home: "/fx/home", ReceiptID: "20261121T100000Z-abc123", ReceiptPath: "/fx/state/receipts/20261121T100000Z-abc123.json",
				Moved: []plan.MovedItem{{ID: "aaaaaaaaaaaa", Path: "/fx/home/.cache/pip/http", TrashPath: trash + "/http", Bytes: 4096, ReclaimableBytes: 4096, Mover: mover}}}
			var b strings.Builder
			if err := CleanResult(&b, r); err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "result_"+mover+".golden.txt")
			if *update {
				if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if b.String() != string(want) {
				t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
			}
			got := b.String()
			if mover == plan.MoverFreedesktop {
				if !strings.Contains(got, "~/.local/share/Trash, visible in your file manager") || strings.Contains(got, "Finder") {
					t.Fatalf("Linux result: %s", got)
				}
			} else if strings.Contains(got, "file manager") {
				t.Fatalf("macOS result changed: %s", got)
			}
		})
	}
}

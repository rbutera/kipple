package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/testutil"
)

// failingReader fails the test if anything reads it: commands must not read
// stdin unless they are confirming on a terminal.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("stdin was read")
	return 0, os.ErrClosed
}

// idleProcs reports that nothing is running, so CLI tests never depend on
// the machine's own processes.
type idleProcs struct{}

func (idleProcs) RunningBundleIDs(context.Context) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (idleProcs) RunningNames(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (idleProcs) ProcessCwds(context.Context, []string) ([]string, error) { return nil, nil }

// cleanFixture has two selected safe items, a review item, a manual item
// and an info item, and a .Trash folder.
func cleanFixture(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.example.app/blob", Size: 64 << 10, MTime: old},
			{Path: "Library/Logs/app/log.txt", Size: 1024, MTime: old},
			{Path: "go/pkg/mod/github.com/x/y.go", Size: 1024, MTime: old},
			{Path: "Downloads/Old.dmg", Size: 4096, MTime: old},
			{Path: ".ollama/models/blobs/sha", Size: 4096, MTime: old},
		},
		Dirs: []testutil.Dir{{Path: ".Trash", Mode: 0o700}, {Path: "Library/Caches/com.example.app", MTime: old}},
	})
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// Spec scenario "No flag" (clean-apply).
func TestCleanNoFlag(t *testing.T) {
	home := cleanFixture(t)
	before := testutil.Checksum(t, home)
	code, out, _ := cli(t, "clean")
	if code != exitOK || lastLine(out) != "This was a dry run: nothing was moved. Add --apply to move the selected items to the Trash." {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if testutil.Checksum(t, home) != before {
		t.Fatal("clean without --apply changed the home folder")
	}
	code, out, _ = cli(t, "clean", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if code != exitOK || p.Totals.SelectedCount != 2 || testutil.Checksum(t, home) != before {
		t.Fatalf("dry run JSON: exit %d totals %+v", code, p.Totals)
	}
}

// Spec scenarios "Piped apply" (clean-apply) and "Non-TTY clean"
// (cli-agent-interface): no prompt, stdin never read, the run proceeds.
func TestCleanNonInteractive(t *testing.T) {
	t.Run("Piped apply", func(t *testing.T) {
		home := cleanFixture(t)
		rt := testEnv(t)
		rt.isTTY = func(w io.Writer) bool { return true } // --json alone forbids the prompt
		code, out, _ := cliWith(t, rt, "clean", "--apply", "--json")
		var res plan.Result
		exactlyOneJSON(t, out, &res)
		if code != exitOK || len(res.Moved) != 2 || res.Home != home {
			t.Fatalf("exit %d result %+v", code, res)
		}
		_, schema, _ := cli(t, "schema", "result")
		if err := plantest.ValidateWith([]byte(schema), "result", []byte(out)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Non-TTY clean", func(t *testing.T) {
		home := cleanFixture(t)
		code, out, _ := cli(t, "clean", "--apply")
		if code != exitOK || !strings.Contains(out, "Moved 2 items to the Trash") {
			t.Fatalf("exit %d output:\n%s", code, out)
		}
		if _, err := os.Lstat(filepath.Join(home, "Library/Logs/app")); !os.IsNotExist(err) {
			t.Fatal("item not moved")
		}
	})
}

// On a terminal without --yes, apply shows the total and the word Trash and
// waits for y.
func TestCleanConfirmsOnTTY(t *testing.T) {
	for _, tc := range []struct {
		answer string
		moved  bool
		exit   int
	}{{"n\n", false, exitNothing}, {"\n", false, exitNothing}, {"y\n", true, exitOK}} {
		t.Run(strings.TrimSpace("answer "+tc.answer), func(t *testing.T) {
			home := cleanFixture(t)
			rt := testEnv(t)
			rt.isTTY = func(io.Writer) bool { return true }
			rt.stdin = strings.NewReader(tc.answer)
			code, out, _ := cliWith(t, rt, "clean", "--apply")
			if !strings.Contains(out, "Move 2 items (") || !strings.Contains(out, "to the Trash? [y/N]") || code != tc.exit {
				t.Fatalf("exit %d output:\n%s", code, out)
			}
			_, err := os.Lstat(filepath.Join(home, "Library/Logs/app"))
			if moved := os.IsNotExist(err); moved != tc.moved {
				t.Fatalf("moved = %v, want %v", moved, tc.moved)
			}
		})
	}
	t.Run("--yes skips the question", func(t *testing.T) {
		cleanFixture(t)
		rt := testEnv(t)
		rt.isTTY = func(io.Writer) bool { return true }
		code, out, _ := cliWith(t, rt, "clean", "--apply", "--yes")
		if code != exitOK || strings.Contains(out, "[y/N]") {
			t.Fatalf("exit %d output:\n%s", code, out)
		}
	})
}

func TestCleanUsage(t *testing.T) {
	home := cleanFixture(t)
	before := testutil.Checksum(t, home)
	for _, args := range [][]string{
		{"clean", "--permanent"},
		{"clean", "--only", "not-an-id", "--apply"},
		{"restore"},
	} {
		if code, _, _ := cli(t, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if testutil.Checksum(t, home) != before {
		t.Fatal("a usage error changed the home folder")
	}
}

// Spec scenario "Agent approves a subset" through the CLI, with a plan file.
func TestCleanPlanFileSubset(t *testing.T) {
	home := cleanFixture(t)
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	file := filepath.Join(filepath.Dir(home), "plan.json")
	if err := os.WriteFile(file, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	var pick string
	for _, it := range p.Items {
		if strings.HasSuffix(it.Path, "/Downloads/Old.dmg") {
			pick = it.ID
		}
	}
	code, out, _ := cli(t, "clean", "--plan", file, "--only", pick, "--apply", "--json")
	var res plan.Result
	exactlyOneJSON(t, out, &res)
	if code != exitOK || len(res.Moved) != 1 || res.Moved[0].ID != pick || len(res.Untouched) != len(p.Items)-1 {
		t.Fatalf("exit %d result %+v", code, res)
	}
	t.Run("plan for another home", func(t *testing.T) {
		bad := strings.Replace(string(mustRead(t, file)), `"home": "`+home+`"`, `"home": "/elsewhere"`, 1)
		if err := os.WriteFile(file, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if code, _, _ := cli(t, "clean", "--plan", file, "--apply"); code != exitError {
			t.Fatalf("exit %d, want %d", code, exitError)
		}
	})
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Spec scenario "Empty history" (history-restore).
func TestEmptyHistory(t *testing.T) {
	cleanFixture(t)
	code, out, _ := cli(t, "history")
	if code != exitOK || !strings.Contains(out, "Nothing has been cleaned yet.") {
		t.Fatalf("exit %d output:\n%s", code, out)
	}
	code, out, _ = cli(t, "history", "--json")
	var h plan.History
	exactlyOneJSON(t, out, &h)
	if code != exitOK || h.Receipts == nil || len(h.Receipts) != 0 {
		t.Fatalf("exit %d history %+v", code, h)
	}
	_, schema, _ := cli(t, "schema", "history")
	if err := plantest.ValidateWith([]byte(schema), "history", []byte(out)); err != nil {
		t.Fatal(err)
	}
}

// Spec scenarios "Restore to vacant path" and "Original path occupied"
// (history-restore) through the CLI, with restore's dry-run default.
func TestRestoreCLI(t *testing.T) {
	home := cleanFixture(t)
	before := testutil.ContentChecksum(t, home, ".Trash")
	_, out, _ := cli(t, "clean", "--apply", "--json")
	var res plan.Result
	exactlyOneJSON(t, out, &res)

	code, out, _ := cli(t, "restore", res.ReceiptID)
	if code != exitOK || !strings.Contains(out, "Would restore 2 items") || !strings.Contains(out, "dry run") {
		t.Fatalf("dry run: exit %d output:\n%s", code, out)
	}
	if _, err := os.Lstat(res.Moved[0].Path); !os.IsNotExist(err) {
		t.Fatal("the dry run moved an item")
	}

	occupied := res.Moved[0]
	if err := os.MkdirAll(occupied.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, _ = cli(t, "restore", res.ReceiptID, "--apply", "--json")
	var rr plan.Result
	exactlyOneJSON(t, out, &rr)
	if code != exitPartial || len(rr.Restored) != 1 || len(rr.Skipped) != 1 || rr.Skipped[0].Reason != plan.SkipDestinationExists {
		t.Fatalf("exit %d result %+v", code, rr)
	}
	if err := os.Remove(occupied.Path); err != nil {
		t.Fatal(err)
	}
	// By item ID: the newest receipt that moved it and has not restored it.
	code, _, _ = cli(t, "restore", occupied.ID, "--apply")
	if code != exitOK {
		t.Fatalf("restore by item ID: exit %d", code)
	}
	if testutil.ContentChecksum(t, home, ".Trash") != before {
		t.Fatal("the fixture is not as it was")
	}
	_, out, _ = cli(t, "history", "--json")
	var h plan.History
	exactlyOneJSON(t, out, &h)
	if len(h.Receipts) != 1 || h.Receipts[0].Moved[0].RestoredAt == nil || h.Receipts[0].Moved[1].RestoredAt == nil {
		t.Fatalf("history %+v", h.Receipts)
	}
}

// End-to-end on a fixture (wave 3 brief; task 4.5's flow): scan --json,
// clean --apply --json --only <two ids>, history --json, restore --apply;
// every step exits 0 and the fixture is restored to its original checksum.
func TestEndToEnd(t *testing.T) {
	home := cleanFixture(t)
	before := testutil.ContentChecksum(t, home, ".Trash")

	code, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if code != exitOK {
		t.Fatalf("scan exit %d", code)
	}
	var ids []string
	for _, it := range p.Items {
		if strings.HasSuffix(it.Path, "/Library/Logs/app") || strings.HasSuffix(it.Path, "/Downloads/Old.dmg") {
			ids = append(ids, it.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("fixture items %v", ids)
	}

	code, out, _ = cli(t, "clean", "--apply", "--json", "--only", strings.Join(ids, ","))
	var res plan.Result
	exactlyOneJSON(t, out, &res)
	if code != exitOK || len(res.Moved) != 2 {
		t.Fatalf("clean exit %d result %+v", code, res)
	}
	moved := []string{res.Moved[0].ID, res.Moved[1].ID}
	slices.Sort(moved)
	slices.Sort(ids)
	if !slices.Equal(moved, ids) || testutil.ContentChecksum(t, home, ".Trash") == before {
		t.Fatal("clean did not move exactly the two items")
	}

	code, out, _ = cli(t, "history", "--json")
	var h plan.History
	exactlyOneJSON(t, out, &h)
	if code != exitOK || len(h.Receipts) != 1 || h.Receipts[0].ID != res.ReceiptID {
		t.Fatalf("history exit %d %+v", code, h)
	}

	code, _, _ = cli(t, "restore", res.ReceiptID, "--apply")
	if code != exitOK {
		t.Fatalf("restore exit %d", code)
	}
	if testutil.ContentChecksum(t, home, ".Trash") != before {
		t.Fatal("the fixture's checksum was not restored")
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".Trash")); len(entries) != 0 {
		t.Fatalf("the Trash still holds %v", entries)
	}
}

// "café" with a precomposed é (NFC) and with e plus a combining acute
// accent (NFD).
const (
	nfcName = "caf\u00e9"
	nfdName = "cafe\u0301"
)

// Addendum 3: a KIPPLE_HOME spelled in NFD for a folder stored in NFC is
// respelled to the stored bytes, so plan.home matches what the policy sees.
func TestNFDHomeIsRespelledToStoredNFC(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nfc := filepath.Join(base, nfcName)
	if err := os.MkdirAll(filepath.Join(nfc, "Library/Logs/app"), 0o755); err != nil {
		t.Fatal(err)
	}
	names, _ := os.ReadDir(base)
	if len(names) != 1 || names[0].Name() != nfcName {
		t.Skip("this volume does not store the name as given (not APFS?)")
	}
	nfd := filepath.Join(base, nfdName)
	if nfd == nfc {
		t.Fatal("test bug: NFC and NFD spellings are equal")
	}
	t.Setenv("KIPPLE_HOME", nfd)
	t.Setenv("KIPPLE_STATE_DIR", filepath.Join(base, "state"))
	testutil.GuardRealHome(t)
	code, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if code != exitOK || p.Home != nfc {
		t.Fatalf("exit %d, plan home %q (% x), want the stored NFC %q", code, p.Home, p.Home, nfc)
	}
}

// Should-fix 8: a plan file never raises the level. A plan made at level
// review, applied without --level review, does not move review items.
func TestPlanFileCannotRaiseLevel(t *testing.T) {
	home := cleanFixture(t)
	_, out, _ := cli(t, "scan", "--json", "--level", "review")
	file := filepath.Join(filepath.Dir(home), "plan.json")
	if err := os.WriteFile(file, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	review := filepath.Join(home, "go/pkg/mod/github.com")
	code, out, _ := cli(t, "clean", "--plan", file, "--apply", "--json")
	var res plan.Result
	exactlyOneJSON(t, out, &res)
	var skipped *plan.SkippedItem
	for i := range res.Skipped {
		if res.Skipped[i].Path == review {
			skipped = &res.Skipped[i]
		}
	}
	if code != exitPartial || skipped == nil || skipped.Reason != plan.ReasonTierReview || len(res.Moved) != 2 {
		t.Fatalf("exit %d result %+v", code, res)
	}
	if _, err := os.Lstat(review); err != nil {
		t.Fatal("the review item moved")
	}
	code, out, _ = cli(t, "clean", "--plan", file, "--apply", "--json", "--level", "review")
	exactlyOneJSON(t, out, &res)
	// The safe items went in the first run, so they are not_found now.
	if code != exitPartial || len(res.Moved) != 1 || res.Moved[0].Path != review {
		t.Fatalf("with --level review: exit %d %+v", code, res)
	}
}

// Should-fix 6: the dry run of a plan file runs the same re-derivation and
// re-checks as --apply and reports what would really move.
func TestPlanDryRunRechecks(t *testing.T) {
	home := cleanFixture(t)
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	var tampered string
	for i := range p.Items {
		if strings.HasSuffix(p.Items[i].Path, "/Library/Logs/app") {
			p.Items[i].Ino++
			tampered = p.Items[i].ID
		}
	}
	var buf strings.Builder
	if err := plan.Encode(&buf, p); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(home), "plan.json")
	if err := os.WriteFile(file, []byte(buf.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	before := testutil.Checksum(t, home)
	code, out, _ := cli(t, "clean", "--plan", file, "--json")
	var res plan.Result
	exactlyOneJSON(t, out, &res)
	if code != exitOK || !res.DryRun || len(res.Moved) != 1 || len(res.Skipped) != 1 || res.Skipped[0].ID != tampered || res.Skipped[0].Reason != plan.SkipIdentityChanged {
		t.Fatalf("exit %d result %+v", code, res)
	}
	if res.ReceiptID != "" || testutil.Checksum(t, home) != before {
		t.Fatal("the dry run wrote a receipt or changed the home folder")
	}
	code, out, _ = cli(t, "clean", "--plan", file)
	if code != exitOK || !strings.Contains(out, "Would move 1 item") || lastLine(out) != output.DryRunLine {
		t.Fatalf("human dry run: exit %d\n%s", code, out)
	}
}

// Must-fix 1, through the CLI: with KIPPLE_HOME set there is no
// NSFileManager fallback (it would move fixture items into the real
// Trash), so an unusable Trash is error trash_access and nothing moves.
func TestTrashAccessError(t *testing.T) {
	home := cleanFixture(t)
	before := testutil.ContentChecksum(t, home)
	if err := os.Chmod(filepath.Join(home, ".Trash"), 0); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := cli(t, "clean", "--apply", "--json")
	if err := os.Chmod(filepath.Join(home, ".Trash"), 0o700); err != nil {
		t.Fatal(err)
	}
	var env errorEnvelope
	exactlyOneJSON(t, out, &env)
	if code != exitError || env.Error.Code != codeTrashAccess || !strings.Contains(env.Error.Message, "Full Disk Access") || !strings.Contains(errOut, "Full Disk Access") {
		t.Fatalf("exit %d error %+v", code, env)
	}
	if testutil.ContentChecksum(t, home) != before {
		t.Fatal("something moved")
	}
}

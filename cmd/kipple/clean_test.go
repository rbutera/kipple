package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/receipt"
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
// and an info item, and a Trash folder.
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
		Dirs: append(testutil.TrashDirs(), testutil.Dir{Path: "Library/Caches/com.example.app", MTime: old}),
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
	before := testutil.ContentChecksum(t, home, testutil.TrashRoot)
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
	if testutil.ContentChecksum(t, home, testutil.TrashRoot) != before {
		t.Fatal("the fixture is not as it was")
	}
	_, out, _ = cli(t, "history", "--json")
	var h plan.History
	exactlyOneJSON(t, out, &h)
	if len(h.Receipts) != 1 || h.Receipts[0].Moved[0].RestoredAt == nil || h.Receipts[0].Moved[1].RestoredAt == nil {
		t.Fatalf("history %+v", h.Receipts)
	}
}

// Task 4.5, end to end on a fixture: scan --json, clean --apply --json
// --only <two ids> (a safe item and a manual one, which naming consents
// to), history --json, restore <receipt> --apply; every step exits 0 and the
// fixture is restored to its original checksum. The home is compared with
// ContentChecksum (moving an item out and back changes its parent folders'
// mtimes, which kipple does not promise to keep) and each moved item's own
// tree with the strict Checksum, directory mtimes included.
func TestEndToEnd(t *testing.T) {
	home := cleanFixture(t)
	before := testutil.ContentChecksum(t, home, testutil.TrashRoot)

	code, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if code != exitOK {
		t.Fatalf("scan exit %d", code)
	}
	var ids []string
	strict := map[string]string{}
	for _, it := range p.Items {
		if strings.HasSuffix(it.Path, "/Library/Logs/app") || strings.HasSuffix(it.Path, "/Downloads/Old.dmg") {
			ids = append(ids, it.ID)
			strict[it.Path] = itemChecksum(t, it.Path)
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
	if !slices.Equal(moved, ids) || testutil.ContentChecksum(t, home, testutil.TrashRoot) == before {
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
	if testutil.ContentChecksum(t, home, testutil.TrashRoot) != before {
		t.Fatal("the fixture's checksum was not restored")
	}
	if entries, _ := os.ReadDir(filepath.Join(home, testutil.TrashRel)); len(entries) != 0 {
		t.Fatalf("the Trash still holds %v", entries)
	}
	for path, sum := range strict {
		if itemChecksum(t, path) != sum {
			t.Errorf("%s differs from its original checksum", path)
		}
	}
}

// itemChecksum is testutil.Checksum of an item's tree plus the item's own
// mode, mtime and (for a file) contents, which Checksum, rooted at the
// item, does not cover.
func itemChecksum(t *testing.T, path string) string {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%o %d ", uint32(fi.Mode()), fi.ModTime().UnixNano())
	if fi.Mode().IsRegular() {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return sum + fmt.Sprintf("%x", sha256.Sum256(data))
	}
	return sum + testutil.Checksum(t, path)
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
	if err := os.Chmod(filepath.Join(home, testutil.TrashRel), 0); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := cli(t, "clean", "--apply", "--json")
	if err := os.Chmod(filepath.Join(home, testutil.TrashRel), 0o700); err != nil {
		t.Fatal(err)
	}
	var env errorEnvelope
	exactlyOneJSON(t, out, &env)
	if code != exitError || env.Error.Code != codeTrashAccess {
		t.Fatalf("exit %d error %+v", code, env)
	}
	// macOS explains Full Disk Access; Linux has no such thing and names
	// the folder that cannot be used.
	want := "Full Disk Access"
	if runtime.GOOS == "linux" {
		want = testutil.TrashRel
		if strings.Contains(env.Error.Message, "Full Disk Access") {
			t.Fatalf("the Linux message talks about Full Disk Access: %q", env.Error.Message)
		}
	}
	if !strings.Contains(env.Error.Message, want) || !strings.Contains(errOut, want) {
		t.Fatalf("message %q / stderr %q lacks %q", env.Error.Message, errOut, want)
	}
	if testutil.ContentChecksum(t, home) != before {
		t.Fatal("something moved")
	}
}

// Spec scenario "Receipt file path refused" (history-restore): restore
// uses only receipts in kipple's own receipts folder; a receipt file
// elsewhere is a usage error and moves nothing.
func TestRestoreRefusesReceiptFiles(t *testing.T) {
	t.Run("Receipt file path refused", scenarioReceiptFilePathRefused)
}

func scenarioReceiptFilePathRefused(t *testing.T) {
	home := cleanFixture(t)
	_, out, _ := cli(t, "clean", "--apply", "--json")
	var res plan.Result
	exactlyOneJSON(t, out, &res)
	copyPath := filepath.Join(filepath.Dir(home), "elsewhere.json")
	if err := os.WriteFile(copyPath, mustRead(t, res.ReceiptPath), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := cli(t, "restore", copyPath, "--apply", "--json")
	var env errorEnvelope
	exactlyOneJSON(t, out, &env)
	if code != exitUsage || env.Error.Code != codeUsage || !strings.Contains(env.Error.Message, "receipt files are not accepted") {
		t.Fatalf("exit %d %+v", code, env)
	}
	if _, err := os.Lstat(res.Moved[0].Path); !os.IsNotExist(err) {
		t.Fatal("something was restored")
	}
}

// failingSave fails every receipt save from the nth on, as a full disk
// would; earlier saves go to the real store.
func failingSave(t *testing.T, n int) func(plan.Receipt) error {
	calls := 0
	return func(r plan.Receipt) error {
		calls++
		if calls >= n {
			return errors.New("no space left on device")
		}
		dir := os.Getenv("KIPPLE_STATE_DIR")
		s, err := receipt.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return s.Save(r)
	}
}

// Spec scenario "Receipt save fails before a move" (clean-apply).
func TestReceiptSaveFailsBeforeAMove(t *testing.T) {
	t.Run("Receipt save fails before a move", func(t *testing.T) {
		home := cleanFixture(t)
		before := testutil.Checksum(t, home)
		rt := testEnv(t)
		rt.saveReceipt = failingSave(t, 1)
		code, out, _ := cliWith(t, rt, "clean", "--apply", "--json")
		var env errorEnvelope
		exactlyOneJSON(t, out, &env)
		if code != exitError || env.Error.Code != codeReceipt || env.Result == nil || len(env.Result.Moved) != 0 {
			t.Fatalf("exit %d %+v", code, env)
		}
		if testutil.Checksum(t, home) != before {
			t.Fatal("something moved")
		}
	})
	t.Run("the partial result is in the error", func(t *testing.T) {
		cleanFixture(t)
		rt := testEnv(t)
		rt.saveReceipt = failingSave(t, 2)
		code, out, _ := cliWith(t, rt, "clean", "--apply", "--json")
		var env errorEnvelope
		exactlyOneJSON(t, out, &env)
		if code != exitError || env.Error.Code != codeReceipt || env.Result == nil || len(env.Result.Moved) != 1 {
			t.Fatalf("exit %d %+v", code, env)
		}
	})
}

// Spec scenario "Restore a pending item" (history-restore): a run stopped
// after a move but before recording it leaves the item pending; history
// shows it, and restore by its item ID puts it back.
func TestRestoreAPendingItem(t *testing.T) {
	t.Run("Restore a pending item", func(t *testing.T) {
		home := cleanFixture(t)
		before := testutil.ContentChecksum(t, home, testutil.TrashRoot)
		rt := testEnv(t)
		rt.saveReceipt = failingSave(t, 2)
		_, out, _ := cliWith(t, rt, "clean", "--apply", "--json")
		var env errorEnvelope
		exactlyOneJSON(t, out, &env)
		if env.Result == nil || len(env.Result.Moved) != 1 {
			t.Fatalf("%+v", env)
		}
		_, hout, _ := cli(t, "history")
		if !strings.Contains(hout, "pending 1") {
			t.Fatalf("history does not show the pending item:\n%s", hout)
		}
		code, rout, _ := cli(t, "restore", env.Result.Moved[0].ID, "--apply")
		if code != exitOK || !strings.Contains(rout, "pending 1") {
			t.Fatalf("exit %d\n%s", code, rout)
		}
		if testutil.ContentChecksum(t, home, testutil.TrashRoot) != before {
			t.Fatal("not restored")
		}
	})
}

// Spec scenario "Restore finds a renamed Trash entry by identity"
// (history-restore): an entry renamed inside the Trash (by Finder, or a
// restore interrupted midway) is found again by its identity and tag.
func TestRestoreFindsARenamedTrashEntry(t *testing.T) {
	t.Run("Restore finds a renamed Trash entry by identity", func(t *testing.T) {
		home := cleanFixture(t)
		before := testutil.ContentChecksum(t, home, testutil.TrashRoot)
		_, out, _ := cli(t, "clean", "--apply", "--json")
		var res plan.Result
		exactlyOneJSON(t, out, &res)
		for _, m := range res.Moved {
			if err := os.Rename(m.TrashPath, m.TrashPath+" renamed"); err != nil {
				t.Fatal(err)
			}
		}
		code, _, _ := cli(t, "restore", res.ReceiptID, "--apply")
		if code != exitOK || testutil.ContentChecksum(t, home, testutil.TrashRoot) != before {
			t.Fatalf("exit %d", code)
		}
	})
}

// F1: restore refuses a symlinked receipts folder, as history does.
func TestRestoreRefusesASymlinkedReceiptsFolder(t *testing.T) {
	cleanFixture(t)
	_, out, _ := cli(t, "clean", "--apply", "--json")
	var res plan.Result
	exactlyOneJSON(t, out, &res)
	dir := filepath.Dir(res.ReceiptPath)
	if err := os.Rename(dir, dir+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir+".real", dir); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := cli(t, "restore", res.ReceiptID, "--apply")
	if code != exitError || !strings.Contains(errOut, "symbolic link") {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if _, err := os.Lstat(res.Moved[0].Path); !os.IsNotExist(err) {
		t.Fatal("something was restored")
	}
}

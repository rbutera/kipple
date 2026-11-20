package guimodel

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/testutil"
)

// Brief 7.3: a displaced object shows the Displaced block (ink, never
// amber) and a receipt-card row; the skipped item gives its reason.
func TestDisplacedOnDoneScreen(t *testing.T) {
	f := newFx(t, fixtureSpec())
	f.cfg.ApplyFunc = func(_ context.Context, o apply.Options) (plan.Result, error) {
		p := f.home + "/Library/Logs/app"
		return plan.Result{
			ReceiptID: "20261120T000000Z-abcdef",
			Displaced: []plan.MovedItem{{ID: o.Only[0], Path: p, TrashPath: f.home + "/.Trash/app", Bytes: 10, Mover: plan.MoverNSFileManager}},
			Skipped:   []plan.SkippedItem{{ID: o.Only[0], Path: p, Reason: plan.SkipIdentityChanged}},
		}, nil
	}
	m := f.scanned()
	m.OpenConfirm()
	if err := m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	d := m.Done()
	if !strings.HasPrefix(d.Displaced, "Something else was at an item's name when macOS's Trash command ran") ||
		!strings.Contains(d.Displaced, "kipple restore 20261120T000000Z-abcdef --apply") {
		t.Fatalf("displaced block %q", d.Displaced)
	}
	if d.Card.Displaced != 1 || d.Card.Skipped != 1 {
		t.Fatalf("card %+v", d.Card)
	}
	if len(d.Skipped) != 1 || d.Skipped[0].Text != "It changed after the scan, so it is not the item kipple checked." || d.Skipped[0].Next != "Scan again." {
		t.Fatalf("skipped %+v", d.Skipped)
	}
	if d.Title != "Nothing was moved." || d.Command == "" {
		t.Fatalf("%+v", d)
	}
}

// Brief 6.5: an item whose owner is running says which app to quit.
func TestOwnerRunningSentence(t *testing.T) {
	f := newFx(t, testutil.Spec{})
	f.cfg.ScanFunc = fakePlan(plan.Plan{Items: []plan.Item{
		pItem("e00000000001", "xcode.deriveddata", "safe", f.home+"/Library/Developer/Xcode/DerivedData/App-x", 10, false, plan.ReasonOwnerRunning),
	}})
	m := f.scanned()
	d, _ := m.Detail()
	if d.Status != StatusBlocked || len(d.Blockers) != 1 || d.Blockers[0] != "The app that owns it is running. Quit Xcode and scan again." {
		t.Fatalf("%+v", d)
	}
	if v := m.Item(0); v.Chip != "app open" {
		t.Fatalf("chip %q", v.Chip)
	}
}

// apply reports "checking <path>" then "moving <path>" for each item; the
// applying sheet shows both, home-relative, and counts items once.
func TestApplyingProgressLines(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	m.OpenConfirm()
	if _, ok := m.StartApply(); !ok {
		t.Fatal("no apply")
	}
	p := f.home + "/Library/Logs/app"
	m.ApplyProgress("checking " + p)
	if v := m.Applying(); v.Current != "Checking ~/Library/Logs/app" || !strings.HasPrefix(v.Count, "0 of ") {
		t.Fatalf("%+v", v)
	}
	m.ApplyProgress("moving " + p)
	if v := m.Applying(); v.Current != "Moving ~/Library/Logs/app" || !strings.HasPrefix(v.Count, "0 of ") {
		t.Fatalf("%+v", v)
	}
	m.ApplyProgress("checking " + f.home + "/Library/Caches/com.other.app")
	if v := m.Applying(); !strings.HasPrefix(v.Count, "1 of ") || v.Bytes == "0 B" {
		t.Fatalf("%+v", v)
	}
}

// The confirmation lists rule titles in the review list's order (tier,
// then rule bytes), not plan order.
func TestConfirmRulesInListOrder(t *testing.T) {
	f := newFx(t, testutil.Spec{})
	h := f.home
	f.cfg.ScanFunc = fakePlan(plan.Plan{Items: []plan.Item{
		pItem("f00000000001", "logs.user", "safe", h+"/Library/Logs/a", 5, true),
		pItem("f00000000002", "go.mod-cache", "review", h+"/go/pkg/mod/a", 500, false, plan.ReasonTierReview),
		pItem("f00000000003", "caches.user", "safe", h+"/Library/Caches/a", 50, true),
	}})
	m := f.scanned()
	m.ToggleRow(rowOf(t, m, "go/pkg/mod/a"))
	m.OpenConfirm()
	want := "from 3 rules: " + m.ruleTitle("caches.user") + ", " + m.ruleTitle("logs.user") + ", " + m.ruleTitle("go.mod-cache")
	if got := m.Confirm().Rules; got != want {
		t.Fatalf("rules %q, want %q", got, want)
	}
}

// When the Trash can't be used for a reason other than permission, the
// sheet says what was found, not the Full Disk Access story.
func TestMissingTrashNote(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	if err := os.Remove(testutil.Abs(f.home, ".Trash")); err != nil {
		t.Fatal(err)
	}
	m.OpenConfirm()
	v := m.Confirm()
	if v.CanMove || strings.Contains(v.Note, "Full Disk Access") || !strings.HasPrefix(v.Note, "kipple can't use the Trash folder here, so it can't move anything: ") {
		t.Fatalf("%+v", v)
	}
}

// After Put back the done screen reports the put back, not the move; when
// something could not go back (its place is taken), Put back is offered
// again, and a second run adds up with the first.
func TestPartialPutBackAndRetry(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	m.OpenConfirm()
	if err := m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	total := len(m.Result().Moved)
	blocker := testutil.Abs(f.home, "Library/Logs/app")
	if err := os.Mkdir(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	m.OpenPutBack()
	if err := m.PutBackNow(); err != nil {
		t.Fatal(err)
	}
	d := m.Done()
	want := "Put back " + itoa(total-1) + " of " + itoa(total) + " items"
	if d.Title != want || d.SpaceLine != "is back where it was." || d.MovedLabel != "PUT BACK" || d.SkippedLabel != "STILL IN THE TRASH" {
		t.Fatalf("%+v", d)
	}
	if strings.Contains(d.SpaceLine+d.SharedNote, "Empty the Trash") || d.SharedNote != "1 item is still in the Trash; the list says why." {
		t.Fatalf("space line %q note %q", d.SpaceLine, d.SharedNote)
	}
	if len(d.Skipped) != 1 || d.Skipped[0].Text != "Something is already at its original path." || !d.CanPutBack || d.ChangedMind == "" {
		t.Fatalf("%+v", d)
	}
	m.OpenPutBack()
	if v := m.PutBack(); v.Title != "Put 1 item back?" {
		t.Fatalf("%+v", v)
	}
	m.CancelPutBack()
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	m.OpenPutBack()
	if err := m.PutBackNow(); err != nil {
		t.Fatal(err)
	}
	d = m.Done()
	if d.Title != "Put back "+itoa(total)+" items" || len(d.Skipped) != 0 || d.CanPutBack || d.ChangedMind != "" || len(d.Moved)+d.MovedMore != total {
		t.Fatalf("%+v", d)
	}
	if entries, _ := os.ReadDir(testutil.Abs(f.home, ".Trash")); len(entries) != 0 {
		t.Fatalf("Trash still holds %d", len(entries))
	}
}

package guimodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/selection"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/words"
)

// Pre-selection: exactly what a scan at level safe selects, never info.
func TestPreselectionIsScanLevelSafe(t *testing.T) {
	f := newFx(t, fixtureSpec())
	f.cfg.Scan.Level = rules.LevelReview // the GUI ignores the level it is given
	m := f.scanned()
	p, err := scan.Run(context.Background(), scan.Options{Env: f.cfg.Scan.Env, Rules: f.cfg.Scan.Rules, Level: rules.LevelSafe})
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, it := range p.Items {
		if it.Selected {
			want = append(want, it.ID)
		}
	}
	if got := m.Preselected(); !reflect.DeepEqual(got, want) || len(want) == 0 {
		t.Fatalf("pre-selected %v, scan --level safe selects %v", got, want)
	}
	if !reflect.DeepEqual(m.Selected(), want) {
		t.Fatalf("selected %v", m.Selected())
	}
}

// Spec "Tier parity": info items are never selectable, by any path.
func TestInfoItemsNeverSelectable(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	row := rowOf(t, m, ".ollama/models/blobs")
	it := itemAt(m, row)
	if it.Tier != "info" || m.Item(m.Rows()[row].Item).Tile != TileInfo {
		t.Fatalf("not an info row: %+v", it)
	}
	m.ToggleRow(row)
	if contains(m.Selected(), it.ID) {
		t.Fatal("toggling selected an info item")
	}
	if !strings.Contains(m.Status(), "Instead, run: ollama rm <model>") {
		t.Fatalf("status %q", m.Status())
	}
	m.SetCursor(row)
	m.Toggle()
	m.ToggleCursorGroup()
	m.ToggleGroup(m.Rows()[row].Group)
	if contains(m.Selected(), it.ID) {
		t.Fatal("a toggle path selected an info item")
	}
	if g := m.Group(m.Rows()[row].Group); g.Tile != selection.NoTile {
		t.Fatalf("info group tile %v", g.Tile)
	}
	if d, _ := m.Detail(); d.Status != StatusInfo || d.Instead != "ollama rm <model>" {
		t.Fatalf("detail %+v", d)
	}
}

// Spec "Confirmation required": select, open the confirmation, cancel:
// nothing under home changed and no receipt was written.
func TestConfirmationRequired(t *testing.T) {
	f := newFx(t, fixtureSpec())
	before := testutil.Checksum(t, f.home)
	m := f.scanned()
	m.ToggleRow(rowOf(t, m, "go/pkg/mod/github.com"))
	m.OpenConfirm()
	if m.Sheet() != SheetConfirm {
		t.Fatalf("sheet %d", m.Sheet())
	}
	m.CancelConfirm()
	if m.Sheet() != SheetNone || m.Status() != "Cancelled. Nothing was moved." {
		t.Fatalf("sheet %d status %q", m.Sheet(), m.Status())
	}
	if _, ok := m.StartApply(); ok {
		t.Fatal("apply started without the confirmation")
	}
	if testutil.Checksum(t, f.home) != before {
		t.Fatal("the fixture changed")
	}
	if es, _ := f.store.List(); len(es) != 0 {
		t.Fatalf("%d receipts written", len(es))
	}
}

func TestReviewAndManualOptIn(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	review := rowOf(t, m, "go/pkg/mod/github.com")
	manual := rowOf(t, m, "Downloads/Old.dmg")
	for _, r := range []int{review, manual} {
		if contains(m.Selected(), itemAt(m, r).ID) {
			t.Fatalf("%s pre-selected", itemAt(m, r).Path)
		}
	}
	if d := m.detailAt(manual); d.Status != StatusManual {
		t.Fatalf("manual status %v", d.Status)
	}
	m.ToggleRow(review)
	m.ToggleRow(manual)
	if !contains(m.Selected(), itemAt(m, review).ID) || !contains(m.Selected(), itemAt(m, manual).ID) {
		t.Fatal("one toggle did not select review and manual items")
	}
	if !strings.Contains(m.Status(), "Manual items are never chosen for you") {
		t.Fatalf("status %q", m.Status())
	}
	if g := m.Group(m.Rows()[manual].Group); g.Tile != selection.NoTile {
		t.Fatal("manual groups have a group tile")
	}
	if g := m.Group(m.Rows()[review].Group); g.Tile != selection.All {
		t.Fatalf("review group tile %v", g.Tile)
	}
}

func (m *Model) detailAt(row int) DetailView {
	m.SetCursor(row)
	d, _ := m.Detail()
	return d
}

// Every non-tier reason keeps an item unselectable, matching apply.Preview.
func TestBlockedItemsRefused(t *testing.T) {
	var items []plan.Item
	n := 0
	for _, r := range plan.Reasons() {
		if r == plan.ReasonTierReview || r == plan.ReasonTierManual || r == plan.ReasonTierInfo || r == plan.ReasonNotRequested {
			continue
		}
		items = append(items, pItem(fmt.Sprintf("a%011d", n), "caches.user", "safe", fmt.Sprintf("/h/Library/Caches/x%d", n), 10, false, r))
		n++
	}
	f := newFx(t, testutil.Spec{})
	p := plan.Plan{Items: items, Home: f.home}
	f.cfg.ScanFunc = fakePlan(p)
	m := f.scanned()
	for i, r := range m.Rows() {
		if r.Kind != RowItem {
			continue
		}
		it := m.Plan().Items[r.Item]
		m.ToggleRow(i)
		if contains(m.Selected(), it.ID) {
			t.Errorf("reason %v: selected", it.Reasons)
		}
		if !strings.HasPrefix(m.Status(), "Can't select it: ") {
			t.Errorf("reason %v: status %q", it.Reasons, m.Status())
		}
		v := m.Item(r.Item)
		if v.Tile != TileBlocked || v.Chip == "" {
			t.Errorf("reason %v: tile %v chip %q", it.Reasons, v.Tile, v.Chip)
		}
		pv := apply.Preview(p, []string{it.ID})
		for _, x := range pv.Items {
			if x.ID == it.ID && x.Selected {
				t.Errorf("reason %v: Preview would select it", it.Reasons)
			}
		}
	}
}

func TestGroupToggle(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	row := rowOf(t, m, "Library/Caches/com.example.app")
	gi := m.Rows()[row].Group
	if m.Group(gi).Tile != selection.All {
		t.Fatalf("safe group tile %v", m.Group(gi).Tile)
	}
	m.ToggleRow(row)
	if m.Group(gi).Tile != selection.Partial {
		t.Fatal("partial")
	}
	m.ToggleGroup(gi)
	if m.Group(gi).Tile != selection.All {
		t.Fatal("all")
	}
	m.ToggleGroup(gi)
	if m.Group(gi).Tile != selection.None {
		t.Fatal("none")
	}
}

func TestConfirmSummary(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	m.ToggleRow(rowOf(t, m, "go/pkg/mod/github.com"))
	m.ToggleRow(rowOf(t, m, "Downloads/Old.dmg"))
	m.OpenConfirm()
	v := m.Confirm()
	n, b := m.sel.Total()
	if v.Title != fmt.Sprintf("Move %d items to the Trash?", n) || v.Total == "" || !strings.Contains(v.Title, "Trash") {
		t.Fatalf("%+v (%d, %d)", v, n, b)
	}
	if !strings.HasPrefix(v.Rules, "from 4 rules: ") {
		t.Fatalf("rules %q", v.Rules)
	}
	if v.TierLine != "Includes 1 review item and 1 manual item you chose." {
		t.Fatalf("tier line %q", v.TierLine)
	}
	if v.Note != "" || !v.CanMove || v.Promise != Promise {
		t.Fatalf("an openable Trash: %+v", v)
	}
	m.CancelConfirm()

	t.Run("fallback note", func(t *testing.T) {
		m.cfg.ProbeFunc = func(apply.Options) (apply.TrashMode, error) { return apply.TrashFallback, nil }
		m.OpenConfirm()
		if v := m.Confirm(); v.Note != FallbackNote || !v.CanMove {
			t.Fatalf("%+v", v)
		}
		m.CancelConfirm()
	})
	t.Run("an unusable Trash disables Move", func(t *testing.T) {
		m.cfg.ProbeFunc = apply.ProbeTrash
		if err := os.Chmod(testutil.Abs(f.home, testutil.TrashRel), 0); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(testutil.Abs(f.home, testutil.TrashRel), 0o700) }()
		m.OpenConfirm()
		if v := m.Confirm(); !unavailableNote(v.Note) || v.CanMove {
			t.Fatalf("%+v", v)
		}
		if _, ok := m.StartApply(); ok {
			t.Fatal("apply started with no usable Trash")
		}
	})
}

func TestAtRiskLine(t *testing.T) {
	f := newFx(t, testutil.Spec{})
	var items []plan.Item
	for i := 0; i < 5; i++ {
		it := pItem(fmt.Sprintf("b%011d", i), "caches.user", "safe", fmt.Sprintf("%s/Library/Caches/r%d", f.home, i), 10, true)
		it.AtRisk = true
		items = append(items, it)
	}
	f.cfg.ScanFunc = fakePlan(plan.Plan{Items: items})
	m := f.scanned()
	m.OpenConfirm()
	v := m.Confirm()
	if v.AtRiskLine != "5 items are at risk:" || len(v.AtRisk) != 4 || v.AtRisk[3] != "and 2 more" || v.AtRisk[0] != "~/Library/Caches/r0" {
		t.Fatalf("%+v", v)
	}
	if !m.Item(0).AtRisk || !strings.HasSuffix(m.Item(0).Label, "at risk") {
		t.Fatal("row at risk")
	}
}

// Apply through the fixture's own Trash: what moved is the selection, the
// receipt exists, and the done screen matches it.
func TestApplyMovesSelection(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	m.ToggleRow(rowOf(t, m, "go/pkg/mod/github.com"))
	want := m.Selected()
	m.OpenConfirm()
	if err := m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	if m.Screen() != ScreenDone {
		t.Fatalf("screen %d", m.Screen())
	}
	res := m.Result()
	var moved []string
	for _, mv := range res.Moved {
		moved = append(moved, mv.ID)
		if _, err := os.Lstat(mv.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there", mv.Path)
		}
	}
	if !reflect.DeepEqual(sorted(moved), sorted(want)) {
		t.Fatalf("moved %v, selected %v", moved, want)
	}
	e, err := f.store.Load(res.ReceiptID)
	if err != nil || len(e.Receipt.Moved) != len(want) {
		t.Fatalf("receipt: %v %+v", err, e.Receipt)
	}
	d := m.Done()
	if d.Title != fmt.Sprintf("Moved %d items to the Trash", len(want)) || d.Card.ID != res.ReceiptID || d.Card.How != words.Mover(homeMover) {
		t.Fatalf("%+v", d)
	}
	if d.Card.TrashDir != testutil.Abs(f.home, testutil.TrashRel) || !d.CanPutBack || !strings.Contains(d.ChangedMind, d.Command) {
		t.Fatalf("%+v", d)
	}
	if homeMover == plan.MoverRename && !strings.Contains(d.ChangedMind, "Finder's Put Back doesn't know") {
		t.Fatalf("changed mind %q", d.ChangedMind)
	}
	if !strings.HasPrefix(m.Status(), "Finished ") {
		t.Fatalf("status %q", m.Status())
	}
}

// Put back restores through apply.Restore, as kipple restore <id> --apply.
func TestPutBack(t *testing.T) {
	f := newFx(t, fixtureSpec())
	before := testutil.Checksum(t, f.home)
	m := f.scanned()
	m.OpenConfirm()
	if err := m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	m.OpenPutBack()
	if m.Sheet() != SheetPutBack || !strings.HasPrefix(m.PutBack().Title, "Put ") {
		t.Fatalf("sheet %d", m.Sheet())
	}
	m.CancelPutBack()
	if m.Sheet() != SheetNone || m.Status() != "Cancelled. Nothing was put back." {
		t.Fatal("cancel")
	}
	m.OpenPutBack()
	if err := m.PutBackNow(); err != nil {
		t.Fatal(err)
	}
	d := m.Done()
	if d.Restored == nil || !strings.HasPrefix(d.Restored.Title, "Put back ") || d.CanPutBack {
		t.Fatalf("%+v", d.Restored)
	}
	// Everything is back where it was; the Trash is empty again.
	entries, _ := os.ReadDir(testutil.Abs(f.home, testutil.TrashRel))
	if len(entries) != 0 {
		t.Fatalf("Trash still holds %d entries", len(entries))
	}
	_ = before
	e, _ := f.store.Load(m.Result().ReceiptID)
	for _, mv := range e.Receipt.Moved {
		if mv.RestoredAt == nil {
			t.Errorf("%s not marked restored in the receipt", mv.Path)
		}
	}
}

// After a run that used macOS's Trash command (no Full Disk Access), the
// app points to Finder's Put Back and offers no Put back of its own, which
// would need Full Disk Access.
func TestPutBackNeedsADirectMove(t *testing.T) {
	f := newFx(t, fixtureSpec())
	f.cfg.ApplyFunc = func(_ context.Context, o apply.Options) (plan.Result, error) {
		return plan.Result{ReceiptID: "20261120T000000Z-abcdef", Moved: []plan.MovedItem{{ID: o.Only[0], Path: f.home + "/Library/Logs/app", Bytes: 10, Mover: plan.MoverNSFileManager}}}, nil
	}
	m := f.scanned()
	m.OpenConfirm()
	if err := m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	d := m.Done()
	if d.CanPutBack || !strings.HasPrefix(d.ChangedMind, "Open the Trash, select an item and choose Put Back.") || d.Card.How != "Moved with macOS's Trash command (no Full Disk Access)." {
		t.Fatalf("%+v", d)
	}
	m.OpenPutBack()
	if m.Sheet() != SheetNone {
		t.Fatal("Put back opened after a move without Full Disk Access")
	}
}

func TestStopDuringApply(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	m.OpenConfirm()
	job, ok := m.StartApply()
	if !ok {
		t.Fatal("no apply")
	}
	n := 0
	res, err := job.Run(func(s string) {
		m.ApplyProgress(s)
		n++
		if n == 2 { // while the second item is being checked
			m.StopApply()
		}
	})
	m.ApplyDone(res, err)
	if err != nil {
		t.Fatal(err)
	}
	total := len(m.Preselected())
	if len(res.Moved) != 1 || len(res.Skipped) != total-1 {
		t.Fatalf("moved %d skipped %+v of %d", len(res.Moved), res.Skipped, total)
	}
	d := m.Done()
	t.Logf("%+v", res.Skipped)
	if d.Title != fmt.Sprintf("Stopped. Moved 1 of %d items to the Trash.", total) || d.NotTried+len(d.Skipped) != total-1 || d.NotTried == 0 {
		t.Fatalf("%+v", d)
	}
}

func TestQuitWhileApplyingStopsFirst(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	m.OpenConfirm()
	job, _ := m.StartApply()
	if m.RequestQuit() {
		t.Fatal("quit allowed while applying")
	}
	res, err := job.Run(m.ApplyProgress)
	m.ApplyDone(res, err)
	if !m.QuitNow() || len(res.Moved) != 0 {
		t.Fatalf("quit now %v, moved %d", m.QuitNow(), len(res.Moved))
	}
}

func TestReceiptErrorState(t *testing.T) {
	f := newFx(t, fixtureSpec())
	saves := 0
	f.cfg.Apply.SaveReceipt = func(r plan.Receipt) error {
		saves++
		if saves >= 2 {
			return errors.New("no space left on device")
		}
		return f.store.Save(r)
	}
	m := f.scanned()
	m.OpenConfirm()
	if err := m.ApplyNow(); !errors.Is(err, apply.ErrReceipt) {
		t.Fatalf("error %v", err)
	}
	p := m.Problem()
	if m.Screen() != ScreenProblem || p.Kind != ProblemReceipt || p.Heading != "kipple stopped: it couldn't save the receipt." {
		t.Fatalf("%+v", p)
	}
	if p.Card == nil || p.Card.ID == "" || p.Card.Pending != 1 || p.Card.PendingPath == "" || p.Card.NotTried == 0 {
		t.Fatalf("card %+v", p.Card)
	}
	if !strings.Contains(strings.Join(p.Body, " "), "it may or may not have moved") || !p.CanScan {
		t.Fatalf("%+v", p)
	}
}

func TestTrashAccessState(t *testing.T) {
	f := newFx(t, fixtureSpec())
	f.cfg.ApplyFunc = func(context.Context, apply.Options) (plan.Result, error) {
		return plan.Result{}, fmt.Errorf("%w: permission denied", apply.ErrTrashAccess)
	}
	m := f.scanned()
	m.OpenConfirm()
	_ = m.ApplyNow()
	p := m.Problem()
	// Open Privacy settings only where Full Disk Access is the cause.
	want := ActionOpenPrivacy
	if !fullDiskAccess {
		want = ActionBack
	}
	if p.Kind != ProblemTrashAccess || p.Heading != "kipple can't use your Trash folder, so nothing was moved." || p.Actions[0] != want {
		t.Fatalf("%+v", p)
	}
	m.Back()
	if m.Screen() != ScreenReview {
		t.Fatal("Back to review")
	}
}

func TestScanError(t *testing.T) {
	f := newFx(t, testutil.Spec{})
	f.cfg.ScanFunc = func(context.Context, scan.Options) (plan.Plan, error) {
		return plan.Plan{}, errors.New("home went away \x1b[31m")
	}
	m := f.model()
	m.ScanNow()
	p := m.Problem()
	if m.Screen() != ScreenProblem || p.Kind != ProblemScan || p.Heading != "kipple couldn't finish the scan." || !p.CanScan {
		t.Fatalf("%+v", p)
	}
	if strings.ContainsRune(strings.Join(p.Body, ""), '\x1b') {
		t.Fatal("the error text was not escaped")
	}
	if _, ok := m.StartScan(); !ok {
		t.Fatal("Scan again is not offered")
	}
}

func TestRefusals(t *testing.T) {
	testutil.GuardRealHome(t)
	cases := []struct {
		kind    ProblemKind
		heading string
	}{
		{ProblemRoot, "kipple doesn't run as root."},
		{ProblemHomeRefused, "kipple won't scan this folder."},
		{ProblemHomeUnreadable, "kipple can't find your home folder."},
		{ProblemConfig, "kipple can't read its settings."},
		{ProblemState, "kipple can't use its receipts folder."},
	}
	for _, c := range cases {
		m := Refuse(Config{}, c.kind, errors.New("why"))
		p := m.Problem()
		if m.Screen() != ScreenProblem || p.Heading != c.heading || !reflect.DeepEqual(p.Actions, []Action{ActionQuit}) {
			t.Errorf("%v: %+v", c.kind, p)
		}
		if _, ok := m.StartScan(); ok {
			t.Errorf("%v: a refusal offers a scan", c.kind)
		}
	}
}

func TestScanStopAndProgress(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.model()
	if m.Status() != "Nothing has been scanned yet." || !m.ShowDownloadsNote() {
		t.Fatal("idle")
	}
	job, _ := m.StartScan()
	if v := m.Scanning(); v.Count != fmt.Sprintf("Looking in 1 of %d places", m.Places()) {
		t.Fatalf("%+v", v)
	}
	m.ScanProgress(job, "scanning caches.user")
	if v := m.Scanning(); !v.Tiles[0] || v.Current != "User app caches" && v.Current == "" {
		t.Fatalf("%+v", v)
	}
	m.StopScan()
	if m.Screen() != ScreenStart || m.Status() != "Scan stopped. Nothing was read further, nothing moved." {
		t.Fatalf("%d %q", m.Screen(), m.Status())
	}
	p, err := job.Run(func(string) {})
	m.ScanDone(job, p, err) // the stopped scan's result is ignored
	if m.Screen() != ScreenStart || !errors.Is(err, context.Canceled) {
		t.Fatalf("screen %d err %v", m.Screen(), err)
	}
	m.ScanNow()
	if m.ShowDownloadsNote() {
		t.Fatal("the Downloads note shows after a scan")
	}
	all := true
	for _, v := range m.Scanning().Tiles {
		all = all && v
	}
	if !all || !m.Scanning().Measuring {
		t.Fatal("every place visited")
	}
}

func TestRescanWarnsWhenChoicesChanged(t *testing.T) {
	f := newFx(t, fixtureSpec())
	m := f.scanned()
	job, ok := m.RequestRescan()
	if !ok {
		t.Fatal("an unchanged selection rescans at once")
	}
	p, err := job.Run(func(string) {})
	m.ScanDone(job, p, err)
	m.ToggleRow(rowOf(t, m, "go/pkg/mod/github.com"))
	if _, ok := m.RequestRescan(); ok || !strings.Contains(m.Status(), "Press ⌘R again") {
		t.Fatalf("status %q", m.Status())
	}
	if _, ok := m.RequestRescan(); !ok {
		t.Fatal("the second press rescans")
	}
}

func TestGitUnavailableBanner(t *testing.T) {
	spec := testutil.Spec{
		Files: []testutil.File{
			{Path: "dev/app/package.json", Content: "{}", MTime: old},
			{Path: "dev/app/node_modules/x/i.js", Content: "x", MTime: old},
		},
	}
	f := newFx(t, spec)
	calls := 0
	f.cfg.GitAvailable = false
	f.cfg.Scan.Git = countingGit{&calls}
	f.cfg.Apply.Git = countingGit{&calls}
	m := f.scanned()
	if calls == 0 {
		t.Fatal("the scan did not use the injected inspector")
	}
	row := rowOf(t, m, "dev/app/node_modules")
	if v := m.Item(m.Rows()[row].Item); v.Chip != "no git" || v.Tile != TileBlocked {
		t.Fatalf("%+v", v)
	}
	if !m.GitBanner() || m.Rows()[0].Kind != RowGitBanner {
		t.Fatal("no git banner")
	}
	f.cfg.GitAvailable = true
	if New(f.cfg).GitBanner() {
		t.Fatal("banner with git available")
	}
}

func TestPartialAndWarnings(t *testing.T) {
	f := newFx(t, testutil.Spec{})
	h := f.home
	p := plan.Plan{
		Warnings: []string{"cannot read ~/Library/Caches/x: permission denied", "second\x1b[2J"},
		Items: []plan.Item{
			pItem("c00000000001", "caches.user", "safe", h+"/Library/Caches/a", 30, false, plan.ReasonScanPartial),
			pItem("c00000000002", "caches.user", "safe", h+"/Library/Caches/b", 20, false, plan.ReasonContainsMount),
			pItem("c00000000003", "caches.user", "safe", h+"/Library/Caches/c", 10, true),
		},
	}
	f.cfg.ScanFunc = fakePlan(p)
	m := f.scanned()
	if m.Rows()[0].Kind != RowBanner || m.BannerText() != "2 places could not be read completely." {
		t.Fatalf("%v %q", m.Rows()[0], m.BannerText())
	}
	m.ToggleWarnings()
	if m.Rows()[1].Kind != RowWarning || strings.ContainsRune(m.WarningText(1), '\x1b') {
		t.Fatal("warnings expand, escaped")
	}
	chips := map[string]string{}
	for _, r := range m.Rows() {
		if r.Kind == RowItem {
			v := m.Item(r.Item)
			chips[v.ID] = v.Chip
		}
	}
	if chips["c00000000001"] != "unreadable" || chips["c00000000002"] != "mounted disk" || chips["c00000000003"] != "" {
		t.Fatalf("%v", chips)
	}
}

func TestEmptyStates(t *testing.T) {
	t.Run("nothing found", func(t *testing.T) {
		f := newFx(t, testutil.Spec{})
		m := f.scanned()
		e, ok := m.EmptyMessage()
		if !ok || e.Title != "Nothing to clean." || !strings.Contains(e.Body, "found nothing it cleans.") || strings.Contains(e.Body, "measured only") {
			t.Fatalf("%+v", e)
		}
		if m.CanMove() || m.Cursor() != -1 {
			t.Fatal("Move enabled on an empty scan")
		}
		m.OpenConfirm()
		if m.Sheet() != SheetNone || m.Status() != "Nothing is selected. Click an item's box to select it." {
			t.Fatalf("%q", m.Status())
		}
	})
	t.Run("only info items", func(t *testing.T) {
		f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: ".ollama/models/blobs/sha", Size: 4096, MTime: old}}})
		m := f.scanned()
		e, ok := m.EmptyMessage()
		if !ok || !strings.HasSuffix(e.Body, "These are measured only:") || m.Status() != "Nothing is selected." {
			t.Fatalf("%+v %q", e, m.Status())
		}
		if h := m.Header(); h.Any || h.Selected != "Selected 0 · 0 B" {
			t.Fatalf("%+v", h)
		}
	})
	t.Run("nothing pre-selected", func(t *testing.T) {
		f := newFx(t, testutil.Spec{Files: []testutil.File{{Path: "go/pkg/mod/x/y.go", Size: 4096, MTime: old}}})
		m := f.scanned()
		if _, ok := m.EmptyMessage(); ok || m.Status() != "Nothing is pre-selected. Review and manual items are yours to choose." {
			t.Fatalf("%q", m.Status())
		}
	})
}

func TestHostileNames(t *testing.T) {
	evil := "Library/Caches/evil\x1b]0;pwned\aname\u202e"
	f := newFx(t, testutil.Spec{
		Files: []testutil.File{{Path: evil + "/blob", Size: 4096, MTime: old}},
		Dirs:  []testutil.Dir{{Path: evil, MTime: old}},
	})
	m := f.scanned()
	bad := func(where, s string) {
		if strings.ContainsAny(s, "\x1b\a\u202e") {
			t.Errorf("%s: %q", where, s)
		}
	}
	for _, r := range m.Rows() {
		if r.Kind == RowItem {
			v := m.Item(r.Item)
			bad("row", v.Path)
			bad("label", v.Label)
		}
	}
	d, _ := m.Detail()
	bad("detail", d.Path)
	m.OpenConfirm()
	for _, p := range m.Confirm().AtRisk {
		bad("confirm", p)
	}
	job, _ := m.StartApply()
	res, err := job.Run(func(s string) { m.ApplyProgress(s); bad("progress", m.Applying().Current) })
	m.ApplyDone(res, err)
	for _, l := range m.Done().Moved {
		bad("done", l.Path)
	}
	if len(m.Done().Moved) == 0 {
		t.Fatal("nothing moved")
	}
}

// Order matches the TUI: tier, then rule bytes descending, then item bytes
// descending within a rule; sections head each tier.
func TestOrdering(t *testing.T) {
	f := newFx(t, testutil.Spec{})
	h := f.home
	p := plan.Plan{Items: []plan.Item{
		pItem("d00000000001", "ollama.models", "info", h+"/.ollama/models/a", 900, false, plan.ReasonTierInfo),
		pItem("d00000000002", "logs.user", "safe", h+"/Library/Logs/a", 5, true),
		pItem("d00000000003", "caches.user", "safe", h+"/Library/Caches/a", 10, true),
		pItem("d00000000004", "caches.user", "safe", h+"/Library/Caches/b", 30, true),
		pItem("d00000000005", "go.mod-cache", "review", h+"/go/pkg/mod/a", 500, false, plan.ReasonTierReview),
		pItem("d00000000006", "downloads.installers", "manual", h+"/Downloads/a.dmg", 1, false, plan.ReasonTierManual),
	}}
	f.cfg.ScanFunc = fakePlan(p)
	m := f.scanned()
	var got []string
	for _, r := range m.Rows() {
		switch r.Kind {
		case RowSection:
			got = append(got, Section(r.Tier).Label)
		case RowGroup:
			got = append(got, m.Group(r.Group).Title)
		case RowItem:
			got = append(got, m.Plan().Items[r.Item].ID)
		}
	}
	want := []string{"SAFE", m.ruleTitle("caches.user"), "d00000000004", "d00000000003", m.ruleTitle("logs.user"), "d00000000002",
		"REVIEW", m.ruleTitle("go.mod-cache"), "d00000000005", "MANUAL", m.ruleTitle("downloads.installers"), "d00000000006",
		"INFO", m.ruleTitle("ollama.models"), "d00000000001"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order\n got %q\nwant %q", got, want)
	}
	// The cursor starts on the first item and skips headings.
	if itemAt(m, m.Cursor()).ID != "d00000000004" {
		t.Fatal("cursor start")
	}
	m.MoveCursor(2)
	if itemAt(m, m.Cursor()).ID != "d00000000002" {
		t.Fatalf("cursor %s", itemAt(m, m.Cursor()).ID)
	}
	m.JumpGroup(1)
	if itemAt(m, m.Cursor()).ID != "d00000000005" {
		t.Fatal("jump group")
	}
	m.CursorEnd()
	if itemAt(m, m.Cursor()).ID != "d00000000001" {
		t.Fatal("end")
	}
	m.CursorHome()
	if itemAt(m, m.Cursor()).ID != "d00000000004" {
		t.Fatal("home")
	}
	if h := m.Header(); h.Found != "Found 6 · 1.4 KB" && !strings.HasPrefix(h.Found, "Found 6 · ") {
		t.Fatalf("%+v", h)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func sorted(ss []string) []string {
	out := append([]string(nil), ss...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

package tui

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/trash"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

var old = time.Now().Add(-200 * 24 * time.Hour)

type idleProcs struct{}

func (idleProcs) RunningBundleIDs(context.Context) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (idleProcs) RunningNames(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (idleProcs) ProcessCwds(context.Context, []string) ([]string, error) { return nil, nil }

// fixtureSpec has two safe items, a review item, a manual item, a manual
// item too recent to select, and an info item.
func fixtureSpec() testutil.Spec {
	return testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.example.app/blob", Size: 64 << 10, MTime: old},
			{Path: "Library/Logs/app/log.txt", Size: 1024, MTime: old},
			{Path: "go/pkg/mod/github.com/x/y.go", Size: 1024, MTime: old},
			{Path: "Library/Developer/Xcode/Archives/2026-01-01/App.xcarchive/Info.plist", Size: 8192, MTime: old},
			{Path: "Downloads/New.dmg", Size: 4096, MTime: time.Now()},
			{Path: ".ollama/models/blobs/sha", Size: 4096, MTime: old},
		},
		Dirs: []testutil.Dir{
			{Path: "Library/Caches/com.example.app", MTime: old},
			{Path: ".Trash", Mode: 0o700},
		},
	}
}

type fx struct {
	home  string
	store receipt.Store
	cfg   Config
}

func newFx(t *testing.T, color bool) *fx {
	t.Helper()
	home := testutil.NewHome(t, fixtureSpec())
	fi, err := os.Lstat(home)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, _ := policy.Identity(fi)
	env := policy.Env{Home: home, HomeIdentity: &policy.FileIdentity{Dev: dev, Ino: ino}}
	store, err := receipt.Open(testutil.StateDir(home))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := trash.OpenDirTrasher(testutil.Abs(home, ".Trash"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return &fx{home: home, store: store, cfg: Config{
		Scan: scan.Options{Env: env, Rules: rules.Catalogue(), Level: rules.LevelSafe, KippleVersion: "test"},
		Apply: apply.Options{
			Env: env, Rules: rules.Catalogue(), Receipts: store, KippleVersion: "test",
			Procs: idleProcs{}, Trash: tr,
		},
		Color:    color,
		Version:  "test",
		Renderer: lipgloss.NewRenderer(io.Discard),
	}}
}

// run executes cmd and returns the messages it produces, expanding
// batches. Spinner ticks are dropped so nothing waits on a timer.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch msg := msg.(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, run(c)...)
		}
		return out
	case spinner.TickMsg, nil:
		return nil
	}
	return []tea.Msg{msg}
}

// drive feeds cmd's messages (and theirs) to m.
func drive(m Model, cmd tea.Cmd) Model {
	for _, msg := range run(cmd) {
		if _, quit := msg.(tea.QuitMsg); quit {
			continue
		}
		next, c := m.Update(msg)
		m = drive(next.(Model), c)
	}
	return m
}

// start builds the model at w×h and runs its scan.
func start(t *testing.T, f *fx, w, h int) Model {
	t.Helper()
	m := New(f.cfg)
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = drive(next.(Model), next.(Model).Init())
	if m.state != stateReview {
		t.Fatalf("state %d after the scan, want review: %v", m.state, m.outcome.Err)
	}
	return m
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, cmd := m.Update(msg)
		m = drive(next.(Model), cmd)
	}
	return m
}

// highlight moves the cursor to the item whose path ends in rel.
func highlight(t *testing.T, m Model, rel string) Model {
	t.Helper()
	m = press(m, "g")
	for i := 0; i < len(m.rows); i++ {
		if it, ok := m.current(); ok && strings.HasSuffix(it.Path, "/"+rel) {
			return m
		}
		m = press(m, "down")
	}
	t.Fatalf("no item %s in the list", rel)
	return m
}

func item(t *testing.T, m Model, rel string) plan.Item {
	t.Helper()
	for _, it := range m.plan.Items {
		if strings.HasSuffix(it.Path, "/"+rel) {
			return it
		}
	}
	t.Fatalf("no item %s in the plan", rel)
	return plan.Item{}
}

// selectedLine returns the header's "Selected n · size" text.
func selectedLine(t *testing.T, view string) string {
	t.Helper()
	for _, l := range strings.Split(view, "\n") {
		if i := strings.Index(l, "Selected "); i >= 0 && strings.Contains(l, "Found ") {
			return strings.TrimSpace(l[i:])
		}
	}
	t.Fatalf("no Selected total in the view:\n%s", view)
	return ""
}

// Spec scenario "Launch" (tui): every rule with items appears with its
// total and each item shows its size.
func TestLaunch(t *testing.T) {
	f := newFx(t, false)
	m := start(t, f, 140, 60)
	view := m.View()
	if !strings.Contains(view, "kipple") {
		t.Fatalf("no header:\n%s", view)
	}
	for _, g := range m.groups {
		total := len(g.items)
		want := g.title
		line := ""
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, want) {
				line = l
				break
			}
		}
		if line == "" || !strings.Contains(line, output.Bytes(g.bytes)) || !strings.Contains(line, "("+g.tier+")") {
			t.Errorf("rule %s (%d items, %s) heading missing or incomplete: %q", g.id, total, output.Bytes(g.bytes), line)
		}
	}
	for _, it := range m.plan.Items {
		rel := strings.TrimPrefix(it.Path, f.home+"/")
		found := false
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, rel) && strings.Contains(l, output.Bytes(it.AllocatedBytes)) {
				found = true
			}
		}
		if !found {
			t.Errorf("item %s with its size %s missing:\n%s", rel, output.Bytes(it.AllocatedBytes), view)
		}
	}
	if len(m.groups) < 5 {
		t.Fatalf("fixture should give at least 5 rules, got %d", len(m.groups))
	}
}

// Spec scenario "Model test" (tui): space on a safe item unselects it and
// reduces the total.
func TestModelTest(t *testing.T) {
	f := newFx(t, false)
	m := highlight(t, start(t, f, 100, 30), "Library/Caches/com.example.app")
	it := item(t, m, "Library/Caches/com.example.app")
	if !m.selected[it.ID] {
		t.Fatal("the safe item is not pre-selected")
	}
	n0, b0 := m.selectedTotal()
	before := selectedLine(t, m.View())
	m = press(m, " ")
	n1, b1 := m.selectedTotal()
	view := m.View()
	if m.selected[it.ID] || n1 != n0-1 || b1 != b0-it.AllocatedBytes {
		t.Fatalf("after space: selected=%v total %d/%d -> %d/%d", m.selected[it.ID], n0, b0, n1, b1)
	}
	after := selectedLine(t, view)
	if after == before || !strings.Contains(after, output.Bytes(b1)) {
		t.Fatalf("total line %q -> %q, want %s", before, after, output.Bytes(b1))
	}
	row := false
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "[x] ~/Library/Caches/com.example.app") {
			t.Fatalf("item row still marked selected: %q", l)
		}
		row = row || strings.Contains(l, "[ ] ~/Library/Caches/com.example.app")
	}
	if !row {
		t.Fatalf("no unselected row for the item:\n%s", view)
	}
	// Space again selects it again.
	if m = press(m, " "); !m.selected[it.ID] {
		t.Fatal("space did not select the item again")
	}
}

// Spec scenario "Toggle info item" (tui): space on an info item does not
// change its selection and a hint names the owner's command.
func TestToggleInfoItem(t *testing.T) {
	f := newFx(t, false)
	m := highlight(t, start(t, f, 100, 30), ".ollama/models/blobs")
	it := item(t, m, ".ollama/models/blobs")
	n0, _ := m.selectedTotal()
	m = press(m, " ")
	n1, _ := m.selectedTotal()
	if m.selected[it.ID] || n1 != n0 {
		t.Fatal("space selected an info item")
	}
	if !strings.Contains(m.View(), "ollama rm <model>") || !strings.Contains(m.hint, "ollama rm <model>") {
		t.Fatalf("hint does not name the owner's command: %q", m.hint)
	}
}

// An item blocked by anything other than its tier cannot be selected (as
// kipple clean --only would skip it), and the hint says why.
func TestBlockedItemRefused(t *testing.T) {
	f := newFx(t, false)
	m := highlight(t, start(t, f, 100, 30), "Downloads/New.dmg")
	it := item(t, m, "Downloads/New.dmg")
	m = press(m, " ")
	if m.selected[it.ID] {
		t.Fatal("selected an item that is too recent")
	}
	if !strings.Contains(m.hint, "changed too recently") {
		t.Fatalf("hint %q", m.hint)
	}
}

// Review and manual items follow kipple clean --only: choosing one is
// consent to its tier.
func TestReviewAndManualSelectable(t *testing.T) {
	f := newFx(t, false)
	m := start(t, f, 100, 30)
	for _, rel := range []string{"go/pkg/mod/github.com", "Library/Developer/Xcode/Archives/2026-01-01"} {
		m = highlight(t, m, rel)
		it := item(t, m, rel)
		if m.selected[it.ID] {
			t.Fatalf("%s pre-selected at level safe", rel)
		}
		if m = press(m, " "); !m.selected[it.ID] {
			t.Fatalf("%s not selectable: %q", rel, m.hint)
		}
	}
}

// Spec scenario "Cancel" (tui): a then n moves nothing and returns to the
// review view.
func TestCancel(t *testing.T) {
	f := newFx(t, false)
	sum := testutil.Checksum(t, f.home)
	m := press(start(t, f, 100, 30), "a")
	if m.state != stateConfirm {
		t.Fatalf("a did not open the confirmation (state %d)", m.state)
	}
	view := m.View()
	n, b := m.selectedTotal()
	if !strings.Contains(view, "Trash") || !strings.Contains(view, output.Bytes(b)) || !strings.Contains(view, items(n)) {
		t.Fatalf("confirmation lacks the total or the word Trash:\n%s", view)
	}
	m = press(m, "n")
	if m.state != stateReview || m.outcome.Applied {
		t.Fatalf("state %d applied=%v after n", m.state, m.outcome.Applied)
	}
	if !strings.Contains(m.View(), "Nothing was moved") {
		t.Fatal("no cancellation hint")
	}
	if got := testutil.Checksum(t, f.home); got != sum {
		t.Fatal("cancel changed the fixture")
	}
	if es, _ := f.store.List(); len(es) != 0 {
		t.Fatalf("cancel wrote %d receipts", len(es))
	}
}

// With nothing selected, a does not open the confirmation.
func TestApplyWithNothingSelected(t *testing.T) {
	f := newFx(t, false)
	m := start(t, f, 100, 30)
	for id := range m.selected {
		delete(m.selected, id)
	}
	if m = press(m, "a"); m.state != stateReview || !strings.Contains(m.hint, "Nothing is selected") {
		t.Fatalf("state %d hint %q", m.state, m.hint)
	}
}

// Apply: y moves the selection through apply.Run into the fixture's Trash
// (a DirTrasher), writes a receipt and shows its summary; unselected and
// info items stay.
func TestApplyThroughDirTrasher(t *testing.T) {
	f := newFx(t, true)
	m := start(t, f, 100, 30)
	m = highlight(t, m, "Library/Developer/Xcode/Archives/2026-01-01")
	manual := item(t, m, "Library/Developer/Xcode/Archives/2026-01-01")
	m = press(m, " ")
	want := m.selectedIDs()
	if len(want) != 3 {
		t.Fatalf("selection %v, want the two safe items and the manual one", want)
	}
	m = press(m, "a", "y")
	if m.state != stateDone {
		t.Fatalf("state %d after y: %v", m.state, m.outcome.Err)
	}
	res := m.Outcome().Result
	if !m.Outcome().Applied || len(res.Moved) != 3 || len(res.Skipped) != 0 || res.ReceiptID == "" {
		t.Fatalf("result %+v", res)
	}
	for _, mv := range res.Moved {
		if _, err := os.Lstat(mv.Path); !os.IsNotExist(err) {
			t.Errorf("%s still at home", mv.Path)
		}
		if !strings.HasPrefix(mv.TrashPath, f.home+"/.Trash/") || mv.Mover != plan.MoverRename {
			t.Errorf("%s went to %s by %s", mv.Path, mv.TrashPath, mv.Mover)
		}
		if _, err := os.Lstat(mv.TrashPath); err != nil {
			t.Errorf("not in the Trash: %v", err)
		}
	}
	for _, rel := range []string{".ollama/models/blobs", "go/pkg/mod/github.com", "Downloads/New.dmg"} {
		if _, err := os.Lstat(testutil.Abs(f.home, rel)); err != nil {
			t.Errorf("%s was moved: %v", rel, err)
		}
	}
	if _, err := os.Lstat(manual.Path); !os.IsNotExist(err) {
		t.Error("the chosen manual item was not moved")
	}
	e, err := f.store.Load(res.ReceiptID)
	if err != nil || len(e.Receipt.Moved) != 3 {
		t.Fatalf("receipt %+v %v", e.Receipt, err)
	}
	view := m.View()
	for _, s := range []string{"Moved 3 items to the Trash", res.ReceiptID, "kipple restore " + res.ReceiptID + " --apply", "straight into ~/.Trash"} {
		if !strings.Contains(view, s) {
			t.Errorf("done view lacks %q:\n%s", s, view)
		}
	}
	// Restoring the receipt puts the fixture back.
	if _, err := apply.Restore(context.Background(), apply.RestoreOptions{Env: f.cfg.Apply.Env, Rules: rules.Catalogue(), Receipt: e, Store: &f.store, Apply: true}); err != nil {
		t.Fatal(err)
	}
	for _, mv := range res.Moved {
		if _, err := os.Lstat(mv.Path); err != nil {
			t.Errorf("%s not restored: %v", mv.Path, err)
		}
	}
}

// The TUI never deletes: apply runs with Permanent and DryRun unset, even if
// the caller's options set them.
func TestApplyNeverPermanent(t *testing.T) {
	f := newFx(t, false)
	f.cfg.Apply.Permanent = true
	f.cfg.Apply.DryRun = true
	m := press(start(t, f, 100, 30), "a", "y")
	res := m.Outcome().Result
	if res.DryRun || len(res.Moved) != 2 {
		t.Fatalf("%+v", res)
	}
	for _, mv := range res.Moved {
		if mv.Permanent || mv.TrashPath == "" {
			t.Fatalf("%+v", mv)
		}
	}
}

// 80 columns work: no rendered line is wider than the terminal, in every
// view, and the side-by-side layout fits at 140.
func TestNarrowTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {80, 40}, {140, 40}} {
		f := newFx(t, true)
		m := start(t, f, size[0], size[1])
		check := func(name, view string) {
			t.Helper()
			lines := strings.Split(view, "\n")
			if len(lines) > size[1] {
				t.Errorf("%dx%d %s: %d lines", size[0], size[1], name, len(lines))
			}
			for _, l := range lines {
				if w := lipgloss.Width(l); w > size[0] {
					t.Errorf("%dx%d %s: line of width %d: %q", size[0], size[1], name, w, l)
				}
			}
		}
		check("review", m.View())
		m = highlight(t, m, ".ollama/models/blobs")
		check("review info", press(m, " ").View())
		m = press(m, "a")
		check("confirm", m.View())
		m = press(m, "y")
		check("done", m.View())
	}
}

// NO_COLOR: the plain theme sets no colours; the brand theme does, with
// amber only on selection and at-risk styles.
func TestNoColorTheme(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard)
	plain := newTheme(r, false)
	for name, st := range map[string]lipgloss.Style{"pill": plain.pill, "title": plain.title, "text": plain.text, "muted": plain.muted, "amber": plain.amber, "cursor": plain.cursor, "label": plain.label} {
		if _, ok := st.GetForeground().(lipgloss.NoColor); !ok {
			t.Errorf("plain %s has a foreground colour", name)
		}
		if _, ok := st.GetBackground().(lipgloss.NoColor); !ok {
			t.Errorf("plain %s has a background colour", name)
		}
	}
	if plain.markOn != "[x]" || plain.markOff != "[ ]" {
		t.Errorf("plain marks %q %q", plain.markOn, plain.markOff)
	}
	brand := newTheme(r, true)
	if brand.amber.GetForeground() != colAmber || brand.text.GetForeground() != colInk {
		t.Error("brand theme does not use the palette")
	}
	// Should-fix 7: amber is for selected and at-risk items only; the
	// cursor, the spinner, warnings, borders and headings use other tokens.
	for name, st := range map[string]lipgloss.Style{
		"title": brand.title, "text": brand.text, "muted": brand.muted, "pill": brand.pill, "label": brand.label,
		"cursor": brand.cursor, "pointer": brand.pointer, "spinner": brand.spinner, "hint": brand.hintWarn,
		"border": brand.border, "pane title": brand.paneTitle, "key": brand.key,
	} {
		if st.GetForeground() == colAmber || st.GetBackground() == colAmber {
			t.Errorf("amber used for %s", name)
		}
	}
	if brand.modal.GetBorderTopForeground() == colAmber || brand.pane.GetBorderLeftForeground() == colAmber {
		t.Error("amber used for a border")
	}
	for tier, st := range brand.badge {
		if st.GetForeground() == colAmber || st.GetBackground() == colAmber {
			t.Errorf("amber used for the %s badge", tier)
		}
	}
	m := New(Config{Color: true, Renderer: r})
	if m.spin.Style.GetForeground() == colAmber {
		t.Error("the spinner is amber")
	}
}

// TUI polish (wave 4 review): titled panes, the selection bar, the home
// label, ruled group headings, and the receipt card, in both themes.
func TestTUIPolish(t *testing.T) {
	for _, color := range []bool{false, true} {
		f := newFx(t, color)
		f.cfg.HomeLabel = "KIPPLE_HOME …/fixture/home"
		m := start(t, f, 80, 24)
		view := m.View()
		for _, want := range []string{"╭─ Items ", "╭─ Detail ", "KIPPLE_HOME …/fixture/home", "──── 1 · 65.5 kB"} {
			if !strings.Contains(view, want) {
				t.Errorf("color=%v: review view lacks %q:\n%s", color, want, view)
			}
		}
		bar := "[========--]"
		if color {
			bar = "━━━━━━━━──"
		}
		if !strings.Contains(view, "Selected 2 · 69.6 kB "+bar) {
			t.Errorf("color=%v: no selection bar %q:\n%s", color, bar, view)
		}
		badge := map[bool]string{false: "(review)", true: " review "}[color]
		if !strings.Contains(view, "Go module cache "+badge) && !strings.Contains(view, "Go module cache  review ") {
			t.Errorf("color=%v: no review badge:\n%s", color, view)
		}
		m = press(m, "a", "y")
		done := m.View()
		for _, want := range []string{"╭─ Receipt ", "Moved      2 items · 69.6 kB", "Undo       kipple restore " + m.Outcome().Result.ReceiptID + " --apply"} {
			if !strings.Contains(done, want) {
				t.Errorf("color=%v: done view lacks %q:\n%s", color, want, done)
			}
		}
	}
}

// q quits from the review view without applying.
func TestQuit(t *testing.T) {
	f := newFx(t, false)
	m := start(t, f, 100, 30)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not quit")
	}
	if next.(Model).Outcome().Applied {
		t.Fatal("quit applied")
	}
}

// evilName retitles the terminal, clears the screen, recolours text, starts
// a new row and reverses what follows when printed raw.
const evilName = "evil\x1b]0;PWNED\x07\x1b[2J\x1b[31mred\x1b[0m\nrow\u202etxt"

// Must-fix 1 (wave 4 review): a fixture folder whose name carries terminal
// escapes and a newline is shown escaped in every TUI view, and the layout
// keeps its rows and widths.
func TestTerminalEscapesInTUI(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {140, 40}} {
		f := newFx(t, true)
		evil := testutil.Abs(f.home, "Library/Caches/"+evilName)
		if err := os.MkdirAll(evil, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(evil+"/blob", make([]byte, 4096), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{evil + "/blob", evil} {
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
		}
		m := start(t, f, size[0], size[1])
		check := func(name, view string) {
			t.Helper()
			for i, r := range view {
				if r != '\n' && output.Unsafe(r) {
					t.Fatalf("%v %s: unsafe character %U at byte %d:\n%q", size, name, r, i, view)
				}
			}
			lines := strings.Split(view, "\n")
			if len(lines) != size[1] {
				t.Errorf("%v %s: %d rows, want %d", size, name, len(lines), size[1])
			}
			for _, l := range lines {
				if lipgloss.Width(l) > size[0] {
					t.Errorf("%v %s: row wider than the terminal: %q", size, name, l)
				}
			}
		}
		m = highlight(t, m, "Library/Caches/"+evilName)
		view := m.View()
		check("review", view)
		if !strings.Contains(view, `\x1b]0;PWNED\x07`) && !strings.Contains(view, `PWNED`) {
			t.Fatalf("%v: the name is not shown:\n%s", size, view)
		}
		m = press(m, "a")
		check("confirm", m.View())
		m = press(m, "y")
		if m.state != stateDone {
			t.Fatalf("state %d: %v", m.state, m.outcome.Err)
		}
		check("done", m.View())
	}
}

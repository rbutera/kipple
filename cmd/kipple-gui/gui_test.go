//go:build gui

package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/rbutera/kipple/cmd/internal/guilaunch"
	"github.com/rbutera/kipple/cmd/internal/launch"
	"github.com/rbutera/kipple/internal/guimodel"
	"github.com/rbutera/kipple/internal/testutil"
)

// TestMain redirects HOME before any Fyne call (home_test.go), then runs
// the tests under the usual fixture guard.
func TestMain(m *testing.M) {
	check := redirectHome()
	os.Exit(check(testutil.GuardedMain(m)))
}

var old = time.Now().Add(-200 * 24 * time.Hour)

type idle struct{}

func (idle) RunningBundleIDs(context.Context) (map[string]bool, error) { return map[string]bool{}, nil }
func (idle) RunningNames(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (idle) ProcessCwds(context.Context, []string) ([]string, error) { return nil, nil }

func fixture(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.example.app/blob", Size: 64 << 10, MTime: old},
			{Path: "Library/Logs/app/log.txt", Size: 4 << 10, MTime: old},
			{Path: "go/pkg/mod/github.com/x/y.go", Size: 8 << 10, MTime: old},
			{Path: "Downloads/Old.dmg", Size: 4 << 10, MTime: old},
			{Path: ".ollama/models/blobs/sha", Size: 4 << 10, MTime: old},
		},
		Dirs: []testutil.Dir{{Path: "Library/Caches/com.example.app", MTime: old}, {Path: ".Trash", Mode: 0o700}},
	})
}

// newTestUI builds the real window (Fyne's test driver, software
// rendering, no display) over the model guilaunch builds for KIPPLE_HOME.
func newTestUI(t *testing.T, dark bool) *ui {
	t.Helper()
	testutil.GuardRealHome(t)
	a := test.NewTempApp(t)
	m := guilaunch.Model(guilaunch.Options{
		Runtime: launch.System(), Geteuid: func() int { return 501 }, Version: "test", Procs: idle{},
		CommandLineTools: func() bool { return true },
	})
	if !m.Config().TestHome {
		t.Fatal("the GUI under test is not on a KIPPLE_HOME fixture")
	}
	u := newWindow(a, m, loadSystemFonts(nil))
	v := theme.VariantLight
	if dark {
		v = theme.VariantDark
	}
	u.th.forced = &v
	u.do = func(fn func()) { fn() }
	u.open = func(...string) { t.Error("the test opened something with /usr/bin/open") }
	u.quit = func() {}
	u.win.Resize(fyne.NewSize(960, 640))
	u.update()
	return u
}

// setVariant switches the window between light and dark.
func setVariant(u *ui, dark bool) {
	v := theme.VariantLight
	if dark {
		v = theme.VariantDark
	}
	u.th.forced = &v
	u.app.Settings().SetTheme(u.th)
	u.update()
}

func capture(t *testing.T, u *ui, dir, name string) {
	t.Helper()
	if min := u.win.Content().MinSize(); min.Width > 760 || min.Height > 520 {
		t.Errorf("screen %d sheet %d: minimum size %v is larger than 760x520", u.m.Screen(), u.m.Sheet(), min)
	}
	if dir == "" {
		_ = u.win.Canvas().Capture()
		return
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, u.win.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

// Every screen renders at 960x640 and at the 760x520 minimum, in light and
// dark, without panicking.
func TestScreensRender(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, size := range []fyne.Size{fyne.NewSize(960, 640), fyne.NewSize(760, 520)} {
			fixture(t)
			u := newTestUI(t, dark)
			u.win.Resize(size)
			capture(t, u, "", "")
			u.m.ScanNow()
			u.update()
			capture(t, u, "", "")
			u.m.OpenConfirm()
			u.update()
			capture(t, u, "", "")
			if err := u.m.ApplyNow(); err != nil {
				t.Fatal(err)
			}
			u.update()
			capture(t, u, "", "")
			if u.m.Screen() != guimodel.ScreenDone {
				t.Fatalf("screen %d", u.m.Screen())
			}
		}
	}
}

// find returns the first button in o labelled label.
func find(o fyne.CanvasObject, label string) *kbutton {
	switch x := o.(type) {
	case *kbutton:
		if x.Text == label {
			return x
		}
	case *fyne.Container:
		for _, c := range x.Objects {
			if b := find(c, label); b != nil {
				return b
			}
		}
	}
	return nil
}

func TestMoveDisabledWithNothingSelected(t *testing.T) {
	testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "go/pkg/mod/x/y.go", Size: 4096, MTime: old}}})
	u := newTestUI(t, false)
	u.m.ScanNow()
	u.update()
	b := find(u.win.Content(), "Move to Trash…")
	if b == nil || !b.Disabled() {
		t.Fatalf("Move to Trash… %v", b)
	}
	u.moveToTrash()
	if u.m.Sheet() != guimodel.SheetNone || u.m.Status() != "Nothing is selected. Click an item's box to select it." {
		t.Fatalf("sheet %d status %q", u.m.Sheet(), u.m.Status())
	}
}

func TestSpaceOnInfoRowChangesNothing(t *testing.T) {
	fixture(t)
	u := newTestUI(t, false)
	u.m.ScanNow()
	u.update()
	before := u.m.Selected()
	for i, r := range u.m.Rows() {
		if r.Kind == guimodel.RowItem && u.m.Plan().Items[r.Item].Tier == "info" {
			u.m.SetCursor(i)
		}
	}
	u.typedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if got := u.m.Selected(); len(got) != len(before) {
		t.Fatalf("selection changed: %v -> %v", before, got)
	}
	if u.m.Status() != "Info items can't be selected. Instead, run: ollama rm <model>" {
		t.Fatalf("status %q", u.m.Status())
	}
}

// Spec "Confirmation required", through the window: Move to Trash… opens
// the sheet with Cancel focused; Esc (and Return on Cancel) cancels and
// nothing under home changes.
func TestEscOnSheetCancels(t *testing.T) {
	home := fixture(t)
	before := testutil.Checksum(t, home)
	u := newTestUI(t, false)
	u.m.ScanNow()
	u.update()
	u.moveToTrash()
	u.moveToTrash() // the same key press reaching the menu too: ignored
	if u.m.Sheet() != guimodel.SheetConfirm {
		t.Fatalf("sheet %d", u.m.Sheet())
	}
	focused, ok := u.win.Canvas().Focused().(*kbutton)
	if !ok || focused.Text != "Cancel" {
		t.Fatalf("focused %v", u.win.Canvas().Focused())
	}
	focused.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if u.m.Sheet() != guimodel.SheetNone || u.m.Status() != "Cancelled. Nothing was moved." {
		t.Fatalf("sheet %d status %q", u.m.Sheet(), u.m.Status())
	}
	u.last = map[string]time.Time{}
	u.moveToTrash()
	u.win.Canvas().Focused().TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if u.m.Sheet() != guimodel.SheetNone {
		t.Fatal("Return on the focused Cancel did not cancel")
	}
	if testutil.Checksum(t, home) != before {
		t.Fatal("the fixture changed")
	}
}

func TestStopScanWithEsc(t *testing.T) {
	fixture(t)
	u := newTestUI(t, false)
	if _, ok := u.m.StartScan(); !ok {
		t.Fatal("no scan")
	}
	u.typedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if u.m.Screen() != guimodel.ScreenStart {
		t.Fatalf("screen %d", u.m.Screen())
	}
}

func TestZoomClamps(t *testing.T) {
	fixture(t)
	u := newTestUI(t, false)
	for i := 0; i < 20; i++ {
		u.zoom(1.1)
	}
	if u.th.zoom != zoomMax || u.th.Size(theme.SizeNameText) != sizeBody*zoomMax {
		t.Fatalf("zoom %v", u.th.zoom)
	}
	u.zoom(0)
	if u.th.zoom != 1 {
		t.Fatal("actual size")
	}
}

func TestThemeColours(t *testing.T) {
	th := newTheme(systemFonts{})
	for _, n := range []fyne.ThemeColorName{theme.ColorNameError, theme.ColorNameWarning, theme.ColorNameSuccess} {
		if th.Color(n, theme.VariantLight) != th.Color(cInk, theme.VariantLight) {
			t.Errorf("%s is not ink: amber means one thing only", n)
		}
	}
	if th.Color(theme.ColorNameBackground, theme.VariantDark) != th.Color(cGround, theme.VariantDark) ||
		th.Color(theme.ColorNameBackground, theme.VariantLight) == th.Color(theme.ColorNameBackground, theme.VariantDark) {
		t.Error("the theme does not follow the appearance")
	}
}

func TestSystemFonts(t *testing.T) {
	missing := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if f := loadSystemFonts(missing); f.regular != nil || f.bold != nil || f.mono != nil {
		t.Fatal("missing fonts must fall back to Fyne's")
	}
	if _, err := faceFromCollection([]byte("not a font"), 700); err == nil {
		t.Fatal("garbage parsed as a collection")
	}
	f := loadSystemFonts(nil)
	if f.bold == nil {
		t.Skip("HelveticaNeue.ttc is not on this machine")
	}
	if !isSFNT(f.bold.Content()) {
		t.Fatal("the extracted bold face is not a font file")
	}
}

// Holding ⌘⌫ repeats the shortcut (Fyne passes key repeats on, and AppKit
// repeats menu key equivalents). The repeats must never confirm the sheet:
// only a ⌘⌫ after a key release, with the sheet up for a second, moves.
func TestHeldShortcutDoesNotConfirm(t *testing.T) {
	home := fixture(t)
	before := testutil.Checksum(t, home)
	u := newTestUI(t, false)
	var mu sync.Mutex
	u.do = func(fn func()) { mu.Lock(); defer mu.Unlock(); fn() }
	clock := time.Now()
	u.now = func() time.Time { return clock }
	u.m.ScanNow()
	u.update()

	u.moveToTrash() // opens the sheet
	u.moveToTrash() // the same press, or its first repeat
	clock = clock.Add(300 * time.Millisecond)
	u.moveToTrash() // a repeat at 300 ms
	clock = clock.Add(2 * time.Second)
	u.moveToTrash() // still held after 2 s: no key-up yet
	if u.m.Sheet() != guimodel.SheetConfirm {
		t.Fatalf("sheet %d: a held ⌘⌫ confirmed the move", u.m.Sheet())
	}
	if testutil.Checksum(t, home) != before {
		t.Fatal("the fixture changed")
	}

	// Released, then pressed again: moves.
	u.keyUp(&fyne.KeyEvent{Name: fyne.KeyBackspace})
	clock = clock.Add(400 * time.Millisecond)
	u.moveToTrash()
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		done := u.m.Screen() == guimodel.ScreenDone
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the deliberate second ⌘⌫ did not move")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A release that comes too soon after the sheet opened is not enough.
func TestShortcutNeedsTheSheetUpForASecond(t *testing.T) {
	home := fixture(t)
	before := testutil.Checksum(t, home)
	u := newTestUI(t, false)
	clock := time.Now()
	u.now = func() time.Time { return clock }
	u.m.ScanNow()
	u.update()
	u.moveToTrash()
	clock = clock.Add(200 * time.Millisecond)
	u.keyUp(&fyne.KeyEvent{Name: fyne.KeyBackspace})
	clock = clock.Add(400 * time.Millisecond)
	u.moveToTrash()
	if u.m.Sheet() != guimodel.SheetConfirm || testutil.Checksum(t, home) != before {
		t.Fatalf("sheet %d: ⌘⌫ confirmed after 0.6 s", u.m.Sheet())
	}
}

// assertDoneFits checks the done screen needs no scrolling in the window.
func assertDoneFits(t *testing.T, u *ui) {
	t.Helper()
	if u.doneScroll == nil {
		t.Fatal("not on the done screen")
	}
	if need, have := u.doneScroll.Content.MinSize().Height, u.doneScroll.Size().Height; need > have {
		t.Errorf("the done screen needs %.0f pt but the window shows %.0f", need, have)
	}
}

func TestDoneFitsDefaultWindow(t *testing.T) {
	fixture(t)
	u := newTestUI(t, false)
	u.m.ScanNow()
	u.update()
	u.m.OpenConfirm()
	if err := u.m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	u.update()
	_ = u.win.Canvas().Capture()
	assertDoneFits(t, u)
}

// Open Trash and Open receipt pass /usr/bin/open only a real folder and a
// real file: a receipt's trash_dir that has become a link is refused.
func TestOpenChecksPaths(t *testing.T) {
	home := fixture(t)
	u := newTestUI(t, false)
	var opened [][]string
	u.open = func(args ...string) { opened = append(opened, args) }
	u.m.ScanNow()
	u.update()
	u.m.OpenConfirm()
	if err := u.m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	u.update()
	u.openTrash()
	u.openReceipt()
	if len(opened) != 2 || opened[0][0] != home+"/.Trash" || opened[1][0] != "-R" {
		t.Fatalf("opened %v", opened)
	}
	trash := filepath.Join(home, ".Trash")
	if err := os.Rename(trash, trash+"-real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(trash+"-real", trash); err != nil {
		t.Fatal(err)
	}
	u.openTrash()
	if len(opened) != 2 || !strings.Contains(u.m.Status(), "it is not a folder") {
		t.Fatalf("opened %v status %q", opened, u.m.Status())
	}
}

// The real-home watch notices a file that changes or appears.
func TestRealHomeWatch(t *testing.T) {
	testutil.GuardRealHome(t)
	if os.Getenv("HOME") == "" || len(realHomeFiles()) == 0 {
		t.Fatal("no HOME during the GUI tests")
	}
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(a, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := stamps([]string{a, b})
	if err := os.Chtimes(a, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := changed([]string{a, b}, before, stamps([]string{a, b})); len(got) != 2 {
		t.Fatalf("changed %v", got)
	}
}

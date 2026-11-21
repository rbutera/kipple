//go:build gui && darwin

// The GUI is macOS-only in v0.3.0 (linux-port design L11): its tests use
// macOS fonts, Finder and the Full Disk Access flow, so they build only on
// macOS.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/rbutera/kipple/internal/guimodel"
	"github.com/rbutera/kipple/internal/testutil"
)

// TestScreenshots writes the README and review screenshots. It runs only
// when KIPPLE_SCREENSHOTS names an output folder (scripts/gui-screenshots.sh
// sets it). The home is KIPPLE_SCREENSHOT_HOME, a fixture the script made
// with tools/mkfixture in a temp folder, or this test's own fixture; either
// way KIPPLE_HOME and KIPPLE_STATE_DIR point into a temp folder and the
// real-home guard runs before anything is read. Nothing opens a window: the
// pictures come from Fyne's software renderer.
func TestScreenshots(t *testing.T) {
	out := os.Getenv("KIPPLE_SCREENSHOTS")
	if out == "" {
		t.Skip("KIPPLE_SCREENSHOTS is not set")
	}
	if !filepath.IsAbs(out) {
		t.Fatal("KIPPLE_SCREENSHOTS must be an absolute path")
	}
	if home := os.Getenv("KIPPLE_SCREENSHOT_HOME"); home != "" {
		t.Setenv("KIPPLE_HOME", home)
		t.Setenv("KIPPLE_STATE_DIR", filepath.Join(filepath.Dir(home), "state"))
	} else {
		fixture(t)
	}
	testutil.GuardRealHome(t)

	u := newTestUI(t, false)
	capture(t, u, out, "gui-start-light.png")
	u.m.ScanNow()
	u.update()
	capture(t, u, out, "gui-review-light.png")
	setVariant(u, true)
	capture(t, u, out, "gui-review-dark.png")
	setVariant(u, false)
	u.win.Resize(fyne.NewSize(760, 520))
	u.update()
	capture(t, u, out, "gui-review-min-light.png")
	u.win.Resize(fyne.NewSize(960, 640))
	u.update()
	// The at-risk project, further down the list.
	for i, r := range u.m.Rows() {
		if r.Kind == guimodel.RowItem && strings.HasSuffix(u.m.Plan().Items[r.Item].Path, "/kipple-demo/node_modules") {
			u.m.SetCursor(i)
		}
	}
	u.update()
	u.scrollToCursor()
	capture(t, u, out, "gui-review-blocked-light.png")
	u.m.CursorHome()
	u.update()
	u.scrollToCursor()
	u.m.OpenConfirm()
	u.update()
	capture(t, u, out, "gui-confirm-light.png")
	if err := u.m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	u.update()
	capture(t, u, out, "gui-done-light.png")
	assertDoneFits(t, u)
	setVariant(u, true)
	capture(t, u, out, "gui-done-dark.png")
	u.win.Resize(fyne.NewSize(760, 520))
	u.update()
	capture(t, u, out, "gui-done-min-dark.png")
	assertDoneFits(t, u)
	setVariant(u, false)
	capture(t, u, out, "gui-done-min-light.png")
	u.win.Resize(fyne.NewSize(960, 640))
	u.update()

	// Put back, from the done screen.
	u.m.OpenPutBack()
	u.update()
	capture(t, u, out, "gui-putback-light.png")
	if err := u.m.PutBackNow(); err != nil {
		t.Fatal(err)
	}
	u.update()
	capture(t, u, out, "gui-restored-light.png")
	assertDoneFits(t, u)
	u.win.Resize(fyne.NewSize(760, 520))
	u.update()
	capture(t, u, out, "gui-restored-min-light.png")
	assertDoneFits(t, u)
}

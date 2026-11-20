//go:build gui

// Command kipple-gui is kipple's optional graphical front end (design D13,
// docs/gui-design.md). It builds only with the gui tag and contains no
// deletion logic: the view-model (internal/guimodel) scans and moves
// through the same packages as kipple clean.
package main

import (
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/rbutera/kipple/assets/brand"
	"github.com/rbutera/kipple/cmd/internal/guilaunch"
	"github.com/rbutera/kipple/cmd/internal/launch"
	"github.com/rbutera/kipple/internal/guimodel"
	"github.com/rbutera/kipple/internal/procs"
)

// Set with -ldflags "-X main.version=... -X main.commit=..." (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
)

func main() {
	a := app.NewWithID("com.rbutera.kipple")
	a.SetIcon(fyne.NewStaticResource("kipple-icon.png", brand.Icon()))
	m := guilaunch.Model(guilaunch.Options{
		Runtime: launch.System(), Geteuid: os.Geteuid, Version: version, Procs: procs.New(),
	})
	u := newWindow(a, m, loadSystemFonts(nil))
	u.win.ShowAndRun()
}

// newWindow builds the window for m.
func newWindow(a fyne.App, m *guimodel.Model, fonts systemFonts) *ui {
	th := newTheme(fonts)
	a.Settings().SetTheme(th)
	w := a.NewWindow("kipple")
	u := newUI(a, w, th, m)
	w.SetMainMenu(u.menus())
	w.SetCloseIntercept(u.requestQuit)
	w.Resize(fyne.NewSize(960, 640))
	a.Settings().AddListener(func(fyne.Settings) { u.update() })
	u.update()
	return u
}

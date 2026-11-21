//go:build gui

// Command kipple-gui is kipple's optional graphical front end (design D13,
// docs/gui-design.md). It builds only with the gui tag and contains no
// deletion logic: the view-model (internal/guimodel) scans and moves
// through the same packages as kipple clean.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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
	// Before anything reads home or Fyne starts (design L11 of linux-port).
	if msg := platformRefusal(runtime.GOOS); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
	o := guilaunch.Options{Runtime: launch.System(), Geteuid: os.Geteuid, Version: version, Procs: procs.New()}
	m := guilaunch.Model(o)
	// With KIPPLE_HOME set, Fyne's preferences and font cache go to the
	// run's state folder, never the real home. This must happen before
	// Fyne starts; home resolution above has already read the real HOME.
	dir, err := guilaunch.FyneHome(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kipple-gui:", err)
		os.Exit(1)
	}
	if dir != "" {
		for k, v := range map[string]string{
			"HOME": dir, "XDG_CACHE_HOME": filepath.Join(dir, ".cache"),
			"XDG_CONFIG_HOME": filepath.Join(dir, ".config"), "XDG_DATA_HOME": filepath.Join(dir, ".local", "share"),
		} {
			if err := os.Setenv(k, v); err != nil {
				fmt.Fprintln(os.Stderr, "kipple-gui:", err)
				os.Exit(1)
			}
		}
	}
	a := app.NewWithID("com.rbutera.kipple")
	a.SetIcon(fyne.NewStaticResource("kipple-icon.png", brand.Icon()))
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

//go:build gui

// Command kipple-gui is kipple's optional graphical front end (design D13,
// docs/gui-design.md). It builds only with the gui tag and contains no
// deletion logic: the view-model (internal/guimodel) scans and moves
// through the same packages as kipple clean.
package main

import (
	"fyne.io/fyne/v2/app"
)

// Set with -ldflags "-X main.version=... -X main.commit=..." (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
)

func main() {
	a := app.NewWithID("com.rbutera.kipple")
	_ = a
}

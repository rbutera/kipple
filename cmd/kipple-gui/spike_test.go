//go:build gui

package main

import (
	"image"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

type spikeTheme struct {
	fyne.Theme
	reg, bold fyne.Resource
}

func (s spikeTheme) Font(st fyne.TextStyle) fyne.Resource {
	if st.Bold && s.bold != nil {
		return s.bold
	}
	if s.reg != nil {
		return s.reg
	}
	return theme.DefaultTheme().Font(st)
}

func ink(img image.Image) (n int) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			if r+g+bb < 3*0x8000 {
				n++
			}
		}
	}
	return n
}

func render(t *testing.T, th fyne.Theme, bold bool) int {
	a := test.NewTempApp(t)
	a.Settings().SetTheme(th)
	txt := canvas.NewText("kipple Moved 24 items to the Trash", color.Black)
	txt.TextSize = 20
	txt.TextStyle.Bold = bold
	w := test.NewWindow(container.NewStack(canvas.NewRectangle(color.White), txt))
	w.Resize(fyne.NewSize(500, 60))
	defer w.Close()
	return ink(w.Canvas().Capture())
}

func TestFontSpike(t *testing.T) {
	f := loadSystemFonts(nil)
	if f.regular == nil || f.bold == nil {
		t.Skip("system fonts not present")
	}
	reg, bold := render(t, spikeTheme{Theme: theme.DefaultTheme(), reg: f.regular, bold: f.bold}, false), render(t, spikeTheme{Theme: theme.DefaultTheme(), reg: f.regular, bold: f.bold}, true)
	sfBold := render(t, spikeTheme{Theme: theme.DefaultTheme(), reg: f.regular, bold: f.regular}, true)
	t.Logf("SF regular %d, SF as bold %d, Helvetica Neue Bold %d", reg, sfBold, bold)
}

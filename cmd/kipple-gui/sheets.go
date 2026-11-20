//go:build gui

package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rbutera/kipple/internal/guimodel"
)

// scrim covers the window behind a sheet. Clicking it does nothing:
// dismissing takes a deliberate Cancel or Esc.
type scrim struct {
	widget.BaseWidget
	content fyne.CanvasObject
}

func newScrim(content fyne.CanvasObject) *scrim {
	s := &scrim{content: content}
	s.ExtendBaseWidget(s)
	return s
}

func (s *scrim) Tapped(*fyne.PointEvent) {}
func (s *scrim) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(col(cScrim))
	return widget.NewSimpleRenderer(container.NewStack(bg, container.NewCenter(s.content)))
}

// sheetBox is the sheet: 600pt wide, surface, 12pt radius, 24pt padding,
// shadow.
func sheetBox(o fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(col(cSurface))
	bg.CornerRadius = sz(12)
	bg.Shadow = canvas.Shadow{Color: col("kipple.shadow"), BlurRadius: sz(24), Offset: fyne.NewPos(0, sz(6)), Variant: canvas.DropShadow}
	return container.New(fixed{600, 0}, container.NewStack(bg, pad(o, space5, space5, space5, space5)))
}

// syncSheet shows the model's sheet over the window.
func (u *ui) syncSheet() {
	kind := u.m.Sheet()
	live := kind == guimodel.SheetApplying || kind == guimodel.SheetRestoring
	if kind == u.sheetKind && !live && (kind == guimodel.SheetNone || u.sheet != nil) {
		return
	}
	if u.sheet != nil {
		u.win.Canvas().Overlays().Remove(u.sheet)
		u.sheet = nil
	}
	u.sheetKind = kind
	var content fyne.CanvasObject
	var focus fyne.Focusable
	switch kind {
	case guimodel.SheetConfirm:
		content, focus = u.confirmSheet()
	case guimodel.SheetApplying, guimodel.SheetRestoring:
		content = u.applyingSheet()
	case guimodel.SheetPutBack:
		content, focus = u.putBackSheet()
	default:
		return
	}
	u.sheet = newScrim(sheetBox(content))
	u.win.Canvas().Overlays().Add(u.sheet)
	if focus != nil {
		u.win.Canvas().Focus(focus)
	}
}

func buttonsRight(bs ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, container.NewHBox(bs...))
}

func (u *ui) confirmSheet() (fyne.CanvasObject, fyne.Focusable) {
	v := u.m.Confirm()
	title := rich(seg("Move ", cInk, "kipple.heading", true), seg(v.Count, cAmberInk, "kipple.heading", true), seg(" to the Trash?", cInk, "kipple.heading", true))
	col := container.NewVBox(title, spacer(1, 4), line(v.Total, cInk, szDisplay, true), para(v.Rules, cMuted, theme.SizeNameText, false))
	if v.TierLine != "" || v.AtRiskLine != "" {
		col.Add(spacer(1, 8))
	}
	if v.TierLine != "" {
		col.Add(para(v.TierLine, cInk, theme.SizeNameText, false))
	}
	if v.AtRiskLine != "" {
		risk := container.NewVBox(para(v.AtRiskLine, cAmberInk, theme.SizeNameText, false))
		for _, p := range v.AtRisk {
			risk.Add(newPathText(p, cInk, sizeBody))
		}
		col.Add(container.NewBorder(nil, nil, container.NewVBox(spacer(1, 4), riskMark()), nil, risk))
	}
	col.Add(spacer(1, 8))
	col.Add(para(v.Promise, cMuted, theme.SizeNameText, false))
	if v.Note != "" {
		bar := canvas.NewRectangle(col2(cSep))
		bar.SetMinSize(fyne.NewSize(sz(2), 1))
		col.Add(spacer(1, 4))
		col.Add(container.NewBorder(nil, nil, container.NewHBox(bar, spacer(8, 1)), nil, para(v.Note, cInk, theme.SizeNameText, false)))
	}
	cancel := newButton("Cancel", false, func() { u.m.CancelConfirm(); u.update() })
	move := newButton("Move to Trash", true, u.moveToTrash)
	if !v.CanMove {
		move.Disable()
	}
	cancel.fallback, move.fallback = u.typedKey, u.typedKey
	col.Add(spacer(1, 12))
	col.Add(buttonsRight(cancel, move))
	return col, cancel
}

func (u *ui) applyingSheet() fyne.CanvasObject {
	v := u.m.Applying()
	col := container.NewVBox(line(v.Title, cInk, "kipple.heading", true), spacer(1, 12))
	if u.m.Sheet() == guimodel.SheetRestoring {
		col.Add(sized(widget.NewProgressBarInfinite(), 0, 6))
		return col
	}
	col.Add(newMeter(v.Fraction, cPrimary, cHeader, 0, 6))
	col.Add(container.NewBorder(nil, nil, line(v.Count, cInk, theme.SizeNameText, false), line(v.Bytes, cMuted, theme.SizeNameText, false)))
	col.Add(newPathText(v.Current, cMuted, sizeBody))
	stop := newButton(v.Stop, false, func() { u.m.StopApply(); u.update() })
	if v.Stopping {
		stop.Disable()
	}
	col.Add(spacer(1, 12))
	col.Add(buttonsRight(stop))
	return col
}

func (u *ui) putBackSheet() (fyne.CanvasObject, fyne.Focusable) {
	v := u.m.PutBack()
	cancel := newButton("Cancel", false, func() { u.m.CancelPutBack(); u.update() })
	put := newButton("Put Back", true, u.putBack)
	cancel.fallback, put.fallback = u.typedKey, u.typedKey
	col := container.NewVBox(line(v.Title, cInk, "kipple.heading", true), spacer(1, 8), para(v.Body, cMuted, theme.SizeNameText, false), spacer(1, 12), buttonsRight(cancel, put))
	return col, cancel
}

// col2 reads a theme colour where a local variable shadows col.
func col2(n fyne.ThemeColorName) color.Color { return col(n) }

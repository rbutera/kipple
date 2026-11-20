//go:build gui

package main

import (
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rbutera/kipple/assets/brand"
	"github.com/rbutera/kipple/internal/guimodel"
)

// brandImage returns the wordmark or mark for the current appearance.
func brandImage(word bool, h float32) fyne.CanvasObject {
	dark := isDark()
	var res fyne.Resource
	name := map[bool]string{false: "light", true: "dark"}[dark]
	if word {
		res = fyne.NewStaticResource("kipple-wordmark-"+name+".svg", brand.Wordmark(dark))
	} else {
		res = fyne.NewStaticResource("kipple-mark-"+name+".svg", brand.Mark(dark))
	}
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillContain
	w := h
	if word {
		w = h * 303.78 / 95.48
	}
	return sized(img, w, h)
}

// ---- header and action bar ----

func (u *ui) header() fyne.CanvasObject {
	cfg := u.m.Config()
	left := container.NewHBox(brandImage(true, 20), spacer(8, 1), container.NewCenter(text(versionLabel(), cMuted, sizeBody, false)), spacer(4, 1))
	home := spacer(1, 1)
	if cfg.HomeLabel != "" {
		var tag fyne.CanvasObject
		if cfg.TestHome {
			tag = container.NewCenter(chip("test home"))
		}
		// A long KIPPLE_HOME label gives way first, from the left.
		home = container.NewBorder(nil, nil, tag, nil, newPathText(cfg.HomeLabel, cMuted, sizeBody))
	}
	right := container.NewHBox()
	if u.m.Screen() == guimodel.ScreenReview {
		h := u.m.Header()
		numbers := cMuted
		if h.Any {
			numbers = cAmberInk
		}
		sel := widget.NewRichText(seg("Selected ", cMuted, theme.SizeNameText, false), seg(h.Selected[len("Selected "):], numbers, theme.SizeNameText, h.Any))
		right.Add(text(h.Found, cMuted, sizeBody, false))
		right.Add(spacer(16, 1))
		right.Add(container.NewCenter(sel))
		right.Add(container.NewCenter(newMeter(h.Share, cAmber, cHeader, 120, 4)))
	}
	row := container.NewBorder(nil, nil, container.NewCenter(left), container.NewCenter(right), pad(home, 0, 0, 0, space4))
	return sized(pad(row, 0, 0, space4, space4), 0, 56)
}

func versionLabel() string {
	if version == "dev" {
		return "dev"
	}
	return "v" + version
}

func (u *ui) actionBar() fyne.CanvasObject {
	m := u.m
	status := text(m.Status(), cInk, sizeBody, false)
	buttons := container.NewHBox()
	switch m.Screen() {
	case guimodel.ScreenReview:
		again := newButton("Scan again", false, u.rescan)
		move := newButton("Move to Trash…", true, u.moveToTrash)
		if !m.CanMove() {
			move.Disable()
		}
		buttons.Add(again)
		buttons.Add(move)
	case guimodel.ScreenDone:
		if m.Done().CanPutBack {
			buttons.Add(newButton("Put back…", false, func() { u.m.OpenPutBack(); u.update() }))
		}
		buttons.Add(newButton("Scan again", false, u.scan))
		buttons.Add(newButton("Done", true, u.requestQuit))
	case guimodel.ScreenProblem:
		for _, a := range m.Problem().Actions {
			buttons.Add(u.actionButton(a))
		}
	}
	return sized(pad(container.NewBorder(nil, nil, nil, container.NewCenter(buttons), container.NewHBox(container.NewCenter(status))), 0, 0, space4, space4), 0, 56)
}

func (u *ui) actionButton(a guimodel.Action) fyne.CanvasObject {
	switch a {
	case guimodel.ActionScan:
		return newButton("Scan again", false, u.scan)
	case guimodel.ActionOpenPrivacy:
		return newButton("Open Privacy Settings", true, u.openPrivacy)
	case guimodel.ActionBack:
		label := "Back"
		if u.m.Problem().BackTo == guimodel.ScreenReview {
			label = "Back to review"
		}
		return newButton(label, false, func() { u.m.Back(); u.update() })
	case guimodel.ActionOpenTrash:
		return newButton("Open Trash", false, u.openTrash)
	case guimodel.ActionOpenReceipt:
		return newButton("Open receipt", false, u.openReceipt)
	}
	return newButton("Quit", true, u.requestQuit)
}

// ---- start and scanning ----

func (u *ui) start() fyne.CanvasObject {
	center := func(o fyne.CanvasObject) fyne.CanvasObject { return container.NewCenter(o) }
	col := container.NewVBox(
		center(brandImage(false, 96)),
		spacer(1, space6),
		center(text("Find the caches and developer junk in your home folder.", cInk, sizeHeading, true)),
		spacer(1, space3),
		center(text("kipple looks first and shows you everything before anything moves.", cMuted, sizeBody, false)),
		center(text("What you choose goes to the Trash, with a receipt to put it back.", cMuted, sizeBody, false)),
		spacer(1, space6),
		center(container.NewHBox(sized(newButton("Scan", true, u.scan), 140, 32), container.NewCenter(text("⏎", cMuted, sizeBody, false)))),
	)
	if u.m.ShowDownloadsNote() {
		col.Add(spacer(1, space6))
		col.Add(center(text("macOS may ask to let kipple look in Downloads. It only reads", cMuted, sizeCaption, false)))
		col.Add(center(text("there; installers move only if you pick them.", cMuted, sizeCaption, false)))
	}
	return container.NewCenter(col)
}

func (u *ui) scanning() fyne.CanvasObject {
	v := u.m.Scanning()
	cells := make([]fyne.CanvasObject, len(v.Tiles))
	for i, on := range v.Tiles {
		r := canvas.NewRectangle(col(cRecess))
		if on {
			r.FillColor = col(cInk)
		}
		r.CornerRadius = sz(4)
		cells[i] = r
	}
	grid := container.New(gridLayout{cols: 12, cell: tileSz, gap: 8}, cells...)
	var current fyne.CanvasObject
	if v.Measuring {
		bar := widget.NewProgressBarInfinite()
		current = sized(bar, 240, 4)
	} else {
		current = container.NewCenter(text(v.Current, cMuted, sizeBody, false))
	}
	stop := newButton("Stop scan", false, u.escape)
	body := container.NewVBox(
		container.NewCenter(grid),
		spacer(1, space5),
		container.NewCenter(text(v.Count, cInk, sizeSubheading, true)),
		sized(current, 0, 20),
		spacer(1, space6),
		container.NewCenter(container.NewHBox(sized(stop, 140, 32), container.NewCenter(text("esc", cMuted, sizeBody, false)))),
	)
	return container.NewCenter(body)
}

// ---- problem ----

func (u *ui) problem() fyne.CanvasObject {
	p := u.m.Problem()
	col := container.NewVBox(para(p.Heading, cInk, "kipple.heading", true), spacer(1, space3))
	for _, b := range p.Body {
		if b != "" {
			col.Add(para(b, cInk, theme.SizeNameText, false))
		}
	}
	if p.Card != nil {
		col.Add(spacer(1, space4))
		col.Add(sized(u.receiptCard(*p.Card, false), 440, 0))
	}
	return container.NewVScroll(pad(container.NewBorder(col, nil, nil, nil), space6, space4, space6+8, space6))
}

// ---- receipt card ----

// cardBox is a surface card with a 10pt radius.
func cardBox(o fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(col(cSurface))
	bg.CornerRadius = sz(10)
	bg.StrokeColor, bg.StrokeWidth = col(cSep), 1
	return container.NewStack(bg, pad(o, space4, space4, space4, space4))
}

// field is a labelled value with an 80pt label column.
func field(label string, value fyne.CanvasObject) fyne.CanvasObject {
	l := line(label, cMuted, "kipple.caption", false)
	return container.NewBorder(nil, nil, container.New(&labelCol{}, l), nil, value)
}

// labelCol is the detail and card label column: 80pt wide, top-aligned.
type labelCol struct{}

func (labelCol) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(sz(80), objs[0].MinSize().Height)
}
func (labelCol) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(s.Width, objs[0].MinSize().Height))
}

// withButton is a value with a quiet button after it, centred on one line.
func withButton(v fyne.CanvasObject, b fyne.CanvasObject) fyne.CanvasObject {
	return container.NewHBox(container.NewCenter(v), b)
}

func (u *ui) receiptCard(c guimodel.ReceiptCard, buttons bool) fyne.CanvasObject {
	rows := container.NewVBox(line("RECEIPT", cMuted, "kipple.caption", true))
	rows.Add(field("Receipt", withButton(monoLine(c.ID, cInk), linkButton("Copy", func() { u.copyText(c.ID) }))))
	rows.Add(field("Moved", line(c.Moved, cInk, theme.SizeNameText, false)))
	if c.Skipped > 0 {
		rows.Add(field("Skipped", line(itoa(c.Skipped), cInk, theme.SizeNameText, false)))
	}
	if c.Displaced > 0 {
		rows.Add(field("Displaced", line(itoa(c.Displaced), cInk, theme.SizeNameText, false)))
	}
	if c.Pending > 0 {
		rows.Add(field("Pending", para(itoa(c.Pending)+" · "+c.PendingPath, cInk, theme.SizeNameText, false)))
	}
	if c.NotTried > 0 {
		rows.Add(field("Not tried", line(itoa(c.NotTried), cInk, theme.SizeNameText, false)))
	}
	if c.How != "" {
		rows.Add(field("How", para(c.How, cMuted, theme.SizeNameText, false)))
	}
	if buttons {
		rows.Add(spacer(1, 4))
		rows.Add(container.NewHBox(newButton("Open Trash", false, u.openTrash), newButton("Open receipt", false, u.openReceipt)))
	}
	return cardBox(rows)
}

// ---- done ----

// recessMark is the mark's six tiles with an empty recess where the stray
// tile sat: the item has left, and the slot shows where it can go back.
func recessMark(size float32) fyne.CanvasObject {
	ink := []pt{{9.55, 2.95}, {9.55, 17.55}, {9.55, 32.15}, {9.55, 46.75}, {38.75, 17.55}, {24.15, 32.15}}
	var objs []fyne.CanvasObject
	var pos []pt
	for _, s := range ink {
		r := canvas.NewRectangle(col(cInk))
		objs, pos = append(objs, r), append(pos, s)
	}
	hole := canvas.NewRectangle(col(cRecess))
	hole.StrokeColor, hole.StrokeWidth = col(cControl), 1
	objs, pos = append(objs, hole), append(pos, pt{38.75, 46.75})
	return container.New(&absLayout{size: size, unit: 64, pos: pos, cell: 12}, objs...)
}

type pt struct{ x, y float32 }

type absLayout struct {
	size, unit, cell float32
	pos              []pt
}

func (a *absLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSquareSize(sz(a.size)) }
func (a *absLayout) Layout(objs []fyne.CanvasObject, _ fyne.Size) {
	k := sz(a.size) / a.unit
	for i, o := range objs {
		o.Move(fyne.NewPos(a.pos[i].x*k, a.pos[i].y*k))
		o.Resize(fyne.NewSquareSize(a.cell * k))
		if r, ok := o.(*canvas.Rectangle); ok {
			r.CornerRadius = 3.2 * k
		}
	}
}

func (u *ui) done() fyne.CanvasObject {
	d := u.m.Done()
	head := container.NewVBox(para(d.Title, cInk, "kipple.heading", true))
	if d.Total != "" {
		head.Add(line(d.Total, cInk, szDisplay, true))
		head.Add(para(d.SpaceLine, cMuted, theme.SizeNameText, false))
		if d.SharedNote != "" {
			head.Add(para(d.SharedNote, cMuted, theme.SizeNameText, false))
		}
	}
	top := container.NewBorder(nil, nil, pad(recessMark(72), 0, 0, 0, space5), nil, head)
	left := container.NewVBox(u.receiptCard(d.Card, true))
	if d.Restored != nil {
		left.Add(spacer(1, space4))
		left.Add(para(d.Restored.Title, cInk, "kipple.subheading", true))
		for _, s := range d.Restored.Skipped {
			left.Add(skipLine(s))
		}
	}
	if d.Displaced != "" {
		left.Add(spacer(1, space3))
		left.Add(container.NewBorder(nil, nil, container.NewVBox(widget.NewIcon(theme.WarningIcon())), nil, para(d.Displaced, cInk, theme.SizeNameText, false)))
	}
	for _, w := range d.Warnings {
		left.Add(para("Warning: "+w, cInk, theme.SizeNameText, false))
	}
	right := container.NewVBox()
	if len(d.Moved) > 0 {
		var total string
		if d.Total != "" {
			total = d.Total
		}
		right.Add(container.NewBorder(nil, nil, line("MOVED", cMuted, "kipple.caption", true), line(total, cMuted, "kipple.caption", true)))
		for _, l := range d.Moved {
			right.Add(container.NewBorder(nil, nil, nil, text(l.Text, cMuted, sizeBody, false), newPathText(l.Path, cInk, sizeBody)))
		}
		if d.MovedMore > 0 {
			right.Add(container.NewBorder(nil, nil, text("and "+itoa(d.MovedMore)+" more", cMuted, sizeBody, false), linkButton("Show ›", func() { u.m.ShowAllMoved(); u.update() })))
		}
	}
	if len(d.Skipped) > 0 || d.NotTried > 0 {
		right.Add(spacer(1, space3))
		right.Add(line("SKIPPED", cMuted, "kipple.caption", true))
		for _, s := range d.Skipped {
			right.Add(skipLine(s))
		}
		if d.NotTried > 0 {
			right.Add(text("Not tried "+itoa(d.NotTried), cInk, sizeBody, false))
		}
	}
	// Restore guidance sits under the moved list, so the whole result fits
	// in the default window.
	mind := container.NewVBox()
	if d.ChangedMind != "" {
		mind.Add(spacer(1, space3))
		mind.Add(line("Changed your mind?", cInk, "kipple.subheading", true))
		mind.Add(para(d.ChangedMind, cMuted, theme.SizeNameText, false))
		mind.Add(container.NewBorder(nil, nil, nil, container.NewVBox(linkButton("Copy", func() { u.copyText(d.Command) })), monoPara(d.Command, cInk)))
	}
	rightCol := container.NewVBox(mind)
	if len(right.Objects) > 0 {
		rightCol = container.NewVBox(cardBox(right), mind)
	}
	cols := container.New(halves{space5}, left, rightCol)
	return container.NewVScroll(pad(container.NewVBox(top, spacer(1, space4), cols), space4, space4, space6, space6))
}

func skipLine(s guimodel.Line) fyne.CanvasObject {
	v := container.NewVBox(newPathText(s.Path, cInk, sizeBody))
	t := s.Text
	if s.Next != "" {
		t += " " + s.Next
	}
	v.Add(para(t, cMuted, theme.SizeNameText, false))
	return v
}

func itoa(n int) string { return strconv.Itoa(n) }

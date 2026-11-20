//go:build gui

package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Spacing scale (docs/gui-design.md 8.4).
const (
	space3 = 12
	space4 = 16
	space5 = 24
	space6 = 32
	space7 = 48
	tileSz = 16
	hitSz  = 32
)

// ---- text ----

// text is a single-line canvas text in a theme colour.
func text(s string, c fyne.ThemeColorName, size float32, bold bool) *canvas.Text {
	t := canvas.NewText(s, col(c))
	t.TextSize = sz(size)
	t.TextStyle.Bold = bold
	return t
}

// Custom size names for RichText segments.
const (
	szCaption fyne.ThemeSizeName = "text"
	szDisplay fyne.ThemeSizeName = "kipple.display"
)

func init() {
	sizes[szDisplay] = sizeDisplay
	sizes["kipple.caption"] = sizeCaption
	sizes["kipple.subheading"] = sizeSubheading
	sizes["kipple.heading"] = sizeHeading
}

// seg is a RichText segment.
func seg(s string, c fyne.ThemeColorName, size fyne.ThemeSizeName, bold bool) *widget.TextSegment {
	return &widget.TextSegment{Text: s, Style: widget.RichTextStyle{ColorName: c, SizeName: size, Inline: true, TextStyle: fyne.TextStyle{Bold: bold}}}
}

// para is word-wrapped text.
func para(s string, c fyne.ThemeColorName, size fyne.ThemeSizeName, bold bool) *widget.RichText {
	r := widget.NewRichText(seg(s, c, size, bold))
	r.Wrapping = fyne.TextWrapWord
	return r
}

// line is one line of RichText (no wrapping), aligned with para.
func line(s string, c fyne.ThemeColorName, size fyne.ThemeSizeName, bold bool) *widget.RichText {
	return widget.NewRichText(seg(s, c, size, bold))
}

// monoLine is one line of monospace RichText.
func monoLine(s string, c fyne.ThemeColorName) *widget.RichText {
	g := seg(s, c, theme.SizeNameText, false)
	g.Style.TextStyle.Monospace = true
	return widget.NewRichText(g)
}

// monoPara is word-wrapped monospace text (commands).
func monoPara(s string, c fyne.ThemeColorName) *widget.RichText {
	g := seg(s, c, theme.SizeNameText, false)
	g.Style.TextStyle.Monospace = true
	r := widget.NewRichText(g)
	r.Wrapping = fyne.TextWrapWord
	return r
}

// halves lays out two columns of equal width with a gap. Unlike a grid it
// does not force each column to the wider one's minimum, so the window's
// minimum stays small.
type halves struct{ gap float32 }

func (h halves) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var ht float32
	for _, o := range objs {
		ht = fyne.Max(ht, o.MinSize().Height)
	}
	return fyne.NewSize(sz(600), ht)
}
func (h halves) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	w := (s.Width - sz(h.gap)) / 2
	for i, o := range objs {
		o.Move(fyne.NewPos(float32(i)*(w+sz(h.gap)), 0))
		o.Resize(fyne.NewSize(w, o.MinSize().Height))
	}
}

// rich is word-wrapped text made of segments.
func rich(segs ...widget.RichTextSegment) *widget.RichText {
	r := widget.NewRichText(segs...)
	r.Wrapping = fyne.TextWrapWord
	return r
}

// ---- left-truncating path text ----

// pathText draws a path on one line, truncated from the left so its
// distinctive end stays visible ("…/DerivedData/Kipple-hdz…").
type pathText struct {
	widget.BaseWidget
	full  string
	color fyne.ThemeColorName
	size  float32
	t     *canvas.Text
}

func newPathText(s string, c fyne.ThemeColorName, size float32) *pathText {
	p := &pathText{full: s, color: c, size: size}
	p.ExtendBaseWidget(p)
	return p
}

func (p *pathText) set(s string, c fyne.ThemeColorName) {
	p.full, p.color = s, c
	p.Refresh()
}

func (p *pathText) CreateRenderer() fyne.WidgetRenderer {
	p.t = canvas.NewText(p.full, col(p.color))
	return &pathRenderer{p: p}
}

type pathRenderer struct{ p *pathText }

func (r *pathRenderer) Layout(s fyne.Size) {
	t := r.p.t
	t.TextSize = sz(r.p.size)
	t.Color = col(r.p.color)
	t.Text = truncLeft(r.p.full, s.Width, t.TextSize, t.TextStyle)
	h := t.MinSize().Height
	t.Move(fyne.NewPos(0, (s.Height-h)/2))
	t.Resize(fyne.NewSize(s.Width, h))
}
func (r *pathRenderer) MinSize() fyne.Size {
	h := fyne.MeasureText("Ag", sz(r.p.size), fyne.TextStyle{}).Height
	return fyne.NewSize(sz(40), h)
}
func (r *pathRenderer) Refresh()                     { r.Layout(r.p.Size()); r.p.t.Refresh() }
func (r *pathRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.p.t} }
func (r *pathRenderer) Destroy()                     {}

// truncLeft cuts s from the left until it fits w.
func truncLeft(s string, w, size float32, st fyne.TextStyle) string {
	if w <= 0 || fyne.MeasureText(s, size, st).Width <= w {
		return s
	}
	rs := []rune(s)
	lo, hi := 0, len(rs)
	for lo < hi { // smallest cut that fits
		mid := (lo + hi) / 2
		if fyne.MeasureText("…"+string(rs[mid:]), size, st).Width <= w {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return "…" + string(rs[lo:])
}

// ---- tile ----

// tileState is what a tile shows.
type tileState int

const (
	tileNone    tileState = iota // nothing drawn
	tileOff                      // outline: may be selected
	tileOn                       // amber with a check: selected
	tilePartial                  // amber with a bar: some of a group selected
	tileBlocked                  // recess: something stops it
	tileDot                      // info: a dot, no box
)

// tile is kipple's checkbox: the mark's rounded square at 16pt inside a
// 32pt hit area (docs/gui-design.md 2).
type tile struct {
	widget.BaseWidget
	state tileState
	onTap func()
	label string
}

func newTile(st tileState, onTap func()) *tile {
	t := &tile{state: st, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tile) set(st tileState, label string) {
	if t.state == st && t.label == label {
		return
	}
	t.state, t.label = st, label
	t.Refresh()
}

// Tapped implements fyne.Tappable.
func (t *tile) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

// AccessibilityLabel and AccessibilityRole implement fyne.Accessible.
func (t *tile) AccessibilityLabel() string             { return t.label }
func (t *tile) AccessibilityRole() fyne.AccessibleRole { return fyne.AccessibleRoleButton }

func (t *tile) CreateRenderer() fyne.WidgetRenderer {
	r := &tileRenderer{t: t, box: canvas.NewRectangle(color.Transparent), dot: canvas.NewCircle(color.Transparent),
		a: canvas.NewLine(color.Transparent), b: canvas.NewLine(color.Transparent), bar: canvas.NewRectangle(color.Transparent)}
	r.Refresh()
	return r
}

type tileRenderer struct {
	t        *tile
	box, bar *canvas.Rectangle
	dot      *canvas.Circle
	a, b     *canvas.Line
}

func (r *tileRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(sz(hitSz)) }
func (r *tileRenderer) Layout(s fyne.Size) {
	side := sz(tileSz)
	o := fyne.NewPos((s.Width-side)/2, (s.Height-side)/2)
	r.box.Move(o)
	r.box.Resize(fyne.NewSquareSize(side))
	d := side * 0.4
	r.dot.Position1 = fyne.NewPos(o.X+(side-d)/2, o.Y+(side-d)/2)
	r.dot.Position2 = fyne.NewPos(o.X+(side+d)/2, o.Y+(side+d)/2)
	// check glyph
	r.a.Position1 = fyne.NewPos(o.X+side*0.25, o.Y+side*0.52)
	r.a.Position2 = fyne.NewPos(o.X+side*0.43, o.Y+side*0.70)
	r.b.Position1 = r.a.Position2
	r.b.Position2 = fyne.NewPos(o.X+side*0.76, o.Y+side*0.32)
	r.bar.Move(fyne.NewPos(o.X+side*0.25, o.Y+side*0.45))
	r.bar.Resize(fyne.NewSize(side*0.5, side*0.12))
}
func (r *tileRenderer) Refresh() {
	r.box.CornerRadius = sz(4)
	r.box.StrokeWidth = 0
	r.box.FillColor, r.box.StrokeColor = color.Transparent, color.Transparent
	for _, l := range []*canvas.Line{r.a, r.b} {
		l.StrokeColor = color.Transparent
		l.StrokeWidth = sz(2)
	}
	r.dot.FillColor = color.Transparent
	r.bar.FillColor = color.Transparent
	switch r.t.state {
	case tileOn:
		r.box.FillColor = col(cAmber)
		r.a.StrokeColor, r.b.StrokeColor = col(cOnAmber), col(cOnAmber)
	case tilePartial:
		r.box.FillColor = col(cAmber)
		r.bar.FillColor = col(cOnAmber)
	case tileOff:
		r.box.StrokeColor, r.box.StrokeWidth = col(cControl), sz(1.5)
	case tileBlocked:
		r.box.FillColor = col(cRecess)
	case tileDot:
		r.dot.FillColor = col(cMuted)
	}
	r.Layout(r.t.Size())
	for _, o := range r.Objects() {
		o.Refresh()
	}
}
func (r *tileRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.box, r.dot, r.bar, r.a, r.b}
}
func (r *tileRenderer) Destroy() {}

// ---- pills, chips, marks ----

// pill is a tier pill (docs/gui-design.md 5.2).
func pill(tier string) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = sz(4)
	fg := cInk
	switch tier {
	case "safe":
		bg.FillColor = col(cCursor)
	case "review":
		bg.FillColor, fg = col(cPrimary), cOnPrim
	case "manual":
		bg.StrokeColor, bg.StrokeWidth = col(cControl), 1
	default:
		fg = cMuted
	}
	t := text(tier, fg, sizeCaption, true)
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(sz(1), sz(1), sz(6), sz(6)), t))
}

// chip is a short reason on a blocked row: muted on header, never amber.
func chip(s string) fyne.CanvasObject {
	bg := canvas.NewRectangle(col(cHeader))
	bg.CornerRadius = sz(4)
	return container.NewCenter(container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(sz(1), sz(1), sz(6), sz(6)), text(s, cMuted, sizeCaption, false))))
}

// riskMark is the amber at-risk triangle.
func riskMark() fyne.CanvasObject {
	p := &canvas.RegularPolygon{Sides: 3, FillColor: col(cAmber), CornerRadius: sz(1)}
	return container.NewGridWrap(fyne.NewSquareSize(sz(12)), p)
}

// meter is a thin bar: fill colour fc on track colour tc, share 0 to 1.
type meter struct {
	widget.BaseWidget
	share    float64
	fc, tc   fyne.ThemeColorName
	w, h     float32
	fill, tr *canvas.Rectangle
}

func newMeter(share float64, fc, tc fyne.ThemeColorName, w, h float32) *meter {
	m := &meter{share: share, fc: fc, tc: tc, w: w, h: h}
	m.ExtendBaseWidget(m)
	return m
}

func (m *meter) CreateRenderer() fyne.WidgetRenderer {
	m.tr, m.fill = canvas.NewRectangle(col(m.tc)), canvas.NewRectangle(col(m.fc))
	return &meterRenderer{m: m}
}

type meterRenderer struct{ m *meter }

func (r *meterRenderer) MinSize() fyne.Size { return fyne.NewSize(sz(r.m.w), sz(r.m.h)) }
func (r *meterRenderer) Layout(s fyne.Size) {
	h := sz(r.m.h)
	y := (s.Height - h) / 2
	r.m.tr.Move(fyne.NewPos(0, y))
	r.m.tr.Resize(fyne.NewSize(s.Width, h))
	share := r.m.share
	if share < 0 {
		share = 0
	}
	if share > 1 {
		share = 1
	}
	w := float32(share) * s.Width
	if share > 0 && w < h {
		w = h
	}
	r.m.fill.Move(fyne.NewPos(0, y))
	r.m.fill.Resize(fyne.NewSize(w, h))
	r.m.fill.Hidden = share == 0
}
func (r *meterRenderer) Refresh() {
	r.m.tr.FillColor, r.m.fill.FillColor = col(r.m.tc), col(r.m.fc)
	r.m.tr.CornerRadius, r.m.fill.CornerRadius = sz(r.m.h)/2, sz(r.m.h)/2
	r.Layout(r.m.Size())
	r.m.tr.Refresh()
	r.m.fill.Refresh()
}
func (r *meterRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.m.tr, r.m.fill} }
func (r *meterRenderer) Destroy()                     {}

// ---- buttons ----

// kbutton is a button that also answers Return when it has focus, so the
// confirmation's focused Cancel takes a stray Return.
type kbutton struct {
	widget.Button
	// fallback receives the keys the button does not handle (Esc).
	fallback func(*fyne.KeyEvent)
}

func newButton(label string, primary bool, tapped func()) *kbutton {
	b := &kbutton{}
	b.Text, b.OnTapped = label, tapped
	b.Importance = widget.MediumImportance
	if primary {
		b.Importance = widget.HighImportance
	}
	b.ExtendBaseWidget(b)
	return b
}

// TypedKey implements fyne.Focusable.
func (b *kbutton) TypedKey(ev *fyne.KeyEvent) {
	if ev.Name == fyne.KeyReturn || ev.Name == fyne.KeyEnter {
		if !b.Disabled() && b.OnTapped != nil {
			b.OnTapped()
		}
		return
	}
	if ev.Name == fyne.KeyEscape && b.fallback != nil {
		b.fallback(ev)
		return
	}
	b.Button.TypedKey(ev)
}

// linkButton is a quiet text button ("Copy path", "Show in Finder").
func linkButton(label string, tapped func()) *widget.Button {
	b := widget.NewButton(label, tapped)
	b.Importance = widget.LowImportance
	return b
}

// ---- rows ----

// rowBox is a list row's background: hover fill, the cursor's lichen fill
// and 3pt ink bar at the left edge.
type rowBox struct {
	widget.BaseWidget
	content       fyne.CanvasObject
	bg, bar       *canvas.Rectangle
	base          fyne.ThemeColorName
	cursor, hover bool
	onTap         func()
}

func newRowBox(content fyne.CanvasObject, base fyne.ThemeColorName, onTap func()) *rowBox {
	r := &rowBox{content: content, base: base, onTap: onTap, bg: canvas.NewRectangle(color.Transparent), bar: canvas.NewRectangle(color.Transparent)}
	r.ExtendBaseWidget(r)
	return r
}

func (r *rowBox) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}
func (r *rowBox) MouseIn(*desktop.MouseEvent)    { r.hover = true; r.Refresh() }
func (r *rowBox) MouseMoved(*desktop.MouseEvent) {}
func (r *rowBox) MouseOut()                      { r.hover = false; r.Refresh() }

func (r *rowBox) CreateRenderer() fyne.WidgetRenderer {
	return &rowRenderer{r: r}
}

type rowRenderer struct{ r *rowBox }

func (x *rowRenderer) MinSize() fyne.Size { return x.r.content.MinSize() }
func (x *rowRenderer) Layout(s fyne.Size) {
	x.r.bg.Resize(s)
	x.r.bar.Move(fyne.NewPos(0, 0))
	x.r.bar.Resize(fyne.NewSize(sz(3), s.Height))
	x.r.content.Resize(s)
}
func (x *rowRenderer) Refresh() {
	r := x.r
	r.bg.FillColor = col(r.base)
	switch {
	case r.cursor:
		r.bg.FillColor = col(cCursor)
	case r.hover:
		r.bg.FillColor = col(cHover)
	}
	r.bar.FillColor = color.Transparent
	if r.cursor {
		r.bar.FillColor = col(cInk)
	}
	r.bg.Refresh()
	r.bar.Refresh()
	r.content.Refresh()
}
func (x *rowRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{x.r.bg, x.r.bar, x.r.content}
}
func (x *rowRenderer) Destroy() {}

// ---- layouts ----

// fixed is a layout that gives its single object a fixed size, centred.
type fixed struct{ w, h float32 }

func (f fixed) MinSize(objs []fyne.CanvasObject) fyne.Size {
	w, h := sz(f.w), sz(f.h)
	for _, o := range objs {
		if f.w == 0 {
			w = fyne.Max(w, o.MinSize().Width)
		}
		if f.h == 0 {
			h = fyne.Max(h, o.MinSize().Height)
		}
	}
	return fyne.NewSize(w, h)
}
func (f fixed) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	for _, o := range objs {
		w, h := sz(f.w), sz(f.h)
		if w == 0 {
			w = s.Width
		}
		if h == 0 {
			h = s.Height
		}
		o.Resize(fyne.NewSize(w, h))
		o.Move(fyne.NewPos((s.Width-w)/2, (s.Height-h)/2))
	}
}

// sized wraps o at a fixed size.
func sized(o fyne.CanvasObject, w, h float32) fyne.CanvasObject {
	return container.New(fixed{w, h}, o)
}

// spacer is an empty block of a fixed size.
func spacer(w, h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(sz(w), sz(h)))
	return r
}

// pad surrounds o with padding (top, bottom, left, right).
func pad(o fyne.CanvasObject, t, b, l, r float32) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(sz(t), sz(b), sz(l), sz(r)), o)
}

// fill puts o on a rectangle of colour c.
func fill(o fyne.CanvasObject, c fyne.ThemeColorName) fyne.CanvasObject {
	return container.NewStack(canvas.NewRectangle(col(c)), o)
}

// hline is a 1pt separator.
func hline() fyne.CanvasObject {
	r := canvas.NewRectangle(col(cSep))
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

// vline is a 1pt vertical separator.
func vline() fyne.CanvasObject { return hline() }

// gridLayout lays fixed cells in rows of cols, centred (the scan grid).
type gridLayout struct {
	cols      int
	cell, gap float32
}

func (g gridLayout) rows(n int) int { return (n + g.cols - 1) / g.cols }
func (g gridLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	c, gap := sz(g.cell), sz(g.gap)
	cols := g.cols
	if len(objs) < cols {
		cols = len(objs)
	}
	r := g.rows(len(objs))
	return fyne.NewSize(float32(cols)*c+float32(cols-1)*gap, float32(r)*c+float32(r-1)*gap)
}
func (g gridLayout) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	c, gap := sz(g.cell), sz(g.gap)
	min := g.MinSize(objs)
	x0 := (s.Width - min.Width) / 2
	for i, o := range objs {
		o.Resize(fyne.NewSquareSize(c))
		o.Move(fyne.NewPos(x0+float32(i%g.cols)*(c+gap), float32(i/g.cols)*(c+gap)))
	}
}

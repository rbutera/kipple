//go:build gui

package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rbutera/kipple/internal/guimodel"
	"github.com/rbutera/kipple/internal/selection"
)

// Row heights (docs/gui-design.md 4.2).
const (
	hBanner  = 32
	hSection = 40
	hGroup   = 40
	hItem    = 32
)

// split lays out the list pane (58%) and the detail pane with a 1pt
// separator between them.
type split struct{ ratio float32 }

func (s split) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(objs[0].MinSize().Width+1+objs[2].MinSize().Width, fyne.Max(objs[0].MinSize().Height, objs[2].MinSize().Height))
}
func (s split) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	lw := float32(int(size.Width * s.ratio))
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(lw, size.Height))
	objs[1].Move(fyne.NewPos(lw, 0))
	objs[1].Resize(fyne.NewSize(1, size.Height))
	objs[2].Move(fyne.NewPos(lw+1, 0))
	objs[2].Resize(fyne.NewSize(size.Width-lw-1, size.Height))
}

func (u *ui) review() fyne.CanvasObject {
	listPane := fyne.CanvasObject(u.reviewList())
	if e, ok := u.m.EmptyMessage(); ok {
		msg := container.NewVBox(para(e.Title, cInk, "kipple.heading", true), para(e.Body, cMuted, theme.SizeNameText, false))
		listPane = container.NewBorder(pad(msg, space4, 4, space4, space4), nil, nil, nil, listPane)
	}
	detail := container.NewVScroll(pad(u.detail(), space4, space4, space4, space4))
	return container.New(split{0.58}, fill(listPane, cSurface), vline(), fill(detail, cSurface))
}

// reviewList is the one virtualised list holding every row kind; the
// widget is kept across redraws so its scroll position stays.
func (u *ui) reviewList() *widget.List {
	if u.list == nil {
		u.list = widget.NewList(func() int { return len(u.m.Rows()) }, u.newListRow, u.updateListRow)
		u.list.HideSeparators = true
	}
	rows := u.m.Rows()
	for i, r := range rows {
		u.list.SetItemHeight(i, sz(rowHeight(r.Kind)))
	}
	u.listRows = len(rows)
	u.list.Refresh()
	return u.list
}

func rowHeight(k guimodel.RowKind) float32 {
	switch k {
	case guimodel.RowSection:
		return hSection
	case guimodel.RowGroup:
		return hGroup
	case guimodel.RowItem:
		return hItem
	}
	return hBanner
}

// listRow is the list's item template: it shows one row kind at a time.
type listRow struct {
	widget.BaseWidget
	u     *ui
	index int
	box   *rowBox
	stack *fyne.Container

	// item
	itemRow  fyne.CanvasObject
	tile     *tile
	path     *pathText
	itemSide *fyne.Container
	// group
	groupRow fyne.CanvasObject
	gTitleBt *tapText
	gSide    *fyne.Container
	gTile    *tile
	// section and banners
	other *fyne.Container
}

func (u *ui) newListRow() fyne.CanvasObject {
	r := &listRow{u: u}
	r.tile = newTile(tileOff, func() { u.m.ToggleRow(r.index); u.update() })
	r.path = newPathText("", cInk, sizeBody)
	r.itemSide = container.NewHBox()
	r.itemRow = container.NewBorder(nil, nil, container.NewHBox(spacer(4, 1), r.tile), r.itemSide, r.path)
	r.gTile = newTile(tileNone, func() { u.m.ToggleGroup(u.m.Rows()[r.index].Group); u.update() })
	r.gTitleBt = newTapText(func() { u.m.FocusGroup(u.m.Rows()[r.index].Group); u.update(); u.scrollToCursor() })
	r.gSide = container.NewHBox()
	r.groupRow = container.NewBorder(nil, nil, spacer(12, 1), r.gSide, r.gTitleBt)
	r.other = container.NewStack()
	r.stack = container.NewStack(r.itemRow, r.groupRow, r.other)
	r.box = newRowBox(r.stack, cSurface, func() {
		if u.m.Rows()[r.index].Kind == guimodel.RowItem {
			u.m.SetCursor(r.index)
			u.update()
		}
	})
	r.ExtendBaseWidget(r)
	return r
}

func (r *listRow) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(r.box) }

// AccessibilityLabel and AccessibilityRole implement fyne.Accessible.
func (r *listRow) AccessibilityLabel() string {
	rows := r.u.m.Rows()
	if r.index < 0 || r.index >= len(rows) {
		return ""
	}
	row := rows[r.index]
	switch row.Kind {
	case guimodel.RowItem:
		return r.u.m.Item(row.Item).Label
	case guimodel.RowGroup:
		g := r.u.m.Group(row.Group)
		return g.Title + ", " + g.Tier + ", " + g.Count
	}
	return ""
}
func (r *listRow) AccessibilityRole() fyne.AccessibleRole { return fyne.AccessibleRoleText }

func (u *ui) updateListRow(id widget.ListItemID, o fyne.CanvasObject) {
	r := o.(*listRow)
	rows := u.m.Rows()
	if id < 0 || id >= len(rows) {
		return
	}
	r.index = id
	row := rows[id]
	r.itemRow.Hide()
	r.groupRow.Hide()
	r.other.Hide()
	r.box.base, r.box.cursor = cSurface, false
	switch row.Kind {
	case guimodel.RowItem:
		v := u.m.Item(row.Item)
		r.tile.set(tileFor(v.Tile), v.Label)
		c := cInk
		if v.Tile == guimodel.TileBlocked || v.Tile == guimodel.TileInfo {
			c = cMuted
		}
		r.path.set(v.Path, c)
		r.itemSide.Objects = nil
		if v.Chip != "" {
			r.itemSide.Add(chip(v.Chip))
		}
		if v.AtRisk {
			r.itemSide.Add(container.NewCenter(riskMark()))
		}
		size := text(v.Size, cMuted, sizeBody, false)
		size.Alignment = fyne.TextAlignTrailing
		r.itemSide.Add(sized(size, 72, 0))
		r.itemSide.Add(spacer(8, 1))
		r.itemSide.Refresh()
		r.box.cursor = id == u.m.Cursor()
		r.itemRow.Show()
	case guimodel.RowGroup:
		g := u.m.Group(row.Group)
		r.box.base = cHeader
		r.gTitleBt.set(g.Title)
		r.gSide.Objects = []fyne.CanvasObject{container.NewCenter(pill(g.Tier))}
		if g.Tile != selection.NoTile {
			r.gTile.set(groupTile(g.Tile), g.Title+", all items")
			r.gSide.Add(r.gTile)
		} else {
			r.gSide.Add(spacer(hitSz, 1))
		}
		count := text(g.Count, cMuted, sizeBody, false)
		count.Alignment = fyne.TextAlignTrailing
		r.gSide.Add(sized(count, 120, 0))
		r.gSide.Add(spacer(8, 1))
		r.gSide.Refresh()
		r.groupRow.Show()
	case guimodel.RowSection:
		s := guimodel.Section(row.Tier)
		r.other.Objects = []fyne.CanvasObject{pad(container.NewHBox(
			container.NewCenter(text(s.Label, cMuted, sizeCaption, true)), spacer(4, 1),
			container.NewCenter(text(s.Line, cMuted, sizeBody, false))), 6, 0, space3, 0)}
		r.other.Show()
	case guimodel.RowBanner:
		label := "Details ›"
		if u.m.WarningsOpen() {
			label = "Hide details"
		}
		r.other.Objects = []fyne.CanvasObject{container.NewBorder(nil, nil,
			container.NewHBox(spacer(8, 1), widget.NewIcon(theme.WarningIcon()), container.NewCenter(text(u.m.BannerText(), cInk, sizeBody, false))),
			linkButton(label, func() { u.m.ToggleWarnings(); u.update() }))}
		r.other.Show()
	case guimodel.RowWarning:
		r.other.Objects = []fyne.CanvasObject{pad(newPathText(u.m.WarningText(row.Item), cMuted, sizeCaption), 0, 0, space6, space3)}
		r.other.Show()
	case guimodel.RowGitBanner:
		r.other.Objects = []fyne.CanvasObject{container.NewBorder(nil, nil, container.NewHBox(spacer(8, 1), widget.NewIcon(theme.InfoIcon())), nil,
			newPathText(guimodel.GitBannerText, cInk, sizeCaption))}
		r.other.Show()
	}
	r.box.Refresh()
}

func tileFor(t guimodel.Tile) tileState {
	switch t {
	case guimodel.TileSelected:
		return tileOn
	case guimodel.TileBlocked:
		return tileBlocked
	case guimodel.TileInfo:
		return tileDot
	}
	return tileOff
}

func groupTile(s selection.GroupState) tileState {
	switch s {
	case selection.All:
		return tileOn
	case selection.Partial:
		return tilePartial
	case selection.None:
		return tileOff
	}
	return tileNone
}

// tapText is a bold rule title that moves the cursor to the rule's first
// item when clicked.
type tapText struct {
	widget.BaseWidget
	label *widget.Label
	onTap func()
}

func newTapText(onTap func()) *tapText {
	t := &tapText{onTap: onTap, label: widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})}
	t.label.SizeName = theme.SizeNameSubHeadingText
	t.label.Truncation = fyne.TextTruncateEllipsis
	t.ExtendBaseWidget(t)
	return t
}

func (t *tapText) set(s string)            { t.label.SetText(s) }
func (t *tapText) Tapped(*fyne.PointEvent) { t.onTap() }
func (t *tapText) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewVBox(layout.NewSpacer(), t.label, layout.NewSpacer()))
}

// ---- detail pane ----

func (u *ui) detail() fyne.CanvasObject {
	d, ok := u.m.Detail()
	if !ok {
		return layout.NewSpacer()
	}
	v := container.NewVBox()
	size := line(d.Size, cInk, "kipple.subheading", true)
	v.Add(container.NewBorder(nil, nil, container.NewHBox(line(d.Title, cInk, "kipple.subheading", true), container.NewCenter(pill(d.Tier))), size))
	v.Add(para(d.Path, cMuted, theme.SizeNameText, false))
	actions := container.NewHBox(linkButton("Copy path", u.copyPath))
	actions.Add(linkButton("Show in Finder", func() { u.open("-R", d.FullPath) }))
	v.Add(actions)
	v.Add(spacer(1, 8))

	status := container.NewVBox()
	switch d.Status {
	case guimodel.StatusSelected:
		status.Add(container.NewBorder(nil, nil, sized(newTile(tileOn, nil), 20, 20), nil, para(d.StatusText, cAmberInk, theme.SizeNameText, false)))
	default:
		status.Add(para(d.StatusText, cMuted, theme.SizeNameText, false))
		for _, b := range d.Blockers {
			status.Add(para(b, cInk, theme.SizeNameText, false))
		}
	}
	v.Add(field("Status", status))
	if d.Instead != "" {
		v.Add(field("Instead", withButton(monoLine(d.Instead, cInk), linkButton("Copy", func() { u.copyText(d.Instead) }))))
	}
	if d.AtRisk {
		v.Add(field("At risk", container.NewBorder(nil, nil, container.NewVBox(spacer(1, 3), riskMark()), nil,
			para("Removing this may lose work; read If removed.", cAmberInk, theme.SizeNameText, false))))
	}
	v.Add(field("What", para(d.What, cInk, theme.SizeNameText, false)))
	v.Add(field("If removed", para(d.IfRem, cInk, theme.SizeNameText, false)))
	if d.Git != "" {
		g := container.NewVBox(para(d.Git, cInk, theme.SizeNameText, false))
		if d.GitNote != "" {
			g.Add(para(d.GitNote, cMuted, theme.SizeNameText, false))
		}
		v.Add(field("Git", g))
	}
	if len(d.Protected) > 0 {
		p := container.NewVBox()
		for _, x := range d.Protected {
			p.Add(monoLine(x, cMuted))
		}
		v.Add(field("Protected", p))
	}
	v.Add(field("Contents", para(d.Contents, cMuted, theme.SizeNameText, false)))
	for _, w := range d.Warnings {
		v.Add(field("Warning", para(w, cInk, theme.SizeNameText, false)))
	}
	v.Add(field("ID", withButton(monoLine(d.ID, cMuted), linkButton("Copy", u.copyID))))
	return v
}

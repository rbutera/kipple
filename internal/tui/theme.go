package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Brand palette (docs/brand.md). Each colour adapts to the terminal's
// background: spruce ink and slate secondary text on light grounds, lichen
// ink and fog secondary text on dark ones. Amber has one meaning, "this one
// does not belong": items selected for the Trash, the selected share of the
// header bar, and at-risk markers. It is never decoration, never the
// cursor, a spinner, a warning or an error, and never a success colour.
var (
	colInk    = lipgloss.AdaptiveColor{Light: "#10221E", Dark: "#E3EBE4"} // spruce / lichen
	colMuted  = lipgloss.AdaptiveColor{Light: "#4A5C57", Dark: "#9DB2AA"} // slate / fog
	colAmber  = lipgloss.AdaptiveColor{Light: "#C26A0C", Dark: "#F2A43A"} // amber-deep / amber
	colPill   = lipgloss.AdaptiveColor{Light: "#10221E", Dark: "#E3EBE4"} // spruce / lichen
	colOnPill = lipgloss.AdaptiveColor{Light: "#F3F4EF", Dark: "#10221E"} // paper / spruce
	colCursor = lipgloss.AdaptiveColor{Light: "#E3EBE4", Dark: "#1C3A33"} // lichen / spruce-hi
	colRule   = lipgloss.AdaptiveColor{Light: "#9DB2AA", Dark: "#4A5C57"} // fog / slate
	// Tier pills: safe is a soft lichen chip, review the inverse (paper on
	// spruce, lichen on dark grounds), manual an outline in ink, info dim.
	colSafeBg = lipgloss.AdaptiveColor{Light: "#E3EBE4", Dark: "#1C3A33"} // lichen / spruce-hi
)

// theme holds every style the view uses, so the colour and NO_COLOR
// variants differ in one place.
type theme struct {
	color bool

	pill      lipgloss.Style // the "kipple" name in the header
	title     lipgloss.Style // headings and rule titles
	text      lipgloss.Style
	muted     lipgloss.Style
	amber     lipgloss.Style // selected items and at-risk markers only
	key       lipgloss.Style // a key in the footer
	cursor    lipgloss.Style // the highlighted row
	pointer   lipgloss.Style // the cursor's bar
	pane      lipgloss.Style // a bordered pane (its top edge is drawn by paneTop)
	border    lipgloss.Style // border lines drawn by hand
	paneTitle lipgloss.Style // the title set in a pane's top border
	modal     lipgloss.Style // the confirmation box
	label     lipgloss.Style // labels in the detail pane
	badge     map[string]lipgloss.Style
	hintWarn  lipgloss.Style // refusals and warnings: emphasis, not amber
	spinner   lipgloss.Style

	// Row markers and the header bar.
	markOn, markOff, markInfo, markBlocked, markRisk, cursorMark string
	barOn, barOff                                                string
}

// newTheme returns the brand theme, or with color false the NO_COLOR
// theme: no colours at all, structure carried by text and borders.
func newTheme(r *lipgloss.Renderer, color bool) theme {
	s := r.NewStyle
	t := theme{
		color:       color,
		pill:        s().Bold(true).Padding(0, 1),
		title:       s().Bold(true),
		text:        s(),
		muted:       s(),
		amber:       s().Bold(true),
		key:         s().Bold(true),
		cursor:      s().Bold(true),
		pointer:     s().Bold(true),
		pane:        s().Border(lipgloss.RoundedBorder()).BorderTop(false).Padding(0, 1),
		border:      s(),
		paneTitle:   s().Bold(true),
		modal:       s().Border(lipgloss.RoundedBorder()).Padding(1, 3),
		label:       s(),
		hintWarn:    s().Bold(true),
		spinner:     s(),
		markOn:      "[x]",
		markOff:     "[ ]",
		markInfo:    " - ",
		markBlocked: "[ ]",
		markRisk:    "!",
		cursorMark:  ">",
		barOn:       "=",
		barOff:      "-",
		badge: map[string]lipgloss.Style{
			"safe":   s(),
			"review": s(),
			"manual": s(),
			"info":   s(),
		},
	}
	if !color {
		return t
	}
	t.pill = t.pill.Foreground(colOnPill).Background(colPill)
	t.title = t.title.Foreground(colInk)
	t.text = t.text.Foreground(colInk)
	t.muted = t.muted.Foreground(colMuted)
	t.amber = t.amber.Foreground(colAmber)
	t.key = t.key.Foreground(colInk)
	t.cursor = t.cursor.Foreground(colInk).Background(colCursor)
	t.pointer = t.pointer.Foreground(colInk)
	t.pane = t.pane.BorderForeground(colRule)
	t.border = t.border.Foreground(colRule)
	t.paneTitle = t.paneTitle.Foreground(colMuted)
	t.modal = t.modal.BorderForeground(colInk)
	t.label = t.label.Foreground(colMuted)
	t.hintWarn = t.hintWarn.Foreground(colInk)
	t.spinner = t.spinner.Foreground(colMuted)
	t.markOn, t.markOff, t.markInfo, t.markBlocked, t.markRisk, t.cursorMark = "●", "○", "·", "○", "▲", "▌"
	t.barOn, t.barOff = "━", "─"
	t.badge = map[string]lipgloss.Style{
		"safe":   s().Foreground(colInk).Background(colSafeBg).Padding(0, 1),
		"review": s().Foreground(colOnPill).Background(colPill).Padding(0, 1),
		"manual": s().Foreground(colInk),
		"info":   s().Foreground(colMuted).Faint(true),
	}
	return t
}

// tierBadge renders a tier name as a small pill (brand theme) or in
// parentheses (NO_COLOR).
func (t theme) tierBadge(tier string) string {
	st, ok := t.badge[tier]
	if !ok {
		st = t.muted
	}
	if !t.color {
		return st.Render("(" + tier + ")")
	}
	switch tier {
	case "manual":
		// An outline: the name between thin brackets, no fill.
		return t.border.Render("⟨") + st.Render(tier) + t.border.Render("⟩")
	case "info":
		return st.Render(" " + tier + " ")
	}
	return st.Render(tier)
}

// bar draws the selected share of the found bytes as a thin bar of width w.
func (t theme) bar(selected, found int64, w int) string {
	on := 0
	if found > 0 && selected > 0 {
		on = int((selected*int64(w) + found - 1) / found)
		if on > w {
			on = w
		}
	}
	if !t.color {
		return "[" + strings.Repeat(t.barOn, on) + strings.Repeat(t.barOff, w-on) + "]"
	}
	return t.amber.UnsetBold().Render(strings.Repeat(t.barOn, on)) + t.border.Render(strings.Repeat(t.barOff, w-on))
}

// paneTop draws a pane's top border of outer width w with title set into
// it: ╭─ Items ────╮.
func (t theme) paneTop(title string, w int) string {
	fill := w - 5 - lipgloss.Width(title)
	if fill < 0 {
		return t.border.Render("╭" + strings.Repeat("─", max(w-2, 0)) + "╮")
	}
	return t.border.Render("╭─ ") + t.paneTitle.Render(title) + t.border.Render(" "+strings.Repeat("─", fill)+"╮")
}

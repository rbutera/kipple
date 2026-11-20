package tui

import "github.com/charmbracelet/lipgloss"

// Brand palette (docs/brand.md). Each colour adapts to the terminal's
// background: spruce ink and slate secondary text on light grounds, lichen
// ink and fog secondary text on dark ones. Amber has one meaning, "this one
// does not belong": items selected for the Trash and at-risk markers. It is
// never decoration and never a success colour.
var (
	colInk    = lipgloss.AdaptiveColor{Light: "#10221E", Dark: "#E3EBE4"} // spruce / lichen
	colMuted  = lipgloss.AdaptiveColor{Light: "#4A5C57", Dark: "#9DB2AA"} // slate / fog
	colAmber  = lipgloss.AdaptiveColor{Light: "#C26A0C", Dark: "#F2A43A"} // amber-deep / amber
	colPill   = lipgloss.AdaptiveColor{Light: "#10221E", Dark: "#E3EBE4"}
	colOnPill = lipgloss.AdaptiveColor{Light: "#F3F4EF", Dark: "#10221E"} // paper / spruce
	colCursor = lipgloss.AdaptiveColor{Light: "#E3EBE4", Dark: "#1C3A33"} // lichen / spruce-hi
	colRule   = lipgloss.AdaptiveColor{Light: "#9DB2AA", Dark: "#4A5C57"}
)

// theme holds every style the view uses, so the colour and NO_COLOR
// variants differ in one place.
type theme struct {
	color bool

	pill     lipgloss.Style // the "kipple" name in the header
	title    lipgloss.Style // headings and rule titles
	text     lipgloss.Style
	muted    lipgloss.Style
	amber    lipgloss.Style
	key      lipgloss.Style // a key in the footer
	cursor   lipgloss.Style // the highlighted row
	pane     lipgloss.Style // a bordered pane
	modal    lipgloss.Style // the confirmation box
	label    lipgloss.Style // labels in the detail pane
	badge    map[string]lipgloss.Style
	hintWarn lipgloss.Style

	// Row markers.
	markOn, markOff, markInfo, markBlocked, markRisk, pointer string
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
		pane:        s().Border(lipgloss.RoundedBorder()).Padding(0, 1),
		modal:       s().Border(lipgloss.RoundedBorder()).Padding(1, 3),
		label:       s(),
		hintWarn:    s().Bold(true),
		markOn:      "[x]",
		markOff:     "[ ]",
		markInfo:    " - ",
		markBlocked: "[ ]",
		markRisk:    "!",
		pointer:     ">",
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
	t.pane = t.pane.BorderForeground(colRule)
	t.modal = t.modal.BorderForeground(colAmber)
	t.label = t.label.Foreground(colMuted)
	t.hintWarn = t.hintWarn.Foreground(colAmber)
	t.markOn, t.markOff, t.markInfo, t.markBlocked, t.markRisk, t.pointer = "●", "○", "·", "○", "▲", "▌"
	t.badge = map[string]lipgloss.Style{
		"safe":   s().Foreground(colInk),
		"review": s().Foreground(colInk).Italic(true),
		"manual": s().Foreground(colMuted).Italic(true),
		"info":   s().Foreground(colMuted).Faint(true),
	}
	return t
}

// tierBadge renders a tier name as a small label.
func (t theme) tierBadge(tier string) string {
	st, ok := t.badge[tier]
	if !ok {
		st = t.muted
	}
	if t.color {
		return st.Render(tier)
	}
	return st.Render("(" + tier + ")")
}

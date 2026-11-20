package guimodel

import (
	"sort"
	"strings"
	"time"

	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/selection"
	"github.com/rbutera/kipple/internal/words"
)

// RowKind is the kind of a review-list row.
type RowKind int

const (
	// RowBanner: "N places could not be read completely." with Details.
	RowBanner RowKind = iota
	// RowWarning: one plan warning, shown when the banner is expanded.
	RowWarning
	// RowGitBanner: git isn't available (no Command Line Tools).
	RowGitBanner
	// RowSection: a tier heading.
	RowSection
	// RowGroup: a rule.
	RowGroup
	// RowItem: an item.
	RowItem
)

// Row is one line of the review list.
type Row struct {
	Kind  RowKind
	Tier  string
	Group int // index into the groups (RowGroup, RowItem)
	Item  int // index into the plan's items (RowItem); warning index (RowWarning)
}

// group is one rule's items.
type group struct {
	id, title, tier string
	items           []int
	ids             []string
	bytes           int64
}

var tierRank = map[string]int{"safe": 0, "review": 1, "manual": 2, "info": 3}

// load builds the grouped rows from p and pre-selects what the plan
// selects (never an info item). The order is the TUI's: tier, then rule
// bytes descending, then item bytes descending.
func (m *Model) load(p plan.Plan, started time.Time) {
	m.plan, m.started = p, started
	m.sel = selection.New(p)
	m.pre = selection.Preselected(p)
	m.groups, m.rows, m.cursor, m.warnOpen, m.hint = nil, nil, -1, false, ""
	idx := map[string]int{}
	for i, it := range p.Items {
		gi, ok := idx[it.Rule]
		if !ok {
			gi = len(m.groups)
			idx[it.Rule] = gi
			m.groups = append(m.groups, group{id: it.Rule, title: m.ruleTitle(it.Rule), tier: it.Tier})
		}
		g := &m.groups[gi]
		g.items = append(g.items, i)
		g.ids = append(g.ids, it.ID)
		g.bytes += it.AllocatedBytes
	}
	sort.SliceStable(m.groups, func(a, b int) bool {
		ga, gb := m.groups[a], m.groups[b]
		if tierRank[ga.tier] != tierRank[gb.tier] {
			return tierRank[ga.tier] < tierRank[gb.tier]
		}
		return ga.bytes > gb.bytes
	})
	for gi := range m.groups {
		g := &m.groups[gi]
		sort.SliceStable(g.items, func(a, b int) bool {
			return p.Items[g.items[a]].AllocatedBytes > p.Items[g.items[b]].AllocatedBytes
		})
	}
	m.buildRows()
	for i, r := range m.rows {
		if r.Kind == RowItem {
			m.cursor = i
			break
		}
	}
	if len(m.pre) == 0 && m.hasSelectable() {
		m.hint = "Nothing is pre-selected. Review and manual items are yours to choose."
	}
}

func (m *Model) hasSelectable() bool {
	for _, it := range m.plan.Items {
		if it.Tier != string(rules.TierInfo) {
			return true
		}
	}
	return false
}

// buildRows lays out the rows; the cursor stays on the same item.
func (m *Model) buildRows() {
	cur := -1
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		cur = m.rows[m.cursor].Item
	}
	m.rows = nil
	if len(m.plan.Warnings) > 0 {
		m.rows = append(m.rows, Row{Kind: RowBanner})
		if m.warnOpen {
			for i := range m.plan.Warnings {
				m.rows = append(m.rows, Row{Kind: RowWarning, Item: i})
			}
		}
	}
	if m.GitBanner() {
		m.rows = append(m.rows, Row{Kind: RowGitBanner})
	}
	tier := ""
	for gi, g := range m.groups {
		if g.tier != tier {
			tier = g.tier
			m.rows = append(m.rows, Row{Kind: RowSection, Tier: tier})
		}
		m.rows = append(m.rows, Row{Kind: RowGroup, Tier: tier, Group: gi})
		for _, i := range g.items {
			m.rows = append(m.rows, Row{Kind: RowItem, Tier: tier, Group: gi, Item: i})
		}
	}
	m.cursor = -1
	for i, r := range m.rows {
		if r.Kind == RowItem && (cur < 0 || r.Item == cur) {
			m.cursor = i
			if cur >= 0 {
				break
			}
			cur = r.Item
		}
	}
}

// Rows returns the review list.
func (m *Model) Rows() []Row { return m.rows }

// Cursor returns the cursor row's index, or -1 when there are no items.
func (m *Model) Cursor() int { return m.cursor }

// GitBanner reports whether the "git isn't available" row shows: Apple's
// Command Line Tools are missing and some project item could not be
// checked for that reason.
func (m *Model) GitBanner() bool {
	if m.cfg.GitAvailable {
		return false
	}
	for _, it := range m.plan.Items {
		for _, r := range it.Reasons {
			if r == plan.ReasonGitUnavailable {
				return true
			}
		}
	}
	return false
}

// GitBannerText is the git banner's copy.
const GitBannerText = "git isn't available, so kipple can't check project folders. They stay unselectable. Install Apple's Command Line Tools (xcode-select --install) and scan again."

// BannerText is the warning banner's copy.
func (m *Model) BannerText() string {
	return words.Plural(len(m.plan.Warnings), "place") + " could not be read completely."
}

// WarningText returns plan warning i, escaped.
func (m *Model) WarningText(i int) string {
	if i < 0 || i >= len(m.plan.Warnings) {
		return ""
	}
	return output.SafeText(m.plan.Warnings[i])
}

// WarningsOpen reports whether the banner is expanded.
func (m *Model) WarningsOpen() bool { return m.warnOpen }

// ToggleWarnings expands or collapses the warning banner.
func (m *Model) ToggleWarnings() {
	m.warnOpen = !m.warnOpen
	m.buildRows()
}

// SectionView is a tier heading.
type SectionView struct {
	Label string // "SAFE"
	Line  string // "Rebuilds itself. Selected for you."
}

// Section describes a tier heading.
func Section(tier string) SectionView {
	return SectionView{Label: strings.ToUpper(tier), Line: words.TierLine(tier)}
}

// GroupView is a rule row.
type GroupView struct {
	Title string
	Tier  string
	Tile  selection.GroupState
	Count string // "14 items · 9.8 GB"
}

// Group describes group gi.
func (m *Model) Group(gi int) GroupView {
	g := m.groups[gi]
	return GroupView{
		Title: g.title,
		Tier:  g.tier,
		Tile:  m.sel.Group(g.tier, g.ids),
		Count: words.Items(len(g.items)) + " · " + output.Bytes(g.bytes),
	}
}

// Tile is an item's box.
type Tile int

const (
	// TileUnselected: may be selected.
	TileUnselected Tile = iota
	// TileSelected: goes to the Trash on apply.
	TileSelected
	// TileBlocked: something stops it; the chip says what.
	TileBlocked
	// TileInfo: measured only, never movable (a dot, no box).
	TileInfo
)

// ItemView is an item row.
type ItemView struct {
	ID     string
	Tile   Tile
	Path   string // home-relative, escaped; the view truncates it from the left
	Chip   string // short reason for a blocked item
	AtRisk bool
	Size   string
	Tier   string
	// Label is what a screen reader says for the row.
	Label string
}

// Item describes plan item i.
func (m *Model) Item(i int) ItemView {
	it := m.plan.Items[i]
	v := ItemView{ID: it.ID, Path: m.Rel(it.Path), AtRisk: it.AtRisk, Size: output.Bytes(it.AllocatedBytes), Tier: it.Tier}
	state := ""
	switch {
	case it.Tier == string(rules.TierInfo):
		v.Tile, state = TileInfo, "measured only"
	case m.sel.Selected(it.ID):
		v.Tile, state = TileSelected, "selected"
	case m.sel.Selectable(it.ID):
		v.Tile, state = TileUnselected, "not selected"
	default:
		v.Tile = TileBlocked
		r := words.FirstBlocker(it.Reasons)
		v.Chip = words.Chip(r)
		state = "can't be selected: " + words.Reason(r)
	}
	v.Label = m.ruleTitle(it.Rule) + ", " + v.Path + ", " + v.Size + ", " + it.Tier + ", " + state
	if it.AtRisk {
		v.Label += ", at risk"
	}
	return v
}

// ---- Cursor and selection ----

// MoveCursor moves the cursor by d items (headings are skipped).
func (m *Model) MoveCursor(d int) {
	if m.cursor < 0 {
		return
	}
	step := 1
	if d < 0 {
		step, d = -1, -d
	}
	pos := m.cursor
	for moved := 0; moved < d; moved++ {
		next := pos + step
		for next >= 0 && next < len(m.rows) && m.rows[next].Kind != RowItem {
			next += step
		}
		if next < 0 || next >= len(m.rows) {
			break
		}
		pos = next
	}
	m.cursor = pos
	m.clearHint()
}

// CursorHome and CursorEnd move to the first and last item.
func (m *Model) CursorHome() { m.MoveCursor(-len(m.rows)) }

// CursorEnd moves to the last item.
func (m *Model) CursorEnd() { m.MoveCursor(len(m.rows)) }

// JumpGroup moves to the first item of the next (d=1) or previous rule.
func (m *Model) JumpGroup(d int) {
	if m.cursor < 0 {
		return
	}
	g := m.rows[m.cursor].Group + d
	if g < 0 || g >= len(m.groups) {
		return
	}
	m.FocusGroup(g)
}

// FocusGroup moves the cursor to group gi's first item.
func (m *Model) FocusGroup(gi int) {
	for i, r := range m.rows {
		if r.Kind == RowItem && r.Group == gi {
			m.cursor = i
			m.clearHint()
			return
		}
	}
}

// SetCursor moves the cursor to row i if it is an item.
func (m *Model) SetCursor(i int) {
	if i >= 0 && i < len(m.rows) && m.rows[i].Kind == RowItem {
		m.cursor = i
		m.clearHint()
	}
}

func (m *Model) clearHint() {
	m.hint = ""
	m.rescanWarn = false
}

// current returns the cursor item.
func (m *Model) current() (plan.Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.rows[m.cursor].Kind != RowItem {
		return plan.Item{}, false
	}
	return m.plan.Items[m.rows[m.cursor].Item], true
}

// Toggle flips the cursor item, under kipple clean --only's rules.
func (m *Model) Toggle() {
	if m.cursor >= 0 {
		m.ToggleRow(m.cursor)
	}
}

// ToggleRow moves the cursor to row i and flips its item. Info items are
// never selected, and anything other than its tier that blocks an item
// keeps it unselectable; the status says why.
func (m *Model) ToggleRow(i int) {
	if m.screen != ScreenReview || m.sheet != SheetNone || i < 0 || i >= len(m.rows) || m.rows[i].Kind != RowItem {
		return
	}
	m.cursor = i
	m.rescanWarn = false
	it := m.plan.Items[m.rows[i].Item]
	switch m.sel.Toggle(it.ID) {
	case selection.RefusedInfo:
		if cmd := m.ownerCommand(it); cmd != "" {
			m.hint = "Info items can't be selected. Instead, run: " + cmd
		} else {
			m.hint = "Info items can't be selected; kipple never moves them."
		}
	case selection.RefusedBlocked:
		m.hint = "Can't select it: " + words.Reason(words.FirstBlocker(it.Reasons)) + "."
	case selection.Added:
		m.hint = ""
		if it.Tier == string(rules.TierManual) {
			m.hint = "Selected. Manual items are never chosen for you; this one is your call."
		}
	default:
		m.hint = ""
	}
}

// ToggleGroup selects or unselects every selectable item of group gi
// (safe and review groups only).
func (m *Model) ToggleGroup(gi int) {
	if m.screen != ScreenReview || m.sheet != SheetNone || gi < 0 || gi >= len(m.groups) {
		return
	}
	g := m.groups[gi]
	m.rescanWarn = false
	if m.sel.ToggleGroup(g.tier, g.ids) == selection.NoTile {
		if g.tier == string(rules.TierManual) {
			m.hint = "Manual items are chosen one at a time."
		}
		return
	}
	m.hint = ""
}

// ToggleCursorGroup toggles the cursor item's whole group.
func (m *Model) ToggleCursorGroup() {
	if m.cursor >= 0 {
		m.ToggleGroup(m.rows[m.cursor].Group)
	}
}

// Selected returns the selected IDs in plan order.
func (m *Model) Selected() []string { return m.sel.IDs() }

// Preselected returns the IDs the model selected for the person.
func (m *Model) Preselected() []string { return append([]string(nil), m.pre...) }

func (m *Model) ownerCommand(it plan.Item) string {
	cmd := it.OwnerCommand
	if cmd == "" {
		cmd = m.ruleByID[it.Rule].OwnerCommand
	}
	return output.SafeText(cmd)
}

// ---- Header ----

// HeaderView is the header's totals.
type HeaderView struct {
	Found    string // "Found 41 · 38.6 GB"
	Selected string // "Selected 23 · 21.4 GB"
	// Share is the selected fraction of the found bytes, 0 to 1.
	Share float64
	// Any is set when something is selected (amber is used only then).
	Any bool
}

// Header describes the header's totals (review screen only).
func (m *Model) Header() HeaderView {
	var found int64
	for _, it := range m.plan.Items {
		found += it.AllocatedBytes
	}
	n, b := m.sel.Total()
	v := HeaderView{
		Found:    "Found " + itoa(len(m.plan.Items)) + " · " + output.Bytes(found),
		Selected: "Selected " + itoa(n) + " · " + output.Bytes(b),
		Any:      n > 0,
	}
	if found > 0 {
		v.Share = float64(b) / float64(found)
	}
	return v
}

// Empty describes the list pane's empty message.
type Empty struct {
	Title string
	Body  string
}

// EmptyMessage returns the empty-state message, if the scan found nothing
// that can be moved.
func (m *Model) EmptyMessage() (Empty, bool) {
	if m.screen != ScreenReview || m.hasSelectable() {
		return Empty{}, false
	}
	e := Empty{Title: "Nothing to clean.", Body: "kipple looked in " + words.Plural(len(m.places), "place") + " and found nothing it cleans."}
	if len(m.plan.Items) > 0 {
		e.Body += " These are measured only:"
	}
	return e, true
}

// CanMove reports whether Move to Trash… is enabled.
func (m *Model) CanMove() bool {
	return m.screen == ScreenReview && m.sheet == SheetNone && m.sel.Len() > 0
}

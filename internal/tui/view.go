package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
)

const (
	headerLines = 2
	footerLines = 2
	// wideWidth is the width from which the list and detail panes sit side
	// by side; below it the detail pane goes under the list.
	wideWidth = 104
	labelW    = 11
)

// View renders the model.
func (m Model) View() string {
	var body, keys string
	switch m.state {
	case stateScanning:
		body = m.center(m.spin.View() + " " + m.th.text.Render("Scanning "+output.SafeText(m.cfg.Scan.Env.Home)+" …") + "\n\n" + m.th.muted.Render(m.trunc(output.SafeText(m.prog.get()), m.width-4)))
		keys = m.keys("q", "quit")
	case stateReview:
		body = m.reviewBody()
		keys = m.keys("↑↓", "move", "tab", "next rule", "space", "select", "a", "apply", "q", "quit")
	case stateConfirm:
		body = m.center(m.confirmBox())
		keys = m.keys("y", "move to Trash", "any other key", "cancel")
	case stateApplying:
		n, _ := m.selectedTotal()
		line := m.spin.View() + " " + m.th.text.Render(fmt.Sprintf("Moving %s to the Trash …", items(n)))
		if m.stopping {
			line += "  " + m.th.muted.Render("stopping after the current item")
		}
		body = m.center(line + "\n\n" + m.th.muted.Render(m.trunc(output.SafeText(m.prog.get()), m.width-4)))
		keys = m.keys("ctrl+c", "stop after the current item")
	case stateDone:
		body = m.doneBody()
		keys = m.keys("q", "quit")
	case stateError:
		body = m.errorBody()
		keys = m.keys("q", "quit")
	}
	body = fitHeight(body, m.bodyHeight())
	return lipgloss.JoinVertical(lipgloss.Left, m.header(), body, m.footer(keys))
}

func (m Model) bodyHeight() int {
	h := m.height - headerLines - footerLines
	if h < 3 {
		h = 3
	}
	return h
}

// header is the name, version and home, then the totals.
func (m Model) header() string {
	left := m.th.pill.Render("kipple")
	if m.cfg.Version != "" {
		left += " " + m.th.muted.Render(m.cfg.Version)
	}
	right := ""
	if m.plan.Level != "" {
		right = m.th.muted.Render("level ") + m.th.text.Render(m.plan.Level)
	}
	room := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	label := m.cfg.HomeLabel
	if label == "" {
		label = m.cfg.Scan.Env.Home
	}
	home := m.th.muted.Render(m.truncLeft(output.SafeText(label), room))
	line1 := spread(left+"  "+home, right, m.width)

	var line2 string
	if len(m.plan.Items) > 0 || m.state == stateReview || m.state == stateConfirm || m.state == stateDone {
		var listed int64
		for _, it := range m.plan.Items {
			listed += it.AllocatedBytes
		}
		n, b := m.selectedTotal()
		found := m.th.muted.Render(fmt.Sprintf("Found %s · %s", items(len(m.plan.Items)), output.Bytes(listed)))
		sel := m.th.muted.Render("Selected ") + m.th.amber.Render(fmt.Sprintf("%d · %s", n, output.Bytes(b))) +
			" " + m.th.bar(b, listed, barWidth(m.width))
		if m.state == stateDone {
			sel = ""
		}
		line2 = spread(found, sel, m.width)
	}
	return line1 + "\n" + line2
}

// footer is the hint line and the key line.
func (m Model) footer(keys string) string {
	hint := ""
	if m.hint != "" {
		st := m.th.muted
		if m.hintWarn {
			st = m.th.hintWarn
		}
		hint = st.Render(m.trunc(m.hint, m.width))
	}
	return hint + "\n" + keys
}

func (m Model) keys(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, m.th.key.Render(pairs[i])+" "+m.th.muted.Render(pairs[i+1]))
	}
	s := strings.Join(parts, "   ")
	if lipgloss.Width(s) > m.width {
		s = strings.Join(parts, " ")
	}
	return s
}

// layout returns the list and detail pane sizes (outer).
func (m Model) layout() (wide bool, listW, listH, detailW, detailH int) {
	bh := m.bodyHeight()
	if m.width >= wideWidth {
		listW = m.width * 58 / 100
		return true, listW, bh, m.width - listW, bh
	}
	detailH = bh * 45 / 100
	if detailH < 6 {
		detailH = 6
	}
	if detailH > 12 {
		detailH = 12
	}
	listH = bh - detailH
	if listH < 4 {
		listH = 4
	}
	return false, m.width, listH, m.width, detailH
}

// listHeight is the number of list rows visible.
func (m Model) listHeight() int {
	_, _, h, _, _ := m.layout()
	return h - 2
}

func (m Model) reviewBody() string {
	if len(m.rows) == 0 {
		msg := m.th.title.Render("Nothing to clean.") + "\n\n" +
			m.th.muted.Render("kipple found no caches or developer junk it knows about under this home folder.")
		return m.center(msg)
	}
	wide, lw, lh, dw, dh := m.layout()
	list := m.pane("Items", lw, lh, m.listView(lw-4, lh-2))
	detail := m.pane("Detail", dw, dh, m.detailView(dw-4, dh-2))
	if wide {
		return lipgloss.JoinHorizontal(lipgloss.Top, list, detail)
	}
	return lipgloss.JoinVertical(lipgloss.Left, list, detail)
}

// pane draws content in a bordered box of outer size w×h with title set in
// its top border.
func (m Model) pane(title string, w, h int, content string) string {
	return m.th.paneTop(title, w) + "\n" + m.th.pane.Width(w-2).Height(h-2).Render(content)
}

// barWidth is the header bar's width for a terminal w columns wide.
func barWidth(w int) int {
	if w >= 110 {
		return 20
	}
	return 10
}

// listView renders the visible rows at inner width w.
func (m Model) listView(w, h int) string {
	var lines []string
	for i := m.offset; i < len(m.rows) && len(lines) < h; i++ {
		r := m.rows[i]
		if r.header {
			lines = append(lines, m.groupLine(m.groups[r.group], w))
		} else {
			lines = append(lines, m.itemLine(m.plan.Items[r.item], w, i == m.cursor))
		}
	}
	return strings.Join(lines, "\n")
}

// groupLine is a rule's heading: title, tier badge, a faint rule that
// separates the groups, and the rule's item count and size.
func (m Model) groupLine(g group, w int) string {
	right := m.th.muted.Render(fmt.Sprintf("%d · %s", len(g.items), output.Bytes(g.bytes)))
	badge := m.th.tierBadge(g.tier)
	room := w - lipgloss.Width(right) - lipgloss.Width(badge) - 5
	left := m.th.title.Render(m.trunc(output.SafeText(g.title), room)) + " " + badge
	fill := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if fill < 1 {
		return spread(left, right, w)
	}
	return left + " " + m.th.border.Faint(m.th.color).Render(strings.Repeat("─", fill)) + " " + right
}

// itemLine renders one item: pointer, mark, path, size, at-risk marker.
func (m Model) itemLine(it plan.Item, w int, cur bool) string {
	th := m.th
	style := func(s lipgloss.Style) lipgloss.Style {
		if cur && th.color {
			return s.Background(colCursor)
		}
		if cur {
			return s.Bold(true)
		}
		return s
	}
	pointer := " "
	if cur {
		pointer = th.cursorMark
	}
	selectable := it.Tier != string(rules.TierInfo) && len(blockers(it.Reasons)) == 0
	mark, markSt, pathSt := th.markOff, th.muted, th.text
	switch {
	case m.sel.Selected(it.ID):
		mark, markSt = th.markOn, th.amber
	case it.Tier == string(rules.TierInfo):
		mark, pathSt = th.markInfo, th.muted
	case !selectable:
		mark, pathSt = th.markBlocked, th.muted
	}
	risk := " "
	if it.AtRisk {
		risk = th.markRisk
	}
	size := fmt.Sprintf("%9s", output.Bytes(it.AllocatedBytes))
	fixed := 2 + lipgloss.Width(mark) + 1 + 1 + len(size) + 2
	path := m.truncLeft(m.rel(it.Path), w-fixed)
	gap := w - fixed - lipgloss.Width(path)
	if gap < 0 {
		gap = 0
	}
	sp := style(th.text)
	return style(th.pointer).Render(pointer) + sp.Render(" ") +
		style(markSt).Render(mark) + sp.Render(" ") +
		style(pathSt).Render(path) + sp.Render(strings.Repeat(" ", gap+1)) +
		style(th.muted).Render(size) + sp.Render(" ") + style(th.amber).Render(risk)
}

// detailView explains the highlighted item at inner width w, height h.
func (m Model) detailView(w, h int) string {
	it, ok := m.current()
	if !ok {
		return ""
	}
	th := m.th
	r := m.ruleByID[it.Rule]
	title := r.Title
	if title == "" {
		title = it.Rule
	}
	var lines []string
	head := th.title.Render(m.trunc(output.SafeText(title), w-24)) + " " + th.tierBadge(it.Tier)
	lines = append(lines, spread(head, th.text.Render(output.Bytes(it.AllocatedBytes)), w))
	lines = append(lines, wrapLines(th.muted, m.rel(it.Path), w)...)
	field := func(label, value string, st lipgloss.Style) {
		vw := w - labelW
		if vw < 10 {
			vw = 10
		}
		vals := wrapLines(st, value, vw)
		for i, v := range vals {
			l := strings.Repeat(" ", labelW)
			if i == 0 {
				l = th.label.Render(fmt.Sprintf("%-*s", labelW, label))
			}
			lines = append(lines, l+v)
		}
	}
	switch {
	case it.Tier == string(rules.TierInfo):
		cmd := it.OwnerCommand
		if cmd == "" {
			cmd = r.OwnerCommand
		}
		field("Status", "Measured only; kipple never moves it.", th.muted)
		if cmd != "" {
			field("Instead", output.SafeText(cmd), th.text)
		}
	case m.sel.Selected(it.ID):
		field("Status", "Selected: goes to the Trash when you apply.", th.amber)
	default:
		why := blockers(it.Reasons)
		if len(why) == 0 {
			msg := "Not selected. Press space to select it."
			if it.Tier == string(rules.TierManual) {
				msg = "Not selected: manual items are never chosen for you."
			}
			field("Status", msg, th.muted)
		} else {
			field("Status", "Can't be selected: "+strings.Join(why, "; ")+".", th.muted)
		}
	}
	if it.AtRisk {
		field("At risk", "Removing this may lose work; read \"If removed\".", th.amber)
	}
	field("What", r.What, th.text)
	field("If removed", r.IfRemoved, th.text)
	if it.Git != nil {
		g := output.SafeText(it.Git.State)
		if it.Git.Detail != "" {
			g += ": " + output.SafeText(it.Git.Detail)
		}
		field("Git", g, th.text)
	}
	files := plural(int(it.FileCount), "file")
	if !it.NewestMtime.IsZero() {
		files += ", newest " + it.NewestMtime.Format("2 Jan 2006")
	}
	field("Contents", files, th.muted)
	field("ID", output.SafeText(it.ID), th.muted)
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func (m Model) confirmBox() string {
	n, b := m.selectedTotal()
	rulesN := map[string]bool{}
	for _, it := range m.plan.Items {
		if m.sel.Selected(it.ID) {
			rulesN[it.Rule] = true
		}
	}
	w := m.width - 12
	if w > 60 {
		w = 60
	}
	th := m.th
	q := th.title.Render("Move ") + th.amber.Render(items(n)) + th.title.Render(" to the Trash?")
	body := []string{
		q,
		"",
		th.text.Render(output.Bytes(b) + " on disk, from " + plural(len(rulesN), "rule") + "."),
		"",
	}
	body = append(body, wrapLines(th.muted, "Nothing is deleted. Each item is checked again just before it moves, goes to your Trash, and is listed in a receipt; kipple restore puts it back.", w)...)
	body = append(body, "", th.key.Render("y")+" "+th.text.Render("move to Trash")+"     "+th.key.Render("any other key")+" "+th.muted.Render("cancel"))
	return th.modal.Render(strings.Join(body, "\n"))
}

func (m Model) doneBody() string {
	th := m.th
	r := m.outcome.Result
	w := m.width - 4
	if w > 76 {
		w = 76 // the receipt card's width; the moved list lines up with it
	}
	var bytes int64
	for _, mv := range r.Moved {
		bytes += mv.Bytes
	}
	var out []string
	switch {
	case len(r.Moved) == 0 && len(r.Skipped) == 0:
		out = append(out, th.title.Render("Nothing was moved."))
	default:
		out = append(out, th.title.Render(fmt.Sprintf("Moved %s to the Trash", items(len(r.Moved))))+th.muted.Render(" · "+output.Bytes(bytes)))
	}
	out = append(out, "")
	show := len(r.Moved)
	if limit := m.bodyHeight() - 12; show > limit {
		if limit < 3 {
			limit = 3
		}
		if show > limit {
			show = limit
		}
	}
	for _, mv := range r.Moved[:show] {
		size := fmt.Sprintf("%9s", output.Bytes(mv.Bytes))
		p := m.truncLeft(m.rel(mv.Path), w-14)
		pad := w - 14 - lipgloss.Width(p)
		if pad < 0 {
			pad = 0
		}
		out = append(out, "  "+th.text.Render(p)+strings.Repeat(" ", pad)+"  "+th.muted.Render(size))
	}
	if extra := len(r.Moved) - show; extra > 0 {
		out = append(out, th.muted.Render(fmt.Sprintf("  and %d more", extra)))
	}
	if len(r.Skipped) > 0 {
		out = append(out, "", th.title.Render(fmt.Sprintf("Skipped %d", len(r.Skipped))))
		for i, s := range r.Skipped {
			if i == 5 {
				out = append(out, th.muted.Render(fmt.Sprintf("  and %d more", len(r.Skipped)-5)))
				break
			}
			line := m.rel(s.Path) + "  " + output.SafeText(s.Reason)
			if s.Detail != "" {
				line += ": " + output.SafeText(s.Detail)
			}
			out = append(out, "  "+th.muted.Render(m.trunc(line, w-2)))
		}
	}
	if len(r.Displaced) > 0 {
		out = append(out, "", th.title.Render(fmt.Sprintf("Displaced %d", len(r.Displaced))))
		out = append(out, wrapLines(th.muted, "Another object was at an item's name when the system's Trash call ran and went to the Trash instead. kipple restore puts it back.", w)...)
	}
	if card := m.receiptCard(r, bytes, w); card != "" {
		out = append(out, "", card)
	}
	for _, warn := range r.Warnings {
		out = append(out, wrapLines(th.hintWarn, "Warning: "+output.SafeText(warn), w)...)
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(out, "\n"))
}

// receiptCard is the done view's summary box: the receipt, what moved
// and was skipped, how the items moved, and the command that undoes it.
func (m Model) receiptCard(r plan.Result, bytes int64, w int) string {
	if r.ReceiptID == "" {
		return ""
	}
	th := m.th
	cw := w
	if cw > 76 {
		cw = 76
	}
	inner := cw - 4
	row := func(label, value string, st lipgloss.Style) []string {
		var lines []string
		for i, l := range wrapLines(st, value, inner-labelW) {
			lbl := strings.Repeat(" ", labelW)
			if i == 0 {
				lbl = th.label.Render(fmt.Sprintf("%-*s", labelW, label))
			}
			lines = append(lines, lbl+l)
		}
		return lines
	}
	var lines []string
	lines = append(lines, row("Receipt", output.SafeText(r.ReceiptID), th.text)...)
	lines = append(lines, row("Moved", items(len(r.Moved))+" · "+output.Bytes(bytes), th.text)...)
	if len(r.Skipped) > 0 || len(r.Displaced) > 0 {
		lines = append(lines, row("Skipped", fmt.Sprintf("%d, displaced %d", len(r.Skipped), len(r.Displaced)), th.text)...)
	}
	if how := moverNote(r); how != "" {
		lines = append(lines, row("How", how, th.muted)...)
	}
	if len(r.Moved) > 0 || len(r.Displaced) > 0 {
		lines = append(lines, row("Undo", "kipple restore "+output.SafeText(r.ReceiptID)+" --apply", th.key)...)
	}
	return m.pane("Receipt", cw, len(lines)+2, strings.Join(lines, "\n"))
}

// moverNote says how the items reached the Trash, from the receipt's
// mover: a direct move into ~/.Trash, or Apple's trash API (the fallback
// without Full Disk Access), which Finder's Put Back also understands.
func moverNote(r plan.Result) string {
	movers := map[string]bool{}
	for _, mv := range r.Moved {
		movers[mv.Mover] = true
	}
	var parts []string
	if movers[plan.MoverRename] {
		parts = append(parts, "moved straight into ~/.Trash; kipple restore puts items back")
	}
	if movers[plan.MoverNSFileManager] {
		parts = append(parts, "moved with Apple's Trash API (no Full Disk Access); Finder's Put Back and kipple restore both work")
	}
	return strings.Join(parts, ". ")
}

func (m Model) errorBody() string {
	th := m.th
	err := m.outcome.Err
	w := m.width - 4
	var out []string
	if m.outcome.Applied {
		out = append(out, th.title.Render("kipple stopped while moving items."))
	} else {
		out = append(out, th.title.Render("kipple could not finish the scan."))
	}
	out = append(out, "")
	if errors.Is(err, apply.ErrTrashAccess) {
		out = append(out, wrapLines(th.text, "kipple cannot use your Trash folder: macOS lets only apps with Full Disk Access open it, so nothing was moved. Grant Full Disk Access to your terminal (System Settings > Privacy & Security > Full Disk Access) and try again.", w)...)
	} else if err != nil {
		out = append(out, wrapLines(th.text, output.SafeText(err.Error()), w)...)
	}
	if r := m.outcome.Result; r.ReceiptID != "" {
		out = append(out, "", th.label.Render(fmt.Sprintf("%-*s", labelW, "Receipt"))+th.text.Render(output.SafeText(r.ReceiptID)))
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(out, "\n"))
}

// center places s in the middle of the body area.
func (m Model) center(s string) string {
	return lipgloss.Place(m.width, m.bodyHeight(), lipgloss.Center, lipgloss.Center, s)
}

// rel shows p relative to the home folder as ~/..., with every character
// that could act on the terminal escaped (output.SafeText).
func (m Model) rel(p string) string {
	home := m.cfg.Scan.Env.Home
	if home != "" && strings.HasPrefix(p, home+"/") {
		p = "~/" + strings.TrimPrefix(p, home+"/")
	}
	return output.SafeText(p)
}

func (m Model) trunc(s string, w int) string {
	if w <= 1 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 && lipgloss.Width(string(rs))+1 > w {
		rs = rs[:len(rs)-1]
	}
	return string(rs) + "…"
}

// truncLeft keeps the end of s, which for a path is the distinctive part.
func (m Model) truncLeft(s string, w int) string {
	if w <= 1 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 && lipgloss.Width(string(rs))+1 > w {
		rs = rs[1:]
	}
	return "…" + string(rs)
}

// spread puts left and right at the two ends of a line of width w.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// wrapLines word-wraps s to width w and styles each line.
func wrapLines(st lipgloss.Style, s string, w int) []string {
	if w < 1 {
		w = 1
	}
	wrapped := lipgloss.NewStyle().Width(w).Render(s)
	var out []string
	for _, l := range strings.Split(wrapped, "\n") {
		out = append(out, st.Render(strings.TrimRight(l, " ")))
	}
	return out
}

// fitHeight pads or cuts s to exactly h lines.
func fitHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func items(n int) string { return plural(n, "item") }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

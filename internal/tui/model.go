// Package tui is kipple's interactive terminal front end. It contains no
// deletion logic: it scans through internal/scan and moves through
// internal/apply, with the selection passed as apply's Only list, so every
// item is authorised and re-checked by the same code as kipple clean. Info
// items cannot be selected, review and manual items follow kipple clean
// --only, and nothing is ever deleted: the only action is the Trash.
package tui

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/selection"
)

// Config is what cmd/kipple hands the TUI: the same scan and apply options
// kipple clean builds. The TUI sets only the plan, the Only list (the
// selection) and ChecksSince on the apply options; it never sets Permanent
// or DryRun.
type Config struct {
	// Ctx bounds the scan and the apply (context.Background when nil).
	Ctx context.Context
	// Scan scans on start. Progress is replaced.
	Scan scan.Options
	// Apply moves the selection. Plan, Fresh, Only, DryRun, Permanent,
	// ChecksSince and Progress are replaced.
	Apply apply.Options
	// Color enables the brand palette; false is the NO_COLOR theme.
	Color bool
	// Version is shown in the header.
	Version string
	// HomeLabel is how the header names the home folder ("~" for the real
	// home, a shortened KIPPLE_HOME otherwise); the full path when empty.
	HomeLabel string
	// Renderer styles the output (lipgloss's default when nil).
	Renderer *lipgloss.Renderer
}

// Outcome is how a TUI session ended.
type Outcome struct {
	// Applied is set when the selection was handed to apply; Result is
	// then apply's result (and Err any error it returned).
	Applied bool
	Result  plan.Result
	// Err is a scan or apply error.
	Err error
}

type state int

const (
	stateScanning state = iota
	stateReview
	stateConfirm
	stateApplying
	stateDone
	stateError
)

// row is one line of the list: a rule heading or an item.
type row struct {
	header bool
	group  int
	item   int // index into plan.Items (item rows)
}

// group is one rule's items.
type group struct {
	id, title, tier string
	items           []int
	bytes           int64
}

type scanDoneMsg struct {
	plan    plan.Plan
	started time.Time
	err     error
}

type applyDoneMsg struct {
	res plan.Result
	err error
}

// progress carries the latest scan or apply message to the view.
type progress struct {
	mu  sync.Mutex
	msg string
}

func (p *progress) set(s string) {
	p.mu.Lock()
	p.msg = s
	p.mu.Unlock()
}

func (p *progress) get() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.msg
}

// Model is the Bubble Tea model. Build it with New.
type Model struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	th     theme
	spin   spinner.Model
	prog   *progress

	state         state
	width, height int

	plan     plan.Plan
	started  time.Time
	ruleByID map[string]rules.Rule
	groups   []group
	rows     []row
	cursor   int // index into rows, always an item row
	offset   int // first visible list row
	sel      *selection.Set

	hint     string
	hintWarn bool
	stopping bool

	outcome Outcome
}

// New returns the TUI model for cfg. It scans when the program starts.
func New(cfg Config) Model {
	if cfg.Ctx == nil {
		cfg.Ctx = context.Background()
	}
	r := cfg.Renderer
	if r == nil {
		r = lipgloss.DefaultRenderer()
	}
	ctx, cancel := context.WithCancel(cfg.Ctx)
	th := newTheme(r, cfg.Color)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = th.spinner
	rs := cfg.Apply.Rules
	if rs == nil {
		rs = cfg.Scan.Rules
	}
	if rs == nil {
		rs = rules.Catalogue()
	}
	byID := map[string]rules.Rule{}
	for _, rl := range rs {
		byID[rl.ID] = rl
	}
	return Model{
		cfg: cfg, ctx: ctx, cancel: cancel, th: th, spin: sp, prog: &progress{},
		width: 80, height: 24, ruleByID: byID, sel: selection.New(plan.Plan{}),
	}
}

// Outcome reports how the session ended.
func (m Model) Outcome() Outcome { return m.outcome }

// Init starts the scan.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.scanCmd())
}

func (m Model) scanCmd() tea.Cmd {
	opts := m.cfg.Scan
	opts.Progress = m.prog.set
	ctx := m.ctx
	return func() tea.Msg {
		started := time.Now()
		p, err := scan.Run(ctx, opts)
		return scanDoneMsg{plan: p, started: started, err: err}
	}
}

// applyCmd moves the selection through apply.Run, as kipple clean --apply
// --only <selection> would.
func (m Model) applyCmd() tea.Cmd {
	opts := m.cfg.Apply
	opts.Plan = m.plan
	opts.Fresh = nil
	opts.Only = m.selectedIDs()
	opts.DryRun = false
	opts.Permanent = false
	opts.ChecksSince = m.started
	opts.Progress = m.prog.set
	ctx := m.ctx
	return func() tea.Msg {
		res, err := apply.Run(ctx, opts)
		return applyDoneMsg{res: res, err: err}
	}
}

// Update handles a message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
		return m, nil
	case spinner.TickMsg:
		if m.state != stateScanning && m.state != stateApplying {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case scanDoneMsg:
		if msg.err != nil {
			m.state, m.outcome.Err = stateError, msg.err
			return m, nil
		}
		m.load(msg.plan, msg.started)
		m.state = stateReview
		return m, nil
	case applyDoneMsg:
		m.outcome = Outcome{Applied: true, Result: msg.res, Err: msg.err}
		if msg.err != nil {
			m.state = stateError
		} else {
			m.state = stateDone
		}
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	switch m.state {
	case stateScanning:
		if s == "q" || s == "ctrl+c" || s == "esc" {
			m.cancel()
			return m, tea.Quit
		}
	case stateApplying:
		// Interrupting stops apply before its next item; the items already
		// moved stay recorded in the receipt.
		if s == "ctrl+c" && !m.stopping {
			m.stopping = true
			m.cancel()
		}
	case stateConfirm:
		if s == "y" || s == "Y" {
			m.state = stateApplying
			m.hint = ""
			return m, tea.Batch(m.spin.Tick, m.applyCmd())
		}
		m.state = stateReview
		m.setHint("Cancelled. Nothing was moved.", false)
	case stateDone, stateError:
		if s == "q" || s == "ctrl+c" || s == "esc" || s == "enter" {
			return m, tea.Quit
		}
	case stateReview:
		return m.reviewKey(s)
	}
	return m, nil
}

func (m Model) reviewKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "q", "ctrl+c", "esc":
		m.cancel()
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-m.listHeight())
	case "pgdown", "ctrl+d":
		m.move(m.listHeight())
	case "home", "g":
		m.move(-len(m.rows))
	case "end", "G":
		m.move(len(m.rows))
	case "tab":
		m.jumpGroup(1)
	case "shift+tab":
		m.jumpGroup(-1)
	case " ":
		m.toggle()
	case "a":
		if m.sel.Len() == 0 {
			m.setHint("Nothing is selected. Press space on an item to select it.", true)
			return m, nil
		}
		m.state = stateConfirm
	}
	return m, nil
}

func (m *Model) setHint(s string, warn bool) { m.hint, m.hintWarn = s, warn }

// load builds the grouped rows from p and pre-selects what the plan
// selects.
func (m *Model) load(p plan.Plan, started time.Time) {
	m.plan, m.started = p, started
	idx := map[string]int{}
	for i, it := range p.Items {
		gi, ok := idx[it.Rule]
		if !ok {
			gi = len(m.groups)
			idx[it.Rule] = gi
			title := it.Rule
			if r, ok := m.ruleByID[it.Rule]; ok && r.Title != "" {
				title = r.Title
			}
			m.groups = append(m.groups, group{id: it.Rule, title: title, tier: it.Tier})
		}
		g := &m.groups[gi]
		g.items = append(g.items, i)
		g.bytes += it.AllocatedBytes
	}
	m.sel = selection.New(p)
	tierRank := map[string]int{"safe": 0, "review": 1, "manual": 2, "info": 3}
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
		m.rows = append(m.rows, row{header: true, group: gi})
		for _, i := range g.items {
			m.rows = append(m.rows, row{group: gi, item: i})
		}
	}
	m.cursor = 0
	m.move(1)
	m.move(-1)
	if len(m.rows) == 0 {
		m.setHint("Nothing to clean: kipple found no items under this home folder.", false)
	}
}

// current returns the highlighted item, if any.
func (m Model) current() (plan.Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.rows[m.cursor].header {
		return plan.Item{}, false
	}
	return m.plan.Items[m.rows[m.cursor].item], true
}

// move moves the cursor by d item rows (headings are skipped).
func (m *Model) move(d int) {
	if len(m.rows) == 0 {
		return
	}
	step := 1
	if d < 0 {
		step, d = -1, -d
	}
	pos := m.cursor
	for moved := 0; moved < d; {
		next := pos + step
		for next >= 0 && next < len(m.rows) && m.rows[next].header {
			next += step
		}
		if next < 0 || next >= len(m.rows) {
			break
		}
		pos = next
		moved++
	}
	if m.rows[pos].header { // only reachable at the start
		for pos < len(m.rows) && m.rows[pos].header {
			pos++
		}
		if pos >= len(m.rows) {
			pos = 0
		}
	}
	m.cursor = pos
	m.hint = ""
	m.scroll()
}

// jumpGroup moves to the first item of the next (d=1) or previous group.
func (m *Model) jumpGroup(d int) {
	if len(m.rows) == 0 {
		return
	}
	g := m.rows[m.cursor].group + d
	if g < 0 || g >= len(m.groups) {
		return
	}
	for i, r := range m.rows {
		if !r.header && r.group == g {
			m.cursor = i
			break
		}
	}
	m.hint = ""
	m.scroll()
}

// scroll keeps the cursor (and its rule heading when possible) visible.
func (m *Model) scroll() {
	h := m.listHeight()
	if h <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
		if m.offset > 0 && m.rows[m.offset-1].header {
			m.offset--
		}
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// toggle flips the highlighted item's selection under the same rules as
// kipple clean --only: info items never; any other item only when nothing
// but its tier keeps it from being selected (apply.Preview decides).
func (m *Model) toggle() {
	it, ok := m.current()
	if !ok {
		return
	}
	if it.Tier == string(rules.TierInfo) {
		cmd := it.OwnerCommand
		if cmd == "" {
			cmd = m.ruleByID[it.Rule].OwnerCommand
		}
		msg := "Info item: kipple never moves it."
		if cmd != "" {
			msg += " Use " + output.SafeText(cmd)
		}
		m.setHint(msg, true)
		return
	}
	switch m.sel.Toggle(it.ID) {
	case selection.Removed:
		m.setHint("", false)
		return
	case selection.RefusedBlocked:
		m.setHint("Can't select this item: "+strings.Join(blockers(it.Reasons), "; ")+".", true)
		return
	case selection.Added:
	default:
		return
	}
	switch it.Tier {
	case string(rules.TierManual):
		m.setHint("Selected a manual item. Read \"If removed\" before you apply.", true)
	case string(rules.TierReview):
		m.setHint("Selected a review item.", false)
	default:
		m.setHint("", false)
	}
}

// selectedIDs returns the selection in plan order.
func (m Model) selectedIDs() []string { return m.sel.IDs() }

// selectedTotal returns the selected count and allocated bytes.
func (m Model) selectedTotal() (int, int64) { return m.sel.Total() }

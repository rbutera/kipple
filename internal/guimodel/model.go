// Package guimodel is the GUI's view-model: which screen is showing, the
// grouped review list, the cursor, the selection, the totals, the status
// line, the confirmation and done summaries, and how errors map to screens.
// It imports no GUI toolkit, so its tests drive it synchronously against
// fixture homes.
//
// It contains no deletion logic. It scans with scan.Run and moves with
// apply.Run, passing the selection as Only and never setting Permanent or
// DryRun, exactly like the TUI and kipple clean --apply --only. Putting
// items back goes through apply.Restore with the same options kipple
// restore <receipt> --apply builds. Every path and code it shows passes
// through output.SafeText.
//
// The model is not safe for concurrent use. Scans, applies and restores run
// through jobs: the GUI starts a job on the main goroutine, runs it on
// another, and hands its progress and result back on the main goroutine.
package guimodel

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/selection"
	"github.com/rbutera/kipple/internal/words"
)

// Config is what the GUI's main hands the model: the options kipple clean
// would build for this home. The model sets only the scan's Progress and
// Level (always safe) and the apply's Plan, Only, ChecksSince and Progress.
type Config struct {
	Version string
	// Home is the effective home folder; paths below it are shown as ~/….
	Home string
	// HomeLabel names the home in the header ("~" or a KIPPLE_HOME label).
	HomeLabel string
	// TestHome is set when Home came from KIPPLE_HOME.
	TestHome bool
	// Scan and Apply are the launch options for this home.
	Scan  scan.Options
	Apply apply.Options
	// Store holds the receipts.
	Store receipt.Store
	// GitAvailable is false when Apple's Command Line Tools are missing;
	// Scan.Git and Apply.Git are then an inspector that never runs git.
	GitAvailable bool

	// ScanFunc, ApplyFunc, RestoreFunc and ProbeFunc replace scan.Run,
	// apply.Run, apply.Restore and apply.ProbeTrash (tests only).
	ScanFunc    func(context.Context, scan.Options) (plan.Plan, error)
	ApplyFunc   func(context.Context, apply.Options) (plan.Result, error)
	RestoreFunc func(context.Context, apply.RestoreOptions) (plan.Result, error)
	ProbeFunc   func(apply.Options) (apply.TrashMode, error)
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

// Screen is what fills the window.
type Screen int

const (
	// ScreenStart: nothing scanned yet, or a scan was stopped.
	ScreenStart Screen = iota
	// ScreenScanning: a scan is running.
	ScreenScanning
	// ScreenReview: the grouped list and the detail pane. The confirm and
	// applying sheets sit over it.
	ScreenReview
	// ScreenDone: the result of a move (and of a put back).
	ScreenDone
	// ScreenProblem: a refusal or an error; see Problem.
	ScreenProblem
)

// Sheet is the modal over the current screen.
type Sheet int

const (
	SheetNone Sheet = iota
	// SheetConfirm asks before anything moves.
	SheetConfirm
	// SheetApplying shows the move in progress.
	SheetApplying
	// SheetPutBack asks before items go back.
	SheetPutBack
	// SheetRestoring shows the put back in progress.
	SheetRestoring
)

// Model is the GUI's state. Build it with New.
type Model struct {
	cfg      Config
	ruleByID map[string]rules.Rule
	places   []place

	screen Screen
	sheet  Sheet
	hint   string

	// scanning
	gen        int
	cancel     context.CancelFunc
	visited    map[string]bool
	scanPlace  string
	firstScan  bool // a scan of this launch has finished
	stopped    bool
	rescanWarn bool

	// review
	plan     plan.Plan
	started  time.Time
	sel      *selection.Set
	pre      []string
	groups   []group
	rows     []Row
	cursor   int
	warnOpen bool

	// confirm and apply
	trashMode apply.TrashMode
	trashErr  error
	progress  applyProgress
	stopping  bool
	quitAfter bool
	quitNow   bool

	// done
	result      plan.Result
	applyErr    error
	pending     []plan.MovedItem
	showAllMove bool
	restored    *plan.Result
	restoreErr  error
	finished    time.Time

	problem Problem
}

// place is one tile of the scan screen's grid: a rule, or the project pass.
type place struct {
	key   string // rule ID, or "projects"
	title string
}

const projectsKey = "projects"

// New returns the model on the start screen.
func New(cfg Config) *Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ScanFunc == nil {
		cfg.ScanFunc = scan.Run
	}
	if cfg.ApplyFunc == nil {
		cfg.ApplyFunc = apply.Run
	}
	if cfg.RestoreFunc == nil {
		cfg.RestoreFunc = apply.Restore
	}
	if cfg.ProbeFunc == nil {
		cfg.ProbeFunc = apply.ProbeTrash
	}
	// The GUI pre-selects exactly what kipple scan --level safe selects:
	// the config file's level is ignored (its protections still apply).
	cfg.Scan.Level = rules.LevelSafe
	rs := cfg.Apply.Rules
	if rs == nil {
		rs = cfg.Scan.Rules
	}
	if rs == nil {
		rs = rules.Catalogue()
	}
	m := &Model{cfg: cfg, ruleByID: map[string]rules.Rule{}, visited: map[string]bool{}, cursor: -1, sel: selection.New(plan.Plan{})}
	projects := false
	for _, r := range rs {
		m.ruleByID[r.ID] = r
		if r.Match.Kind == rules.MatchProjectArtifact {
			projects = true
			continue
		}
		m.places = append(m.places, place{key: r.ID, title: r.Title})
	}
	if projects {
		m.places = append(m.places, place{key: projectsKey, title: projectsTitle(rs)})
	}
	return m
}

// projectsTitle names the project pass: "Projects in ~/dev, ~/code, …".
func projectsTitle(rs []rules.Rule) string {
	seen := map[string]bool{}
	var roots []string
	for _, r := range rs {
		if r.Match.Kind != rules.MatchProjectArtifact {
			continue
		}
		for _, root := range r.Roots {
			if !seen[root] {
				seen[root] = true
				roots = append(roots, "~/"+output.SafeText(root))
			}
		}
	}
	if len(roots) > 3 {
		roots = append(roots[:3], "…")
	}
	return "Projects in " + strings.Join(roots, ", ")
}

// Config returns the model's configuration.
func (m *Model) Config() Config { return m.cfg }

// Screen returns the current screen.
func (m *Model) Screen() Screen { return m.screen }

// Sheet returns the current sheet.
func (m *Model) Sheet() Sheet { return m.sheet }

// Plan returns the latest scan.
func (m *Model) Plan() plan.Plan { return m.plan }

// Rel returns p as shown to people: home-relative (~/…), escaped.
func (m *Model) Rel(p string) string {
	home := m.cfg.Home
	switch {
	case home != "" && p == home:
		p = "~"
	case home != "" && strings.HasPrefix(p, strings.TrimSuffix(home, "/")+"/"):
		p = "~/" + strings.TrimPrefix(p, strings.TrimSuffix(home, "/")+"/")
	}
	return output.SafeText(p)
}

// ruleTitle is a rule's title, or its ID when the catalogue lacks it.
func (m *Model) ruleTitle(id string) string {
	if r, ok := m.ruleByID[id]; ok && r.Title != "" {
		return output.SafeText(r.Title)
	}
	return output.SafeText(id)
}

// ---- Scan ----

// ScanJob is a scan started by StartScan. Run it off the main goroutine.
type ScanJob struct {
	gen  int
	ctx  context.Context
	opts scan.Options
	run  func(context.Context, scan.Options) (plan.Plan, error)
}

// Run scans, calling progress (from this goroutine) once per place.
func (j ScanJob) Run(progress func(string)) (plan.Plan, error) {
	o := j.opts
	o.Progress = progress
	return j.run(j.ctx, o)
}

// CanScan reports whether a scan may start now.
func (m *Model) CanScan() bool {
	return m.sheet == SheetNone && (m.screen == ScreenStart || m.screen == ScreenReview || m.screen == ScreenDone ||
		(m.screen == ScreenProblem && m.problem.CanScan))
}

// StartScan moves to the scanning screen and returns the job, or false
// when a scan may not start now.
func (m *Model) StartScan() (ScanJob, bool) {
	if !m.CanScan() {
		return ScanJob{}, false
	}
	m.gen++
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.screen, m.sheet = ScreenScanning, SheetNone
	m.visited, m.scanPlace, m.stopped, m.rescanWarn = map[string]bool{}, "", false, false
	m.hint = "Reading only. Nothing moves during a scan."
	return ScanJob{gen: m.gen, ctx: ctx, opts: m.cfg.Scan, run: m.cfg.ScanFunc}, true
}

// RequestRescan starts a scan from the review screen, after a warning when
// the person has changed the pre-selection: the first press only warns.
func (m *Model) RequestRescan() (ScanJob, bool) {
	if m.screen == ScreenReview && m.sheet == SheetNone && !m.sel.Same(m.pre) && !m.rescanWarn {
		m.rescanWarn = true
		m.hint = "Scanning again resets your choices. Press ⌘R again to rescan."
		return ScanJob{}, false
	}
	return m.StartScan()
}

// ScanProgress records a progress message from the running scan.
func (m *Model) ScanProgress(job ScanJob, msg string) {
	if job.gen != m.gen || m.screen != ScreenScanning {
		return
	}
	switch {
	case msg == "scanning projects":
		m.visited[projectsKey] = true
		m.scanPlace = m.placeTitle(projectsKey)
	case strings.HasPrefix(msg, "scanning "):
		id := strings.TrimPrefix(msg, "scanning ")
		m.visited[id] = true
		m.scanPlace = m.ruleTitle(id)
	}
}

func (m *Model) placeTitle(key string) string {
	for _, p := range m.places {
		if p.key == key {
			return output.SafeText(p.title)
		}
	}
	return ""
}

// StopScan cancels the running scan and returns to the start screen.
func (m *Model) StopScan() {
	if m.screen != ScreenScanning {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.gen++ // the stopped scan's result is ignored
	m.screen, m.stopped = ScreenStart, true
	m.hint = "Scan stopped. Nothing was read further, nothing moved."
}

// ScanDone records the scan's result.
func (m *Model) ScanDone(job ScanJob, p plan.Plan, err error) {
	if job.gen != m.gen || m.screen != ScreenScanning {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	if err != nil {
		m.setProblem(scanProblem(err))
		return
	}
	m.firstScan = true
	m.load(p, m.cfg.Now())
	m.screen = ScreenReview
}

// ScanNow runs a whole scan on this goroutine (tests and screenshots).
func (m *Model) ScanNow() {
	job, ok := m.StartScan()
	if !ok {
		return
	}
	p, err := job.Run(func(s string) { m.ScanProgress(job, s) })
	m.ScanDone(job, p, err)
}

// ScanView is the scan screen.
type ScanView struct {
	// Tiles is one entry per place, true once it has been visited.
	Tiles []bool
	// Count is "Looking in 31 of 36 places", or "Measuring sizes" once every
	// place has been visited.
	Count string
	// Current names the place being looked at (empty while measuring).
	Current   string
	Measuring bool
}

// Scanning describes the scan screen.
func (m *Model) Scanning() ScanView {
	v := ScanView{Tiles: make([]bool, len(m.places))}
	n := 0
	for i, p := range m.places {
		if m.visited[p.key] {
			v.Tiles[i] = true
			n++
		}
	}
	if n == len(m.places) && n > 0 {
		v.Measuring, v.Count = true, "Measuring sizes"
		return v
	}
	if n == 0 {
		n = 1
	}
	v.Count = "Looking in " + itoa(n) + " of " + words.Plural(len(m.places), "place")
	v.Current = m.scanPlace
	return v
}

// Places is the number of places kipple looks.
func (m *Model) Places() int { return len(m.places) }

// ShowDownloadsNote reports whether the start screen shows the note about
// the Downloads privacy prompt: only until a scan of this launch finishes.
func (m *Model) ShowDownloadsNote() bool { return !m.firstScan }

// ---- Status ----

// Status is the action bar's text.
func (m *Model) Status() string {
	if m.hint != "" {
		return m.hint
	}
	switch m.screen {
	case ScreenStart:
		return "Nothing has been scanned yet."
	case ScreenReview:
		return m.selectionStatus()
	case ScreenDone:
		return "Finished " + m.finished.Format("15:04") + ". Nothing else will move."
	case ScreenProblem:
		return m.problem.Status
	}
	return ""
}

func (m *Model) selectionStatus() string {
	n, _ := m.sel.Total()
	if n == 0 {
		return "Nothing is selected."
	}
	return words.Items(n) + " selected. Each is checked again before it moves."
}

// SetHint replaces the status text until the next action (Copy and such).
func (m *Model) SetHint(s string) { m.hint = s }

func (m *Model) setProblem(p Problem) {
	m.problem = p
	m.screen, m.sheet = ScreenProblem, SheetNone
	m.hint = ""
}

// Problem returns the refusal or error shown on ScreenProblem.
func (m *Model) Problem() Problem { return m.problem }

// Back leaves a problem screen that offers Back to review or Back.
func (m *Model) Back() {
	if m.screen != ScreenProblem || m.problem.BackTo == 0 {
		return
	}
	m.screen = m.problem.BackTo
	m.hint = ""
}

// RequestQuit reports whether the window may close now. A close or quit
// while items move first stops the move; QuitNow then reports when it has
// returned and its receipt is saved, so the window can close.
func (m *Model) RequestQuit() bool {
	if m.sheet == SheetApplying || m.sheet == SheetRestoring {
		m.quitAfter = true
		if m.sheet == SheetApplying {
			m.StopApply()
		} else if m.cancel != nil {
			m.cancel()
		}
		return false
	}
	if m.cancel != nil {
		m.cancel()
	}
	return true
}

// QuitNow reports whether a deferred quit is due (the move it waited for
// has returned and its receipt is saved).
func (m *Model) QuitNow() bool { return m.quitNow }

func itoa(n int) string { return strconv.Itoa(n) }

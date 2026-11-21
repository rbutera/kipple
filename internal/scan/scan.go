// Package scan measures every candidate the catalogue allows and builds a
// plan. It is read-only: it opens directories and stats entries, and never
// creates, changes, moves or deletes anything.
//
// Every candidate goes through policy.Session.Authorize (one Session per
// scan, closed when the scan ends). Allowed candidates are listed; info-tier
// candidates, which the policy never authorises, are listed as measured-only;
// candidates refused for a reason a person should see (a symlink, another
// volume, a name alias) are listed unselected with reason refused:<reason>;
// everything else the policy refuses (denylisted, protected, excluded, not
// an item of the rule) is not listed at all.
//
// Conditions (minimum age, idle project, clean and pushed git state,
// partial walks, mounts and protected contents) are evaluated by the same
// code apply runs again immediately before each move (see Checker). Running
// owners and agents are checked by apply only, at move time.
//
// Every existing rule root is recorded in the plan with its (dev, ino), so
// apply can refuse an item whose root was replaced after the scan.
package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rbutera/kipple/internal/gitstate"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/sizes"
)

// ProjectDepth is how many directory levels below a project root are
// searched for projects. An artifact can sit at most ProjectDepth+1 levels
// below the root.
const ProjectDepth = 4

// Options configure a scan.
type Options struct {
	// Env is the policy environment: the injected home and user protections.
	Env policy.Env
	// Rules is the effective catalogue (see EffectiveRules). Nil means
	// rules.Catalogue() with the default project roots.
	Rules []rules.Rule
	// Level is rules.LevelSafe (default) or rules.LevelReview.
	Level string
	// KippleVersion is recorded in the plan.
	KippleVersion string
	// Now is the clock for age conditions and generated_at (time.Now if nil).
	Now func() time.Time
	// Workers bounds concurrent measurement (min(NumCPU, 8) if zero).
	Workers int
	// Progress, when set, receives short progress messages (for stderr).
	Progress func(string)
	// Git classifies repositories for RequiresCleanGit conditions
	// (gitstate.Inspector with home's parent as ceiling when nil).
	Git GitInspector
	// Warnings are added to the plan's warnings first (the front end's
	// findings about the environment, such as user-dirs.dirs).
	Warnings []string
}

// EffectiveRules returns cat with every project artifact rule's roots
// replaced by projectRoots (home-relative), as configured.
func EffectiveRules(cat []rules.Rule, projectRoots []string) []rules.Rule {
	out := make([]rules.Rule, len(cat))
	for i, r := range cat {
		if r.Match.Kind == rules.MatchProjectArtifact && projectRoots != nil {
			r.Roots = append([]string(nil), projectRoots...)
		}
		out[i] = r
	}
	return out
}

// candidate is one path a rule might offer.
type candidate struct {
	rule  int // index into rules
	path  string
	item  plan.Item
	res   sizes.Result
	list  bool
	open  bool // measure it
	guard bool // check for protected descendants
}

type scanner struct {
	ctx   context.Context
	opts  Options
	rules []rules.Rule
	sess  *policy.Session
	home  string
	now   time.Time
	cond  *conditions

	warnings []string
	status   []string              // per rule
	roots    [][]plan.RootIdentity // per rule: existing roots and identities
	// mountsErr is set when mount points cannot be determined.
	mountsErr error
}

// Run scans and returns the plan. It fails only when home cannot be used
// or ctx is cancelled; problems with individual rules or items are recorded
// in the plan.
func Run(ctx context.Context, opts Options) (plan.Plan, error) {
	if opts.Level == "" {
		opts.Level = rules.LevelSafe
	}
	if opts.Level != rules.LevelSafe && opts.Level != rules.LevelReview {
		return plan.Plan{}, fmt.Errorf("scan: unknown level %q", opts.Level)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	rs := opts.Rules
	if rs == nil {
		rs = rules.Catalogue()
	}
	sess, err := policy.NewSession(opts.Env)
	if err != nil {
		_ = sess.Close()
		return plan.Plan{}, fmt.Errorf("scan: %w", err)
	}
	defer func() { _ = sess.Close() }()

	git := opts.Git
	if git == nil {
		git = gitstate.Inspector{Ceiling: filepath.Dir(opts.Env.Home)}
	}
	s := &scanner{ctx: ctx, opts: opts, rules: rs, sess: sess, home: opts.Env.Home, now: opts.Now(), status: make([]string, len(rs)), roots: make([][]plan.RootIdentity, len(rs))}
	s.warnings = append(s.warnings, opts.Warnings...)
	s.cond = &conditions{ctx: ctx, sess: sess, rules: rs, now: s.now, git: git, idle: map[string]bool{}, repos: map[string]gitstate.Result{}}
	if err := sess.MountsErr(); err != nil {
		// Fail closed: without mount points nothing can be shown to stay
		// on one side of every boundary.
		s.mountsErr = err
		s.warn("%v; every item is marked contains_mount, and clean --apply stops with mounts_unavailable", err)
	}
	var cands []*candidate
	projects := map[string][]int{} // project roots -> project rule indexes
	for i, r := range rs {
		if err := ctx.Err(); err != nil {
			return plan.Plan{}, err
		}
		if r.Match.Kind == rules.MatchProjectArtifact {
			for _, root := range r.Roots {
				projects[root] = append(projects[root], i)
			}
			continue
		}
		s.progress("scanning " + r.ID)
		cands = append(cands, s.ruleCandidates(i)...)
	}
	if len(projects) > 0 {
		s.progress("scanning projects")
		cands = append(cands, s.projectCandidates(projects)...)
	}
	for _, c := range cands {
		s.classify(c)
	}
	if err := s.measure(cands); err != nil {
		return plan.Plan{}, err
	}
	for _, c := range cands {
		if c.list {
			s.decide(c)
		}
	}
	return s.build(cands), nil
}

func (s *scanner) progress(msg string) {
	if s.opts.Progress != nil {
		s.opts.Progress(msg)
	}
}

func (s *scanner) warn(format string, args ...any) {
	s.warnings = append(s.warnings, fmt.Sprintf(format, args...))
}

// setStatus records a root's status; present wins over error, error over absent.
func (s *scanner) setStatus(i int, st string) {
	rank := map[string]int{"": 0, plan.RuleAbsent: 1, plan.RuleError: 2, plan.RulePresent: 3}
	if rank[st] > rank[s.status[i]] {
		s.status[i] = st
	}
}

// rootError records an Open error for a rule root: absent when it does not
// exist, otherwise an error with a plan warning.
func (s *scanner) rootError(i int, root string, err error) {
	var re *policy.RefusedError
	if errors.As(err, &re) && re.Reason == policy.ReasonMissing && errors.Is(err, fs.ErrNotExist) {
		s.setStatus(i, plan.RuleAbsent)
		return
	}
	s.setStatus(i, plan.RuleError)
	reason := err.Error()
	if re != nil {
		reason = re.Reason
	}
	s.warn("rule %s: root %s cannot be scanned: %s", s.rules[i].ID, s.rel(root), reason)
}

func (s *scanner) rel(abs string) string { return strings.TrimPrefix(abs, s.home+"/") }

// recordRoot remembers an existing root folder of rule i and its identity.
func (s *scanner) recordRoot(i int, dir string, info fs.FileInfo) {
	dev, ino, ok := policy.Identity(info)
	if !ok {
		return
	}
	rel := s.rel(dir)
	for _, r := range s.roots[i] {
		if r.Path == rel {
			return
		}
	}
	s.roots[i] = append(s.roots[i], plan.RootIdentity{Path: rel, Dev: dev, Ino: ino})
}

// expandRoot resolves a home-relative root with at most one glob segment
// to the existing directories it names. Directories that do not exist are
// reported through rootError.
func (s *scanner) expandRoot(i int, root string) []string {
	segs, ok := policy.ParseRoot(root)
	if !ok {
		s.setStatus(i, plan.RuleError)
		s.warn("rule %s: invalid root %q", s.rules[i].ID, root)
		return nil
	}
	bases := []string{s.home}
	for _, seg := range segs {
		var next []string
		for _, b := range bases {
			if !strings.ContainsAny(seg, `*?[\`) {
				next = append(next, b+"/"+seg)
				continue
			}
			e, err := s.sess.Open(b) // b is below home: ParseRoot forbids a glob first segment
			if err != nil {
				s.rootError(i, b, err)
				continue
			}
			if e.Dir == nil {
				s.setStatus(i, plan.RuleAbsent)
				continue
			}
			names, err := e.Dir.Readdirnames(-1)
			_ = e.Dir.Close()
			if err != nil {
				s.rootError(i, b, err)
				continue
			}
			sort.Strings(names)
			for _, n := range names {
				if m, err := filepath.Match(seg, n); err == nil && m {
					next = append(next, b+"/"+n)
				}
			}
		}
		bases = next
	}
	if len(bases) == 0 {
		s.setStatus(i, plan.RuleAbsent)
	}
	return bases
}

// ruleCandidates lists the direct children of a children or extensions
// rule's roots.
func (s *scanner) ruleCandidates(i int) []*candidate {
	var out []*candidate
	for _, root := range s.rules[i].Roots {
		for _, dir := range s.expandRoot(i, root) {
			e, err := s.sess.Open(dir)
			if err != nil {
				s.rootError(i, dir, err)
				continue
			}
			if e.Dir == nil {
				s.setStatus(i, plan.RuleError)
				s.warn("rule %s: root %s is not a directory", s.rules[i].ID, s.rel(dir))
				continue
			}
			names, err := e.Dir.Readdirnames(-1)
			_ = e.Dir.Close()
			if err != nil {
				s.rootError(i, dir, err)
				continue
			}
			s.setStatus(i, plan.RulePresent)
			s.recordRoot(i, dir, e.Info)
			sort.Strings(names)
			for _, n := range names {
				out = append(out, &candidate{rule: i, path: dir + "/" + n})
			}
		}
	}
	return out
}

// classify authorises a candidate and decides whether it is listed and
// measured.
func (s *scanner) classify(c *candidate) {
	r := s.rules[c.rule]
	d := s.sess.Authorize(r, c.path)
	rel := s.rel(c.path)
	c.item = plan.Item{
		ID:         plan.ID(r.ID, rel),
		Rule:       r.ID,
		Tier:       string(r.Tier),
		Path:       c.path,
		ScanStatus: plan.ScanComplete,
		Reasons:    []string{},
	}
	switch {
	case d.Allowed:
		c.list, c.open, c.guard = true, true, true
	case d.Reason == policy.ReasonInfoTier && r.Tier == rules.TierInfo:
		c.list, c.open = true, true
		c.item.OwnerCommand = r.OwnerCommand
	case visibleRefusal(d.Reason):
		c.list = true
		c.item.Reasons = append(c.item.Reasons, plan.Refused(d.Reason))
	}
}

// visibleRefusal: refusals that are listed (unselected) so a person can see
// what kipple declined to touch. Other refusals mean the path is not an item
// of the rule at all, or is protected, and are not listed.
func visibleRefusal(reason string) bool {
	switch reason {
	case policy.ReasonSymlinkInPath, policy.ReasonDifferentDevice, policy.ReasonNameMismatch:
		return true
	}
	return false
}

// measureDir measures an item; tests replace it to simulate a mount.
var measureDir = sizes.MeasureDir

// measure sizes every listed candidate with a bounded worker pool.
func (s *scanner) measure(cands []*candidate) error {
	workers := s.opts.Workers
	if workers <= 0 {
		workers = min(runtime.NumCPU(), 8)
	}
	jobs := make(chan *candidate)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				s.measureOne(c)
			}
		}()
	}
	var err error
	for _, c := range cands {
		if !c.open {
			continue
		}
		if err = s.ctx.Err(); err != nil {
			break
		}
		jobs <- c
	}
	close(jobs)
	wg.Wait()
	if err == nil {
		err = s.ctx.Err()
	}
	return err
}

func (s *scanner) measureOne(c *candidate) {
	e, err := s.sess.Open(c.path)
	if err != nil {
		var re *policy.RefusedError
		reason := policy.ReasonMissing
		if errors.As(err, &re) {
			reason = re.Reason
		}
		c.item.Reasons = append(c.item.Reasons, plan.Refused(reason))
		c.item.ScanStatus = plan.ScanPartial
		c.item.Warnings = append(c.item.Warnings, "could not be measured: "+reason)
		return
	}
	if dev, ino, ok := policy.Identity(e.Info); ok {
		c.item.Dev, c.item.Ino = dev, ino
	}
	if e.Dir == nil {
		c.res = sizes.MeasureFile(e.Info)
		return
	}
	defer func() { _ = e.Dir.Close() }()
	opts := sizes.Options{MountRoot: s.sess.MountRoot}
	if c.guard {
		opts = guardOptions(s.sess, s.rules[c.rule], c.path)
	}
	res, err := measureDir(s.ctx, e.Dir, opts)
	if err != nil && res.Status == "" {
		res.Status = sizes.StatusPartial
		res.Warnings = append(res.Warnings, "could not be measured: "+err.Error())
	}
	c.res = res
}

// decide fills sizes, conditions, reasons and selection for a listed item.
func (s *scanner) decide(c *candidate) {
	r := s.rules[c.rule]
	it := &c.item
	res := c.res
	it.SizeBytes = res.LogicalBytes
	it.AllocatedBytes = res.AllocatedBytes
	it.ReclaimableBytes = res.ReclaimableBytes
	it.FileCount = res.FileCount
	it.NewestMtime = res.NewestMtime.UTC()
	if res.Status == sizes.StatusPartial {
		it.ScanStatus = plan.ScanPartial
	}
	it.Warnings = append(it.Warnings, res.Warnings...)
	if len(res.Protected) > 0 {
		it.ProtectedPaths = append([]string(nil), res.Protected...)
	}
	refused := len(it.Reasons) > 0

	var reasons []string
	switch r.Tier {
	case rules.TierReview:
		if s.opts.Level != rules.LevelReview {
			reasons = append(reasons, plan.ReasonTierReview)
		}
	case rules.TierManual:
		reasons = append(reasons, plan.ReasonTierManual)
	case rules.TierInfo:
		reasons = append(reasons, plan.ReasonTierInfo)
	case rules.TierSafe:
	default:
		reasons = append(reasons, plan.ReasonTierInfo) // unknown tier: never selected
	}
	if !refused {
		v := s.cond.evaluate(r, c.path, res, it.ScanStatus == plan.ScanPartial)
		reasons = append(reasons, v.reasons...)
		it.Git, it.AtRisk = v.git, v.atRisk
	}
	it.Reasons = append(it.Reasons, reasons...)
	if s.mountsErr != nil && !slices.Contains(it.Reasons, plan.ReasonContainsMount) {
		it.Reasons = append(it.Reasons, plan.ReasonContainsMount)
	}
	it.Selected = len(it.Reasons) == 0
}

func (s *scanner) artifactNames() map[string]bool {
	names := map[string]bool{}
	for _, r := range s.rules {
		if r.Match.Kind == rules.MatchProjectArtifact {
			for _, n := range r.Match.Names {
				names[n] = true
			}
		}
	}
	return names
}

// projectCandidates searches each project root to ProjectDepth for
// directories named like an artifact of a rule that claims that root. It
// never descends into artifact directories, symlinks, or denylisted paths.
func (s *scanner) projectCandidates(roots map[string][]int) []*candidate {
	names := s.artifactNames()
	keys := make([]string, 0, len(roots))
	for k := range roots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []*candidate
	for _, root := range keys {
		idx := roots[root]
		abs := s.home + "/" + root
		if _, ok := policy.ParseRoot(root); !ok {
			for _, i := range idx {
				s.setStatus(i, plan.RuleError)
			}
			s.warn("project root %q is invalid", root)
			continue
		}
		e, err := s.sess.Open(abs)
		if err != nil {
			for _, i := range idx {
				s.rootError(i, abs, err)
			}
			continue
		}
		for _, i := range idx {
			s.setStatus(i, plan.RulePresent)
			s.recordRoot(i, abs, e.Info)
		}
		if e.Dir == nil {
			continue
		}
		_ = e.Dir.Close()
		s.walkProjects(abs, root, 0, idx, names, &out)
	}
	return out
}

func (s *scanner) walkProjects(dir, rel string, depth int, idx []int, names map[string]bool, out *[]*candidate) {
	if depth > ProjectDepth || s.ctx.Err() != nil {
		return
	}
	e, err := s.sess.Open(dir)
	if err != nil || e.Dir == nil {
		return
	}
	entries, err := e.Dir.ReadDir(-1)
	_ = e.Dir.Close()
	if err != nil {
		s.warn("%s: cannot be listed while searching for projects", rel)
		return
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	for _, de := range entries {
		if !de.IsDir() || de.Type()&fs.ModeSymlink != 0 {
			continue
		}
		name := de.Name()
		crel := rel + "/" + name
		if names[name] {
			if depth > 0 { // an artifact directly in a project root is not inside a project
				for _, i := range idx {
					for _, n := range s.rules[i].Match.Names {
						if n == name {
							*out = append(*out, &candidate{rule: i, path: dir + "/" + name})
						}
					}
				}
			}
			continue
		}
		if policy.IsDenylisted(crel) {
			continue
		}
		s.walkProjects(dir+"/"+name, crel, depth+1, idx, names, out)
	}
}

// build orders items, dedupes hard-linked inodes across items, and totals.
func (s *scanner) build(cands []*candidate) plan.Plan {
	order := map[string]int{}
	for i, r := range s.rules {
		order[r.ID] = i
	}
	var listed []*candidate
	for _, c := range cands {
		if c.list {
			listed = append(listed, c)
		}
	}
	sort.SliceStable(listed, func(a, b int) bool {
		if listed[a].rule != listed[b].rule {
			return listed[a].rule < listed[b].rule
		}
		return listed[a].path < listed[b].path
	})
	seen := map[sizes.Inode]bool{}
	p := plan.Plan{
		SchemaVersion: plan.SchemaVersion,
		GeneratedAt:   s.now.UTC().Truncate(time.Second),
		KippleVersion: s.opts.KippleVersion,
		Home:          s.home,
		Level:         s.opts.Level,
		Warnings:      s.warnings,
	}
	ruleBytes := make([]int64, len(s.rules))
	ruleCount := make([]int, len(s.rules))
	for _, c := range listed {
		for id, u := range c.res.Shared {
			if seen[id] {
				c.item.SizeBytes -= u.Logical
				c.item.AllocatedBytes -= u.Allocated
				continue
			}
			seen[id] = true
		}
		it := c.item
		p.Items = append(p.Items, it)
		p.Totals.ListedCount++
		p.Totals.ListedBytes += it.AllocatedBytes
		if it.Selected {
			p.Totals.SelectedCount++
			p.Totals.SelectedBytes += it.AllocatedBytes
			p.Totals.ReclaimableBytes += it.ReclaimableBytes
		}
		ruleBytes[c.rule] += it.AllocatedBytes
		ruleCount[c.rule]++
	}
	for i, r := range s.rules {
		st := s.status[i]
		if st == "" {
			st = plan.RuleAbsent
		}
		p.Rules = append(p.Rules, plan.RuleStatus{ID: r.ID, Tier: string(r.Tier), Status: st, ItemCount: ruleCount[i], Bytes: ruleBytes[i], Roots: s.roots[i]})
	}
	p.Normalize()
	return p
}

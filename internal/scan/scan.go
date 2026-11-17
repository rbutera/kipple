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
// Wave 2 does not evaluate git or running-process conditions: project
// artifacts carry git.state "unchecked" and reason git_unchecked and are
// never selected, and owner_running is not checked. Wave 3 adds both.
package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

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

	warnings []string
	status   []string // per rule
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

	s := &scanner{ctx: ctx, opts: opts, rules: rs, sess: sess, home: opts.Env.Home, now: opts.Now(), status: make([]string, len(rs))}
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
	idle := map[string]bool{}
	for _, c := range cands {
		if c.list {
			s.decide(c, idle)
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
	if e.Dir == nil {
		c.res = sizes.MeasureFile(e.Info)
		return
	}
	defer func() { _ = e.Dir.Close() }()
	opts := sizes.Options{}
	if c.guard {
		g := s.sess.Guard(s.rules[c.rule], c.path)
		opts.Protected = func(rel, name string, dev, ino uint64, isDir bool) bool {
			_, hit := g.Check(rel, name, dev, ino, isDir)
			return hit
		}
	}
	res, err := sizes.MeasureDir(s.ctx, e.Dir, opts)
	if err != nil && res.Status == "" {
		res.Status = sizes.StatusPartial
		res.Warnings = append(res.Warnings, "could not be measured: "+err.Error())
	}
	c.res = res
}

// decide fills sizes, conditions, reasons and selection for a listed item.
func (s *scanner) decide(c *candidate, idle map[string]bool) {
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
		if days := r.Conditions.MinAgeDays; days > 0 && s.now.Sub(res.NewestMtime) < time.Duration(days)*24*time.Hour {
			reasons = append(reasons, plan.ReasonTooRecent)
		}
		if r.Match.Kind == rules.MatchProjectArtifact {
			it.Git = &plan.Git{State: plan.GitUnchecked}
			if r.Conditions.RequiresIdleProject && !s.projectIdle(filepath.Dir(c.path), r.Conditions.IdleDays, idle) {
				reasons = append(reasons, plan.ReasonProjectActive)
			}
			if r.Conditions.RequiresCleanGit {
				// TODO(wave 3): gitstate.Inspect replaces this placeholder.
				reasons = append(reasons, plan.ReasonGitUnchecked)
			}
		}
		// An item that was not fully inspected may hide something protected.
		if it.ScanStatus == plan.ScanPartial {
			reasons = append(reasons, plan.ReasonScanPartial)
		}
		if res.ProtectedCount > 0 && r.Tier != rules.TierInfo {
			reasons = append(reasons, plan.ReasonContainsProtected)
		}
	}
	it.Reasons = append(it.Reasons, reasons...)
	it.Selected = len(it.Reasons) == 0
}

// projectIdle reports whether no file in project outside artifact
// directories changed within days. Results are cached per project.
func (s *scanner) projectIdle(project string, days int, cache map[string]bool) bool {
	key := fmt.Sprintf("%s\x00%d", project, days)
	if v, ok := cache[key]; ok {
		return v
	}
	idle := false
	if e, err := s.sess.Open(project); err == nil && e.Dir != nil {
		names := s.artifactNames()
		res, err := sizes.MeasureDir(s.ctx, e.Dir, sizes.Options{SkipDir: func(_, name string) bool { return names[name] }})
		_ = e.Dir.Close()
		// An incomplete walk cannot prove the project idle.
		idle = err == nil && res.Status == sizes.StatusComplete &&
			s.now.Sub(res.NewestFileMtime) >= time.Duration(days)*24*time.Hour
	}
	cache[key] = idle
	return idle
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
		p.Rules = append(p.Rules, plan.RuleStatus{ID: r.ID, Tier: string(r.Tier), Status: st, ItemCount: ruleCount[i], Bytes: ruleBytes[i]})
	}
	p.Normalize()
	return p
}

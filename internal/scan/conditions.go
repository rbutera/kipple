package scan

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/rbutera/kipple/internal/gitstate"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/sizes"
)

// GitInspector classifies a repository; gitstate.Inspector implements it.
type GitInspector interface {
	Inspect(ctx context.Context, dir string) gitstate.Result
}

// conditions evaluates rule conditions for items, for a scan (with caches
// shared across items) and for apply's re-check (fresh each time). Scan and
// apply share it so their decisions cannot drift.
type conditions struct {
	ctx   context.Context
	sess  *policy.Session
	rules []rules.Rule
	now   time.Time
	git   GitInspector

	mu    sync.Mutex
	idle  map[string]bool            // nil: no caching
	repos map[string]gitstate.Result // nil: no caching
}

// verdict is the outcome of evaluating one item's conditions.
type verdict struct {
	reasons []string
	git     *plan.Git
	atRisk  bool
}

// evaluate returns the reasons item (at path, measured as res) of rule r is
// not selectable now, in plan order: too_recent, git state, project_active,
// scan_partial, contains_mount, contains_protected. The git state comes
// before project_active because it is the more serious one (work that is
// not committed or not pushed), and apply reports an item's first reason.
func (c *conditions) evaluate(r rules.Rule, itemPath string, res sizes.Result, partial bool) verdict {
	var v verdict
	// A partial walk cannot prove the item old enough: the unread part may
	// hold newer files.
	if days := r.Conditions.MinAgeDays; days > 0 && (partial || c.now.Sub(res.NewestMtime) < time.Duration(days)*24*time.Hour) {
		v.reasons = append(v.reasons, plan.ReasonTooRecent)
	}
	if r.Conditions.RequiresCleanGit {
		repo := itemPath // a worktree is its own repository
		if r.Match.Kind == rules.MatchProjectArtifact {
			repo = filepath.Dir(itemPath)
		}
		g := c.gitState(repo)
		v.git = &plan.Git{State: g.State, Detail: g.Detail}
		if g.State != gitstate.StateCleanPushed {
			v.reasons = append(v.reasons, GitReason(g.State))
			v.atRisk = g.State == gitstate.StateDirty || g.State == gitstate.StateUnpushed
		}
	}
	if r.Match.Kind == rules.MatchProjectArtifact && r.Conditions.RequiresIdleProject && !c.projectIdle(filepath.Dir(itemPath), r.Conditions.IdleDays) {
		v.reasons = append(v.reasons, plan.ReasonProjectActive)
	}
	// An item that was not fully inspected may hide something protected.
	if partial {
		v.reasons = append(v.reasons, plan.ReasonScanPartial)
	}
	if len(res.Mounts) > 0 {
		v.reasons = append(v.reasons, plan.ReasonContainsMount)
	}
	if res.ProtectedCount > 0 && r.Tier != rules.TierInfo {
		v.reasons = append(v.reasons, plan.ReasonContainsProtected)
	}
	return v
}

// GitReason maps a git state to the item reason that blocks it.
func GitReason(state string) string {
	switch state {
	case gitstate.StateDirty:
		return plan.ReasonGitDirty
	case gitstate.StateUnpushed:
		return plan.ReasonGitUnpushed
	case gitstate.StateNoUpstream:
		return plan.ReasonGitNoUpstream
	case gitstate.StateNotARepo:
		return plan.ReasonGitNotARepo
	default:
		return plan.ReasonGitUnavailable
	}
}

func (c *conditions) gitState(repo string) gitstate.Result {
	c.mu.Lock()
	if g, ok := c.repos[repo]; ok {
		c.mu.Unlock()
		return g
	}
	c.mu.Unlock()
	g := c.git.Inspect(c.ctx, repo)
	c.mu.Lock()
	if c.repos != nil {
		c.repos[repo] = g
	}
	c.mu.Unlock()
	return g
}

// projectIdle reports whether no file in project outside artifact
// directories changed within days.
//
// An artifact directory is one named like a project artifact with one of
// that artifact's markers beside it as a regular file (spec "Marker
// adjacency"): the candidate itself, its siblings such as .venv beside
// pyproject.toml, and nested installs with their own manifest. A folder
// that merely shares a name (a target/ with no Cargo.toml beside it) is
// project content, so recent edits inside it keep the project active.
func (c *conditions) projectIdle(project string, days int) bool {
	key := fmt.Sprintf("%s\x00%d", project, days)
	c.mu.Lock()
	if v, ok := c.idle[key]; ok {
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()
	idle := false
	if e, err := c.sess.Open(project); err == nil && e.Dir != nil {
		markers := c.artifactMarkers()
		skip := func(rel, name string) bool {
			ms, ok := markers[name]
			if !ok {
				return false
			}
			parent := project
			if d := path.Dir(rel); d != "." {
				parent += "/" + d
			}
			return c.hasRegularFile(parent, ms)
		}
		res, err := sizes.MeasureDir(c.ctx, e.Dir, sizes.Options{SkipDir: skip, MountRoot: c.sess.MountRoot})
		_ = e.Dir.Close()
		// An incomplete walk cannot prove the project idle.
		idle = err == nil && res.Status == sizes.StatusComplete &&
			c.now.Sub(res.NewestFileMtime) >= time.Duration(days)*24*time.Hour
	}
	c.mu.Lock()
	if c.idle != nil {
		c.idle[key] = idle
	}
	c.mu.Unlock()
	return idle
}

// artifactMarkers maps each project-artifact directory name to the markers
// that make a directory of that name an artifact.
func (c *conditions) artifactMarkers() map[string][]string {
	out := map[string][]string{}
	for _, r := range c.rules {
		if r.Match.Kind == rules.MatchProjectArtifact {
			for _, n := range r.Match.Names {
				out[n] = append(out[n], r.Match.Markers...)
			}
		}
	}
	return out
}

// hasRegularFile reports whether dir holds a regular file named one of
// names, opened through the session (no symlink is followed).
func (c *conditions) hasRegularFile(dir string, names []string) bool {
	for _, n := range names {
		e, err := c.sess.Open(dir + "/" + n)
		if err != nil {
			continue
		}
		if e.Dir != nil {
			_ = e.Dir.Close()
		}
		if e.Info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// guardOptions returns the size-walk options that report protected
// descendants of item for rule r.
func guardOptions(sess *policy.Session, r rules.Rule, item string) sizes.Options {
	g := sess.Guard(r, item)
	return sizes.Options{ProtectedMode: func(rel, name string, dev, ino uint64, mode fs.FileMode) bool {
		_, hit := g.CheckMode(rel, name, dev, ino, mode)
		return hit
	}, MountRoot: sess.MountRoot}
}

// Checker re-evaluates an item's conditions the way a scan does: it
// measures the item again with the protected-descendant guard and checks
// age, idle project and git state, with nothing cached. Apply calls it
// immediately before each move (specs "Re-check before every move").
type Checker struct {
	c *conditions
}

// NewChecker returns a Checker using sess (which the caller keeps open and
// closes), the effective rules, and git (gitstate.Inspector with home's
// parent as ceiling when nil).
func NewChecker(ctx context.Context, sess *policy.Session, rs []rules.Rule, git GitInspector, now time.Time) *Checker {
	if git == nil {
		git = gitstate.Inspector{Ceiling: filepath.Dir(sess.Home())}
	}
	return &Checker{c: &conditions{ctx: ctx, sess: sess, rules: rs, now: now, git: git}}
}

// Recheck is the outcome of Checker.Check.
type Recheck struct {
	// Reasons block the item now; empty when it may move.
	Reasons []string
	Git     *plan.Git
	AtRisk  bool
	// Measure is the fresh measurement.
	Measure sizes.Result
}

// Check measures the item at itemPath of rule r and evaluates its
// conditions. dir is the item opened by the caller (verified to be the
// approved object) for a directory, nil for a file with info fi. Tier is
// not considered: the caller decides which tiers it acts on.
func (k *Checker) Check(r rules.Rule, itemPath string, dir *os.File, fi fs.FileInfo) Recheck {
	var res sizes.Result
	if dir == nil {
		res = sizes.MeasureFile(fi)
	} else {
		var err error
		res, err = sizes.MeasureDir(k.c.ctx, dir, guardOptions(k.c.sess, r, itemPath))
		if err != nil && res.Status == "" {
			res.Status = sizes.StatusPartial
		}
	}
	v := k.c.evaluate(r, itemPath, res, res.Status == sizes.StatusPartial)
	return Recheck{Reasons: v.reasons, Git: v.git, AtRisk: v.atRisk, Measure: res}
}

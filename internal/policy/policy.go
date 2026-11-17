// Package policy is kipple's single path-authorisation gate. Nothing is
// listed as movable, moved or restored unless Authorize approves it, and apply
// calls Authorize again immediately before each move.
//
// The package depends only on the standard library so it can be exhaustively
// table-tested. It never reads the real home directory: Env.Home is injected.
//
// Authorize checks, in order:
//
//  1. Env.Home and the path are absolute and clean (no "..", no "//", no
//     trailing slash): otherwise ReasonNotAbsolute.
//  2. The path is not home itself (ReasonIsHome) and descends from home,
//     compared case-sensitively (ReasonOutsideHome).
//  3. The path is not, is not inside, and does not contain a denylisted
//     location, compared case-insensitively (ReasonDenylisted).
//  4. Likewise for Env.Protect entries from user config (ReasonUserProtected).
//  5. The path is not one of the rule's roots (ReasonIsRuleRoot) and descends
//     from one of them, outside the rule's exclusions (ReasonOutsideRuleRoots).
//  6. Every component from home down to the path exists (ReasonMissing), is
//     not a symlink (ReasonSymlinkInPath), and is on home's device
//     (ReasonDifferentDevice). Every component above the path must be a
//     directory. Symlinks are never followed.
//
// Lexical refusals (1 to 5) come before filesystem checks so a denylisted path
// is refused with ReasonDenylisted whether or not it exists.
package policy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Refusal reasons. This is a closed set; see Reasons.
const (
	ReasonNotAbsolute      = "not_absolute"
	ReasonIsHome           = "is_home"
	ReasonOutsideHome      = "outside_home"
	ReasonDenylisted       = "denylisted"
	ReasonUserProtected    = "user_protected"
	ReasonIsRuleRoot       = "is_rule_root"
	ReasonOutsideRuleRoots = "outside_rule_roots"
	ReasonMissing          = "missing"
	ReasonSymlinkInPath    = "symlink_in_path"
	ReasonDifferentDevice  = "different_device"
)

// Reasons returns every refusal reason Authorize can produce.
func Reasons() []string {
	return []string{
		ReasonNotAbsolute,
		ReasonIsHome,
		ReasonOutsideHome,
		ReasonDenylisted,
		ReasonUserProtected,
		ReasonIsRuleRoot,
		ReasonOutsideRuleRoots,
		ReasonMissing,
		ReasonSymlinkInPath,
		ReasonDifferentDevice,
	}
}

// Env is the context for a decision. Home is injected by cmd/ (from
// KIPPLE_HOME or the real home); the library never resolves it. Protect holds
// absolute paths from user config that are refused like denylist entries.
type Env struct {
	Home    string
	Protect []string
}

// Rule is the part of a catalogue rule the policy needs. internal/rules.Rule
// implements it; StaticRule is a plain implementation for tests and callers
// that build effective rules (for example with configured project roots).
//
// Roots are home-relative, slash-separated, and may contain at most one
// segment with glob metacharacters (filepath.Match syntax). Excludes are
// patterns matched against the first path component below a root.
type Rule interface {
	RuleID() string
	RuleRoots() []string
	RuleExcludes() []string
}

// StaticRule is a value implementation of Rule.
type StaticRule struct {
	ID      string
	Roots   []string
	Exclude []string
}

func (r StaticRule) RuleID() string         { return r.ID }
func (r StaticRule) RuleRoots() []string    { return r.Roots }
func (r StaticRule) RuleExcludes() []string { return r.Exclude }

// Decision is the outcome of Authorize. Reason is empty when Allowed.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

func refuse(reason string) Decision { return Decision{Reason: reason} }

// Test hooks. Production code uses os.Lstat and the real device number.
var (
	lstat    = os.Lstat
	deviceOf = func(_ string, fi fs.FileInfo) (uint64, bool) { return device(fi) }
)

// Authorize decides whether rule may move path. See the package comment for
// the order of checks.
func Authorize(env Env, rule Rule, path string) Decision {
	home := env.Home
	if !isCanonicalAbs(home) || home == "/" {
		return refuse(ReasonNotAbsolute)
	}
	if !isCanonicalAbs(path) {
		return refuse(ReasonNotAbsolute)
	}
	if path == home {
		return refuse(ReasonIsHome)
	}
	if !strings.HasPrefix(path, home+"/") {
		if strings.EqualFold(path, home) {
			return refuse(ReasonIsHome)
		}
		return refuse(ReasonOutsideHome)
	}
	rel := path[len(home)+1:]
	comps := strings.Split(rel, "/")

	if isDenylisted(comps) {
		return refuse(ReasonDenylisted)
	}
	if isProtected(env.Protect, path) {
		return refuse(ReasonUserProtected)
	}
	if d := checkRule(rule, comps); !d.Allowed {
		return d
	}
	return checkFilesystem(home, comps)
}

// isCanonicalAbs reports whether p is absolute and already clean.
func isCanonicalAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsRune(p, 0)
}

// isProtected: the path equals, descends from, or contains a protected path.
func isProtected(protect []string, path string) bool {
	lp := strings.ToLower(path)
	for _, p := range protect {
		if p == "" {
			continue
		}
		lq := strings.ToLower(filepath.Clean(p))
		if lp == lq || strings.HasPrefix(lp, lq+"/") || strings.HasPrefix(lq, lp+"/") {
			return true
		}
	}
	return false
}

func checkRule(rule Rule, comps []string) Decision {
	if rule == nil {
		return refuse(ReasonOutsideRuleRoots)
	}
	excludes := rule.RuleExcludes()
	isRoot, under := false, false
	for _, root := range rule.RuleRoots() {
		rsegs, ok := ParseRoot(root)
		if !ok {
			continue // malformed roots authorise nothing
		}
		if len(comps) < len(rsegs) || !segmentsMatch(rsegs, comps[:len(rsegs)], false) {
			continue
		}
		if len(comps) == len(rsegs) {
			isRoot = true
			continue
		}
		if excluded(excludes, comps[len(rsegs)]) {
			continue
		}
		under = true
	}
	switch {
	case isRoot:
		return refuse(ReasonIsRuleRoot)
	case under:
		return Decision{Allowed: true}
	default:
		return refuse(ReasonOutsideRuleRoots)
	}
}

// ParseRoot validates a rule root and splits it into segments. A valid root
// is home-relative, clean, slash-separated, has no "." or ".." segment, and
// has at most one segment containing glob metacharacters (and never "**").
func ParseRoot(root string) ([]string, bool) {
	if root == "" || strings.HasPrefix(root, "/") || strings.HasPrefix(root, "~") || strings.Contains(root, "**") {
		return nil, false
	}
	if filepath.ToSlash(filepath.Clean(root)) != root {
		return nil, false
	}
	segs := strings.Split(root, "/")
	globs := 0
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return nil, false
		}
		if hasMeta(s) {
			if _, err := filepath.Match(s, ""); err != nil {
				return nil, false
			}
			globs++
		}
	}
	if globs > 1 {
		return nil, false
	}
	return segs, true
}

func hasMeta(s string) bool { return strings.ContainsAny(s, `*?[\`) }

// segmentsMatch matches path segments against pattern segments. Allowing
// checks (rule roots) are case-sensitive; refusing checks fold case.
func segmentsMatch(patterns, segs []string, fold bool) bool {
	for i, p := range patterns {
		s := segs[i]
		if fold {
			p, s = strings.ToLower(p), strings.ToLower(s)
		}
		if hasMeta(p) {
			if ok, err := filepath.Match(p, s); err != nil || !ok {
				return false
			}
		} else if p != s {
			return false
		}
	}
	return true
}

// excluded matches a child name against a rule's exclusions, folding case
// (an exclusion refuses, so it errs towards matching).
func excluded(patterns []string, name string) bool {
	ln := strings.ToLower(name)
	for _, p := range patterns {
		if ok, err := filepath.Match(strings.ToLower(p), ln); err == nil && ok {
			return true
		}
	}
	return false
}

// checkFilesystem lstats every component from home down to the path.
func checkFilesystem(home string, comps []string) Decision {
	hfi, err := lstat(home)
	if err != nil {
		return refuse(ReasonMissing)
	}
	if hfi.Mode()&fs.ModeSymlink != 0 {
		return refuse(ReasonSymlinkInPath)
	}
	if !hfi.IsDir() {
		return refuse(ReasonMissing)
	}
	homeDev, ok := deviceOf(home, hfi)
	if !ok {
		return refuse(ReasonDifferentDevice)
	}
	cur := home
	for i, c := range comps {
		cur = cur + "/" + c
		fi, err := lstat(cur)
		if err != nil {
			// Not found, not a directory, or not inspectable (permission
			// denied): all refused as missing, since nothing can be verified.
			return refuse(ReasonMissing)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return refuse(ReasonSymlinkInPath)
		}
		dev, ok := deviceOf(cur, fi)
		if !ok || dev != homeDev {
			return refuse(ReasonDifferentDevice)
		}
		if i < len(comps)-1 && !fi.IsDir() {
			return refuse(ReasonMissing)
		}
	}
	return Decision{Allowed: true}
}

// ErrRunningAsRoot is returned by CheckNotRoot for euid 0. The CLI prints it
// and exits 1.
var ErrRunningAsRoot = errors.New("kipple will not run as root")

// CheckNotRoot refuses effective user ID 0.
func CheckNotRoot(euid int) error {
	if euid == 0 {
		return ErrRunningAsRoot
	}
	return nil
}

// RefuseRoot applies CheckNotRoot to the current process.
func RefuseRoot() error { return CheckNotRoot(os.Geteuid()) }

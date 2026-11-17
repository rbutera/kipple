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
//  3. The rule is movable: info-tier rules are measured only and never
//     approve anything (ReasonInfoTier).
//  4. The path is not, is not inside, and does not contain a denylisted
//     location, compared with APFS-style Unicode case folding (see fold.go)
//     (ReasonDenylisted).
//  5. Likewise for Env.Protect entries from user config (ReasonUserProtected).
//     Relative entries are home-relative; "/" protects everything.
//  6. The path is not one of the rule's roots (ReasonIsRuleRoot) and descends
//     from one of them, outside the rule's exclusions (ReasonOutsideRuleRoots).
//  7. The path has the shape of one of the rule's items (ReasonNotRuleItem):
//     a direct child of the root for MatchChildren; a direct child with one
//     of the rule's extensions for MatchExtensions; for MatchProjectArtifact,
//     a directory named one of the rule's names, at least two levels below
//     the root (root/project/name) and not inside another such directory.
//  8. Every component from home down to the path exists (ReasonMissing), is
//     not a symlink (ReasonSymlinkInPath), and is on home's device
//     (ReasonDifferentDevice). Every component above the path must be a
//     directory. Symlinks are never followed. An extensions item must be a
//     regular file and a project artifact a directory (ReasonNotRuleItem).
//  9. Identity: no component of the path, nor the path itself, has the same
//     (dev, ino) as an existing denylist location or Env.Protect entry, and
//     the path is not a directory containing one (ReasonDenylisted,
//     ReasonUserProtected). This catches spellings that folding misses, such
//     as NFC/NFD variants, since APFS resolves them to the same object.
//
// Lexical refusals (1 to 7) come before filesystem checks so a denylisted path
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
	ReasonInfoTier         = "info_tier"
	ReasonNotRuleItem      = "not_rule_item"
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
		ReasonInfoTier,
		ReasonNotRuleItem,
	}
}

// Match kinds, mirroring internal/rules.MatchKind values.
const (
	MatchChildren        = "children"
	MatchExtensions      = "extensions"
	MatchProjectArtifact = "project_artifact"
)

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
//
// RuleMovable is false for info-tier rules, which Authorize always refuses
// with ReasonInfoTier. RuleMatchKind is one of the Match* constants (any
// other value authorises nothing); RuleMatchExtensions and RuleMatchNames
// give the item shape for MatchExtensions and MatchProjectArtifact.
type Rule interface {
	RuleID() string
	RuleRoots() []string
	RuleExcludes() []string
	RuleMovable() bool
	RuleMatchKind() string
	RuleMatchExtensions() []string
	RuleMatchNames() []string
}

// StaticRule is a value implementation of Rule. Info marks an info-tier
// rule (the zero value is movable). An empty Kind means MatchChildren.
type StaticRule struct {
	ID         string
	Roots      []string
	Exclude    []string
	Info       bool
	Kind       string
	Extensions []string
	Names      []string
}

func (r StaticRule) RuleID() string                { return r.ID }
func (r StaticRule) RuleRoots() []string           { return r.Roots }
func (r StaticRule) RuleExcludes() []string        { return r.Exclude }
func (r StaticRule) RuleMovable() bool             { return !r.Info }
func (r StaticRule) RuleMatchExtensions() []string { return r.Extensions }
func (r StaticRule) RuleMatchNames() []string      { return r.Names }
func (r StaticRule) RuleMatchKind() string {
	if r.Kind == "" {
		return MatchChildren
	}
	return r.Kind
}

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
	if rule != nil && !rule.RuleMovable() {
		return refuse(ReasonInfoTier)
	}
	rel := path[len(home)+1:]
	comps := strings.Split(rel, "/")

	if isDenylisted(comps) {
		return refuse(ReasonDenylisted)
	}
	if isProtected(home, env.Protect, path) {
		return refuse(ReasonUserProtected)
	}
	d, want := checkRule(rule, comps)
	if !d.Allowed {
		return d
	}
	return checkFilesystem(home, comps, want, collectProtectedIDs(home, env.Protect))
}

// itemType is the file type an item must have, checked with lstat.
type itemType int

const (
	anyType itemType = iota
	regularFile
	directory
)

// isCanonicalAbs reports whether p is absolute and already clean.
func isCanonicalAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsRune(p, 0)
}

// protectPath returns the absolute, clean form of an Env.Protect entry:
// absolute entries are cleaned and relative ones are joined onto home.
func protectPath(home, p string) (string, bool) {
	if p == "" {
		return "", false
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), true
	}
	return filepath.Join(home, p), true
}

// isProtected: the path equals, descends from, or contains a protected path,
// compared with foldName.
func isProtected(home string, protect []string, path string) bool {
	lp := foldName(path)
	for _, p := range protect {
		abs, ok := protectPath(home, p)
		if !ok {
			continue
		}
		if abs == "/" {
			return true
		}
		lq := foldName(abs)
		if lp == lq || strings.HasPrefix(lp, lq+"/") || strings.HasPrefix(lq, lp+"/") {
			return true
		}
	}
	return false
}

func checkRule(rule Rule, comps []string) (Decision, itemType) {
	if rule == nil {
		return refuse(ReasonOutsideRuleRoots), anyType
	}
	excludes := rule.RuleExcludes()
	isRoot, under, ok := false, false, false
	want := anyType
	for _, root := range rule.RuleRoots() {
		rsegs, valid := ParseRoot(root)
		if !valid {
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
		if t, shaped := itemShape(rule, comps[len(rsegs):]); shaped {
			ok, want = true, t
		}
	}
	switch {
	case isRoot:
		return refuse(ReasonIsRuleRoot), anyType
	case ok:
		return Decision{Allowed: true}, want
	case under:
		return refuse(ReasonNotRuleItem), anyType
	default:
		return refuse(ReasonOutsideRuleRoots), anyType
	}
}

// itemShape checks rest, the components below a matched root, against the
// rule's item shape and returns the file type the item must have.
func itemShape(rule Rule, rest []string) (itemType, bool) {
	last := rest[len(rest)-1]
	switch rule.RuleMatchKind() {
	case MatchChildren:
		return anyType, len(rest) == 1
	case MatchExtensions:
		return regularFile, len(rest) == 1 && hasExtension(last, rule.RuleMatchExtensions())
	case MatchProjectArtifact:
		names := rule.RuleMatchNames()
		if len(rest) < 2 || !contains(names, last) {
			return anyType, false
		}
		for _, c := range rest[:len(rest)-1] {
			if contains(names, c) {
				return anyType, false // inside another artifact directory
			}
		}
		return directory, true
	default:
		return anyType, false
	}
}

// hasExtension: name has a non-empty stem and ends in one of exts, ignoring
// ASCII case.
func hasExtension(name string, exts []string) bool {
	ext := filepath.Ext(name)
	if ext == "" || len(ext) == len(name) {
		return false
	}
	for _, e := range exts {
		if e != "" && strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ParseRoot validates a rule root and splits it into segments. A valid root
// is home-relative, clean, slash-separated, valid UTF-8 without confusable
// runes (see ConfusableRune), has no "." or ".." segment, and has at most one
// segment containing glob metacharacters (and never "**").
func ParseRoot(root string) ([]string, bool) {
	if root == "" || strings.HasPrefix(root, "/") || strings.HasPrefix(root, "~") || strings.Contains(root, "**") {
		return nil, false
	}
	if _, bad := ConfusableRune(root); bad {
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

// Overlaps reports whether two home-relative slash paths are equal or one
// lies inside the other, comparing segments with foldName. Either may contain
// glob segments; a malformed glob counts as overlapping, since callers use
// this to refuse.
func Overlaps(a, b string) bool {
	as := strings.Split(strings.Trim(a, "/"), "/")
	bs := strings.Split(strings.Trim(b, "/"), "/")
	for i := 0; i < min(len(as), len(bs)); i++ {
		x, y := foldName(as[i]), foldName(bs[i])
		if x == y || globOverlap(x, y) || globOverlap(y, x) {
			continue
		}
		return false
	}
	return true
}

func globOverlap(pattern, name string) bool {
	if !hasMeta(pattern) {
		return false
	}
	ok, err := filepath.Match(pattern, name)
	return err != nil || ok
}

// segmentsMatch matches path segments against pattern segments. Allowing
// checks (rule roots) are case-sensitive; refusing checks fold with foldName.
func segmentsMatch(patterns, segs []string, fold bool) bool {
	for i, p := range patterns {
		s := segs[i]
		if fold {
			p, s = foldName(p), foldName(s)
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

// excluded matches a child name against a rule's exclusions, folding case.
// An exclusion refuses, so it errs towards matching: a malformed pattern
// excludes everything.
func excluded(patterns []string, name string) bool {
	ln := foldName(name)
	for _, p := range patterns {
		ok, err := filepath.Match(foldName(p), ln)
		if err != nil || ok {
			return true
		}
	}
	return false
}

// checkFilesystem lstats every component from home down to the path and
// compares each with the identities of protected objects.
func checkFilesystem(home string, comps []string, want itemType, ids *protectedIDs) Decision {
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
		id, ok := identityOf(fi)
		if !ok {
			return refuse(ReasonDifferentDevice)
		}
		if reason, hit := ids.refusal(id, i == len(comps)-1); hit {
			return refuse(reason)
		}
		if i < len(comps)-1 && !fi.IsDir() {
			return refuse(ReasonMissing)
		}
		if i == len(comps)-1 {
			if (want == regularFile && !fi.Mode().IsRegular()) || (want == directory && !fi.IsDir()) {
				return refuse(ReasonNotRuleItem)
			}
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

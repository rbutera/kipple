package policy

import "strings"

// denyPrefixes are home-relative locations refused along with everything
// beneath them and every ancestor that contains them. Segments may use
// filepath.Match globs. Matching folds case (APFS is case-insensitive).
//
// This list is a constant of the program. User config can add protections
// (Env.Protect) but can never remove an entry. docs/safety.md is checked
// against Denylist() by a test.
var denyPrefixes = [...]string{
	// Personal files and iCloud.
	"Documents",
	"Desktop",
	"Pictures",
	"Pictures/Photos Library.photoslibrary",
	"Movies",
	"Music",
	"Library/Mobile Documents",
	"Library/CloudStorage",
	"Library/Mail",
	"Library/Messages",
	"Library/Photos",
	"Library/Application Support/MobileSync",

	// System state and sandboxed app data.
	"Library/Keychains",
	"Library/Preferences",
	"Library/Containers",
	"Library/Group Containers",

	// Credentials.
	".ssh",
	".gnupg",
	".aws",
	".config/op",
	".config/gh",
	".kube",
	".netrc",
	".npmrc",
	".docker/config.json",

	// Agent state that is not a cache.
	".ark",
	".claude/projects",
	".claude/memory",
	".claude/plans",
	".claude/skills",
	".claude/settings*.json",
	".claude/.credentials.json",
	".claude.json",
	".codex/sessions",
	".codex/memory",
	".codex/memories",
	".codex/auth.json",
}

// denyComponents refuse a path if any single component matches, at any depth.
var denyComponents = [...]string{
	".env",
	".env.*",
	"*.keychain*",
	"*.photoslibrary",
}

// Denylist returns the built-in denylist for documentation and tests:
// home-relative prefixes as written, then any-depth component patterns
// prefixed with "**/".
func Denylist() []string {
	out := make([]string, 0, len(denyPrefixes)+len(denyComponents))
	out = append(out, denyPrefixes[:]...)
	for _, c := range denyComponents {
		out = append(out, "**/"+c)
	}
	return out
}

// IsDenylisted reports whether the home-relative slash path rel is refused by
// the built-in denylist. Config validation uses it.
func IsDenylisted(rel string) bool {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return false
	}
	return isDenylisted(strings.Split(rel, "/"))
}

// isDenylisted: comps (home-relative components) equal, descend from, or
// contain a denied prefix, or any component matches a denied component.
func isDenylisted(comps []string) bool {
	for _, c := range comps {
		for _, pat := range denyComponents {
			if segmentsMatch([]string{pat}, []string{c}, true) {
				return true
			}
		}
	}
	for _, entry := range denyPrefixes {
		esegs := strings.Split(entry, "/")
		n := len(esegs)
		if len(comps) < n {
			n = len(comps)
		}
		// Equal or descendant (len(comps) >= len(esegs)), or ancestor
		// (len(comps) < len(esegs)): the shared prefix must match.
		if segmentsMatch(esegs[:n], comps[:n], true) {
			return true
		}
	}
	return false
}

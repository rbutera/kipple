package policy

import "strings"

// denyPrefixes are home-relative locations refused along with everything
// beneath them and every ancestor that contains them. Segments may use
// filepath.Match globs. Matching uses foldName (APFS folds case and some
// Unicode letters), and the filesystem phase also compares identities.
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
	"Library/Application Support/CloudDocs",
	"Library/Application Support/AddressBook",
	"Library/Calendars",
	"Library/Autosave Information",

	// System state, accounts and sandboxed app data.
	"Library/Keychains",
	"Library/Preferences",
	"Library/Containers",
	"Library/Group Containers",
	"Library/Accounts",
	"Library/IdentityServices",
	"Library/Application Support/com.apple.TCC",

	// Browser profiles, cookies and web storage.
	"Library/Safari",
	"Library/Cookies",
	"Library/HTTPStorages",
	"Library/Application Support/Google/Chrome",
	"Library/Application Support/BraveSoftware",
	"Library/Application Support/Microsoft Edge",
	"Library/Application Support/Arc",
	"Library/Application Support/Firefox",

	// Password managers.
	"Library/Application Support/1Password",
	"Library/Application Support/Bitwarden",
	".password-store",

	// Editor and app settings (their Cache folders stay cleanable).
	"Library/Application Support/Code/User",
	"Library/Application Support/Cursor/User",
	"Library/Application Support/Windsurf/User",
	"Library/Application Support/Claude",

	// Developer identity and device state. CoreSimulator Devices and
	// Runtimes are info-tier: measured, never moved.
	"Library/Developer/Xcode/UserData",
	"Library/MobileDevice/Provisioning Profiles",
	"Library/Developer/CoreSimulator/Devices",
	"Library/Developer/CoreSimulator/Profiles/Runtimes",

	// Credentials.
	".ssh",
	".gnupg",
	".aws",
	".config/op",
	".config/gh",
	".kube",
	".netrc",
	".npmrc",
	".docker",
	".config/gcloud",
	".azure",
	".terraform.d",
	".git-credentials",
	".pgpass",
	".vault-token",
	".pypirc",
	".gem/credentials",
	".cargo/credentials*",

	// Shell history.
	".zsh_history",
	".bash_history",

	// Agent state that is not a cache.
	".ark",
	".claude/projects",
	".claude/memory",
	".claude/plans",
	".claude/skills",
	".claude/agents",
	".claude/commands",
	".claude/CLAUDE.md",
	".claude/settings*.json",
	".claude/.credentials.json",
	".claude.json",
	".codex/sessions",
	".codex/memory",
	".codex/memories",
	".codex/auth.json",
	".codex/config.toml",
	".codex/prompts",

	// Models are info-tier: measured, never moved.
	".ollama",
}

// denyComponents refuse a path if any single component matches, at any depth.
var denyComponents = [...]string{
	".env",
	".env.*",
	"*.env", // prod.env, staging.env
	".envrc",
	".git",
	"*.keychain*",
	"*.photoslibrary",
	"*.kdbx",
	"*.kdb", // KeePass 1
	"*.pem",
	"*.p12",
	"id_rsa*",
	"id_ed25519*",
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

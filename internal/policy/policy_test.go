package policy

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

var (
	cachesRule = StaticRule{
		ID:      "caches.user",
		Roots:   []string{"Library/Caches"},
		Exclude: []string{"com.apple.bird", "CloudKit", "com.apple.cloudkit", "com.apple.icloud*", "Homebrew"},
		Kind:    MatchChildren,
	}
	derivedDataRule = StaticRule{ID: "xcode.deriveddata", Roots: []string{"Library/Developer/Xcode/DerivedData"}, Kind: MatchChildren}
	mailRule        = StaticRule{ID: "hypothetical.mail", Roots: []string{"Library/Mail/V10"}, Kind: MatchChildren}
	editorRule      = StaticRule{ID: "editor.glob", Roots: []string{"Library/Application Support/*/Cache"}, Kind: MatchChildren}
	libraryRule     = StaticRule{ID: "hypothetical.library", Roots: []string{"Library"}, Kind: MatchChildren}
	claudeRule      = StaticRule{ID: "hypothetical.claude", Roots: []string{".claude"}, Kind: MatchChildren}
	anyRule         = StaticRule{ID: "hypothetical.any", Roots: []string{"Library/Caches", "dev", "Library/Logs"}, Kind: MatchChildren}
	downloadsRule   = StaticRule{ID: "downloads.installers", Roots: []string{"Downloads"}, Kind: MatchExtensions, Extensions: []string{".dmg", ".pkg", ".iso"}}
	nodeModulesRule = StaticRule{ID: "project.node-modules", Roots: []string{"dev", "code"}, Kind: MatchProjectArtifact, Names: []string{"node_modules"}, Markers: []string{"package.json"}}
	venvRule        = StaticRule{ID: "project.venv", Roots: []string{"dev"}, Kind: MatchProjectArtifact, Names: []string{".venv", "venv"}, Markers: []string{"pyproject.toml", "requirements.txt"}}
	targetRule      = StaticRule{ID: "project.target", Roots: []string{"dev"}, Kind: MatchProjectArtifact, Names: []string{"target"}, Markers: []string{"Cargo.toml"}}
)

func fixture(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.example.app/blob", Size: 64},
			{Path: "Library/Caches/com.example.keep/blob", Size: 64},
			{Path: "Library/Caches/plain.txt", Content: "x"},
			{Path: "Library/Caches/com.apple.bird/state", Content: "x"},
			{Path: "Library/Caches/com.apple.icloud.fmfd/state", Content: "x"},
			{Path: "Library/Caches/CLOUDKIT/state", Content: "x"},
			{Path: "Library/Developer/Xcode/DerivedData/App-abc/info.plist", Content: "x"},
			{Path: "Library/Logs/foo/log.txt", Content: "x"},
			{Path: "Library/Application Support/Cursor/Cache/Cache_Data/f", Content: "x"},
			{Path: "Library/Mail/V10/MailData/x", Content: "x"},
			{Path: "Documents/important/report.pdf", Content: "x"},
			{Path: "dev/app/.env", Content: "SECRET=1"},
			{Path: "dev/app/node_modules/x/index.js", Content: "x"},
			{Path: "realdir/child/f", Content: "x"},
			{Path: "Downloads/taxes-2025.pdf", Content: "x"},
			{Path: "Downloads/thesis/chapter1.docx", Content: "x"},
			{Path: "Downloads/old.dmg", Content: "x"},
			{Path: "Downloads/OLD2.DMG", Content: "x"},
			{Path: "Downloads/dir.dmg/f", Content: "x"},
			{Path: "Downloads/sub/x.dmg", Content: "x"},
			{Path: "Downloads/.dmg", Content: "x"},
			{Path: "dev/proj/package.json", Content: "{}"},
			{Path: "dev/proj/src/main.go", Content: "package main"},
			{Path: "dev/proj/.git/HEAD", Content: "ref: refs/heads/main"},
			{Path: "dev/proj/node_modules/x/node_modules/y/index.js", Content: "x"},
			{Path: "dev/org/proj/node_modules/x/index.js", Content: "x"},
			{Path: "dev/fileproj/node_modules", Content: "not a directory"},
			{Path: "dev/node_modules/x/index.js", Content: "x"},
			{Path: "dev/proj/venv/bin/python", Content: "x"},
			{Path: "dev/proj/requirements.txt", Content: "x"},
			{Path: "dev/org/proj/package.json", Content: "{}"},
			{Path: "dev/notes/target/f", Content: "x"},
			{Path: "dev/rust/Cargo.toml", Content: "[package]"},
			{Path: "dev/rust/target/f", Content: "x"},
			{Path: "dev/dirmarker/Cargo.toml/f", Content: "x"},
			{Path: "dev/dirmarker/target/f", Content: "x"},
			{Path: "dev/real/Cargo.toml", Content: "[package]"},
			{Path: "dev/linkmarker/target/f", Content: "x"},
			{Path: "dev/casemarker/cargo.toml", Content: "[package]"},
			{Path: "dev/casemarker/target/f", Content: "x"},
		},
		Symlinks: []testutil.Symlink{
			{Path: "Library/Caches/foo", Target: "~/Documents/important"},
			{Path: "Library/Caches/mid", Target: "~/realdir"},
			{Path: "Library/Caches/link-to-file", Target: "~/Library/Caches/plain.txt"},
			{Path: "dev/linkmarker/Cargo.toml", Target: "~/dev/real/Cargo.toml"},
		},
	})
}

func TestAuthorize(t *testing.T) {
	home := fixture(t)
	p := func(rel string) string { return home + "/" + rel }

	cases := []struct {
		name string
		env  Env
		rule Rule
		path string
		want string // "" means allowed
	}{
		// Allowed baselines.
		{"Allowed cache directory", Env{Home: home}, cachesRule, p("Library/Caches/com.example.app"), ""},
		{"Allowed regular file as the item", Env{Home: home}, cachesRule, p("Library/Caches/plain.txt"), ""},
		{"Allowed under one-level glob root", Env{Home: home}, editorRule, p("Library/Application Support/Cursor/Cache/Cache_Data"), ""},

		// Requirement: Home-only.
		{"Path outside home", Env{Home: home}, StaticRule{ID: "x", Roots: []string{"Library/Caches"}, Kind: MatchChildren}, "/Library/Caches/x", ReasonOutsideHome},
		{"Path is a sibling sharing the home prefix", Env{Home: home}, cachesRule, home + "-other/Library/Caches/x", ReasonOutsideHome},
		{"Path is home itself", Env{Home: home}, cachesRule, home, ReasonIsHome},
		{"Home prefix with different case is outside home", Env{Home: home}, cachesRule, strings.ToUpper(home) + "/Library/Caches/com.example.app", ReasonOutsideHome},

		// Requirement: No symlink traversal.
		{"Symlinked cache directory", Env{Home: home}, cachesRule, p("Library/Caches/foo"), ReasonSymlinkInPath},
		{"Symlink as the item itself", Env{Home: home}, cachesRule, p("Library/Caches/link-to-file"), ReasonSymlinkInPath},
		{"Symlink in the middle of the path", Env{Home: home}, StaticRule{ID: "x", Roots: []string{"Library/Caches/mid"}, Kind: MatchChildren}, p("Library/Caches/mid/child"), ReasonSymlinkInPath},

		// Canonical form.
		{"Relative path", Env{Home: home}, cachesRule, "Library/Caches/com.example.app", ReasonNotAbsolute},
		{"Dot-dot segments", Env{Home: home}, cachesRule, p("Library/Caches/com.example.app/../../../Documents"), ReasonNotAbsolute},
		{"Dot-dot that stays under the root", Env{Home: home}, cachesRule, p("Library/Caches/x/../com.example.app"), ReasonNotAbsolute},
		{"Trailing slash", Env{Home: home}, cachesRule, p("Library/Caches/com.example.app") + "/", ReasonNotAbsolute},
		{"Trailing slash on a symlink", Env{Home: home}, cachesRule, p("Library/Caches/foo") + "/", ReasonNotAbsolute},
		{"Double slash", Env{Home: home}, cachesRule, home + "/Library//Caches/com.example.app", ReasonNotAbsolute},
		{"Dot segment", Env{Home: home}, cachesRule, home + "/Library/./Caches/com.example.app", ReasonNotAbsolute},
		{"Empty path", Env{Home: home}, cachesRule, "", ReasonNotAbsolute},
		{"Home not absolute", Env{Home: "relative/home"}, cachesRule, p("Library/Caches/com.example.app"), ReasonNotAbsolute},
		{"Home empty", Env{}, cachesRule, p("Library/Caches/com.example.app"), ReasonNotAbsolute},
		{"Home is filesystem root", Env{Home: "/"}, cachesRule, "/Library/Caches/x", ReasonNotAbsolute},
		{"Home with trailing slash", Env{Home: home + "/"}, cachesRule, p("Library/Caches/com.example.app"), ReasonNotAbsolute},

		// Requirement: Hard-coded denylist.
		{"Rule points into denylist", Env{Home: home}, mailRule, p("Library/Mail/V10/MailData"), ReasonDenylisted},
		{"Unicode alias of a denylisted folder", Env{Home: home}, StaticRule{ID: "x", Roots: []string{"Documentſ"}, Kind: MatchChildren}, p("Documentſ/important"), ReasonDenylisted},
		{"Denylisted rule root itself is denylisted, not is_rule_root", Env{Home: home}, mailRule, p("Library/Mail/V10"), ReasonDenylisted},
		{"Denylist matches regardless of case", Env{Home: home}, anyRule, p("documents/important"), ReasonDenylisted},
		{"Ancestor of a denylisted path", Env{Home: home}, libraryRule, p("Library/Application Support"), ReasonDenylisted},
		{"Ancestor of agent memory", Env{Home: home}, claudeRule, p(".claude"), ReasonDenylisted},
		{"Dotenv file at any depth", Env{Home: home}, anyRule, p("dev/app/.env"), ReasonDenylisted},
		{"Dotenv variant at any depth", Env{Home: home}, anyRule, p("Library/Caches/x/.env.local"), ReasonDenylisted},
		{"Keychain file at any depth", Env{Home: home}, anyRule, p("Library/Caches/x/login.keychain-db"), ReasonDenylisted},
		{"Photos library at any depth", Env{Home: home}, anyRule, p("Library/Caches/x/Old.photoslibrary"), ReasonDenylisted},
		{"Denylist wins over missing", Env{Home: home}, anyRule, p("Library/Caches/never-created/.env"), ReasonDenylisted},

		// Requirement: User protections are additive.
		{"Extra protection honoured", Env{Home: home, Protect: []string{p("Library/Caches/com.example.keep")}}, cachesRule, p("Library/Caches/com.example.keep"), ReasonUserProtected},
		{"Extra protection covers descendants", Env{Home: home, Protect: []string{p("Library/Caches/com.example.keep")}}, cachesRule, p("Library/Caches/com.example.keep/blob"), ReasonUserProtected},
		{"Extra protection covers ancestors", Env{Home: home, Protect: []string{p("Library/Caches/com.example.app/blob")}}, cachesRule, p("Library/Caches/com.example.app"), ReasonUserProtected},
		{"Extra protection matches regardless of case", Env{Home: home, Protect: []string{p("library/caches/COM.EXAMPLE.KEEP")}}, cachesRule, p("Library/Caches/com.example.keep"), ReasonUserProtected},
		{"Relative protection fails closed", Env{Home: home, Protect: []string{"Library/Caches/com.example.keep"}}, cachesRule, p("Library/Caches/com.example.app"), ReasonInvalidEnv},
		{"Empty protection fails closed", Env{Home: home, Protect: []string{""}}, cachesRule, p("Library/Caches/com.example.app"), ReasonInvalidEnv},
		{"Unclean protection fails closed", Env{Home: home, Protect: []string{p("Library/x/../Caches/com.example.keep")}}, cachesRule, p("Library/Caches/com.example.app"), ReasonInvalidEnv},
		{"Protection with trailing slash fails closed", Env{Home: home, Protect: []string{p("Library/Caches/com.example.keep") + "/"}}, cachesRule, p("Library/Caches/com.example.app"), ReasonInvalidEnv},
		{"Protection of the filesystem root refuses everything", Env{Home: home, Protect: []string{"/"}}, cachesRule, p("Library/Caches/com.example.app"), ReasonUserProtected},
		{"Protection matches with Unicode folding", Env{Home: home, Protect: []string{p("Library/Cacheſ/com.example.keep")}}, cachesRule, p("Library/Caches/com.example.keep"), ReasonUserProtected},
		{"Pattern in a caller-built protection is matched", Env{Home: home, Protect: []string{p("Library/Caches/com.example.k*")}}, cachesRule, p("Library/Caches/com.example.keep"), ReasonUserProtected},
		{"Malformed pattern in a protection refuses its siblings", Env{Home: home, Protect: []string{p("Library/Caches/[")}}, cachesRule, p("Library/Caches/com.example.app"), ReasonUserProtected},
		{"Unrelated protection does not refuse", Env{Home: home, Protect: []string{p("Library/Caches/com.example.keep")}}, cachesRule, p("Library/Caches/com.example.app"), ""},

		// Requirement: Rule roots are kept.
		{"Root is the candidate", Env{Home: home}, cachesRule, p("Library/Caches"), ReasonIsRuleRoot},
		{"Glob root is the candidate", Env{Home: home}, editorRule, p("Library/Application Support/Cursor/Cache"), ReasonIsRuleRoot},

		// Requirement: Allowlist descent.
		{"Path not under rule root", Env{Home: home}, derivedDataRule, p("Library/Logs/foo"), ReasonOutsideRuleRoots},
		{"Rule root prefix is not a path prefix", Env{Home: home}, derivedDataRule, p("Library/Developer/Xcode/DerivedDataX/a"), ReasonOutsideRuleRoots},
		{"Excluded child of a rule root", Env{Home: home}, cachesRule, p("Library/Caches/com.apple.bird"), ReasonOutsideRuleRoots},
		{"Excluded child by glob", Env{Home: home}, cachesRule, p("Library/Caches/com.apple.icloud.fmfd"), ReasonOutsideRuleRoots},
		{"Exclusion matches regardless of case", Env{Home: home}, cachesRule, p("Library/Caches/CLOUDKIT/state"), ReasonOutsideRuleRoots},
		{"Rule root matching is case-sensitive", Env{Home: home}, cachesRule, p("library/caches/com.example.app"), ReasonOutsideRuleRoots},
		{"Nil rule", Env{Home: home}, nil, p("Library/Caches/com.example.app"), ReasonOutsideRuleRoots},

		// Requirement: Tier semantics (info rules are never movable).
		{"Info rule item", Env{Home: home}, StaticRule{ID: "ollama.models", Roots: []string{".ollama/models"}, Info: true, Kind: MatchChildren}, p(".ollama/models/blobs"), ReasonInfoTier},
		{"Info rule never authorised", Env{Home: home}, StaticRule{ID: "info.caches", Roots: []string{"Library/Caches"}, Info: true, Kind: MatchChildren}, p("Library/Caches/com.example.app"), ReasonInfoTier},
		{"Info rule refused before filesystem checks", Env{Home: home}, StaticRule{ID: "info.caches", Roots: []string{"Library/Caches"}, Info: true, Kind: MatchChildren}, p("Library/Caches/never-created"), ReasonInfoTier},
		{"Rule without roots", Env{Home: home}, StaticRule{ID: "empty", Kind: MatchChildren}, p("Library/Caches/com.example.app"), ReasonOutsideRuleRoots},
		{"Absolute rule root authorises nothing", Env{Home: home}, StaticRule{ID: "abs", Roots: []string{home + "/Library/Caches"}, Kind: MatchChildren}, p("Library/Caches/com.example.app"), ReasonOutsideRuleRoots},
		{"Dot-dot rule root authorises nothing", Env{Home: home}, StaticRule{ID: "dd", Roots: []string{"Library/../Library/Caches"}, Kind: MatchChildren}, p("Library/Caches/com.example.app"), ReasonOutsideRuleRoots},
		{"Two glob segments authorise nothing", Env{Home: home}, StaticRule{ID: "g2", Roots: []string{"Library/*/*"}, Kind: MatchChildren}, p("Library/Caches/com.example.app/blob"), ReasonOutsideRuleRoots},
		{"Double-star root authorises nothing", Env{Home: home}, StaticRule{ID: "gg", Roots: []string{"Library/**"}, Kind: MatchChildren}, p("Library/Caches/com.example.app"), ReasonOutsideRuleRoots},
		{"Dot root authorises nothing", Env{Home: home}, StaticRule{ID: "dot", Roots: []string{"."}, Kind: MatchChildren}, p("Library/Caches/com.example.app"), ReasonOutsideRuleRoots},
		{"Malformed exclusion excludes everything", Env{Home: home}, StaticRule{ID: "bad", Roots: []string{"Library/Caches"}, Exclude: []string{"["}, Kind: MatchChildren}, p("Library/Caches/com.example.app"), ReasonOutsideRuleRoots},
		{"Empty match kind authorises nothing", Env{Home: home}, StaticRule{ID: "project.node-modules", Roots: []string{"dev"}}, p("dev/app"), ReasonNotRuleItem},
		{"Empty match kind authorises no cache child", Env{Home: home}, StaticRule{ID: "caches.user", Roots: []string{"Library/Caches"}}, p("Library/Caches/com.example.app"), ReasonNotRuleItem},
		{"Unknown match kind authorises nothing", Env{Home: home}, StaticRule{ID: "k", Roots: []string{"Library/Caches"}, Kind: "everything"}, p("Library/Caches/com.example.app"), ReasonNotRuleItem},

		// Item shape: children rules offer direct children only.
		{"File inside a cache child is not an item", Env{Home: home}, cachesRule, p("Library/Caches/com.example.app/blob"), ReasonNotRuleItem},

		// Item shape: extension rules offer direct regular files with a listed extension.
		{"Installer in Downloads", Env{Home: home}, downloadsRule, p("Downloads/old.dmg"), ""},
		{"Installer extension in upper case", Env{Home: home}, downloadsRule, p("Downloads/OLD2.DMG"), ""},
		{"Path is not an item of the rule", Env{Home: home}, downloadsRule, p("Downloads/taxes-2025.pdf"), ReasonNotRuleItem},
		{"Nested document in Downloads", Env{Home: home}, downloadsRule, p("Downloads/thesis/chapter1.docx"), ReasonNotRuleItem},
		{"Nested folder in Downloads", Env{Home: home}, downloadsRule, p("Downloads/thesis"), ReasonNotRuleItem},
		{"Installer below a subfolder", Env{Home: home}, downloadsRule, p("Downloads/sub/x.dmg"), ReasonNotRuleItem},
		{"Directory named like an installer", Env{Home: home}, downloadsRule, p("Downloads/dir.dmg"), ReasonNotRuleItem},
		{"Bare extension is not a file name", Env{Home: home}, downloadsRule, p("Downloads/.dmg"), ReasonNotRuleItem},

		// Item shape: project artifact rules offer named directories inside a project.
		{"node_modules inside a project", Env{Home: home}, nodeModulesRule, p("dev/proj/node_modules"), ""},
		{"node_modules inside a nested project", Env{Home: home}, nodeModulesRule, p("dev/org/proj/node_modules"), ""},
		{"Second artifact name of a rule", Env{Home: home}, venvRule, p("dev/proj/venv"), ""},
		{"Project folder itself", Env{Home: home}, nodeModulesRule, p("dev/proj"), ReasonNotRuleItem},
		{"Project source folder", Env{Home: home}, nodeModulesRule, p("dev/proj/src"), ReasonNotRuleItem},
		{"Project source file", Env{Home: home}, nodeModulesRule, p("dev/proj/src/main.go"), ReasonNotRuleItem},
		{"Project git folder", Env{Home: home}, nodeModulesRule, p("dev/proj/.git"), ReasonDenylisted},
		{"Artifact directly in the project root", Env{Home: home}, nodeModulesRule, p("dev/node_modules"), ReasonNotRuleItem},
		{"Artifact inside another artifact", Env{Home: home}, nodeModulesRule, p("dev/proj/node_modules/x/node_modules"), ReasonNotRuleItem},
		{"Artifact name on a regular file", Env{Home: home}, nodeModulesRule, p("dev/fileproj/node_modules"), ReasonNotRuleItem},
		{"Artifact name from another rule", Env{Home: home}, nodeModulesRule, p("dev/proj/venv"), ReasonNotRuleItem},
		{"target without Cargo.toml beside it", Env{Home: home}, targetRule, p("dev/notes/target"), ReasonNotRuleItem},
		{"target with Cargo.toml beside it", Env{Home: home}, targetRule, p("dev/rust/target"), ""},
		{"Marker that is a directory", Env{Home: home}, targetRule, p("dev/dirmarker/target"), ReasonNotRuleItem},
		{"Marker that is a symlink", Env{Home: home}, targetRule, p("dev/linkmarker/target"), ReasonNotRuleItem},
		{"Marker stored in a different case", Env{Home: home}, targetRule, p("dev/casemarker/target"), ReasonNotRuleItem},
		{"Second marker of a rule", Env{Home: home}, venvRule, p("dev/proj/venv"), ""},
		{"Rule without markers authorises no artifact", Env{Home: home}, StaticRule{ID: "p", Roots: []string{"dev"}, Kind: MatchProjectArtifact, Names: []string{"target"}}, p("dev/rust/target"), ReasonNotRuleItem},
		{"Glob marker never matches", Env{Home: home}, StaticRule{ID: "p", Roots: []string{"dev"}, Kind: MatchProjectArtifact, Names: []string{"target"}, Markers: []string{"Cargo.*"}}, p("dev/rust/target"), ReasonNotRuleItem},

		// Filesystem state.
		{"Missing path", Env{Home: home}, cachesRule, p("Library/Caches/never-created"), ReasonMissing},
		{"Path through a regular file", Env{Home: home}, StaticRule{ID: "x", Roots: []string{"Library/Caches/plain.txt"}, Kind: MatchChildren}, p("Library/Caches/plain.txt/child"), ReasonMissing},
		{"Home does not exist", Env{Home: home + "-missing"}, StaticRule{ID: "x", Roots: []string{"a"}, Kind: MatchChildren}, home + "-missing/a/b", ReasonMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authorizeBoth(t, tc.env, tc.rule, tc.path)
			if tc.want == "" {
				if !got.Allowed || got.Reason != "" {
					t.Fatalf("want allowed, got %+v", got)
				}
				return
			}
			if got.Allowed || got.Reason != tc.want {
				t.Fatalf("want refused %q, got %+v", tc.want, got)
			}
		})
	}
}

// Under a hypothetical ".claude" root, agent memory stays refused while a
// sibling cache directory is allowed.
func TestAgentStateUnderDotClaudeRoot(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Dirs: []testutil.Dir{{Path: ".claude/projects"}, {Path: ".claude/debug"}}})
	if got := authorizeBoth(t, Env{Home: home}, claudeRule, home+"/.claude/projects"); got.Reason != ReasonDenylisted {
		t.Fatalf("want denylisted, got %+v", got)
	}
	if got := authorizeBoth(t, Env{Home: home}, claudeRule, home+"/.claude/debug"); !got.Allowed {
		t.Fatalf("want .claude/debug allowed under a .claude root, got %+v", got)
	}
}

func TestApplyReauthorisesBeforeMoving(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.example.app/blob", Size: 128},
			{Path: "Documents/important/report.pdf", Content: "x"},
		},
	})
	env := Env{Home: home}
	item := home + "/Library/Caches/com.example.app"
	if d := authorizeBoth(t, env, cachesRule, item); !d.Allowed {
		t.Fatalf("scan-time authorisation should pass, got %+v", d)
	}
	// Between scan and apply, the item is swapped for a symlink.
	if err := os.RemoveAll(item); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home+"/Documents/important", item); err != nil {
		t.Fatal(err)
	}
	if d := authorizeBoth(t, env, cachesRule, item); d.Allowed || d.Reason != ReasonSymlinkInPath {
		t.Fatalf("apply-time authorisation: want %q, got %+v", ReasonSymlinkInPath, d)
	}
}

func TestDifferentDevice(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/mounted/data/blob", Content: "x"},
			{Path: "Library/Caches/local/blob", Content: "x"},
		},
	})
	mount := home + "/Library/Caches/mounted"
	mountedRule := StaticRule{ID: "x", Roots: []string{"Library/Caches/mounted"}, Kind: MatchChildren}
	orig := deviceOf
	t.Cleanup(func() { deviceOf = orig })
	// Pretend a volume is mounted at Library/Caches/mounted.
	deviceOf = func(path string, fi fs.FileInfo) (uint64, bool) {
		dev, ok := orig(path, fi)
		if path == mount || strings.HasPrefix(path, mount+"/") {
			return dev + 1, ok
		}
		return dev, ok
	}
	cases := []struct {
		name string
		rule Rule
		path string
		want string
	}{
		{"Different device", mountedRule, mount + "/data", ReasonDifferentDevice},
		{"Mount point itself", cachesRule, mount, ReasonDifferentDevice},
		{"Same device sibling", cachesRule, home + "/Library/Caches/local", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authorizeBoth(t, Env{Home: home}, tc.rule, tc.path)
			if tc.want == "" {
				if !got.Allowed {
					t.Fatalf("want allowed, got %+v", got)
				}
				return
			}
			if got.Reason != tc.want {
				t.Fatalf("want %q, got %+v", tc.want, got)
			}
		})
	}

	deviceOf = func(string, fs.FileInfo) (uint64, bool) { return 0, false }
	if got := authorizeBoth(t, Env{Home: home}, cachesRule, home+"/Library/Caches/local"); got.Reason != ReasonDifferentDevice {
		t.Fatalf("unknown device must refuse, got %+v", got)
	}
}

func TestHomeIsSymlink(t *testing.T) {
	target := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/a/b", Content: "x"}}})
	link := filepath.Join(filepath.Dir(target), "linked-home")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if got := authorizeBoth(t, Env{Home: link}, cachesRule, link+"/Library/Caches/a"); got.Reason != ReasonSymlinkInPath {
		t.Fatalf("want %q, got %+v", ReasonSymlinkInPath, got)
	}
}

func TestRunAsRoot(t *testing.T) {
	err := CheckNotRoot(0)
	if err == nil || err.Error() != "kipple will not run as root" {
		t.Fatalf("euid 0: got %v", err)
	}
	if err := CheckNotRoot(501); err != nil {
		t.Fatalf("euid 501: got %v", err)
	}
	if os.Geteuid() != 0 {
		if err := RefuseRoot(); err != nil {
			t.Fatalf("RefuseRoot as non-root: %v", err)
		}
	}
}

func TestDenylistContainsSpecMinimum(t *testing.T) {
	// From specs/safety-policy "Hard-coded denylist" and design D3.
	required := []string{
		"Documents", "Desktop", "Pictures", "Movies", "Music", ".docker", ".ollama",
		"Library/Safari", "Library/Cookies", "Library/Application Support/Code/User",
		"**/.git", "**/.envrc", "**/*.pem", "**/id_rsa*", "**/id_ed25519*", "**/*.kdbx", "**/*.p12",
		"Library/Mobile Documents", "Library/CloudStorage", "Library/Mail", "Library/Messages",
		"Library/Photos", "Pictures/Photos Library.photoslibrary", "Library/Keychains", "Library/Preferences",
		"Library/Containers", "Library/Group Containers", "Library/Application Support/MobileSync",
		".ssh", ".gnupg", ".aws", ".config/op", ".ark",
		".claude/projects", ".claude/memory", ".claude/plans", ".claude/skills", ".claude/settings*.json",
		".codex/sessions", ".codex/memory",
		"**/.env", "**/.env.*", "**/*.keychain*", "**/*.photoslibrary",
	}
	have := map[string]bool{}
	for _, d := range Denylist() {
		have[d] = true
	}
	for _, r := range required {
		if !have[r] {
			t.Errorf("denylist is missing %q", r)
		}
	}
}

// Every denylist entry, its descendants and its case variants are refused.
func TestEveryDenylistEntryRefused(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{})
	for _, entry := range Denylist() {
		rel := strings.TrimPrefix(entry, "**/")
		rel = strings.ReplaceAll(rel, "*", "x")
		if strings.HasPrefix(entry, "**/") {
			rel = "Library/Caches/some/" + rel
		}
		root := StaticRule{ID: "everything", Roots: []string{strings.Split(rel, "/")[0]}, Kind: MatchChildren}
		for _, cand := range []string{rel, rel + "/child", strings.ToUpper(rel)} {
			got := authorizeBoth(t, Env{Home: home}, root, home+"/"+cand)
			if got.Allowed || got.Reason != ReasonDenylisted {
				t.Errorf("%s: candidate %q got %+v", entry, cand, got)
			}
			if !IsDenylisted(cand) {
				t.Errorf("IsDenylisted(%q) = false for entry %q", cand, entry)
			}
		}
	}
}

func TestIsDenylisted(t *testing.T) {
	cases := map[string]bool{
		"Documents":                             true,
		"Documents/x":                           true,
		"documents/x":                           true,
		"Library":                               true, // contains Library/Mail and others
		".claude":                               true, // contains .claude/projects
		".claude/settings.json":                 true,
		".claude/settings.local.json":           true,
		".claude/debug":                         false,
		".claude/shell-snapshots":               false,
		".claude.json":                          true,
		"Library/Caches":                        false,
		"Library/Caches/com.x":                  false,
		"Library/Logs":                          false,
		"dev/app/.env.production":               true,
		"dev/app/.envrc":                        true,
		"dev/app/.git":                          true,
		"dev/app/.git/objects":                  true,
		"dev/app/.github":                       false,
		"dev/app/.gitignore":                    false,
		"Library/Caches/x/server.pem":           true,
		"Library/Caches/x/id_rsa.pub":           true,
		"Library/Caches/x/vault.kdbx":           true,
		"Library/Caches/x/cert.p12":             true,
		".docker":                               true,
		".docker/machine":                       true,
		".ollama/models":                        true,
		".cargo/credentials.toml":               true,
		".cargo/registry/cache":                 false,
		".cargo":                                true, // contains .cargo/credentials*
		".zsh_history":                          true,
		".codex/config.toml":                    true,
		".codex/log":                            false,
		".claude/agents/x.md":                   true,
		".claude/CLAUDE.md":                     true,
		"Library/Application Support/Code/User": true,
		"Library/Application Support/Code/User/settings.json": true,
		"Library/Application Support/Code/Cache":              false,
		"Library/Application Support/Cursor/Cache":            false,
		"Library/Application Support/Google/Chrome/Default":   true,
		"Library/Safari": true,
		"Library/Developer/CoreSimulator/Devices": true,
		"Library/Developer/CoreSimulator/Caches":  false,
		"Library/Developer/Xcode/DerivedData":     false,
		"Library/Developer/Xcode/UserData":        true,
		"dev/app/.venv":                           false,
		"dev/app/node_modules":                    false,
		"":                                        false,
	}
	for rel, want := range cases {
		if got := IsDenylisted(rel); got != want {
			t.Errorf("IsDenylisted(%q) = %v, want %v", rel, got, want)
		}
	}
}

// Reasons is a closed set: every reason produced by the table above is in it.
func TestReasonsClosedSet(t *testing.T) {
	want := []string{"not_absolute", "is_home", "outside_home", "denylisted", "user_protected",
		"is_rule_root", "outside_rule_roots", "missing", "symlink_in_path", "different_device", "info_tier", "not_rule_item", "name_mismatch", "invalid_env"}
	got := Reasons()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Reasons() = %v, want %v", got, want)
	}
}

func TestParseRoot(t *testing.T) {
	valid := []string{"Library/Caches", "Library/Application Support/*/Cache", ".npm/_cacache", "dev"}
	invalid := []string{"", "/abs", "~/x", "a/../b", "a//b", "a/", "./a", ".", "..", "a/*/*", "a/**", "a/[", "a/b\\", "*", "*/Cache", "Lib*/Caches", "[L]ibrary/Caches"}
	for _, r := range valid {
		if _, ok := ParseRoot(r); !ok {
			t.Errorf("ParseRoot(%q) rejected a valid root", r)
		}
	}
	for _, r := range invalid {
		if _, ok := ParseRoot(r); ok {
			t.Errorf("ParseRoot(%q) accepted an invalid root", r)
		}
	}
}

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Downloads", "Downloads", true},
		{"downloads", "Downloads", true},
		{"go", "go/pkg/mod", true},
		{"Library/Caches/proj", "Library/Caches", true},
		{"Library/Developer/Xcode/iOS DeviceSupport/x", "Library/Developer/Xcode/* DeviceSupport", true},
		{"Library/Developer/Xcode/Archives", "Library/Developer/Xcode/* DeviceSupport", false},
		{"Documentſ", "Documents", true},
		{"dev", "Developer", false},
		{"work", "Library/Caches", false},
		{"a/[", "a/b", true},
	}
	for _, tc := range cases {
		if got := Overlaps(tc.a, tc.b); got != tc.want {
			t.Errorf("Overlaps(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := Overlaps(tc.b, tc.a); got != tc.want {
			t.Errorf("Overlaps(%q, %q) = %v, want %v", tc.b, tc.a, got, tc.want)
		}
	}
}

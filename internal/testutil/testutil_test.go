package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) { os.Exit(GuardedMain(m)) }

// fakeTB records Fatalf instead of stopping the test, so the guard's failure
// path can be asserted.
type fakeTB struct {
	testing.TB
	failed bool
	msg    string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.msg = format
	_ = args
}

var old = time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)

func TestNewHomeRoundTrip(t *testing.T) {
	spec := Spec{
		Dirs: []Dir{
			{Path: "Library/Caches/empty", MTime: old},
		},
		Files: []File{
			{Path: "Library/Caches/com.example/blob.bin", Size: 4096, MTime: old},
			{Path: "Library/Logs/app.log", Content: "hello\n", MTime: old.Add(time.Hour)},
			{Path: "Documents/important.txt", Content: "keep me"},
			{Path: "Downloads/x.dmg", Size: 1 << 20, Mode: 0o600},
		},
		Symlinks: []Symlink{
			{Path: "Library/Caches/foo", Target: "~/Documents"},
			{Path: "Library/Caches/rel", Target: "../Logs"},
		},
		Hardlinks: []Hardlink{
			{Path: "Library/Caches/com.example/blob-link.bin", Target: "Library/Caches/com.example/blob.bin"},
		},
	}
	home := NewHome(t, spec)
	GuardRealHome(t)

	if got := os.Getenv("KIPPLE_HOME"); got != home {
		t.Fatalf("KIPPLE_HOME = %q, want %q", got, home)
	}
	if got := os.Getenv("KIPPLE_STATE_DIR"); got != StateDir(home) {
		t.Fatalf("KIPPLE_STATE_DIR = %q, want %q", got, StateDir(home))
	}
	if strings.HasPrefix(StateDir(home), home+string(filepath.Separator)) {
		t.Fatalf("state dir %q must be outside the fixture home", StateDir(home))
	}

	byPath := map[string]Entry{}
	for _, e := range Snapshot(t, home) {
		byPath[e.Path] = e
	}
	for _, f := range spec.Files {
		e, ok := byPath[f.Path]
		if !ok || e.Type != "file" {
			t.Fatalf("file %s missing or wrong type: %+v", f.Path, e)
		}
		want := f.Size
		if f.Content != "" {
			want = int64(len(f.Content))
		}
		if e.Size != want {
			t.Errorf("%s size = %d, want %d", f.Path, e.Size, want)
		}
		if !f.MTime.IsZero() && !e.MTime.Equal(f.MTime) {
			t.Errorf("%s mtime = %v, want %v", f.Path, e.MTime, f.MTime)
		}
		if f.Mode != 0 && e.Mode.Perm() != f.Mode {
			t.Errorf("%s mode = %v, want %v", f.Path, e.Mode.Perm(), f.Mode)
		}
	}
	if e := byPath["Library/Caches/empty"]; e.Type != "dir" || !e.MTime.Equal(old) {
		t.Errorf("dir round trip: %+v", e)
	}
	if e := byPath["Library/Caches/foo"]; e.Type != "symlink" || e.Target != filepath.Join(home, "Documents") {
		t.Errorf("absolute symlink round trip: %+v", e)
	}
	if e := byPath["Library/Caches/rel"]; e.Type != "symlink" || e.Target != "../Logs" {
		t.Errorf("relative symlink round trip: %+v", e)
	}

	a, err := os.Stat(Abs(home, "Library/Caches/com.example/blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(Abs(home, "Library/Caches/com.example/blob-link.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Error("hardlink does not share an inode with its target")
	}
}

func TestChecksumStableAndSensitive(t *testing.T) {
	home := NewHome(t, Spec{Files: []File{{Path: "a/b.txt", Content: "x", MTime: old}}})
	first := Checksum(t, home)
	if again := Checksum(t, home); again != first {
		t.Fatal("checksum changed without any modification")
	}
	if err := os.WriteFile(Abs(home, "a/b.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Checksum(t, home) == first {
		t.Fatal("checksum did not change after content change")
	}
}

func TestNewHomeGitRepos(t *testing.T) {
	home := NewHome(t, Spec{
		Repos: []Repo{
			{Path: "dev/pushed", Upstream: true, Pushed: true, MTime: old,
				Commits: []Commit{{Message: "init", Files: []File{{Path: "package.json", Content: "{}"}}}}},
			{Path: "dev/ahead", Upstream: true, Pushed: true,
				Commits:      []Commit{{Files: []File{{Path: "Cargo.toml", Content: "[package]"}}}},
				LocalCommits: []Commit{{Message: "local", Files: []File{{Path: "src/main.rs", Content: "fn main(){}"}}}}},
			{Path: "dev/dirty", Commits: []Commit{{Files: []File{{Path: "a.txt", Content: "a"}}}}},
		},
		Files: []File{{Path: "dev/dirty/b.txt", Content: "uncommitted"}},
	})

	pushed := Abs(home, "dev/pushed")
	if out := Git(t, pushed, "status", "--porcelain"); out != "" {
		t.Errorf("pushed repo not clean: %q", out)
	}
	if out := Git(t, pushed, "rev-list", "--count", "@{upstream}..HEAD"); out != "0" {
		t.Errorf("pushed repo ahead of upstream by %s", out)
	}
	if local, up := Git(t, pushed, "rev-parse", "HEAD"), Git(t, UpstreamDir(home, "dev/pushed"), "rev-parse", "main"); local != up {
		t.Errorf("upstream main %s != local HEAD %s", up, local)
	}
	if strings.HasPrefix(UpstreamDir(home, "dev/pushed"), home+string(filepath.Separator)) {
		t.Error("upstream must live outside the fixture home")
	}
	fi, err := os.Stat(filepath.Join(pushed, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Errorf("Repo.MTime not applied: %v", fi.ModTime())
	}

	if out := Git(t, Abs(home, "dev/ahead"), "rev-list", "--count", "@{upstream}..HEAD"); out != "1" {
		t.Errorf("ahead repo: want 1 unpushed commit, got %s", out)
	}
	if out := Git(t, Abs(home, "dev/dirty"), "status", "--porcelain"); !strings.Contains(out, "b.txt") {
		t.Errorf("dirty repo should show b.txt untracked, got %q", out)
	}
}

func TestBuildRejectsEscapingPaths(t *testing.T) {
	home := NewHome(t, Spec{})
	cases := []struct {
		name string
		spec Spec
	}{
		{"dotdot", Spec{Files: []File{{Path: "../escape.txt", Content: "x"}}}},
		{"absolute", Spec{Files: []File{{Path: "/tmp/escape.txt", Content: "x"}}}},
		{"tilde", Spec{Dirs: []Dir{{Path: "~/x"}}}},
		{"unclean", Spec{Dirs: []Dir{{Path: "a//b"}}}},
		{"symlink path escapes", Spec{Symlinks: []Symlink{{Path: "a/../../b", Target: "x"}}}},
		{"size disagrees", Spec{Files: []File{{Path: "f", Content: "abc", Size: 10}}}},
		{"pushed without upstream", Spec{Repos: []Repo{{Path: "r", Pushed: true, Commits: []Commit{{}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore, err := build(home, tc.spec)
			restore()
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "escape.txt")); err == nil {
		t.Fatal("a spec path escaped the fixture home")
	}
}

func TestUnreadableDirRestoredForCleanup(t *testing.T) {
	home := NewHome(t, Spec{
		Files: []File{{Path: "Library/Caches/x/locked/f", Content: "x"}},
		Dirs:  []Dir{{Path: "Library/Caches/x/locked", Unreadable: true}},
	})
	if _, err := os.ReadDir(Abs(home, "Library/Caches/x/locked")); err == nil {
		t.Fatal("expected the directory to be unreadable")
	}
	// t.TempDir cleanup would fail the test if permissions were not restored.
}

func TestGuardRealHome(t *testing.T) {
	homes, err := realHomeDirs()
	if err != nil {
		t.Fatal(err)
	}
	realHome := homes[0]
	fixture := NewHome(t, Spec{})
	aliasDir := t.TempDir()
	alias := filepath.Join(aliasDir, "alias")
	// A symlink in the temp dir pointing at the real home: creating it does
	// not touch the real home, and the guard must see through it.
	if err := os.Symlink(realHome, alias); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		home     string
		state    string
		wantFail bool
	}{
		{"fixture home passes", fixture, StateDir(fixture), false},
		{"KIPPLE_HOME equals real home", realHome, StateDir(fixture), true},
		{"KIPPLE_HOME equals real home with trailing slash", realHome + "/", StateDir(fixture), true},
		{"KIPPLE_HOME equals real home in other case", strings.ToUpper(realHome), StateDir(fixture), true},
		{"KIPPLE_HOME inside real home", filepath.Join(realHome, "fixture"), StateDir(fixture), true},
		{"KIPPLE_HOME symlink to real home", alias, StateDir(fixture), true},
		{"KIPPLE_HOME empty", "", StateDir(fixture), true},
		{"KIPPLE_HOME relative", "relative/home", StateDir(fixture), true},
		{"KIPPLE_STATE_DIR empty", fixture, "", true},
		{"KIPPLE_STATE_DIR inside real home", fixture, filepath.Join(realHome, "Library/Application Support/kipple"), true},
		{"KIPPLE_HOME is an ancestor of real home", filepath.Dir(realHome), StateDir(fixture), true},
		{"KIPPLE_HOME is filesystem root", "/", StateDir(fixture), true},
		{"KIPPLE_STATE_DIR is filesystem root", fixture, "/", true},
		{"KIPPLE_STATE_DIR is an ancestor of real home in other case", fixture, strings.ToUpper(filepath.Dir(realHome)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KIPPLE_HOME", tc.home)
			t.Setenv("KIPPLE_STATE_DIR", tc.state)
			f := &fakeTB{TB: t}
			GuardRealHome(f)
			if f.failed != tc.wantFail {
				t.Fatalf("guard failed=%v, want %v (%s)", f.failed, tc.wantFail, f.msg)
			}
		})
	}
}

// Overriding HOME must not hide the real home: the guard also consults the
// user database.
func TestGuardRealHomeIgnoresHOMEOverride(t *testing.T) {
	account, err := accountHomeDir()
	if err != nil {
		t.Skipf("no user database entry: %v", err)
	}
	fixture := NewHome(t, Spec{})
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct{ name, home string }{
		{"KIPPLE_HOME equals the account home", account},
		{"KIPPLE_HOME inside the account home", filepath.Join(account, "fixture")},
		{"KIPPLE_HOME is an ancestor of the account home", filepath.Dir(account)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KIPPLE_HOME", tc.home)
			t.Setenv("KIPPLE_STATE_DIR", StateDir(fixture))
			f := &fakeTB{TB: t}
			GuardRealHome(f)
			if !f.failed {
				t.Fatal("guard passed with HOME overridden")
			}
		})
	}
}

// Spellings that the case-insensitive string check accepts but that name
// the real home on APFS: the guard must refuse them by identity. The long-s
// case reads (stats) the machine's real home and changes nothing.
func TestGuardRealHomeByIdentity(t *testing.T) {
	homes, err := realHomeDirs()
	if err != nil {
		t.Fatal(err)
	}
	fixture := NewHome(t, Spec{})
	t.Run("KIPPLE_HOME is a long s spelling of the real home", func(t *testing.T) {
		real := homes[0]
		i := strings.IndexAny(real, "sS")
		if i < 0 {
			t.Skip("real home path has no s to respell")
		}
		alias := real[:i] + "\u017f" + real[i+1:]
		if fi, err := os.Stat(alias); err != nil || !fi.IsDir() {
			t.Skipf("long s does not fold on this volume: %v", err)
		}
		t.Setenv("KIPPLE_HOME", alias)
		t.Setenv("KIPPLE_STATE_DIR", StateDir(fixture))
		f := &fakeTB{TB: t}
		GuardRealHome(f)
		if !f.failed {
			t.Fatalf("guard passed %q", alias)
		}
	})

	// The rest use a stand-in real home in a temp folder.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "Users", "m\u00e9") // stored precomposed (NFC)
	other := filepath.Join(base, "other")
	for _, d := range []string{filepath.Join(real, "Documents"), other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name     string
		path     string
		wantFail bool
	}{
		{"Separate folder passes", other, false},
		{"Missing folder under a separate folder passes", filepath.Join(other, "state", "later"), false},
		{"Long s spelling inside the real home", filepath.Join(base, "U\u017fers", "m\u00e9", "Documents"), true},
		{"NFD spelling inside the real home", filepath.Join(base, "Users", "me\u0301", "Documents"), true},
		{"Long s spelling of an ancestor", filepath.Join(base, "U\u017fers"), true},
		{"Missing folder inside a long s spelling of the real home", filepath.Join(base, "U\u017fers", "m\u00e9", "state"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkIdentity("KIPPLE_HOME", tc.path, real)
			if (err != nil) != tc.wantFail {
				t.Fatalf("checkIdentity(%q) = %v, want failure %v", tc.path, err, tc.wantFail)
			}
		})
	}
}

func TestContentChecksum(t *testing.T) {
	home := NewHome(t, Spec{Files: []File{{Path: "a/b/f", Content: "x"}, {Path: ".Trash/old", Content: "y"}}})
	sum := ContentChecksum(t, home, ".Trash")
	if err := os.Chtimes(filepath.Join(home, "a"), time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".Trash/new"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ContentChecksum(t, home, ".Trash") != sum {
		t.Fatal("directory mtimes or skipped entries changed the checksum")
	}
	if err := os.WriteFile(filepath.Join(home, "a/b/f"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ContentChecksum(t, home, ".Trash") == sum {
		t.Fatal("a content change was not detected")
	}
}

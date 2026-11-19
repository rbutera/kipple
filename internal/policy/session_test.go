//go:build unix

package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/testutil"
)

// authorizeBoth runs Authorize and a fresh Session's Authorize (twice, so the
// second call exercises the directory cache) and fails the test unless all
// three decisions are identical. Every Authorize call in the policy tests
// goes through it, so the whole existing table also proves the Session.
func authorizeBoth(t *testing.T, env Env, rule Rule, path string) Decision {
	t.Helper()
	want := Authorize(env, rule, path)
	s, _ := NewSession(env)
	defer func() { _ = s.Close() }()
	for i := 0; i < 2; i++ {
		if got := s.Authorize(rule, path); got != want {
			t.Errorf("Session.Authorize (call %d) = %+v, stateless Authorize = %+v for %q", i+1, got, want, path)
		}
	}
	f, got := s.AuthorizeFresh(rule, path)
	if got != want {
		t.Errorf("Session.AuthorizeFresh = %+v, stateless Authorize = %+v for %q", got, want, path)
	}
	if got.Allowed {
		checkFresh(t, f, path)
	} else if f != nil {
		t.Errorf("AuthorizeFresh returned a descriptor for a refused path %q", path)
	}
	return want
}

// checkFresh verifies that f's parent descriptor and name reach the object
// at path, then closes it.
func checkFresh(t *testing.T, f *Fresh, path string) {
	t.Helper()
	if f == nil {
		t.Fatalf("AuthorizeFresh allowed %q without a descriptor", path)
	}
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if err := unix.Fstatat(f.ParentFd, f.Name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatalf("fstatat(parent, %q): %v", f.Name, err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, _ := Identity(fi)
	if uint64(st.Dev) != dev || st.Ino != ino || filepath.Base(path) != f.Name {
		t.Fatalf("descriptor for %q reaches another object", path)
	}
}

// AuthorizeFresh collects protected identities again: a protected file that
// appears after the session started is refused, where the session snapshot
// still allows a hard link to it.
func TestAuthorizeFreshSeesNewProtections(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/innocent", Content: "machine x"}}})
	s, err := NewSession(Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	item := home + "/Library/Caches/innocent"
	if err := os.Link(item, home+"/.netrc"); err != nil {
		t.Fatal(err)
	}
	if d := s.Authorize(cachesRule, item); !d.Allowed {
		t.Fatalf("the cached snapshot predates .netrc and should still allow: %+v", d)
	}
	if f, d := s.AuthorizeFresh(cachesRule, item); d.Reason != ReasonDenylisted || f != nil {
		t.Fatalf("AuthorizeFresh: want %q, got %+v", ReasonDenylisted, d)
	}
	if d := Authorize(Env{Home: home}, cachesRule, item); d.Reason != ReasonDenylisted {
		t.Fatalf("stateless: %+v", d)
	}
}

func TestSessionHomeErrors(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/a/b", Content: "x"}}})
	cases := []struct {
		name string
		env  Env
		want string
	}{
		{"Missing home", Env{Home: home + "-missing"}, ReasonMissing},
		{"Relative home", Env{Home: "rel"}, ReasonNotAbsolute},
		{"Invalid protection", Env{Home: home, Protect: []string{"rel"}}, ReasonInvalidEnv},
		{"Home is a file", Env{Home: home + "/Library/Caches/a/b"}, ReasonMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSession(tc.env)
			defer func() { _ = s.Close() }()
			var he *HomeError
			if !errors.As(err, &he) || he.Reason != tc.want {
				t.Fatalf("want HomeError %q, got %v", tc.want, err)
			}
		})
	}
	s, err := NewSession(Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := s.Authorize(cachesRule, home+"/Library/Caches/a"); got.Reason != ReasonInvalidEnv {
		t.Fatalf("closed session must refuse, got %+v", got)
	}
	if _, err := s.Open(home + "/Library/Caches/a"); err == nil {
		t.Fatal("closed session must not open")
	}
}

// The stored-name cache is invalidated when a directory changes: a renamed
// entry is refused under its old spelling and allowed under its new one.
func TestSessionCacheFollowsChanges(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/sample/blob", Content: "x"}}})
	s, err := NewSession(Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := s.Authorize(cachesRule, home+"/Library/Caches/sample"); !got.Allowed {
		t.Fatalf("want allowed, got %+v", got)
	}
	if err := os.Mkdir(home+"/Library/Caches/later", 0o755); err != nil {
		t.Fatal(err)
	}
	if got := s.Authorize(cachesRule, home+"/Library/Caches/later"); !got.Allowed {
		t.Fatalf("entry created after the cache was filled: want allowed, got %+v", got)
	}
	if err := os.Rename(home+"/Library/Caches/sample", home+"/Library/Caches/Renamed"); err != nil {
		t.Fatal(err)
	}
	if got := s.Authorize(cachesRule, home+"/Library/Caches/sample"); got.Allowed {
		t.Fatalf("old spelling after rename must be refused, got %+v", got)
	}
	if got := s.Authorize(cachesRule, home+"/Library/Caches/Renamed"); !got.Allowed {
		t.Fatalf("new spelling after rename: want allowed, got %+v", got)
	}
}

func TestSessionOpen(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/dir/f", Content: "x"},
			{Path: "Library/Caches/file", Content: "x"},
			{Path: ".claude/projects/p/s.jsonl", Content: "x"},
			{Path: "elsewhere/f", Content: "x"},
		},
		Dirs:     []testutil.Dir{{Path: "Library/Caches/locked", Unreadable: true}},
		Symlinks: []testutil.Symlink{{Path: "Library/Caches/link", Target: "~/elsewhere"}},
	})
	s, err := NewSession(Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	e, err := s.Open(home + "/Library/Caches/dir")
	if err != nil || e.Dir == nil || !e.Info.IsDir() {
		t.Fatalf("open dir: %+v %v", e, err)
	}
	names, _ := e.Dir.Readdirnames(-1)
	_ = e.Dir.Close()
	if len(names) != 1 || names[0] != "f" {
		t.Fatalf("listing: %v", names)
	}
	if e, err := s.Open(home + "/Library/Caches/file"); err != nil || e.Dir != nil || !e.Info.Mode().IsRegular() {
		t.Fatalf("open file: %+v %v", e, err)
	}
	// Open measures only; it opens denylisted info roots.
	if e, err := s.Open(home + "/.claude/projects"); err != nil {
		t.Fatalf("open info root: %v", err)
	} else {
		_ = e.Dir.Close()
	}
	var re *RefusedError
	if _, err := s.Open(home + "/Library/Caches/link/f"); !errors.As(err, &re) || re.Reason != ReasonSymlinkInPath {
		t.Fatalf("through symlink: %v", err)
	}
	if _, err := s.Open(home + "/Library/Caches/link"); !errors.As(err, &re) || re.Reason != ReasonSymlinkInPath {
		t.Fatalf("symlink itself: %v", err)
	}
	if _, err := s.Open(home + "/Library/Caches/none"); !errors.As(err, &re) || re.Reason != ReasonMissing || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := s.Open(home + "/Library/Caches/locked"); !errors.As(err, &re) || re.Reason != ReasonMissing || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unreadable: %v", err)
	}
	if _, err := s.Open("/etc"); !errors.As(err, &re) || re.Reason != ReasonOutsideHome {
		t.Fatalf("outside home: %v", err)
	}
}

func TestGuard(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: ".netrc", Content: "machine x"},
			{Path: "Library/Caches/app/f", Content: "x"},
		},
		Hardlinks: []testutil.Hardlink{{Path: "Library/Caches/app/innocent", Target: ".netrc"}},
	})
	protected := home + "/Library/Caches/app/keep"
	s, err := NewSession(Env{Home: home, Protect: []string{protected}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	netrc, err := os.Lstat(home + "/.netrc")
	if err != nil {
		t.Fatal(err)
	}
	nid, _ := identityOf(netrc)
	// pkg is the expectation inside a package cache, other inside any other
	// rule's item.
	cases := []struct {
		rel, name  string
		dev, ino   uint64
		dir        bool
		pkg, other string
	}{
		{"x/.env", ".env", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		{"x/.ENV.local", ".ENV.local", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		{"vault.kdbx", "vault.kdbx", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		{"login.keychain-db", "login.keychain-db", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		{"Old.photoslibrary", "Old.photoslibrary", 0, 0, true, ReasonDenylisted, ReasonDenylisted},
		{"keep", "keep", 0, 0, true, ReasonUserProtected, ReasonUserProtected},
		{"KEEP/inner", "inner", 0, 0, false, ReasonUserProtected, ReasonUserProtected},
		{"innocent", "innocent", nid.dev, nid.ino, false, ReasonDenylisted, ReasonDenylisted},
		{"config/prod.env", "prod.env", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		{"old.kdb", "old.kdb", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		// Signing keys, SSH keys and direnv files block package caches too.
		{"x/testdata/key.p12", "key.p12", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		{"x/testdata/id_rsa", "id_rsa", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		{"x/id_ed25519.pub", "id_ed25519.pub", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		{"x/.envrc", ".envrc", 0, 0, false, ReasonDenylisted, ReasonDenylisted},
		// Regenerable inside a package cache (*.pem; .git is handled for
		// artifacts separately); protected inside anything else.
		{".git", ".git", 0, 0, true, "", ReasonDenylisted},
		{"wt/.git", ".git", 0, 0, false, "", ReasonDenylisted},
		{"pip/_vendor/certifi/cacert.pem", "cacert.pem", 0, 0, false, "", ReasonDenylisted},
		{"plain/file.txt", "file.txt", 0, 0, false, "", ""},
		{"keeper", "keeper", 0, 0, true, "", ""},
	}
	pkgRule := cachesRule
	pkgRule.PackageCache = true
	for _, branch := range []struct {
		name string
		rule Rule
		pkg  bool
	}{
		{"package cache", pkgRule, true},
		{"other rule", cachesRule, false},
		{"no rule", nil, false},
	} {
		g := s.Guard(branch.rule, home+"/Library/Caches/app")
		for _, tc := range cases {
			want := tc.other
			if branch.pkg {
				want = tc.pkg
			}
			t.Run(branch.name+"/"+tc.rel, func(t *testing.T) {
				got, hit := g.Check(tc.rel, tc.name, tc.dev, tc.ino, tc.dir)
				if got != want || hit != (want != "") {
					t.Fatalf("Check(%q) = %q %v, want %q", tc.rel, got, hit, want)
				}
			})
		}
	}
	// A denylist prefix below the item is found by path.
	g2 := s.Guard(cachesRule, home+"/Library/Application Support")
	if r, hit := g2.Check("Claude/x", "x", 0, 0, false); !hit || r != ReasonDenylisted {
		t.Fatalf("prefix below item: %q %v", r, hit)
	}
	// Inside a project artifact, a .git directory is protected (an artifact
	// never legitimately holds a repository); a .git file is not.
	ga := s.Guard(nodeModulesRule, home+"/dev/app/node_modules")
	if r, hit := ga.Check("dep/.git", ".git", 0, 0, true); !hit || r != ReasonDenylisted {
		t.Fatalf(".git directory in an artifact: %q %v", r, hit)
	}
	if _, hit := ga.Check("dep/.git", ".git", 0, 0, false); hit {
		t.Fatal(".git file in an artifact must not be protected")
	}
	if _, hit := ga.Check("certifi/cacert.pem", "cacert.pem", 0, 0, false); hit {
		t.Fatal("*.pem in an artifact must not be protected")
	}
}

// benchFixture builds a home whose cache folder holds n entries.
func benchFixture(b *testing.B, n int) (string, []string) {
	b.Helper()
	dirs := make([]testutil.Dir, n)
	paths := make([]string, n)
	for i := range dirs {
		dirs[i] = testutil.Dir{Path: fmt.Sprintf("Library/Caches/com.example.app%05d", i)}
	}
	home := testutil.NewHome(b, testutil.Spec{Dirs: dirs})
	for i := range dirs {
		paths[i] = home + "/" + dirs[i].Path
	}
	return home, paths
}

func BenchmarkAuthorizeStateless(b *testing.B) {
	home, paths := benchFixture(b, 3000)
	env := Env{Home: home}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if d := Authorize(env, cachesRule, paths[i%len(paths)]); !d.Allowed {
			b.Fatal(d)
		}
	}
}

func BenchmarkAuthorizeSession(b *testing.B) {
	home, paths := benchFixture(b, 3000)
	s, err := NewSession(Env{Home: home})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if d := s.Authorize(cachesRule, paths[i%len(paths)]); !d.Allowed {
			b.Fatal(d)
		}
	}
}

// Close waits for in-flight walks, and walks after Close refuse.
func TestSessionCloseDuringWalks(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "Library/Caches/a/b", Content: "x"}}})
	s, err := NewSession(Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				d := s.Authorize(cachesRule, home+"/Library/Caches/a")
				if !d.Allowed && d.Reason != ReasonInvalidEnv {
					t.Errorf("walk raced Close: %+v", d)
					return
				}
				if e, err := s.Open(home + "/Library/Caches/a"); err == nil {
					_ = e.Dir.Close()
				}
			}
		}()
	}
	_ = s.Close()
	for g := 0; g < 8; g++ {
		<-done
	}
	if d := s.Authorize(cachesRule, home+"/Library/Caches/a"); d.Reason != ReasonInvalidEnv {
		t.Fatalf("after Close: %+v", d)
	}
}

// Review finding (wave 2 re-review): cmd/ approves a home directory, then
// pins its identity; if the path later names another directory (an
// ancestor swapped for a symlink, or home replaced), NewSession,
// AuthorizeFresh and Authorize refuse with home_identity_mismatch.
func TestHomeIdentityPinned(t *testing.T) {
	setup := func(t *testing.T) (base, home, item string, pin *FileIdentity) {
		t.Helper()
		testutil.GuardRealHome(t)
		var err error
		base, err = filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		home = filepath.Join(base, "a", "home")
		item = filepath.Join(home, "Library", "Caches", "app")
		other := filepath.Join(base, "other", "home", "Library", "Caches", "app")
		for _, d := range []string{item, other} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		fi, err := os.Lstat(home)
		if err != nil {
			t.Fatal(err)
		}
		dev, ino, ok := Identity(fi)
		if !ok {
			t.Fatal("no identity")
		}
		return base, home, item, &FileIdentity{Dev: dev, Ino: ino}
	}
	// swapParent replaces base/a with a symlink to base/other, so home's path
	// now names base/other/home.
	swapParent := func(t *testing.T, base string) {
		t.Helper()
		if err := os.Rename(filepath.Join(base, "a"), filepath.Join(base, "a-approved")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(base, "other"), filepath.Join(base, "a")); err != nil {
			t.Fatal(err)
		}
	}
	// replaceHome moves home aside and makes a new directory at its path.
	replaceHome := func(t *testing.T, base string) {
		t.Helper()
		home := filepath.Join(base, "a", "home")
		if err := os.Rename(home, home+"-approved"); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, "Library", "Caches", "app"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// homeToSymlink replaces home itself with a symlink to another directory.
	homeToSymlink := func(t *testing.T, base string) {
		t.Helper()
		home := filepath.Join(base, "a", "home")
		if err := os.Rename(home, home+"-approved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(base, "other", "home"), home); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("Unchanged pinned home is accepted", func(t *testing.T) {
		_, home, item, pin := setup(t)
		env := Env{Home: home, HomeIdentity: pin}
		s, err := NewSession(env)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		if d := s.Authorize(cachesRule, item); !d.Allowed {
			t.Fatalf("session: %+v", d)
		}
		f, d := s.AuthorizeFresh(cachesRule, item)
		if !d.Allowed {
			t.Fatalf("fresh: %+v", d)
		}
		_ = f.Close()
		if d := Authorize(env, cachesRule, item); !d.Allowed {
			t.Fatalf("authorize: %+v", d)
		}
	})
	for _, sw := range []struct {
		name   string
		swap   func(*testing.T, string)
		reason string // NewSession's refusal
	}{
		{"Parent swapped for a symlink", swapParent, ReasonHomeIdentityMismatch},
		{"Home replaced by another directory", replaceHome, ReasonHomeIdentityMismatch},
		{"Home replaced by a symlink", homeToSymlink, ReasonSymlinkInPath},
	} {
		t.Run(sw.name+" before NewSession", func(t *testing.T) {
			base, home, item, pin := setup(t)
			sw.swap(t, base)
			s, err := NewSession(Env{Home: home, HomeIdentity: pin})
			defer func() { _ = s.Close() }()
			var he *HomeError
			if !errors.As(err, &he) || he.Reason != sw.reason {
				t.Fatalf("NewSession: %v, want reason %s", err, sw.reason)
			}
			if d := s.Authorize(cachesRule, item); d.Allowed || d.Reason != sw.reason {
				t.Fatalf("session authorize: %+v", d)
			}
			if d := Authorize(Env{Home: home, HomeIdentity: pin}, cachesRule, item); d.Allowed {
				t.Fatalf("authorize allowed after swap: %+v", d)
			}
		})
		t.Run(sw.name+" during a session", func(t *testing.T) {
			base, home, item, pin := setup(t)
			s, err := NewSession(Env{Home: home, HomeIdentity: pin})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			sw.swap(t, base)
			if f, d := s.AuthorizeFresh(cachesRule, item); d.Allowed || d.Reason != ReasonHomeIdentityMismatch {
				if f != nil {
					_ = f.Close()
				}
				t.Fatalf("fresh after swap: %+v", d)
			}
		})
	}
	t.Run("Authorize with a pinned identity that differs", func(t *testing.T) {
		_, home, item, pin := setup(t)
		wrong := *pin
		wrong.Ino++
		if d := Authorize(Env{Home: home, HomeIdentity: &wrong}, cachesRule, item); d.Allowed || d.Reason != ReasonHomeIdentityMismatch {
			t.Fatalf("authorize: %+v", d)
		}
	})
}

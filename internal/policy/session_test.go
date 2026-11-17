//go:build unix

package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"testing"

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
	return want
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
	g := s.Guard(cachesRule, home+"/Library/Caches/app")
	netrc, err := os.Lstat(home + "/.netrc")
	if err != nil {
		t.Fatal(err)
	}
	nid, _ := identityOf(netrc)
	cases := []struct {
		rel, name string
		dev, ino  uint64
		dir       bool
		want      string
	}{
		{"x/.env", ".env", 0, 0, false, ReasonDenylisted},
		{"x/.ENV.local", ".ENV.local", 0, 0, false, ReasonDenylisted},
		{"vault.kdbx", "vault.kdbx", 0, 0, false, ReasonDenylisted},
		{"login.keychain-db", "login.keychain-db", 0, 0, false, ReasonDenylisted},
		{"Old.photoslibrary", "Old.photoslibrary", 0, 0, true, ReasonDenylisted},
		{"keep", "keep", 0, 0, true, ReasonUserProtected},
		{"KEEP/inner", "inner", 0, 0, false, ReasonUserProtected},
		{"innocent", "innocent", nid.dev, nid.ino, false, ReasonDenylisted},
		// Regenerable content that only refuses a path naming it.
		{".git", ".git", 0, 0, true, ""},
		{"wt/.git", ".git", 0, 0, false, ""},
		{"pip/_vendor/certifi/cacert.pem", "cacert.pem", 0, 0, false, ""},
		{"x/testdata/key.p12", "key.p12", 0, 0, false, ""},
		{"x/testdata/id_rsa", "id_rsa", 0, 0, false, ""},
		{"x/id_ed25519.pub", "id_ed25519.pub", 0, 0, false, ""},
		{"x/.envrc", ".envrc", 0, 0, false, ""},
		{"plain/file.txt", "file.txt", 0, 0, false, ""},
		{"keeper", "keeper", 0, 0, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.rel, func(t *testing.T) {
			got, hit := g.Check(tc.rel, tc.name, tc.dev, tc.ino, tc.dir)
			if got != tc.want || hit != (tc.want != "") {
				t.Fatalf("Check(%q) = %q %v, want %q", tc.rel, got, hit, tc.want)
			}
		})
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

//go:build darwin

package policy

import (
	"io"
	"io/fs"
	"os"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

func newSession(t *testing.T, env Env) *Session {
	t.Helper()
	s, err := NewSession(env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// VerifyParent: the F_GETPATH re-check apply runs immediately before each
// rename.
func TestVerifyParent(t *testing.T) {
	spec := testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/app/f", Content: "x"},
		{Path: "elsewhere/app/f", Content: "y"},
	}}
	t.Run("unchanged parent is allowed", func(t *testing.T) {
		home := testutil.NewHome(t, spec)
		s := newSession(t, Env{Home: home})
		f, d := s.AuthorizeFresh(cachesRule, home+"/Library/Caches/app")
		if !d.Allowed {
			t.Fatal(d)
		}
		defer func() { _ = f.Close() }()
		if d := s.VerifyParent(f, home+"/Library/Caches"); !d.Allowed {
			t.Fatalf("VerifyParent = %+v", d)
		}
		if d := s.VerifyParent(f, home+"/Library/Logs"); d.Reason != ReasonParentChanged {
			t.Fatalf("wrong planned parent: %+v", d)
		}
	})
	t.Run("parent renamed after authorisation", func(t *testing.T) {
		home := testutil.NewHome(t, spec)
		s := newSession(t, Env{Home: home})
		f, d := s.AuthorizeFresh(cachesRule, home+"/Library/Caches/app")
		if !d.Allowed {
			t.Fatal(d)
		}
		defer func() { _ = f.Close() }()
		if err := os.Rename(home+"/Library/Caches", home+"/Library/Caches.old"); err != nil {
			t.Fatal(err)
		}
		if d := s.VerifyParent(f, home+"/Library/Caches"); d.Reason != ReasonParentChanged {
			t.Fatalf("VerifyParent = %+v, want %s", d, ReasonParentChanged)
		}
	})
	t.Run("parent swapped for a symlink", func(t *testing.T) {
		home := testutil.NewHome(t, spec)
		s := newSession(t, Env{Home: home})
		f, d := s.AuthorizeFresh(cachesRule, home+"/Library/Caches/app")
		if !d.Allowed {
			t.Fatal(d)
		}
		defer func() { _ = f.Close() }()
		if err := os.Rename(home+"/Library/Caches", home+"/Library/Caches.old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(home+"/elsewhere", home+"/Library/Caches"); err != nil {
			t.Fatal(err)
		}
		if d := s.VerifyParent(f, home+"/Library/Caches"); d.Allowed {
			t.Fatalf("VerifyParent allowed a swapped parent: %+v", d)
		}
	})
	t.Run("parent replaced by another directory at the same path", func(t *testing.T) {
		home := testutil.NewHome(t, spec)
		s := newSession(t, Env{Home: home})
		f, d := s.AuthorizeFresh(cachesRule, home+"/Library/Caches/app")
		if !d.Allowed {
			t.Fatal(d)
		}
		defer func() { _ = f.Close() }()
		if err := os.Rename(home+"/Library/Caches", home+"/Library/Caches.old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(home+"/Library/Caches.old", home+"/Library/Caches2"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(home+"/Library/Caches", 0o755); err != nil {
			t.Fatal(err)
		}
		if d := s.VerifyParent(f, home+"/Library/Caches"); d.Reason != ReasonParentChanged {
			t.Fatalf("VerifyParent = %+v, want %s", d, ReasonParentChanged)
		}
	})
}

func TestAuthorizeDestination(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/taken/f", Content: "x"},
			{Path: "Library/Caches/Upper/f", Content: "x"},
			{Path: "dev/app/package.json", Content: "{}"},
			{Path: "dev/nomarker/src/x", Content: "x"},
			{Path: "elsewhere/x", Content: "x"},
		},
		Symlinks: []testutil.Symlink{{Path: "Library/Logs", Target: "~/elsewhere"}},
	})
	logsRule := StaticRule{ID: "logs", Roots: []string{"Library/Logs"}, Kind: MatchChildren}
	s := newSession(t, Env{Home: home, Protect: []string{home + "/Library/Caches/keep"}})
	cases := []struct {
		name   string
		rule   Rule
		rel    string
		kind   fs.FileMode
		reason string
	}{
		{"vacant path is allowed", cachesRule, "Library/Caches/vacant", fs.ModeDir, ""},
		{"occupied path", cachesRule, "Library/Caches/taken", fs.ModeDir, ReasonDestinationExists},
		{"case variant of an existing name", cachesRule, "Library/Caches/upper", fs.ModeDir, ReasonDestinationExists},
		{"symlinked parent", logsRule, "Library/Logs/x", 0, ReasonSymlinkInPath},
		{"missing parent", nodeModulesRule, "dev/gone/node_modules", fs.ModeDir, ReasonMissing},
		{"denylisted", cachesRule, "Documents/x", fs.ModeDir, ReasonDenylisted},
		{"user protected", cachesRule, "Library/Caches/keep", fs.ModeDir, ReasonUserProtected},
		{"rule root itself", derivedDataRule, "Library/Developer/Xcode/DerivedData", fs.ModeDir, ReasonIsRuleRoot},
		{"project artifact beside its marker", nodeModulesRule, "dev/app/node_modules", fs.ModeDir, ""},
		{"project artifact without a marker", nodeModulesRule, "dev/nomarker/node_modules", fs.ModeDir, ReasonNotRuleItem},
		{"project artifact must be a directory", nodeModulesRule, "dev/app/node_modules", 0, ReasonNotRuleItem},
		{"installer must be a file", downloadsRule, "Downloads/a.dmg", fs.ModeDir, ReasonNotRuleItem},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, d := s.AuthorizeDestination(tc.rule, home+"/"+tc.rel, tc.kind)
			if tc.reason == "" {
				if !d.Allowed || f == nil {
					t.Fatalf("refused: %+v", d)
				}
				defer func() { _ = f.Close() }()
				if d := s.VerifyParent(f, home+"/"+tc.rel[:len(tc.rel)-len(f.Name)-1]); !d.Allowed {
					t.Fatalf("parent descriptor does not verify: %+v", d)
				}
				return
			}
			if d.Allowed || d.Reason != tc.reason || f != nil {
				t.Fatalf("got %+v, want %s", d, tc.reason)
			}
		})
	}
}

// OpenUnderHome reads relative to the pinned home descriptor: after home is
// swapped for another directory, it still reads the approved one.
func TestOpenUnderHomeReadsThePinnedHome(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
		{Path: ".config/kipple/config.toml", Content: "level = \"safe\"\n"},
	}})
	s := newSession(t, Env{Home: home})
	if err := os.Rename(home, home+".approved"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home+"/.config/kipple", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home+"/.config/kipple/config.toml", []byte("level = \"review\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := s.OpenUnderHome(".config/kipple/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	_ = f.Close()
	if string(b) != "level = \"safe\"\n" {
		t.Fatalf("read %q from the swapped-in home", b)
	}
	if err := os.Symlink(home+".approved/.config/kipple/config.toml", home+".approved/link.toml"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenUnderHome("link.toml"); err == nil {
		t.Fatal("followed a symlinked final component")
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x"} {
		if _, err := s.OpenUnderHome(bad); err == nil {
			t.Errorf("OpenUnderHome(%q) accepted", bad)
		}
	}
}

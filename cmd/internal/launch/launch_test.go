package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/gitstate"
	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

// fakeReal is a stand-in for the real home folder, in a temp folder.
func fakeReal(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := filepath.Join(base, "Users", "me")
	if err := os.MkdirAll(filepath.Join(h, "Documents", "OldMac"), 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

// runtimeFor reads KIPPLE_HOME from the environment and treats real as the
// real home; asking for the machine's own home fails the test.
func runtimeFor(t *testing.T, real string) Runtime {
	return Runtime{
		Getenv:    os.Getenv,
		UserHome:  func() (string, error) { t.Error("the real home was requested"); return "", errors.New("no") },
		RealHomes: func() ([]string, error) { return []string{real}, nil },
	}
}

// The KIPPLE_HOME guard: a fixture is accepted; the real home, a folder
// inside it, a folder containing it, a relative path, and an unknown real
// home are refused with code home.
func TestKippleHomeGuard(t *testing.T) {
	fixture := testutil.NewHome(t, testutil.Spec{})
	real := fakeReal(t)
	cases := []struct {
		name, home string
		reals      func() ([]string, error)
		ok         bool
	}{
		{"a separate fixture folder is accepted", fixture, nil, true},
		{"the real home is refused", real, nil, false},
		{"a folder inside the real home is refused", filepath.Join(real, "Documents", "OldMac"), nil, false},
		{"a folder containing the real home is refused", filepath.Dir(real), nil, false},
		{"a relative path is refused", "fixture/home", nil, false},
		{"an unknown real home is refused", fixture, func() ([]string, error) { return nil, errors.New("unknown") }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.GuardRealHome(t)
			rt := runtimeFor(t, real)
			if c.reals != nil {
				rt.RealHomes = c.reals
			}
			t.Setenv("KIPPLE_HOME", c.home)
			home, fromEnv, _, err := ResolveHome(rt)
			if c.ok {
				if err != nil || home != fixture || !fromEnv {
					t.Fatalf("%q %v %v", home, fromEnv, err)
				}
				return
			}
			var le *Error
			if !errors.As(err, &le) || le.Code != CodeHome {
				t.Fatalf("not refused with code home: %v", err)
			}
		})
	}
}

func TestStateDirAndStore(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{})
	rt := runtimeFor(t, fakeReal(t))
	h, err := Resolve(rt)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIPPLE_STATE_DIR", "state")
	var le *Error
	if _, err := Store(rt, h); !errors.As(err, &le) || le.Code != CodeReceipt {
		t.Fatalf("a relative KIPPLE_STATE_DIR: %v", err)
	}
	t.Setenv("KIPPLE_STATE_DIR", "")
	if d, _ := StateDir(rt, h); d != DefaultStateDir(runtime.GOOS, home) {
		t.Fatalf("default state dir %q", d)
	}
}

// Spec scenario "Linux state directory" (platform-support), as data: the
// default state folder per platform, under the effective home.
func TestDefaultStateDir(t *testing.T) {
	for _, tc := range []struct{ goos, want string }{
		{"darwin", "/h/Library/Application Support/kipple"},
		{"linux", "/h/.local/state/kipple"},
	} {
		if got := DefaultStateDir(tc.goos, "/h"); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.goos, got, tc.want)
		}
	}
}

func TestOptions(t *testing.T) {
	testutil.GuardRealHome(t)
	h := Home{Path: "/fixture/home", FromEnv: true}
	if h.ApplyOptions(receipt.Store{}, "test", nil, nil).Finder != nil {
		t.Fatal("a KIPPLE_HOME gets the NSFileManager fallback, which moves into the real Trash")
	}
	h.FromEnv = false
	if h.ApplyOptions(receipt.Store{}, "test", nil, nil).Finder == nil {
		t.Fatal("the real home lacks the fallback")
	}
	if o := h.ScanOptions("safe", "test"); o.Level != "safe" || o.Env.HomeIdentity == nil || o.Rules == nil {
		t.Fatalf("%+v", o)
	}
	if HomeLabel("/Users/you", false) != "~" || HomeLabel("/tmp/a/b/home", true) != "KIPPLE_HOME …/b/home" {
		t.Fatal("home label")
	}
	if r := (NoGit{}).Inspect(context.Background(), "/x"); r.State != gitstate.StateUnavailable {
		t.Fatalf("%+v", r)
	}
}

func TestInsideRealHome(t *testing.T) {
	testutil.GuardRealHome(t)
	real := fakeReal(t)
	rt := runtimeFor(t, real)
	for p, want := range map[string]bool{
		real: true, filepath.Join(real, "Documents"): true, filepath.Dir(real): true,
		filepath.Join(filepath.Dir(filepath.Dir(real)), "elsewhere"): false,
	} {
		if got := InsideRealHome(rt, p); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
	rt.RealHomes = func() ([]string, error) { return nil, errors.New("unknown") }
	if !InsideRealHome(rt, "/tmp/x") {
		t.Error("an unknown real home must fail closed")
	}
}

// Task 2.2: a KIPPLE_HOME refused by the home guard (here: the mount table
// cannot be read, so bind-mount aliases of the real home cannot be ruled
// out) fails with code home before anything in the fixture is read: a
// broken config file in it is never reached.
func TestRefusedKippleHomeReadsNothing(t *testing.T) {
	fixture := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: ".config/kipple/config.toml", Content: "this is [not toml"}}})
	real := fakeReal(t)
	rt := runtimeFor(t, real)
	rt.GuardFS = homeguard.System()
	rt.GuardFS.Aliases = func(string) (homeguard.Aliases, error) {
		return homeguard.Aliases{}, errors.New("mountinfo unreadable")
	}
	t.Setenv("KIPPLE_HOME", fixture)
	_, err := Resolve(rt)
	var le *Error
	if !errors.As(err, &le) || le.Code != CodeHome {
		t.Fatalf("err = %v, want code home", err)
	}
	// The same fixture with a readable table gets as far as the config.
	rt.GuardFS.Aliases = func(string) (homeguard.Aliases, error) { return homeguard.Aliases{}, nil }
	if _, err := Resolve(rt); !errors.As(err, &le) || le.Code != CodeConfig {
		t.Fatalf("err = %v, want code config once the guard passes", err)
	}
}

// specs/safety-policy "XDG user directories are protected on Linux",
// through launch with GOOS linux (any host): the folders become
// protections, Downloads stays cleanable, and problems are plan warnings.
func TestUserDirsOnLinux(t *testing.T) {
	old := time.Now().Add(-60 * 24 * time.Hour)
	fixture := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
		{Path: ".config/user-dirs.dirs", Content: "XDG_DOCUMENTS_DIR=\"$HOME/Dokumente\"\nXDG_DOWNLOAD_DIR=\"$HOME/Downloads\"\nXDG_DESKTOP_DIR=\"$HOME\"\nXDG_PICTURES_DIR=\"$HOME/.npm\"\n"},
		{Path: "Dokumente/x/brief.txt", Content: "x"},
		{Path: "Downloads/old.iso", Size: 1024, MTime: old},
		{Path: ".npm/_cacache/content/blob", Content: "x", MTime: old},
	}})
	rt := runtimeFor(t, fakeReal(t))
	rt.GOOS = "linux"
	t.Setenv("KIPPLE_HOME", fixture)
	h, err := Resolve(rt)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Localised Documents folder", func(t *testing.T) {
		r := policy.StaticRule{ID: "hypothetical", Roots: []string{"Dokumente"}, Kind: policy.MatchChildren}
		if d := policy.Authorize(h.PolicyEnv(), r, fixture+"/Dokumente/x"); d.Reason != policy.ReasonUserProtected {
			t.Fatalf("got %+v", d)
		}
	})
	p, err := scan.Run(context.Background(), h.ScanOptions(rules.LevelSafe, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Download dir stays cleanable", func(t *testing.T) {
		found := false
		for _, it := range p.Items {
			found = found || (it.Rule == "downloads.installers" && it.Path == fixture+"/Downloads/old.iso")
		}
		if !found {
			t.Fatalf("no downloads.installers item: %+v", p.Items)
		}
	})
	t.Run("User dir set to home", func(t *testing.T) {
		if !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "XDG_DESKTOP_DIR") }) {
			t.Fatalf("warnings %q", p.Warnings)
		}
	})
	t.Run("protection covers a rule root warns", func(t *testing.T) {
		if !slices.ContainsFunc(p.Warnings, func(w string) bool {
			return strings.Contains(w, "XDG_PICTURES_DIR") && strings.Contains(w, "npm.cache")
		}) {
			t.Fatalf("warnings %q", p.Warnings)
		}
		for _, it := range p.Items {
			if it.Rule == "npm.cache" && it.Selected {
				t.Fatalf("a protected rule root offered %+v", it)
			}
		}
	})
	t.Run("macOS ignores user-dirs.dirs", func(t *testing.T) {
		rt := rt
		rt.GOOS = "darwin"
		h, err := Resolve(rt)
		if err != nil || len(h.UserDirs.Protect) != 0 || len(h.Warnings) != 0 {
			t.Fatalf("%+v %v", h.UserDirs, err)
		}
	})
}

// Wave 2 red-team R2: on Linux the default receipts folder under home is
// opened through the policy session, so a symlink or a bind mount on the
// way (~/.local/state into ~/.ssh) makes the receipt fail and nothing is
// written there.
func TestLinuxStateFolderIsVerified(t *testing.T) {
	resolve := func(t *testing.T, spec testutil.Spec, mounts func(home string) policy.MountOracle) (Home, Runtime) {
		t.Helper()
		fixture := testutil.NewHome(t, spec)
		rt := runtimeFor(t, fakeReal(t))
		rt.GOOS = "linux"
		if mounts != nil {
			rt.Mounts = mounts(fixture)
		}
		t.Setenv("KIPPLE_HOME", fixture)
		t.Setenv("KIPPLE_STATE_DIR", "")
		h, err := Resolve(rt)
		if err != nil {
			t.Fatal(err)
		}
		return h, rt
	}
	create := func(t *testing.T, h Home, rt Runtime) error {
		t.Helper()
		s, err := Store(rt, h)
		if err != nil {
			t.Fatal(err)
		}
		r := plan.Receipt{CreatedAt: time.Now(), Home: h.Path, Level: "safe", Mover: plan.MoverNone}
		_, err = s.Create(&r)
		return err
	}
	t.Run("default folder created 0700 under home", func(t *testing.T) {
		h, rt := resolve(t, testutil.Spec{}, nil)
		if err := create(t, h, rt); err != nil {
			t.Fatal(err)
		}
		for _, d := range []string{".local", ".local/state", ".local/state/kipple", ".local/state/kipple/receipts"} {
			fi, err := os.Stat(filepath.Join(h.Path, d))
			if err != nil || fi.Mode().Perm() != 0o700 {
				t.Errorf("%s: %v %v", d, fi.Mode(), err)
			}
		}
	})
	t.Run("symlinked state folder", func(t *testing.T) {
		h, rt := resolve(t, testutil.Spec{
			Files:    []testutil.File{{Path: ".ssh/id_ed25519", Content: "key"}},
			Symlinks: []testutil.Symlink{{Path: ".local/state", Target: "~/.ssh"}},
		}, nil)
		if err := create(t, h, rt); err == nil {
			t.Fatal("a receipt was written through a symlink")
		}
		if es, _ := os.ReadDir(filepath.Join(h.Path, ".ssh")); len(es) != 1 {
			t.Fatalf(".ssh changed: %v", es)
		}
	})
	t.Run("state folder is a bind mount", func(t *testing.T) {
		h, rt := resolve(t, testutil.Spec{Dirs: []testutil.Dir{{Path: ".local/state"}}}, func(home string) policy.MountOracle {
			fi, err := os.Lstat(filepath.Join(home, ".local/state"))
			if err != nil {
				t.Fatal(err)
			}
			dev, ino, _ := policy.Identity(fi)
			return policy.StubMounts{Roots: map[policy.FileIdentity]bool{{Dev: dev, Ino: ino}: true}}
		})
		if err := create(t, h, rt); err == nil {
			t.Fatal("a receipt was written through a mount point")
		}
		if _, err := os.Stat(filepath.Join(h.Path, ".local/state/kipple")); !os.IsNotExist(err) {
			t.Fatal("something was created inside the mount point")
		}
	})
}

// Wave 2 re-attack F4: an explicit KIPPLE_STATE_DIR inside a denylisted
// folder of home is honoured but warned about.
func TestStateDirInsideAProtectedFolderWarns(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Dirs: []testutil.Dir{{Path: ".ssh"}, {Path: "elsewhere"}}})
	if w := StateDirWarning(home+"/.ssh/kipple", home); !strings.Contains(w, "protected folder") {
		t.Fatalf("warning %q", w)
	}
	link := filepath.Join(filepath.Dir(home), "sshlink")
	if err := os.Symlink(home+"/.ssh", link); err != nil {
		t.Fatal(err)
	}
	if w := StateDirWarning(link+"/receipts", home); w == "" {
		t.Fatal("a state folder reaching .ssh through a symlink is not warned about")
	}
	for _, ok := range []string{home + "/elsewhere/state", filepath.Join(filepath.Dir(home), "state"), ""} {
		if w := StateDirWarning(ok, home); w != "" {
			t.Fatalf("%q: %q", ok, w)
		}
	}
}

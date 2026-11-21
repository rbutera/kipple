package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rbutera/kipple/internal/gitstate"
	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/receipt"
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

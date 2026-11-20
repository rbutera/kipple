package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rbutera/kipple/internal/gitstate"
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
	if d, _ := StateDir(rt, h); d != filepath.Join(home, "Library", "Application Support", "kipple") {
		t.Fatalf("default state dir %q", d)
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

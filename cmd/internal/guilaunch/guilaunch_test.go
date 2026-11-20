package guilaunch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rbutera/kipple/cmd/internal/launch"
	"github.com/rbutera/kipple/internal/guimodel"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

var old = time.Now().Add(-200 * 24 * time.Hour)

func options(t *testing.T) (Options, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "Users", "me")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{
		Runtime: launch.Runtime{
			Getenv:    os.Getenv,
			UserHome:  func() (string, error) { t.Error("the real home was requested"); return "", errors.New("no") },
			RealHomes: func() ([]string, error) { return []string{real}, nil },
		},
		Geteuid: func() int { return 501 }, Version: "test",
		CommandLineTools: func() bool { return true },
	}, real
}

// The GUI ignores the config's level (it always pre-selects what scan
// --level safe selects) but keeps its protections.
func TestConfigLevelIgnoredProtectApplied(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
		{Path: ".config/kipple/config.toml", Content: "level = \"review\"\nprotect = [\"~/Library/Logs/app\"]\n"},
		{Path: "Library/Logs/app/log.txt", Size: 4096, MTime: old},
		{Path: "Library/Logs/other/log.txt", Size: 4096, MTime: old},
		{Path: "go/pkg/mod/x/y.go", Size: 4096, MTime: old},
	}})
	o, _ := options(t)
	m := Model(o)
	cfg := m.Config()
	if cfg.Scan.Level != rules.LevelSafe {
		t.Fatalf("scan level %q", cfg.Scan.Level)
	}
	protected := filepath.Join(home, "Library", "Logs", "app")
	found := false
	for _, p := range cfg.Scan.Env.Protect {
		found = found || p == protected
	}
	if !found || len(cfg.Apply.Env.Protect) != len(cfg.Scan.Env.Protect) {
		t.Fatalf("protect %v / %v", cfg.Scan.Env.Protect, cfg.Apply.Env.Protect)
	}
	m.ScanNow()
	for _, it := range m.Plan().Items {
		if it.Path == protected {
			t.Fatal("a protected folder was listed")
		}
		if it.Rule == "go.mod-cache" && it.Selected {
			t.Fatal("the config's level review selected a review item")
		}
	}
	if len(m.Preselected()) != 1 {
		t.Fatalf("pre-selected %v", m.Preselected())
	}
}

func TestRefusals(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		testutil.NewHome(t, testutil.Spec{})
		o, _ := options(t)
		o.Geteuid = func() int { return 0 }
		if p := Model(o).Problem(); p.Kind != guimodel.ProblemRoot {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("KIPPLE_HOME inside the real home", func(t *testing.T) {
		testutil.NewHome(t, testutil.Spec{})
		o, real := options(t)
		inside := filepath.Join(real, "Documents")
		if err := os.MkdirAll(inside, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KIPPLE_HOME", inside)
		if p := Model(o).Problem(); p.Kind != guimodel.ProblemHomeRefused {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("bad config", func(t *testing.T) {
		testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: ".config/kipple/config.toml", Content: "bogus = 1\n"}}})
		o, _ := options(t)
		if p := Model(o).Problem(); p.Kind != guimodel.ProblemConfig {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("relative KIPPLE_STATE_DIR", func(t *testing.T) {
		testutil.NewHome(t, testutil.Spec{})
		t.Setenv("KIPPLE_STATE_DIR", "state")
		o, _ := options(t)
		if p := Model(o).Problem(); p.Kind != guimodel.ProblemState {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("no Command Line Tools", func(t *testing.T) {
		testutil.NewHome(t, testutil.Spec{})
		o, _ := options(t)
		o.CommandLineTools = func() bool { return false }
		cfg := Model(o).Config()
		if _, ok := cfg.Scan.Git.(launch.NoGit); !ok || cfg.GitAvailable {
			t.Fatal("git is still run without the Command Line Tools")
		}
		if _, ok := cfg.Apply.Git.(launch.NoGit); !ok {
			t.Fatal("apply's re-check still runs git")
		}
	})
}

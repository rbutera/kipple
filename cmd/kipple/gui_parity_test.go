package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rbutera/kipple/cmd/internal/guilaunch"
	"github.com/rbutera/kipple/internal/guimodel"
	"github.com/rbutera/kipple/internal/plan"
)

// guiModel builds the GUI's model for the test environment exactly as
// cmd/kipple-gui does, through cmd/internal/guilaunch.
func guiModel(t *testing.T, rt runtimeEnv) *guimodel.Model {
	t.Helper()
	return guilaunch.Model(guilaunch.Options{
		Runtime: rt.launch(), Geteuid: rt.geteuid, Version: version, Procs: rt.procs,
		CommandLineTools: func() bool { return true },
	})
}

// Spec scenario "Same selection as CLI" (gui): the GUI and kipple scan
// --level safe scan the same fixture and pre-select the same item IDs. The
// config file asks for level review, which the GUI ignores; its protect
// list still applies to both.
func TestSameSelectionAsCLI(t *testing.T) {
	home := fixture(t)
	writeConfig(t, home, "level = \"review\"\nprotect = [\"~/Library/Logs/app\"]\n")
	code, out, _ := cli(t, "scan", "--level", "safe", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var want []string
	for _, it := range p.Items {
		if it.Selected {
			want = append(want, it.ID)
		}
	}
	m := guiModel(t, testEnv(t))
	m.ScanNow()
	if m.Screen() != guimodel.ScreenReview {
		t.Fatalf("screen %d: %+v", m.Screen(), m.Problem())
	}
	if got := m.Preselected(); len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("GUI pre-selects %v, kipple scan --level safe selects %v", got, want)
	}
	// The config's level would select more in the CLI's default scan.
	_, out, _ = cli(t, "scan", "--json")
	var review plan.Plan
	exactlyOneJSON(t, out, &review)
	if review.Totals.SelectedCount <= len(want) {
		t.Fatalf("the config level did not change the CLI default (%d vs %d)", review.Totals.SelectedCount, len(want))
	}
	for _, it := range m.Plan().Items {
		if it.Path == home+"/Library/Logs/app" {
			t.Fatal("the config's protect list did not apply to the GUI")
		}
	}
}

func TestGUILaunchRefusals(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		fixture(t)
		rt := testEnv(t)
		rt.geteuid = func() int { return 0 }
		if p := guiModel(t, rt).Problem(); p.Kind != guimodel.ProblemRoot {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("home refused", func(t *testing.T) {
		fixture(t)
		rt := testEnv(t)
		real := fakeRealHome(t)
		rt.realHomes = func() ([]string, error) { return []string{real}, nil }
		t.Setenv("KIPPLE_HOME", real)
		if p := guiModel(t, rt).Problem(); p.Kind != guimodel.ProblemHomeRefused || len(p.Body) != 2 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("config", func(t *testing.T) {
		home := fixture(t)
		writeConfig(t, home, "level = \"everything\"\n")
		if p := guiModel(t, testEnv(t)).Problem(); p.Kind != guimodel.ProblemConfig {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("a hostile KIPPLE_HOME is shown escaped", func(t *testing.T) {
		base := filepath.Dir(fixture(t))
		odd := filepath.Join(base, "x\x1b]0;t\a", "home")
		if err := os.MkdirAll(odd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KIPPLE_HOME", odd)
		m := guiModel(t, testEnv(t))
		if l := m.Config().HomeLabel; strings.ContainsRune(l, 0x1b) || strings.ContainsRune(l, 0x07) || !strings.Contains(l, `\x1b`) {
			t.Fatalf("label %q", l)
		}
	})
	t.Run("no Command Line Tools", func(t *testing.T) {
		fixture(t)
		rt := testEnv(t)
		m := guilaunch.Model(guilaunch.Options{Runtime: rt.launch(), Geteuid: rt.geteuid, Version: version, Procs: rt.procs, CommandLineTools: func() bool { return false }})
		if m.Config().GitAvailable || m.Config().Scan.Git == nil || m.Config().Apply.Git == nil {
			t.Fatal("git is not replaced when the Command Line Tools are missing")
		}
		if m.Config().Apply.Finder != nil || !m.Config().TestHome {
			t.Fatal("a KIPPLE_HOME gets the NSFileManager fallback")
		}
	})
}

// writeConfig writes the fixture home's config file.
func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	p := filepath.Join(home, ".config", "kipple", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

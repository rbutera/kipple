package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/tui"
)

// runMsgs runs cmd and feeds its messages to m (spinner ticks dropped).
func runMsgs(m tea.Model, cmd tea.Cmd) tea.Model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = runMsgs(m, c)
		}
	case spinner.TickMsg, tea.QuitMsg, nil:
	default:
		next, c := m.Update(msg)
		m = runMsgs(next, c)
	}
	return m
}

// tuiSize is the terminal size headlessTUI renders at.
var tuiSize = [2]int{120, 50}

// headlessTUI stands in for tui.Run: it drives the real model with the
// configuration the CLI built, presses keys, and records the first view
// and the configuration.
func headlessTUI(view *string, got *tui.Config, keys ...string) func(tui.Config, io.Reader, io.Writer) (tui.Outcome, error) {
	return func(cfg tui.Config, _ io.Reader, _ io.Writer) (tui.Outcome, error) {
		*got = cfg
		cfg.Renderer = lipgloss.NewRenderer(io.Discard)
		var m tea.Model = tui.New(cfg)
		m, _ = m.Update(tea.WindowSizeMsg{Width: tuiSize[0], Height: tuiSize[1]})
		m = runMsgs(m, m.Init())
		*view = m.View()
		for _, k := range keys {
			next, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
			m = runMsgs(next, c)
		}
		return m.(tui.Model).Outcome(), nil
	}
}

// Spec scenario "Launch" (tui), through the CLI: kipple tui on a terminal
// scans the fixture and lists every rule with its total and every item
// with its size; quitting moves nothing.
func TestTUILaunch(t *testing.T) {
	home := cleanFixture(t)
	t.Setenv("NO_COLOR", "1")
	before := testutil.Checksum(t, home)
	rt := testEnv(t)
	rt.isTTY = func(io.Writer) bool { return true }
	var view string
	var cfg tui.Config
	rt.tui = headlessTUI(&view, &cfg, "q")
	code, out, _ := cliWith(t, rt, "tui")
	if code != exitOK || strings.TrimSpace(out) != "Nothing was moved." {
		t.Fatalf("exit %d output %q", code, out)
	}
	for _, want := range []string{"App caches", "App logs", "Go module cache", "Old installers in Downloads", "Ollama models", "65.5 kB", "4.1 kB", "~/Library/Caches/com.example.app", "~/Downloads/Old.dmg"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if cfg.Scan.Env.Home != home || cfg.Apply.Env.Home != home || cfg.Apply.Finder != nil || cfg.Color {
		t.Fatalf("config: scan home %q apply home %q finder set %v color %v", cfg.Scan.Env.Home, cfg.Apply.Env.Home, cfg.Apply.Finder != nil, cfg.Color)
	}
	if cfg.Apply.Permanent || cfg.Apply.DryRun {
		t.Fatal("the TUI was given permanent or dry-run options")
	}
	if testutil.Checksum(t, home) != before {
		t.Fatal("kipple tui changed the fixture without apply")
	}
}

// kipple tui applies through the shared apply path and prints the result
// for the scrollback once the full-screen view closes.
func TestTUIApply(t *testing.T) {
	home := cleanFixture(t)
	rt := testEnv(t)
	rt.isTTY = func(io.Writer) bool { return true }
	var view string
	var cfg tui.Config
	rt.tui = headlessTUI(&view, &cfg, "a", "y")
	code, out, _ := cliWith(t, rt, "tui")
	if code != exitOK || !strings.Contains(out, "Moved 2 items to the Trash") || !strings.Contains(out, "kipple restore ") {
		t.Fatalf("exit %d output:\n%s", code, out)
	}
	if _, err := os.Lstat(filepath.Join(home, "Library/Logs/app")); !os.IsNotExist(err) {
		t.Fatal("item not moved")
	}
	if _, err := os.Lstat(filepath.Join(home, ".ollama/models/blobs")); err != nil {
		t.Fatal("info item moved")
	}
	code, out, _ = cli(t, "history", "--json")
	var h plan.History
	exactlyOneJSON(t, out, &h)
	if code != exitOK || len(h.Receipts) != 1 || len(h.Receipts[0].Moved) != 2 {
		t.Fatalf("history: exit %d %+v", code, h)
	}
}

// kipple tui needs a terminal and has no JSON output: both are usage
// errors, and nothing waits for input.
func TestTUIRefusals(t *testing.T) {
	cleanFixture(t)
	code, _, errOut := cli(t, "tui")
	if code != exitUsage || !strings.Contains(errOut, "needs a terminal") {
		t.Fatalf("non-TTY: exit %d stderr %q", code, errOut)
	}
	code, out, _ := cli(t, "tui", "--json")
	var env errorEnvelope
	exactlyOneJSON(t, out, &env)
	if code != exitUsage || env.Error.Code != codeUsage {
		t.Fatalf("--json: exit %d %+v", code, env)
	}
}

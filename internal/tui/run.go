package tui

import (
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Run shows the TUI on the terminal behind in and out (full screen) until
// the user quits, and returns how the session ended.
func Run(cfg Config, in io.Reader, out io.Writer) (Outcome, error) {
	if cfg.Renderer == nil {
		cfg.Renderer = lipgloss.NewRenderer(out)
	}
	p := tea.NewProgram(New(cfg), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return Outcome{}, fmt.Errorf("tui: %w", err)
	}
	m, ok := final.(Model)
	if !ok {
		return Outcome{}, fmt.Errorf("tui: unexpected model %T", final)
	}
	m.cancel()
	return m.Outcome(), nil
}

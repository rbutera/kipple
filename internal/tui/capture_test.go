package tui

import (
	"fmt"
	"os"
	"testing"
)

// TestCaptureViews prints the review, info, confirmation and done views
// from a fixture at 80 and 140 columns with the NO_COLOR theme. It runs
// only with KIPPLE_TUI_CAPTURE set; use it to refresh the README capture.
func TestCaptureViews(t *testing.T) {
	if os.Getenv("KIPPLE_TUI_CAPTURE") == "" {
		t.Skip()
	}
	for _, size := range [][2]int{{80, 24}, {140, 30}} {
		f := newFx(t, false)
		m := start(t, f, size[0], size[1])
		fmt.Printf("=== review %dx%d\n%s\n", size[0], size[1], m.View())
		m = highlight(t, m, ".ollama/models/blobs")
		fmt.Printf("=== info %dx%d\n%s\n", size[0], size[1], press(m, " ").View())
		m = press(m, "a")
		fmt.Printf("=== confirm\n%s\n", m.View())
		m = press(m, "y")
		fmt.Printf("=== done\n%s\n", m.View())
	}
}

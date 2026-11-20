package tui

import (
	"fmt"
	"os"
	"testing"
)

// TestCaptureViews prints the review, info, confirmation and done views
// from a fixture at 80 and 120 columns, in the NO_COLOR and the brand
// theme (rendered without escape codes). It runs only with
// KIPPLE_TUI_CAPTURE set; use it to refresh the README capture.
func TestCaptureViews(t *testing.T) {
	if os.Getenv("KIPPLE_TUI_CAPTURE") == "" {
		t.Skip("set KIPPLE_TUI_CAPTURE to print the views")
	}
	for _, color := range []bool{false, true} {
		for _, size := range [][2]int{{80, 24}, {120, 26}} {
			f := newFx(t, color)
			m := start(t, f, size[0], size[1])
			fmt.Printf("=== review %dx%d color=%v\n%s\n", size[0], size[1], color, m.View())
			m = highlight(t, m, ".ollama/models/blobs")
			fmt.Printf("=== info %dx%d color=%v\n%s\n", size[0], size[1], color, press(m, " ").View())
			m = press(m, "a")
			fmt.Printf("=== confirm %dx%d color=%v\n%s\n", size[0], size[1], color, m.View())
			m = press(m, "y")
			fmt.Printf("=== done %dx%d color=%v\n%s\n", size[0], size[1], color, m.View())
		}
	}
}

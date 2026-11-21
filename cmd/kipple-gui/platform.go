//go:build gui

package main

// platformRefusal returns the message kipple-gui prints before exiting 1 on
// an operating system where the app is not available, or "" on macOS. The
// Linux app arrives in v0.3.1 (design L11 of openspec/changes/linux-port);
// until then the TUI offers the same review and apply.
func platformRefusal(goos string) string {
	switch goos {
	case "darwin":
		return ""
	case "linux":
		return "kipple-gui: the Linux app arrives in v0.3.1; use kipple tui, which offers the same review and apply"
	default:
		return "kipple-gui: the app runs on macOS only; kipple supports macOS and Linux"
	}
}

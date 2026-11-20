package tui

import "github.com/rbutera/kipple/internal/words"

// The reason copy is shared with the GUI (internal/words).

// blockers returns the reasons other than tier reasons, in words.
func blockers(reasons []string) []string { return words.Blockers(reasons) }

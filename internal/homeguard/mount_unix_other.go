//go:build unix && !darwin

package homeguard

// mountPoint returns "": only macOS has firmlinks, which make a volume's
// mount point (and its ancestors) reach the real home under another name.
// Elsewhere the ".." chains and the tail probe in Relate are complete.
func mountPoint(string) (string, error) { return "", nil }

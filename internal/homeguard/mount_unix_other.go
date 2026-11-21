//go:build unix && !darwin && !linux

package homeguard

// mountPoint returns "": only macOS has firmlinks, which make a volume's
// mount point (and its ancestors) reach the real home under another name.
func mountPoint(string) (string, error) { return "", nil }

// aliases is nil: kipple supports macOS and Linux only.
var aliases func(string) (Aliases, error)

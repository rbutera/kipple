//go:build !unix

package homeguard

// System fails every lookup on platforms without directory identities, so
// callers refuse (kipple targets macOS).
func System() FS {
	return FS{
		Chain:      func(string, bool) ([]ID, error) { return nil, ErrUnsupported },
		Stat:       func(string) (ID, bool, error) { return ID{}, false, ErrUnsupported },
		MountPoint: func(string) (string, error) { return "", ErrUnsupported },
	}
}

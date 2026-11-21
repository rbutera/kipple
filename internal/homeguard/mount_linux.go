//go:build linux

package homeguard

import (
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/mountinfo"
)

// mountPoint returns "": Linux has no firmlinks. Its bind mounts are
// listed by aliases instead.
func mountPoint(string) (string, error) { return "", nil }

// openMountinfo is the mount table; tests replace it.
var openMountinfo = func() (io.ReadCloser, error) { return os.Open("/proc/self/mountinfo") }

// aliases reads /proc/self/mountinfo and lists where the real home is also
// visible (AliasesFrom). An unreadable or malformed table is an error, so
// KIPPLE_HOME is refused (fail closed).
var aliases = func(real string) (Aliases, error) {
	// AliasesFrom needs the symlink-resolved path (a Silverblue-style
	// /home -> var/home would otherwise match only the "/" mount).
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		return Aliases{}, err
	}
	real = resolved
	var st unix.Stat_t
	if err := unix.Stat(real, &st); err != nil {
		return Aliases{}, err
	}
	f, err := openMountinfo()
	if err != nil {
		return Aliases{}, err
	}
	defer func() { _ = f.Close() }()
	ms, err := mountinfo.Parse(f)
	if err != nil {
		return Aliases{}, err
	}
	return AliasesFrom(ms, real, st.Dev, mountPointDev)
}

// mountPointDev is the st_dev of the directory mounted at mp, from an
// O_PATH open that follows nothing.
func mountPointDev(mp string) (uint64, error) {
	fd, err := unix.Open(mp, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return 0, err
	}
	return st.Dev, nil
}

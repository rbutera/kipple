//go:build linux

package homeguard

import (
	"io"
	"os"

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
	return AliasesFrom(ms, real, st.Dev)
}

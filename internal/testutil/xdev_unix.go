//go:build unix

package testutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// OtherDevice returns a fresh, empty folder on a different device than
// the folder than, removed when the test ends, for cross-device tests. It
// tries $KIPPLE_XDEV, then /xdev (the second tmpfs of make test-linux),
// then /dev/shm. It fails the test, never skips it, when none qualifies.
func OtherDevice(t testing.TB, than string) string {
	t.Helper()
	var ref syscall.Stat_t
	if err := syscall.Stat(than, &ref); err != nil {
		t.Fatal(err)
	}
	var tried []error
	for _, root := range []string{os.Getenv("KIPPLE_XDEV"), "/xdev", "/dev/shm"} {
		if root == "" {
			continue
		}
		var st syscall.Stat_t
		if err := syscall.Stat(root, &st); err != nil {
			tried = append(tried, err)
			continue
		}
		if st.Dev == ref.Dev {
			tried = append(tried, fmt.Errorf("%s is on the same device as %s", root, than))
			continue
		}
		dir, err := os.MkdirTemp(root, "kipple-xdev-")
		if err != nil {
			tried = append(tried, err)
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			dir = r
		}
		return dir
	}
	t.Fatalf("no writable folder on another device than %s (set KIPPLE_XDEV): %v", than, errors.Join(tried...))
	return ""
}

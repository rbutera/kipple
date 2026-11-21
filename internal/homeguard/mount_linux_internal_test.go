//go:build linux

package homeguard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Standards addendum B (and red-team R4): with the real home under a
// symlinked parent (Silverblue's /home -> var/home), a bind mount of the
// home elsewhere is still found, so a KIPPLE_HOME at that bind is refused.
// The mount table is synthetic; the folders are real.
func TestSymlinkedParentHomeBindIsRefused(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(base, "var", "home", "kh")
	cand := filepath.Join(base, "mnt", "kh")
	for _, d := range []string{realDir, cand} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("var/home", filepath.Join(base, "home")); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if err := unix.Stat(realDir, &st); err != nil {
		t.Fatal(err)
	}
	dev := fmt.Sprintf("%d:%d", unix.Major(st.Dev), unix.Minor(st.Dev))
	esc := func(p string) string { return strings.ReplaceAll(p, " ", `\040`) }
	table := "1 0 " + dev + " / / rw - ext4 /dev/x rw\n" +
		"50 1 " + dev + " " + esc(realDir) + " " + esc(cand) + " rw - ext4 /dev/x rw\n"
	orig := openMountinfo
	openMountinfo = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(table)), nil }
	t.Cleanup(func() { openMountinfo = orig })

	// The stand-in real home is spelled through the symlinked parent.
	real := filepath.Join(base, "home", "kh")
	fsys := System()
	// The candidate is a separate folder in this test; make the identity
	// lookups treat the bind as what it is (the home).
	chain := fsys.Chain
	fsys.Chain = func(dir string, nofollow bool) ([]ID, error) {
		if dir == cand {
			return chain(realDir, nofollow)
		}
		return chain(dir, nofollow)
	}
	rel, err := Relate(fsys, cand, real)
	if err != nil || rel != Same {
		t.Fatalf("Relate(%s, %s) = %v, %v; want the bind of home refused as the real home", cand, real, rel, err)
	}
	al, err := aliases(real)
	if err != nil || len(al.Real) != 1 || al.Real[0] != cand {
		t.Fatalf("aliases(%s) = %+v, %v; want the bind at %s", real, al, err, cand)
	}
}

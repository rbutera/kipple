//go:build darwin

package apply

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/trash"
)

// mountVolume attaches a 2 MB APFS disk image inside the test's temp folder
// and returns its mount point (see the same helper in internal/trash).
func mountVolume(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("hdiutil not available")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	img, mnt := filepath.Join(base, "vol.dmg"), filepath.Join(base, "mnt")
	if err := os.Mkdir(mnt, 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, args := range [][]string{
		{"create", "-quiet", "-size", "2m", "-fs", "APFS", "-volname", "kipple-test", "-layout", "NONE", img},
		{"attach", "-quiet", "-nobrowse", "-noautoopen", "-mountpoint", mnt, img},
	} {
		cmd := exec.Command("hdiutil", args...)
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Skipf("hdiutil %s failed: %v: %s", args[0], err, out.String())
		}
	}
	t.Cleanup(func() {
		if err := exec.Command("hdiutil", "detach", "-quiet", "-force", mnt).Run(); err != nil {
			t.Errorf("hdiutil detach %s: %v", mnt, err)
		}
	})
	return mnt
}

// A Trash on another device: the move fails with EXDEV and the item is
// skipped with different_device; nothing is copied.
func TestTrashOnAnotherDevice(t *testing.T) {
	f := newFx(t, threeSafe())
	mnt := mountVolume(t)
	tr, err := trash.OpenDirTrasher(mnt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()
	o := f.opts(f.scan(""))
	o.Trash = tr
	res := f.run(o)
	if len(res.Moved) != 0 || len(res.Skipped) != 3 {
		t.Fatalf("%+v", res)
	}
	for _, s := range res.Skipped {
		if s.Reason != plan.SkipDifferentDevice || !exists(s.Path) {
			t.Errorf("%+v", s)
		}
	}
	if entries, _ := os.ReadDir(mnt); len(entries) > 1 { // .fseventsd at most
		for _, e := range entries {
			if e.Name() != ".fseventsd" {
				t.Errorf("copied into the other volume: %s", e.Name())
			}
		}
	}
}

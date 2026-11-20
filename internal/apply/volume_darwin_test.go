//go:build darwin

package apply

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/trash"
)

// mountVolume attaches a 2 MB APFS disk image inside the test's temp folder
// and returns its mount point (see the same helper in internal/trash).
func mountVolume(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mnt := filepath.Join(base, "mnt")
	if err := os.Mkdir(mnt, 0o700); err != nil {
		t.Fatal(err)
	}
	mountVolumeAt(t, mnt)
	return mnt
}

// mountVolumeAt attaches a fresh 2 MB disk image at the existing empty
// folder mnt, detached when the test ends.
func mountVolumeAt(t *testing.T, mnt string) {
	t.Helper()
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("hdiutil not available")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(base, "vol.dmg")
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
}

// An item that gained a mounted volume after the scan is skipped: the
// re-measure at move time sees the mount (the walk does not cross it).
func TestItemGainedAMount(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Files: []testutil.File{{Path: "Library/Caches/com.example.a/blob", Size: 10, MTime: old}},
		Dirs:  []testutil.Dir{{Path: "Library/Caches/com.example.a/mnt"}, {Path: "Library/Caches/com.example.a", MTime: old}},
	})
	p := f.scan("")
	mountVolumeAt(t, f.abs("Library/Caches/com.example.a/mnt"))
	res := f.run(f.opts(p))
	s, ok := skipFor(res, f.abs("Library/Caches/com.example.a"))
	if !ok || !strings.Contains(s.Detail, plan.ReasonContainsMount) || !exists(f.abs("Library/Caches/com.example.a/mnt")) {
		t.Fatalf("skipped %+v moved %+v", res.Skipped, res.Moved)
	}
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

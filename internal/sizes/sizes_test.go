//go:build unix

package sizes

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

const mib = 1 << 20

func measure(t *testing.T, path string, opts Options) Result {
	t.Helper()
	r, err := Measure(context.Background(), path, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Spec scenario "Hard-linked store": two entries for one 1 MiB inode count
// about 1 MiB, and none of it is reclaimable.
func TestHardlinkedStore(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files:     []testutil.File{{Path: "store/a", Size: mib}},
		Hardlinks: []testutil.Hardlink{{Path: "store/b", Target: "store/a"}},
	})
	r := measure(t, home+"/store", Options{})
	if r.LogicalBytes != mib {
		t.Fatalf("logical = %d, want %d", r.LogicalBytes, mib)
	}
	if r.AllocatedBytes < mib || r.AllocatedBytes > mib+64*1024 {
		t.Fatalf("allocated = %d, want about 1 MiB", r.AllocatedBytes)
	}
	if r.ReclaimableBytes >= mib {
		t.Fatalf("reclaimable = %d, hard-linked bytes must not count", r.ReclaimableBytes)
	}
	if len(r.Shared) != 1 || r.FileCount != 2 || r.Status != StatusComplete {
		t.Fatalf("shared=%d files=%d status=%s", len(r.Shared), r.FileCount, r.Status)
	}
}

func TestPlainFilesAreReclaimable(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
		{Path: "c/a", Size: 100 * 1024},
		{Path: "c/sub/b", Size: 50 * 1024},
	}})
	r := measure(t, home+"/c", Options{})
	if r.LogicalBytes != 150*1024 || r.FileCount != 2 {
		t.Fatalf("logical=%d files=%d", r.LogicalBytes, r.FileCount)
	}
	if r.ReclaimableBytes != r.AllocatedBytes || r.AllocatedBytes < 150*1024 {
		t.Fatalf("allocated=%d reclaimable=%d", r.AllocatedBytes, r.ReclaimableBytes)
	}
}

func TestSymlinkNotFollowed(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files:    []testutil.File{{Path: "big/blob", Size: 4 * mib}, {Path: "c/small", Content: "x"}},
		Symlinks: []testutil.Symlink{{Path: "c/link", Target: "~/big"}, {Path: "c/filelink", Target: "~/big/blob"}},
	})
	r := measure(t, home+"/c", Options{})
	if r.LogicalBytes > 4096 || r.AllocatedBytes > 64*1024 {
		t.Fatalf("symlink followed: logical=%d allocated=%d", r.LogicalBytes, r.AllocatedBytes)
	}
	if r.FileCount != 3 {
		t.Fatalf("files=%d, want the file and both links", r.FileCount)
	}
	// A symlink as the measured path itself is not followed either.
	if r := measure(t, home+"/c/link", Options{}); r.LogicalBytes > 4096 {
		t.Fatalf("root symlink followed: %d", r.LogicalBytes)
	}
}

// Spec scenario "Partial scan recorded".
func TestUnreadableSubdirIsPartial(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{{Path: "c/ok", Size: 2048}, {Path: "c/locked/secret", Size: mib}},
		Dirs:  []testutil.Dir{{Path: "c/locked", Unreadable: true}},
	})
	r := measure(t, home+"/c", Options{})
	if r.Status != StatusPartial {
		t.Fatalf("status = %s, want partial", r.Status)
	}
	if r.LogicalBytes != 2048 || len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "locked") {
		t.Fatalf("logical=%d warnings=%v", r.LogicalBytes, r.Warnings)
	}
}

func TestStopsAtDeviceChange(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
		{Path: "c/local", Size: 1024},
		{Path: "c/mnt/remote", Size: mib},
	}})
	deviceHook = func(rel string, dev uint64) uint64 {
		if rel == "mnt" || strings.HasPrefix(rel, "mnt/") {
			return dev + 1
		}
		return dev
	}
	t.Cleanup(func() { deviceHook = nil })
	r := measure(t, home+"/c", Options{})
	if r.Status != StatusPartial || r.LogicalBytes != 1024 {
		t.Fatalf("status=%s logical=%d", r.Status, r.LogicalBytes)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "another volume") {
		t.Fatalf("warnings=%v", r.Warnings)
	}
}

func TestProtectedDescendants(t *testing.T) {
	var files []testutil.File
	for _, p := range []string{"c/a/.env", "c/b/.env", "c/c/.env", "c/d/.env", "c/e/.env", "c/f/.env", "c/.git/HEAD", "c/.git/objects/x", "c/plain"} {
		files = append(files, testutil.File{Path: p, Content: "x"})
	}
	home := testutil.NewHome(t, testutil.Spec{Files: files})
	r := measure(t, home+"/c", Options{Protected: func(rel, name string, _, _ uint64, _ bool) bool {
		return name == ".env" || name == ".git"
	}})
	if r.ProtectedCount != 7 || len(r.Protected) != DefaultMaxProtected {
		t.Fatalf("count=%d listed=%v", r.ProtectedCount, r.Protected)
	}
	if r.FileCount != 9 {
		t.Fatalf("protected entries must still be measured: files=%d", r.FileCount)
	}
}

func TestNewestMtime(t *testing.T) {
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{{Path: "c/a", Content: "x", MTime: old}, {Path: "c/sub/b", Content: "x", MTime: newer}},
		Dirs:  []testutil.Dir{{Path: "c/sub", MTime: old}, {Path: "c", MTime: old}},
	})
	if r := measure(t, home+"/c", Options{}); !r.NewestMtime.Equal(newer) {
		t.Fatalf("newest = %v, want %v", r.NewestMtime, newer)
	}
}

func TestCancelled(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: "c/a", Content: "x"}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Measure(ctx, home+"/c", Options{}); err == nil {
		t.Fatal("want cancellation error")
	}
}

func TestSkipDirAndNewestFile(t *testing.T) {
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "p/src/a.go", Content: "x", MTime: old},
			{Path: "p/node_modules/x/i.js", Content: "x", MTime: recent},
		},
		Dirs: []testutil.Dir{{Path: "p/src", MTime: recent}},
	})
	r := measure(t, home+"/p", Options{SkipDir: func(_, name string) bool { return name == "node_modules" }})
	if !r.NewestFileMtime.Equal(old) || r.FileCount != 1 {
		t.Fatalf("newest file = %v files = %d, want %v and 1", r.NewestFileMtime, r.FileCount, old)
	}
	if !r.NewestMtime.After(old) {
		t.Fatalf("newest (dirs included) = %v, want the recent directory time", r.NewestMtime)
	}
}

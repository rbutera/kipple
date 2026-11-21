//go:build linux

package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/procs"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/trash"
)

// The wave 1 catalogue is still the macOS one, so these fixtures use its
// roots (Library/Caches/pip for pip.cache); the Linux catalogue arrives in
// wave 2 of linux-port.

func pipFixture() testutil.Spec {
	return testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/pip/http/blob", Size: 4096, MTime: old},
		{Path: "Library/Caches/com.example.a/blob", Size: 4096, MTime: old},
	}}
}

func infoOf(t *testing.T, f *fx, name string) trash.TrashInfo {
	t.Helper()
	b, err := os.ReadFile(f.abs(".local/share/Trash/info/" + name + ".trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := trash.DecodeTrashInfo(b)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func modeOf(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// specs/clean-apply and specs/platform-support scenarios for the
// freedesktop Trash on Linux.
func TestFreedesktopApply(t *testing.T) {
	t.Run("Apply to the freedesktop Trash on Linux", func(t *testing.T) {
		f := newFx(t, pipFixture())
		res := f.run(f.opts(f.scan("")))
		if len(res.Moved) != 2 || ExitCode(res) != ExitOK {
			t.Fatalf("%+v", res)
		}
		e, err := f.store.Load(res.ReceiptID)
		if err != nil {
			t.Fatal(err)
		}
		if e.Receipt.Mover != plan.MoverFreedesktop || e.Receipt.TrashDir != f.abs(".local/share/Trash/files") {
			t.Fatalf("receipt mover %q trash_dir %q", e.Receipt.Mover, e.Receipt.TrashDir)
		}
		for _, m := range e.Receipt.Moved {
			if m.Mover != plan.MoverFreedesktop || filepath.Dir(m.TrashPath) != f.abs(".local/share/Trash/files") || !exists(m.TrashPath) || exists(m.Path) {
				t.Errorf("moved entry %+v", m)
			}
		}
	})
	t.Run("Trash info written", func(t *testing.T) {
		f := newFx(t, pipFixture())
		p := f.scan("")
		it := itemByRel(t, p, "Library/Caches/pip/http")
		o := f.opts(p)
		o.Only = []string{it.ID}
		before := time.Now().Add(-time.Second)
		res := f.run(o)
		if len(res.Moved) != 1 || res.Moved[0].TrashPath != f.abs(".local/share/Trash/files/http") {
			t.Fatalf("%+v", res)
		}
		info := infoOf(t, f, "http")
		if info.Path != it.Path {
			t.Fatalf("info Path %q, want %q", info.Path, it.Path)
		}
		if info.DeletionDate.Before(before.Truncate(time.Second)) || info.DeletionDate.After(time.Now()) {
			t.Fatalf("DeletionDate %v is not the local time of the move", info.DeletionDate)
		}
		raw, _ := os.ReadFile(f.abs(".local/share/Trash/info/http.trashinfo"))
		if strings.ContainsAny(string(raw[strings.Index(string(raw), "DeletionDate="):]), "Z+") {
			t.Fatalf("DeletionDate carries a zone: %q", raw)
		}
	})
	t.Run("Missing Trash created on apply", func(t *testing.T) {
		f := newFx(t, pipFixture())
		if err := os.RemoveAll(f.abs(".local")); err != nil {
			t.Fatal(err)
		}
		p := f.scan("")
		o := f.opts(p)
		o.Only = []string{itemByRel(t, p, "Library/Caches/pip/http").ID}
		res := f.run(o)
		if len(res.Moved) != 1 {
			t.Fatalf("%+v", res)
		}
		for _, d := range []string{".local/share/Trash", ".local/share/Trash/files", ".local/share/Trash/info"} {
			if m := modeOf(t, f.abs(d)); m != 0o700 {
				t.Errorf("%s has mode %v, want 0700", d, m)
			}
		}
		if !exists(f.abs(".local/share/Trash/files/http")) || !exists(f.abs(".local/share/Trash/info/http.trashinfo")) {
			t.Fatal("the item or its info file is missing from the new Trash")
		}
	})
	t.Run("Dry run creates nothing", func(t *testing.T) {
		f := newFx(t, pipFixture())
		if err := os.RemoveAll(f.abs(".local")); err != nil {
			t.Fatal(err)
		}
		before := testutil.Checksum(t, f.home)
		o := f.opts(f.scan(""))
		o.DryRun = true
		res := f.run(o)
		if len(res.Moved) != 2 {
			t.Fatalf("%+v", res)
		}
		if exists(f.abs(".local")) || testutil.Checksum(t, f.home) != before {
			t.Fatal("a dry run changed the fixture")
		}
		if mode, err := ProbeTrash(f.opts(f.scan(""))); mode != TrashRename || err != nil || exists(f.abs(".local")) {
			t.Fatalf("probe %q %v, or it created the Trash", mode, err)
		}
	})
	t.Run("Nothing to move creates nothing", func(t *testing.T) {
		f := newFx(t, testutil.Spec{})
		if err := os.RemoveAll(f.abs(".local")); err != nil {
			t.Fatal(err)
		}
		if res := f.run(f.opts(f.scan(""))); ExitCode(res) != ExitNothing || exists(f.abs(".local/share")) {
			t.Fatalf("exit %d; .local/share exists: %v", ExitCode(res), exists(f.abs(".local/share")))
		}
	})
	for _, tc := range []struct {
		name  string
		setup func(f *fx)
	}{
		{"Trash folder is a symlink", func(f *fx) {
			if err := os.Rename(f.abs(".local/share/Trash"), f.abs("real-trash")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.abs("real-trash"), f.abs(".local/share/Trash")); err != nil {
				t.Fatal(err)
			}
		}},
		{"files folder is a symlink", func(f *fx) {
			if err := os.Remove(f.abs(".local/share/Trash/files")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.abs("elsewhere"), f.abs(".local/share/Trash/files")); err != nil {
				t.Fatal(err)
			}
		}},
		{"World-writable Trash", func(f *fx) {
			if err := os.Chmod(f.abs(".local/share/Trash/files"), 0o777); err != nil {
				t.Fatal(err)
			}
		}},
		{"Group-writable info folder", func(f *fx) {
			if err := os.Chmod(f.abs(".local/share/Trash/info"), 0o770); err != nil {
				t.Fatal(err)
			}
		}},
		{"Read-only info folder", func(f *fx) {
			if err := os.Chmod(f.abs(".local/share/Trash/info"), 0o500); err != nil {
				t.Fatal(err)
			}
		}},
		{"Trash is a file", func(f *fx) {
			if err := os.RemoveAll(f.abs(".local/share/Trash")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.abs(".local/share/Trash"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t, pipFixture())
			if err := os.MkdirAll(f.abs("elsewhere"), 0o700); err != nil {
				t.Fatal(err)
			}
			p := f.scan("")
			tc.setup(f)
			before := testutil.Checksum(t, f.home)
			_, err := Run(context.Background(), f.opts(p))
			if !errors.Is(err, ErrTrashAccess) {
				t.Fatalf("err = %v, want ErrTrashAccess", err)
			}
			for _, it := range p.Items {
				if !exists(it.Path) {
					t.Errorf("%s moved", it.Path)
				}
			}
			if testutil.Checksum(t, f.home) != before {
				t.Fatal("the fixture changed")
			}
			if es, _ := f.store.List(); len(es) != 0 {
				t.Fatalf("a receipt was written: %d", len(es))
			}
		})
	}
}

// noReplaceMover stands in for a home file system without
// RENAME_NOREPLACE (NFS, some FUSE file systems).
type noReplaceMover struct{ calls *int }

func (m noReplaceMover) Trash(int, string) (string, error) { return "", errors.New("unused") }
func (m noReplaceMover) TrashExpect(int, string, uint64, uint64) (string, error) {
	*m.calls++
	return "", fmt.Errorf("%w (%v)", trash.ErrNoReplaceUnsupported, syscall.EINVAL)
}

// Spec scenario "No RENAME_NOREPLACE" (platform-support): the run stops
// with trash_access at the first move, naming the file system, and
// nothing moves.
func TestNoRenameNoReplace(t *testing.T) {
	f := newFx(t, pipFixture())
	p := f.scan("")
	o := f.opts(p)
	calls := 0
	o.openTrash = func() (*homeTrash, error) {
		d, err := trash.OpenDirTrasher(f.abs(testutil.TrashRel))
		if err != nil {
			return nil, err
		}
		return &homeTrash{dir: d, mover: noReplaceMover{&calls}, name: plan.MoverFreedesktop, close: d.Close}, nil
	}
	res, err := Run(context.Background(), o)
	if !errors.Is(err, ErrTrashAccess) || !strings.Contains(err.Error(), "tmpfs") || !strings.Contains(err.Error(), "RENAME_NOREPLACE") {
		t.Fatalf("err = %v, want ErrTrashAccess naming the file system", err)
	}
	if calls != 1 || len(res.Moved) != 0 || len(res.Skipped) != 2 {
		t.Fatalf("%d move calls; result %+v", calls, res)
	}
	for _, it := range p.Items {
		if !exists(it.Path) {
			t.Errorf("%s moved", it.Path)
		}
	}
}

// Spec scenarios "Read-only package folder tagged" and "No user extended
// attributes" (clean-apply), and special files refused unopened.
func TestFreedesktopTag(t *testing.T) {
	t.Run("Read-only package folder tagged", func(t *testing.T) {
		f := newFx(t, testutil.Spec{
			Files: []testutil.File{{Path: "go/pkg/mod/github.com/x/y.go", Size: 10, MTime: old, Mode: 0o444}},
			Dirs:  []testutil.Dir{{Path: "go/pkg/mod/github.com", MTime: old}, {Path: "go/pkg/mod/github.com/x", MTime: old}},
		})
		t.Cleanup(func() {
			_ = filepath.Walk(f.home, func(p string, _ os.FileInfo, _ error) error { return os.Chmod(p, 0o755) })
		})
		for _, d := range []string{"go/pkg/mod/github.com/x", "go/pkg/mod/github.com"} {
			if err := os.Chmod(f.abs(d), 0o555); err != nil {
				t.Fatal(err)
			}
		}
		p := f.scan(rules.LevelReview)
		it := itemByRel(t, p, "go/pkg/mod/github.com")
		o := f.opts(p)
		o.Only = []string{it.ID}
		res := f.run(o)
		m, ok := movedFor(res, it.Path)
		if !ok {
			t.Fatalf("not moved: %+v", res)
		}
		if got := modeOf(t, m.TrashPath); got != 0o555 {
			t.Fatalf("mode after the move %v, want 0555", got)
		}
		buf := make([]byte, 256)
		n, err := unix.Lgetxattr(m.TrashPath, tagName, buf)
		if err != nil || string(buf[:n]) != string(tagValue(res.ReceiptID, it.ID, f.env.HomeIdentity)) {
			t.Fatalf("tag %q, %v", buf[:max(n, 0)], err)
		}
	})
	t.Run("No user extended attributes", func(t *testing.T) {
		f := newFx(t, pipFixture())
		p := f.scan("")
		it := itemByRel(t, p, "Library/Caches/pip/http")
		o := f.opts(p)
		o.Only = []string{it.ID}
		fsetxattr = func(int, string, []byte, int) error { return unix.ENOTSUP }
		t.Cleanup(func() { fsetxattr = unix.Fsetxattr })
		res := f.run(o)
		s, ok := skipFor(res, it.Path)
		if !ok || s.Reason != plan.SkipMoveFailed || !strings.Contains(s.Detail, "tmpfs") || !exists(it.Path) || ExitCode(res) != ExitPartial {
			t.Fatalf("exit %d skipped %+v", ExitCode(res), res.Skipped)
		}
		if entries, _ := os.ReadDir(f.abs(".local/share/Trash/info")); len(entries) != 0 {
			t.Fatalf("info files written: %v", entries)
		}
	})
	t.Run("EPERM from tmpfs before 6.6 is the same", func(t *testing.T) {
		f := newFx(t, pipFixture())
		p := f.scan("")
		it := itemByRel(t, p, "Library/Caches/pip/http")
		o := f.opts(p)
		o.Only = []string{it.ID}
		fsetxattr = func(int, string, []byte, int) error { return unix.EPERM }
		t.Cleanup(func() { fsetxattr = unix.Fsetxattr })
		res := f.run(o)
		if s, ok := skipFor(res, it.Path); !ok || s.Reason != plan.SkipMoveFailed || !exists(it.Path) {
			t.Fatalf("skipped %+v", res.Skipped)
		}
	})
	t.Run("A FIFO is refused without being opened", func(t *testing.T) {
		f := newFx(t, testutil.Spec{Dirs: []testutil.Dir{{Path: "Library/Caches"}}})
		fifo := f.abs("Library/Caches/pipe")
		if err := unix.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(fifo, old, old); err != nil {
			t.Fatal(err)
		}
		p := f.scan("")
		found := false
		for _, it := range p.Items {
			found = found || it.Path == fifo
		}
		if !found {
			t.Fatalf("the FIFO is not a plan item: %+v", p.Items)
		}
		done := make(chan plan.Result, 1)
		go func() {
			o := f.opts(p)
			var ids []string
			for _, it := range p.Items {
				ids = append(ids, it.ID)
			}
			o.Only = ids
			res, _ := Run(context.Background(), o)
			done <- res
		}()
		select {
		case res := <-done:
			if _, ok := movedFor(res, fifo); ok || !exists(fifo) {
				t.Fatalf("the FIFO moved: %+v", res)
			}
			if s, ok := skipFor(res, fifo); !ok || s.Reason != plan.SkipMoveFailed || !strings.Contains(s.Detail, "FIFO") {
				t.Fatalf("skipped %+v", res.Skipped)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("apply blocked on a FIFO")
		}
	})
}

// Spec scenario "Claude Code open in project" with real processes: a
// process named claude (a copy of sleep) with its working directory in the
// project blocks the project's node_modules, read from /proc.
func TestClaudeCodeOpenInProjectLinux(t *testing.T) {
	f := newFx(t, testutil.Spec{
		Repos: []testutil.Repo{gitProject("dev/app")},
		Files: []testutil.File{{Path: "dev/app/node_modules/x/i.js", Content: "x"}},
	})
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claude, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(claude, "60")
	cmd.Dir = f.abs("dev/app/src")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	p := f.scan(rules.LevelReview)
	o := f.opts(p)
	o.Procs = procs.New()
	res := f.run(o)
	nm := f.abs("dev/app/node_modules")
	if s, ok := skipFor(res, nm); !ok || s.Reason != plan.SkipToolRunning || !exists(nm) || !strings.Contains(s.Detail, "an agent process is working in "+f.abs("dev/app/src")) {
		t.Fatalf("skipped %+v moved %+v", res.Skipped, res.Moved)
	}
}

// Regression for the wave 1 red-team finding: while apply runs, a loop
// exchanges the items' parent folder with a folder outside home
// (renameat2 RENAME_EXCHANGE). Whatever moves, every .trashinfo must name
// where its item really came from, no decoy from outside may reach the
// Trash, and every moved item's info file must exist.
func TestParentExchangeLoopDuringApply(t *testing.T) {
	spec := testutil.Spec{}
	for i := 0; i < 40; i++ {
		spec.Files = append(spec.Files, testutil.File{Path: fmt.Sprintf("Library/Caches/c%03d/blob", i), Content: "item", MTime: old})
	}
	f := newFx(t, spec)
	outside := filepath.Join(filepath.Dir(f.home), "outside")
	for i := 0; i < 40; i++ {
		d := filepath.Join(outside, fmt.Sprintf("c%03d", i))
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "secret"), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
	p := f.scan("")
	caches := f.abs("Library/Caches")
	stop := make(chan struct{})
	done := make(chan int)
	go func() {
		n := 0
		for {
			select {
			case <-stop:
				if n%2 == 1 {
					_ = unix.Renameat2(unix.AT_FDCWD, caches, unix.AT_FDCWD, outside, unix.RENAME_EXCHANGE)
					n++
				}
				done <- n
				return
			default:
			}
			if unix.Renameat2(unix.AT_FDCWD, caches, unix.AT_FDCWD, outside, unix.RENAME_EXCHANGE) == nil {
				n++
			}
		}
	}()
	res, err := Run(context.Background(), f.opts(p))
	close(stop)
	if n := <-done; n == 0 {
		t.Fatal("the exchange loop never ran")
	}
	if err != nil {
		t.Fatal(err)
	}
	infoDir := f.abs(".local/share/Trash/info")
	for _, m := range res.Moved {
		info := infoOf(t, f, filepath.Base(m.TrashPath))
		if info.Path != m.Path {
			t.Errorf("%s: info Path %q", m.Path, info.Path)
		}
		if b, err := os.ReadFile(m.TrashPath + "/blob"); err != nil || string(b) != "item" {
			t.Errorf("%s: the Trash entry is not the item: %q %v", m.TrashPath, b, err)
		}
	}
	es, _ := os.ReadDir(infoDir)
	for _, e := range es {
		info := infoOf(t, f, strings.TrimSuffix(e.Name(), ".trashinfo"))
		if !strings.HasPrefix(info.Path, caches+"/") {
			t.Errorf("%s names %s, outside the rule root", e.Name(), info.Path)
		}
	}
	files, _ := os.ReadDir(f.abs(".local/share/Trash/files"))
	for _, e := range files {
		if _, err := os.Stat(filepath.Join(f.abs(".local/share/Trash/files"), e.Name(), "secret")); err == nil {
			t.Errorf("a folder from outside home reached the Trash: %s", e.Name())
		}
	}
	if len(files) != len(res.Moved) || len(es) != len(res.Moved) {
		t.Errorf("%d files and %d info entries for %d moved items", len(files), len(es), len(res.Moved))
	}
	t.Logf("moved %d, skipped %d", len(res.Moved), len(res.Skipped))
}

// Review should-fix 2: kipple changes the mode only of a folder it created.
// A Trash folder that already exists keeps its mode, and one created by
// someone else between kipple's check and its mkdir is checked, not
// chmodded: world-writable is trash_access.
func TestTrashFolderModesAreNotChanged(t *testing.T) {
	t.Run("Existing Trash keeps its mode", func(t *testing.T) {
		f := newFx(t, pipFixture())
		if err := os.Chmod(f.abs(".local/share/Trash"), 0o755); err != nil {
			t.Fatal(err)
		}
		if res := f.run(f.opts(f.scan(""))); len(res.Moved) != 2 {
			t.Fatalf("%+v", res)
		}
		if m := modeOf(t, f.abs(".local/share/Trash")); m != 0o755 {
			t.Fatalf("mode %v, want 0755 as it was", m)
		}
	})
	t.Run("Folder created concurrently is checked, not chmodded", func(t *testing.T) {
		f := newFx(t, pipFixture())
		if err := os.RemoveAll(f.abs(".local/share/Trash/info")); err != nil {
			t.Fatal(err)
		}
		p := f.scan("")
		beforeMkdir = func(path string) {
			if path == f.abs(".local/share/Trash/info") {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o777); err != nil {
					t.Fatal(err)
				}
			}
		}
		t.Cleanup(func() { beforeMkdir = nil })
		if _, err := Run(context.Background(), f.opts(p)); !errors.Is(err, ErrTrashAccess) {
			t.Fatalf("err = %v, want ErrTrashAccess", err)
		}
		if m := modeOf(t, f.abs(".local/share/Trash/info")); m != 0o777 {
			t.Fatalf("mode %v: kipple changed a folder it did not create", m)
		}
		for _, it := range p.Items {
			if !exists(it.Path) {
				t.Errorf("%s moved", it.Path)
			}
		}
	})
}

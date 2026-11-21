package procs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"testing"
)

// fakeProc is a synthetic /proc: pid -> process.
type fakeProc struct {
	procs   map[int]fakeProcess
	listErr error
}

type fakeProcess struct {
	uid     int
	comm    string // without the kernel's trailing newline
	cmdline string // NUL-separated, as the kernel shows it
	cwd     string
	err     error // returned by every read of this process
	cwdErr  error
	state   string // the status file's State value; "" means no status file
}

func (f fakeProc) PIDs() ([]int, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var pids []int
	for pid := range f.procs {
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	return pids, nil
}

func (f fakeProc) get(pid int) (fakeProcess, error) {
	p, ok := f.procs[pid]
	if !ok {
		return p, fs.ErrNotExist
	}
	return p, p.err
}

func (f fakeProc) Owner(pid int) (int, error) {
	p, err := f.get(pid)
	return p.uid, err
}

func (f fakeProc) ReadFile(pid int, name string) ([]byte, error) {
	p, err := f.get(pid)
	if err != nil {
		return nil, err
	}
	switch name {
	case "comm":
		return []byte(p.comm + "\n"), nil
	case "cmdline":
		return []byte(p.cmdline), nil
	case "status":
		if p.state != "" {
			return []byte(fmt.Sprintf("Name:\t%s\nState:\t%s\nUid:\t%d\t%d\t%d\t%d\n", p.comm, p.state, p.uid, p.uid, p.uid, p.uid)), nil
		}
	}
	return nil, fs.ErrNotExist
}

func (f fakeProc) Readlink(pid int, name string) (string, error) {
	p, err := f.get(pid)
	if err != nil {
		return "", err
	}
	if name != "cwd" {
		return "", fs.ErrNotExist
	}
	if p.cwdErr != nil {
		return "", p.cwdErr
	}
	return p.cwd, nil
}

const me, other = 1000, 1001

// One case per specs/project-artifacts scenario on Linux, plus the
// failure modes of design L7.
func TestProcLinuxMatching(t *testing.T) {
	ctx := context.Background()
	t.Run("Agent found through its command line on Linux", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "node", cmdline: "claude\x00", cwd: "/home/me/dev/app"},
		}}}
		cwds, err := p.ProcessCwds(ctx, []string{"claude", "codex", "cursor-agent"})
		if err != nil || !slices.Equal(cwds, []string{"/home/me/dev/app"}) {
			t.Fatalf("cwds %q, %v", cwds, err)
		}
		names, err := p.RunningNames(ctx, []string{"claude"})
		if err != nil || !names["claude"] {
			t.Fatalf("names %v, %v", names, err)
		}
	})
	t.Run("Agent found by comm", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "codex", cmdline: "/opt/codex/bin/codex-x86\x00--full-auto\x00", cwd: "/home/me/dev/app/src"},
		}}}
		cwds, err := p.ProcessCwds(ctx, []string{"claude", "codex"})
		if err != nil || !slices.Equal(cwds, []string{"/home/me/dev/app/src"}) {
			t.Fatalf("cwds %q, %v", cwds, err)
		}
	})
	t.Run("Agent found through a full path in argv0", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "MainThread", cmdline: "/home/me/.local/bin/claude\x00--resume\x00", cwd: "/home/me/dev/app"},
		}}}
		if names, err := p.RunningNames(ctx, []string{"claude"}); err != nil || !names["claude"] {
			t.Fatalf("names %v, %v", names, err)
		}
	})
	t.Run("Long process name on Linux", func(t *testing.T) {
		const name = "com.docker.backend"
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "com.docker.back", cmdline: "/opt/docker-desktop/bin/com.docker.backend\x00run\x00"},
		}}}
		if names, err := p.RunningNames(ctx, []string{name}); err != nil || !names[name] {
			t.Fatalf("names %v, %v; want %s running", names, err, name)
		}
		q := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "com.docker.back", cmdline: "/usr/bin/com.docker.backup\x00"},
		}}}
		if names, err := q.RunningNames(ctx, []string{name}); err != nil || names[name] {
			t.Fatalf("names %v, %v; a truncated comm with another command line must not match", names, err)
		}
	})
	t.Run("Name of exactly 15 bytes needs the command line", func(t *testing.T) {
		const name = "abcdefghijklmno"
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: name, cmdline: "/usr/bin/abcdefghijklmnopq\x00"},
		}}}
		if names, _ := p.RunningNames(ctx, []string{name}); names[name] {
			t.Fatal("a comm cut to 15 bytes matched a 15-byte name with a different command line")
		}
	})
	t.Run("Another user's agent", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: other, comm: "claude", cmdline: "claude\x00", cwd: "/home/me/dev/app", cwdErr: fs.ErrPermission},
		}}}
		cwds, err := p.ProcessCwds(ctx, []string{"claude"})
		if err != nil || len(cwds) != 0 {
			t.Fatalf("cwds %q, %v; another user's process must be ignored", cwds, err)
		}
		if names, err := p.RunningNames(ctx, []string{"claude"}); err != nil || names["claude"] {
			t.Fatalf("names %v, %v", names, err)
		}
	})
	t.Run("/proc unavailable", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{listErr: fs.ErrNotExist}}
		if _, err := p.ProcessCwds(ctx, []string{"claude"}); !errors.Is(err, ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
		if _, err := p.RunningNames(ctx, []string{"code"}); !errors.Is(err, ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
		if _, err := (Proc{UID: me}).RunningNames(ctx, []string{"code"}); !errors.Is(err, ErrUnknown) {
			t.Fatalf("no FS: err = %v, want ErrUnknown", err)
		}
	})
	t.Run("Own process unreadable is unknown", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "claude", cmdline: "claude\x00", cwdErr: fs.ErrPermission},
		}}}
		if _, err := p.ProcessCwds(ctx, []string{"claude"}); !errors.Is(err, ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
	})
	t.Run("Deleted working directory", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "claude", cmdline: "claude\x00", cwd: "/home/me/dev/app (deleted)"},
		}}}
		cwds, err := p.ProcessCwds(ctx, []string{"claude"})
		if err != nil || !slices.Equal(cwds, []string{"/home/me/dev/app (deleted)", "/home/me/dev/app"}) {
			t.Fatalf("cwds %q, %v", cwds, err)
		}
		// A real folder named "app (deleted)" is never shortened away.
		if !Within(cwds[0], "/home/me/dev/app (deleted)") {
			t.Fatal("the path as read was not kept")
		}
	})
	t.Run("Process vanishing mid-walk", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "claude", err: errProcessGone},
			11: {uid: me, comm: "claude", cmdline: "claude\x00", cwdErr: fs.ErrNotExist},
			12: {uid: me, comm: "claude", cmdline: "claude\x00", cwd: "/home/me/dev/app"},
		}}}
		cwds, err := p.ProcessCwds(ctx, []string{"claude"})
		if err != nil || !slices.Equal(cwds, []string{"/home/me/dev/app"}) {
			t.Fatalf("cwds %q, %v", cwds, err)
		}
	})
	// Ubuntu CI follow-up: "does not exist" for the working directory of a
	// process that is still running is unknown, not absent; a zombie has
	// none and is skipped.
	t.Run("Working directory missing while running", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "claude", cmdline: "claude\x00", cwdErr: fs.ErrNotExist, state: "S (sleeping)"},
		}}}
		if _, err := p.ProcessCwds(ctx, []string{"claude"}); !errors.Is(err, ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
		p.FS = fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "claude", cmdline: "claude\x00", cwdErr: fs.ErrNotExist, state: "Z (zombie)"},
		}}
		if cwds, err := p.ProcessCwds(ctx, []string{"claude"}); err != nil || len(cwds) != 0 {
			t.Fatalf("zombie: %q, %v", cwds, err)
		}
	})
	t.Run("Bundle IDs are unsupported", func(t *testing.T) {
		if _, err := (Proc{UID: me}).RunningBundleIDs(ctx); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("Unrelated processes", func(t *testing.T) {
		p := Proc{UID: me, FS: fakeProc{procs: map[int]fakeProcess{
			10: {uid: me, comm: "bash", cmdline: "-bash\x00"},
			11: {uid: me, comm: "kworker/0:1", cmdline: ""},
			12: {uid: me, comm: "node", cmdline: "node\x00/usr/lib/node_modules/claude/cli.js\x00"},
		}}}
		names, err := p.RunningNames(ctx, []string{"claude", "code"})
		if err != nil || len(names) != 0 {
			t.Fatalf("names %v, %v", names, err)
		}
	})
}

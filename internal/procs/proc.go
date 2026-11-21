package procs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// ProcFS is what the Linux Inspector reads from /proc (design L7). It is
// an interface so tests feed synthetic process tables on any host; the
// real one is procDir (procs_linux.go).
type ProcFS interface {
	// PIDs lists the numeric entries of /proc.
	PIDs() ([]int, error)
	// Owner returns the UID that owns /proc/<pid> (the process's
	// effective UID).
	Owner(pid int) (int, error)
	// ReadFile reads /proc/<pid>/<name> (comm, cmdline).
	ReadFile(pid int, name string) ([]byte, error)
	// Readlink reads the link /proc/<pid>/<name> (cwd).
	Readlink(pid int, name string) (string, error)
}

// commLen is the longest name the kernel keeps in /proc/<pid>/comm
// (TASK_COMM_LEN 16, less the NUL).
const commLen = 15

// Proc is the Inspector that reads a ProcFS. Only processes owned by UID
// are considered: kipple cannot read another user's working directory,
// and another user's processes are not this user's agents.
type Proc struct {
	FS  ProcFS
	UID int
}

// RunningBundleIDs is unsupported: Linux apps have no bundle IDs, and
// Linux rules carry none.
func (Proc) RunningBundleIDs(context.Context) (map[string]bool, error) {
	return nil, ErrUnsupported
}

// RunningNames returns which of names have a process of this user.
func (p Proc) RunningNames(ctx context.Context, names []string) (map[string]bool, error) {
	out := map[string]bool{}
	err := p.each(ctx, func(pid int, comm, argv0 string) error {
		for _, n := range names {
			if matches(n, comm, argv0) {
				out[n] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ProcessCwds returns the working directories of this user's processes
// named one of names. A path ending in " (deleted)" is reported both as
// read and without the suffix: the kernel adds it to a removed directory,
// but a real directory can have that name too.
func (p Proc) ProcessCwds(ctx context.Context, names []string) ([]string, error) {
	var cwds []string
	err := p.each(ctx, func(pid int, comm, argv0 string) error {
		hit := false
		for _, n := range names {
			hit = hit || matches(n, comm, argv0)
		}
		if !hit {
			return nil
		}
		cwd, err := p.FS.Readlink(pid, "cwd")
		switch {
		case gone(err) && p.exited(pid):
			return nil
		case err != nil:
			return fmt.Errorf("%w: the working directory of process %d: %v", ErrUnknown, pid, err)
		}
		// The kernel appends " (deleted)" to a removed directory, but a
		// directory may also really end with that: report the path as read
		// and, for that suffix, also without it. Matching more only makes
		// apply skip more.
		cwds = append(cwds, cwd)
		if trimmed := strings.TrimSuffix(cwd, " (deleted)"); trimmed != cwd {
			cwds = append(cwds, trimmed)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cwds, nil
}

// each calls fn for every process of this user that still exists, with
// its comm and the base name of its first command-line argument. Failing
// to list /proc, or to read one of the user's own processes, is
// ErrUnknown; a process that exits during the walk is skipped.
func (p Proc) each(ctx context.Context, fn func(pid int, comm, argv0 string) error) error {
	if p.FS == nil {
		return fmt.Errorf("%w: processes could not be checked: no /proc", ErrUnknown)
	}
	pids, err := p.FS.PIDs()
	if err != nil {
		return fmt.Errorf("%w: processes could not be checked: %v", ErrUnknown, err)
	}
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %v", ErrUnknown, err)
		}
		uid, err := p.FS.Owner(pid)
		switch {
		case gone(err):
			continue
		case err != nil:
			return fmt.Errorf("%w: processes could not be checked: process %d: %v", ErrUnknown, pid, err)
		case uid != p.UID:
			continue
		}
		comm, err := p.FS.ReadFile(pid, "comm")
		switch {
		case gone(err):
			continue
		case err != nil:
			return fmt.Errorf("%w: processes could not be checked: process %d: %v", ErrUnknown, pid, err)
		}
		cmdline, err := p.FS.ReadFile(pid, "cmdline")
		switch {
		case gone(err):
			continue
		case err != nil:
			return fmt.Errorf("%w: processes could not be checked: process %d: %v", ErrUnknown, pid, err)
		}
		argv0 := cmdline
		if i := bytes.IndexByte(argv0, 0); i >= 0 {
			argv0 = argv0[:i]
		}
		base := ""
		if len(argv0) > 0 {
			base = path.Base(string(argv0))
		}
		if err := fn(pid, strings.TrimSuffix(string(comm), "\n"), base); err != nil {
			return err
		}
	}
	return nil
}

// matches reports whether a process with this comm and argv0 base name is
// a process named name. comm is the kernel's name, cut to 15 bytes, so a
// comm equal to a name of 15 bytes or more may be the prefix of a longer
// one: such a name also needs argv0's base name to be the name. argv0's
// base name alone matches too, which catches programs that set their
// process title (Node's process.title, used by the npm claude command).
func matches(name, comm, argv0 string) bool {
	if name == "" {
		return false
	}
	if argv0 == name {
		return true
	}
	return len(name) < commLen && comm == name
}

// exited reports whether process pid has really ended (or is a zombie,
// which has no working directory any more), after a read of it said it does
// not exist. A process whose status still reads as running has not: an
// agent whose working directory cannot be read is unknown, never absent.
func (p Proc) exited(pid int) bool {
	b, err := p.FS.ReadFile(pid, "status")
	if err != nil {
		return gone(err)
	}
	st := statusField(b, "State:")
	return strings.HasPrefix(st, "Z") || strings.HasPrefix(st, "X")
}

// statusField returns the value of the line starting with key in a
// /proc/<pid>/status file, without leading space, or "".
func statusField(status []byte, key string) string {
	for _, line := range strings.Split(string(status), "\n") {
		if v, ok := strings.CutPrefix(line, key); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// gone reports whether err means the process exited during the walk.
func gone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, errProcessGone)
}

// errProcessGone is what a ProcFS returns for ESRCH.
var errProcessGone = errors.New("procs: the process exited")

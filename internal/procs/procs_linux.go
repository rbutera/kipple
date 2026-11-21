//go:build linux

package procs

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// New returns the platform Inspector: /proc, for this user's processes.
func New() Inspector { return Proc{FS: procDir{root: "/proc"}, UID: os.Geteuid()} }

// procDir is the real /proc (or a directory laid out like it).
type procDir struct{ root string }

func (d procDir) PIDs() ([]int, error) {
	f, err := os.Open(d.root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, n := range names {
		if pid, err := strconv.Atoi(n); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		// /proc always lists at least this process; an empty listing
		// means it is not mounted.
		return nil, errors.New(d.root + " lists no processes (not mounted?)")
	}
	return pids, nil
}

func (d procDir) path(pid int, name string) string {
	p := d.root + "/" + strconv.Itoa(pid)
	if name != "" {
		p += "/" + name
	}
	return p
}

func (d procDir) Owner(pid int) (int, error) {
	var st unix.Stat_t
	if err := unix.Lstat(d.path(pid, ""), &st); err != nil {
		return 0, procErr(err)
	}
	return int(st.Uid), nil
}

func (d procDir) ReadFile(pid int, name string) ([]byte, error) {
	b, err := os.ReadFile(d.path(pid, name))
	return b, procErr(err)
}

func (d procDir) Readlink(pid int, name string) (string, error) {
	s, err := os.Readlink(d.path(pid, name))
	return s, procErr(err)
}

// procErr maps ESRCH (the process exited while it was read) to
// errProcessGone.
func procErr(err error) error {
	if errors.Is(err, unix.ESRCH) {
		return errProcessGone
	}
	return err
}

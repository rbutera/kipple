//go:build linux

package apply

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

// homeTrashRel is where the freedesktop.org home Trash keeps its entries,
// relative to home. kipple uses the default location and does not follow
// XDG_DATA_HOME (design L6).
const homeTrashRel = ".local/share/Trash/files"

// finderFallback: Linux has no system mover to fall back on.
const finderFallback = false

// errTrashCreatable: the Trash, or a folder above it, does not exist yet,
// and every part that exists is usable; an --apply run with something to
// move would create the rest. Only returned when create is false.
var errTrashCreatable = errors.New("the Trash folder does not exist yet; kipple creates it when it first moves an item")

// openHomeTrash opens the freedesktop.org home Trash <home>/.local/share/
// Trash and its files and info folders through the session: no symlink on
// the way, every folder on home's device, stored names. Trash, files and
// info must be directories owned by this user, usable by them (rwx) and
// not writable by group or others. With create, a missing .local, .local/share, Trash, files or info
// is created with mode 0700 from its verified parent (apply does so only
// in an --apply run with at least one item to move); without it, a
// missing part is errTrashCreatable and nothing is created. Every other
// problem is ErrTrashAccess, saying why.
func openHomeTrash(sess *policy.Session, home string, create bool) (*homeTrash, error) {
	root := home + "/.local/share/Trash"
	var held []*os.File
	defer func() {
		for _, f := range held {
			_ = f.Close()
		}
	}()
	for _, p := range []struct {
		path  string
		check bool
	}{{home + "/.local", false}, {home + "/.local/share", false}, {root, true}, {root + "/files", true}, {root + "/info", true}} {
		f, err := trashFolder(sess, p.path, create, p.check)
		if err != nil {
			return nil, err
		}
		held = append(held, f)
	}
	ft, err := trash.NewFreedesktopTrasher(root, held[3], held[4])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTrashAccess, err)
	}
	return &homeTrash{dir: ft.Files(), mover: ft, name: plan.MoverFreedesktop, info: ft, close: ft.Close}, nil
}

// trashFolder opens one folder of the Trash's path, creating it 0700 when
// it is missing and create is set, and with check verifies its owner and
// mode.
func trashFolder(sess *policy.Session, path string, create, check bool) (*os.File, error) {
	e, err := sess.Open(path)
	created := false
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return nil, errTrashCreatable
		}
		if merr := sess.Mkdir(path, 0o700); merr != nil && !errors.Is(merr, fs.ErrExist) {
			return nil, fmt.Errorf("%w: %s could not be created: %v", ErrTrashAccess, path, merr)
		}
		created = true
		e, err = sess.Open(path)
	}
	if err != nil {
		var re *policy.RefusedError
		if errors.As(err, &re) && re.Reason == policy.ReasonSymlinkInPath {
			return nil, fmt.Errorf("%w: %s is or passes through a symbolic link", ErrTrashAccess, path)
		}
		return nil, fmt.Errorf("%w: %s cannot be used: %v", ErrTrashAccess, path, err)
	}
	if e.Dir == nil {
		return nil, fmt.Errorf("%w: %s is not a folder", ErrTrashAccess, path)
	}
	if created {
		// Exactly 0700, whatever the umask.
		if err := unix.Fchmod(int(e.Dir.Fd()), 0o700); err != nil {
			_ = e.Dir.Close()
			return nil, fmt.Errorf("%w: %s: %v", ErrTrashAccess, path, err)
		}
	}
	if !check {
		return e.Dir, nil
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(e.Dir.Fd()), &st); err != nil {
		_ = e.Dir.Close()
		return nil, fmt.Errorf("%w: %s: %v", ErrTrashAccess, path, err)
	}
	switch {
	case int(st.Uid) != os.Geteuid():
		_ = e.Dir.Close()
		return nil, fmt.Errorf("%w: %s is not owned by you", ErrTrashAccess, path)
	case st.Mode&0o022 != 0:
		_ = e.Dir.Close()
		return nil, fmt.Errorf("%w: %s is writable by other users (mode %04o); make it 0700 with: chmod 700 '%s'", ErrTrashAccess, path, st.Mode&0o7777, path)
	case st.Mode&0o700 != 0o700:
		_ = e.Dir.Close()
		return nil, fmt.Errorf("%w: %s is not readable, writable and searchable by you (mode %04o); make it 0700 with: chmod 700 '%s'", ErrTrashAccess, path, st.Mode&0o7777, path)
	}
	return e.Dir, nil
}

// noReplaceError is apply's error when the first move finds that the file
// system holding the item (fd is its parent) cannot rename without
// replacing: the run stops with trash_access, naming the file system.
func noReplaceError(fd int, err error) error {
	return fmt.Errorf("%w: the file system (%s) cannot move items without the risk of replacing something (no RENAME_NOREPLACE), so kipple stopped before moving anything; scanning still works: %v",
		ErrTrashAccess, trash.FileSystemName(fd), err)
}

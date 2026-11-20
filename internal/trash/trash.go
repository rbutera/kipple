// Package trash moves items to a Trash folder (design D9, D16, D17).
//
// Every Trasher takes the item as a verified parent directory descriptor and
// the item's stored name, never as a path string, so the move acts on the
// object the policy approved and a directory swapped above it cannot
// redirect it. Nothing in this package follows a symlink, copies data, or
// falls back to copy-and-delete when the Trash is on another device: that is
// ErrDifferentDevice and the item stays where it is.
//
// Implementations:
//
//   - DirTrasher (trash_dir.go, unix) renames into a verified directory
//     descriptor with MoveNoReplace, naming collisions the way Finder does
//     ("name 2", "report 2.pdf"). Apply uses it with the home folder's
//     .Trash, opened through the policy session; tests use it with any
//     fixture directory.
//   - FinderTrasher (finder_darwin.go, darwin with cgo) asks NSFileManager
//     trashItemAtURL to move the item and returns where it landed. It works
//     on a path, so it re-checks the item's identity before and after; it is
//     kept for parity with Finder's own Trash handling and is not apply's
//     default.
//
// MoveNoReplace (move_darwin.go) is the one no-clobber move every caller
// uses: renameatx_np with RENAME_EXCL on macOS; other platforms return
// ErrUnsupported until a port supplies renameat2 RENAME_NOREPLACE.
package trash

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors returned by Trashers and MoveNoReplace.
var (
	// ErrUnsupported: this platform or build has no implementation.
	ErrUnsupported = errors.New("trash: not supported on this platform or build")
	// ErrDifferentDevice: the item and the Trash are on different devices.
	// The item is not moved; kipple never copies and deletes instead.
	ErrDifferentDevice = errors.New("trash: the item and the Trash are on different devices")
	// ErrTrashChanged: the Trash folder's path no longer names the
	// directory that was verified when the Trasher was opened.
	ErrTrashChanged = errors.New("trash: the Trash folder changed since it was verified")
	// ErrItemChanged: the object at the item's name changed while it was
	// being moved. Whatever was moved has been put back where possible.
	ErrItemChanged = errors.New("trash: the item changed while it was being moved")
	// ErrInvalidName: the item name is empty, "." or "..", or has a slash.
	ErrInvalidName = errors.New("trash: invalid item name")
)

// Trasher moves one item to a Trash.
type Trasher interface {
	// Trash moves the entry name, stored in the directory open at parentFd,
	// into the Trash and returns its new absolute path. It never follows a
	// symlink (a symlink is moved as itself) and never copies.
	Trash(parentFd int, name string) (string, error)
}

// ExpectTrasher is a Trasher that can also refuse, moving nothing, unless
// the entry is the approved object (dev, ino). DirTrasher and FinderTrasher
// implement it; apply uses it.
type ExpectTrasher interface {
	Trasher
	TrashExpect(parentFd int, name string, dev, ino uint64) (string, error)
}

// validName reports whether name is a single, ordinary directory entry name.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsRune(name, '/') && !strings.ContainsRune(name, 0)
}

// DisplacedError reports that a move put something other than the
// approved object into the Trash (the approved object stayed where it was,
// or its identity could not be confirmed). The thing that moved is at Dest;
// Known says whether its identity could be read. It matches ErrItemChanged.
// apply records it in the receipt as a displaced entry, which restore can
// put back.
type DisplacedError struct {
	Dest     string
	Dev, Ino uint64
	Size     int64
	Mtime    time.Time
	Known    bool
	Why      string
}

func (e *DisplacedError) Error() string {
	return fmt.Sprintf("trash: %s: something other than the approved item was moved to %s", e.Why, e.Dest)
}

// Is makes errors.Is(err, ErrItemChanged) true.
func (e *DisplacedError) Is(target error) bool { return target == ErrItemChanged }

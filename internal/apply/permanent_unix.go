//go:build unix && kipple_permanent

package apply

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

// This file is the --permanent delete. It is built only with the
// kipple_permanent build tag: v0.1.0 does not ship permanent deletion
// (see docs/safety.md, "Permanent deletion"). The remaining gap is that an
// entry is checked by name and then unlinked by name; the staging design in
// the roadmap closes it.

// maxRemoveDepth bounds the permanent-delete recursion.
const maxRemoveDepth = 256

// Test hooks for the permanent delete.
var (
	// afterPrecheck runs between the check pass and the delete pass.
	afterPrecheck func()
	// beforeDescend runs in the delete pass before a subfolder is opened.
	beforeDescend func(rel string)
	// afterDescend runs in the delete pass after a subfolder is opened and
	// before anything inside it is checked or deleted.
	afterDescend func(rel string)
)

func ctimeOf(st *unix.Stat_t) time.Time { return time.Unix(st.Ctim.Unix()) }

// removeItem deletes the approved item for --permanent. It is one walk
// that holds a descriptor for every folder it works in and never opens
// anything by path:
//
//   - The item is opened from its verified parent descriptor and must be
//     the approved (dev, ino).
//   - A check pass, which deletes nothing, records the (dev, ino) of every
//     entry by its item-relative path, and refuses the item if any entry is
//     protected, on another device, or changed (ctime) at or after since,
//     the moment the move-time checks began: a folder swapped in, or a
//     file added, after that is refused.
//   - The delete pass walks depth-first through held descriptors. Before
//     touching anything inside a folder it checks that the folder's
//     descriptor is still the recorded object, that its ".." is the held
//     parent, and that it is on the item's device. Every entry must be the
//     recorded object and not protected before it is unlinked; a folder is
//     removed only after its contents were deleted through its own held
//     descriptor and only if its name still leads to that object.
//   - Before deleting in each folder the mount table is read again
//     (refresh), and every entry is checked against it before it is
//     unlinked or entered: a mount made since the check pass stops the
//     delete. A table that cannot be read stops it too.
//
// Any mismatch stops the delete. It returns what was removed, also on
// error.
func removeItem(f *policy.Fresh, dev, ino uint64, protect protectFunc, mounted mountFunc, refresh refreshFunc, since time.Time) (removal, error) {
	var rm removal
	want := ident{dev, ino}
	var st unix.Stat_t
	if err := fstatat(f.ParentFd, f.Name, &st); err != nil {
		return rm, err
	}
	if identOf(&st) != want {
		return rm, trash.ErrItemChanged
	}
	pst, err := fstatOf(f.ParentFd)
	if err != nil {
		return rm, err
	}
	parent := identOf(&pst)
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if !ctimeOf(&st).Before(since) {
			return rm, &errChanged{why: "changed after the checks began"}
		}
		if err := unix.Unlinkat(f.ParentFd, f.Name, 0); err != nil {
			return rm, err
		}
		rm.count++
		rm.bytes += st.Blocks * 512
		return rm, nil
	}
	fd, err := openDirAt(f.ParentFd, f.Name)
	if err != nil {
		return rm, err
	}
	defer func() { _ = unix.Close(fd) }()
	ids := map[string]ident{}
	if err := precheck(fd, "", want, parent, dev, protect, mounted, since, ids, 0); err != nil {
		return rm, err
	}
	if afterPrecheck != nil {
		afterPrecheck()
	}
	w := deleteWalk{dev: dev, protect: protect, mounted: mounted, refresh: refresh, ids: ids, rm: &rm}
	if err := w.contents(fd, "", parent, 0); err != nil {
		return rm, err
	}
	if err := checkHeld(fd, "", want, parent, dev); err != nil {
		return rm, err
	}
	if err := fstatat(f.ParentFd, f.Name, &st); err != nil || identOf(&st) != want {
		return rm, &errChanged{why: "is no longer at its path"}
	}
	if err := unix.Unlinkat(f.ParentFd, f.Name, unix.AT_REMOVEDIR); err != nil {
		return rm, err
	}
	rm.count++
	rm.bytes += st.Blocks * 512
	return rm, nil
}

// checkHeld verifies the folder open at fd is the object want, that its
// ".." is parent, and that it is on device dev.
func checkHeld(fd int, rel string, want, parent ident, dev uint64) error {
	st, err := fstatOf(fd)
	if err != nil {
		return err
	}
	if identOf(&st) != want {
		return &errChanged{rel: rel, why: "is not the folder that was checked"}
	}
	if uint64(st.Dev) != dev {
		return errCrossDevice
	}
	var up unix.Stat_t
	if err := fstatat(fd, "..", &up); err != nil || identOf(&up) != parent {
		return &errChanged{rel: rel, why: "was moved out of its folder"}
	}
	return nil
}

func join(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// precheck walks the folder open at fd (the object self, inside parent)
// without deleting anything, recording identities in ids.
func precheck(fd int, rel string, self, parent ident, dev uint64, protect protectFunc, mounted mountFunc, since time.Time, ids map[string]ident, depth int) error {
	if depth > maxRemoveDepth {
		return fmt.Errorf("deeper than %d levels; stopped", maxRemoveDepth)
	}
	if err := checkHeld(fd, rel, self, parent, dev); err != nil {
		return err
	}
	ids[rel] = self
	names, err := readNames(fd)
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		child := join(rel, n)
		var st unix.Stat_t
		if err := fstatat(fd, n, &st); err != nil {
			return &errChanged{rel: child, why: "could not be inspected (" + err.Error() + ")"}
		}
		if uint64(st.Dev) != dev {
			return errCrossDevice
		}
		if mounted != nil {
			if mr, err := mounted(fd, n, uint64(st.Dev), uint64(st.Ino)); mr || err != nil {
				return errCrossDevice
			}
		}
		if protect != nil {
			if reason, hit := protect(child, n, uint64(st.Dev), uint64(st.Ino), policy.StatMode(uint32(st.Mode))); hit {
				return &errProtected{rel: child, reason: reason}
			}
		}
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			cfd, err := openDirAt(fd, n)
			if err != nil {
				return err
			}
			err = precheck(cfd, child, identOf(&st), self, dev, protect, mounted, since, ids, depth+1)
			_ = unix.Close(cfd)
			if err != nil {
				return err
			}
		} else {
			ids[child] = identOf(&st)
		}
		if !ctimeOf(&st).Before(since) {
			return &errChanged{rel: child, why: "changed after the checks began"}
		}
	}
	// Checked last, so a protected entry inside is reported as such.
	st, err := fstatOf(fd)
	if err != nil {
		return err
	}
	if !ctimeOf(&st).Before(since) {
		return &errChanged{rel: rel, why: "changed after the checks began"}
	}
	return nil
}

// deleteWalk is the delete pass of removeItem.
type deleteWalk struct {
	dev     uint64
	protect protectFunc
	mounted mountFunc
	refresh refreshFunc
	ids     map[string]ident
	rm      *removal
}

// contents deletes everything inside the folder open at fd (item-relative
// rel, inside parent) through held descriptors.
func (w *deleteWalk) contents(fd int, rel string, parent ident, depth int) error {
	if depth > maxRemoveDepth {
		return fmt.Errorf("deeper than %d levels; stopped", maxRemoveDepth)
	}
	self, ok := w.ids[rel]
	if !ok {
		return &errChanged{rel: rel, why: "was not checked"}
	}
	if err := checkHeld(fd, rel, self, parent, w.dev); err != nil {
		return err
	}
	if w.refresh != nil {
		if err := w.refresh(); err != nil {
			return fmt.Errorf("%s: stopped: %w", relName(rel), err)
		}
	}
	names, err := readNames(fd)
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		child := join(rel, n)
		var st unix.Stat_t
		if err := fstatat(fd, n, &st); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return err
		}
		if w.protect != nil {
			if reason, hit := w.protect(child, n, uint64(st.Dev), uint64(st.Ino), policy.StatMode(uint32(st.Mode))); hit {
				return &errProtected{rel: child, reason: reason}
			}
		}
		want, ok := w.ids[child]
		if !ok || identOf(&st) != want {
			return &errChanged{rel: child, why: "is not the entry that was checked"}
		}
		if uint64(st.Dev) != w.dev {
			return errCrossDevice
		}
		if w.mounted != nil {
			if mr, err := w.mounted(fd, n, uint64(st.Dev), uint64(st.Ino)); mr || err != nil {
				return errCrossDevice
			}
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			if err := unix.Unlinkat(fd, n, 0); err != nil {
				return err
			}
			w.rm.count++
			w.rm.bytes += st.Blocks * 512
			continue
		}
		if beforeDescend != nil {
			beforeDescend(child)
		}
		cfd, err := openDirAt(fd, n)
		if err != nil {
			return err
		}
		if afterDescend != nil {
			afterDescend(child)
		}
		err = w.contents(cfd, child, self, depth+1)
		if err == nil {
			err = checkHeld(cfd, child, want, self, w.dev)
		}
		_ = unix.Close(cfd)
		if err != nil {
			return err
		}
		var now unix.Stat_t
		if err := fstatat(fd, n, &now); err != nil || identOf(&now) != want {
			return &errChanged{rel: child, why: "is no longer at its path"}
		}
		if err := unix.Unlinkat(fd, n, unix.AT_REMOVEDIR); err != nil {
			return err
		}
		w.rm.count++
		w.rm.bytes += st.Blocks * 512
	}
	return nil
}

// relName names an item-relative folder for a message.
func relName(rel string) string {
	if rel == "" {
		return "the item"
	}
	return rel
}

// readNames lists the directory open at fd through a fresh descriptor.
func readNames(fd int) ([]string, error) {
	lfd, err := openDirAt(fd, ".")
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(lfd), ".")
	defer func() { _ = f.Close() }()
	var all []string
	for {
		names, err := f.Readdirnames(1024)
		all = append(all, names...)
		if err == io.EOF {
			return all, nil
		}
		if err != nil {
			return all, err
		}
	}
}

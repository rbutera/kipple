// Package homeguard decides by file identity (st_dev, st_ino), never by
// path spelling, whether one directory is another, lies inside it, or
// contains it. kipple uses it to refuse a KIPPLE_HOME that overlaps the real
// home folder.
//
// Spelling cannot decide this on macOS: APFS folds case and Unicode (a long
// s "ſ" reaches "s", an NFD name reaches its NFC twin) and firmlinks make
// /System/Volumes/Data/Users/<me> the same directory as /Users/<me>.
//
// The package never resolves the real home itself. The CLI and the test
// guard resolve it and pass it in.
package homeguard

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ID is a directory's (st_dev, st_ino).
type ID struct{ Dev, Ino uint64 }

// Relation is how a candidate directory relates to the real home.
type Relation int

// Relations, from the candidate's point of view.
const (
	Separate Relation = iota // neither contains the other
	Same                     // the candidate is the real home
	Inside                   // the candidate lies inside the real home
	Contains                 // the candidate is an ancestor of the real home
)

func (r Relation) String() string {
	switch r {
	case Same:
		return "is the real home folder"
	case Inside:
		return "is inside the real home folder"
	case Contains:
		return "contains the real home folder"
	default:
		return "is separate from the real home folder"
	}
}

// FS looks up directory identities. System is the real file system; tests
// substitute stubs to model aliases (firmlinks) that cannot be created in a
// temp folder.
type FS struct {
	// Chain returns the identity of dir followed by the identity of each
	// ancestor, reached with openat(fd, ".."), up to and including the root.
	// With nofollow, dir itself is opened with O_NOFOLLOW.
	Chain func(dir string, nofollow bool) ([]ID, error)
	// Stat returns the identity of path, following symlinks; ok is false
	// when path does not exist or a component is not a directory.
	Stat func(path string) (id ID, ok bool, err error)
	// MountPoint returns the mount point of the volume holding path
	// (statfs f_mntonname on macOS, such as /System/Volumes/Data), or ""
	// where the platform has no firmlinks to alias it.
	MountPoint func(path string) (string, error)
	// Aliases, on Linux, lists where else the real home is visible through
	// bind mounts, from /proc/self/mountinfo (design L2 of linux-port). nil
	// means the platform has none. An error refuses the candidate.
	Aliases func(real string) (Aliases, error)
}

// Aliases are the other places a real home is visible (Linux bind mounts).
type Aliases struct {
	// Real are paths where the real home itself is visible: a bind mount of
	// the home or of one of its ancestors makes mountpoint+remainder the
	// real home again.
	Real []string
	// Inside are mount points of folders inside the real home: such a mount
	// point, and everything below it, is inside the real home.
	Inside []string
}

// ErrUnsupported is returned by System on platforms without directory
// identities; callers fail closed.
var ErrUnsupported = errors.New("directory identities are not available on this platform")

// Relate reports how candidate relates to real, by identity. Any lookup
// error is returned and the caller must refuse the candidate.
//
// It checks, in order: the two have the same identity; the real home's
// identity is on the candidate's ".." chain (inside); the candidate's
// identity is on the real home's ".." chain (contains); and, because a
// firmlink's target is not on the ".." chain of the directory it aliases
// (".." of /Users/<me>/.. is /, never /System/Volumes/Data), candidate
// joined with each tail of real's path (<me>, Users/<me>, ...) having the
// real home's identity (contains). Finally, a candidate on the ".." chain
// of the real home's mount point (/System/Volumes/Data, so also
// /System/Volumes and /System) contains the real home through that volume,
// even though no tail of the real path names it from there.
func Relate(fsys FS, candidate, real string) (Relation, error) {
	rel, _, err := RelateID(fsys, candidate, real)
	return rel, err
}

// RelateID is Relate that also returns the identity of the candidate it
// checked, so a caller can pin exactly the directory that was approved.
func RelateID(fsys FS, candidate, real string) (Relation, ID, error) {
	if fsys.Chain == nil || fsys.Stat == nil || fsys.MountPoint == nil {
		return Separate, ID{}, errors.New("homeguard: incomplete FS")
	}
	c, err := fsys.Chain(candidate, true)
	if err != nil {
		return Separate, ID{}, fmt.Errorf("cannot identify %q: %w", candidate, err)
	}
	if len(c) == 0 {
		return Separate, ID{}, errors.New("homeguard: empty identity chain")
	}
	rel, err := relate(fsys, candidate, c, real)
	if err != nil || rel != Separate || fsys.Aliases == nil {
		return rel, c[0], err
	}
	al, err := fsys.Aliases(real)
	if err != nil {
		return Separate, c[0], fmt.Errorf("cannot list where the real home %q is mounted: %w", real, err)
	}
	for _, a := range al.Real {
		r, err := relate(fsys, candidate, c, a)
		if err != nil {
			return Separate, c[0], err
		}
		if r != Separate {
			return r, c[0], nil
		}
	}
	for _, mp := range al.Inside {
		id, ok, err := fsys.Stat(mp)
		if err != nil {
			return Separate, c[0], fmt.Errorf("cannot identify %q: %w", mp, err)
		}
		if ok && contains(c, id) {
			return Inside, c[0], nil
		}
	}
	// A candidate holding a bind mount of the real home or of part of it
	// (on the mount point's ".." chain) contains that part of home.
	for _, mp := range append(append([]string(nil), al.Real...), al.Inside...) {
		chain, err := fsys.Chain(mp, false)
		if err != nil {
			return Separate, c[0], fmt.Errorf("cannot identify %q: %w", mp, err)
		}
		if contains(chain[1:], c[0]) {
			return Contains, c[0], nil
		}
	}
	return Separate, c[0], nil
}

// relate decides the relation given the candidate's identity chain c.
func relate(fsys FS, candidate string, c []ID, real string) (Relation, error) {
	r, err := fsys.Chain(real, false)
	if err != nil {
		return Separate, fmt.Errorf("cannot identify the real home %q: %w", real, err)
	}
	if len(c) == 0 || len(r) == 0 {
		return Separate, errors.New("homeguard: empty identity chain")
	}
	if c[0] == r[0] {
		return Same, nil
	}
	if contains(c[1:], r[0]) {
		return Inside, nil
	}
	if contains(r[1:], c[0]) {
		return Contains, nil
	}
	comps := strings.Split(strings.Trim(filepath.Clean(real), "/"), "/")
	for i := len(comps) - 1; i >= 0; i-- {
		if comps[i] == "" {
			continue
		}
		p := filepath.Join(append([]string{candidate}, comps[i:]...)...)
		id, ok, err := fsys.Stat(p)
		if err != nil {
			return Separate, fmt.Errorf("cannot identify %q: %w", p, err)
		}
		if ok && id == r[0] {
			return Contains, nil
		}
	}
	mp, err := fsys.MountPoint(real)
	if err != nil {
		return Separate, fmt.Errorf("cannot find the mount point of the real home %q: %w", real, err)
	}
	if mp != "" {
		m, err := fsys.Chain(mp, false)
		if err != nil {
			return Separate, fmt.Errorf("cannot identify the real home's mount point %q: %w", mp, err)
		}
		if contains(m, c[0]) {
			return Contains, nil
		}
	}
	return Separate, nil
}

func contains(ids []ID, id ID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

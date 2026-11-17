package policy

import (
	"io/fs"
	"strings"
)

// fileID is a file's (st_dev, st_ino). APFS does not allow hard links to
// directories, so two paths with the same fileID for a directory are two
// spellings of the same directory (case, Unicode folding or normalisation).
type fileID struct{ dev, ino uint64 }

// protectedIDs records the identities of protected objects that exist on
// disk, and of their ancestors, with the refusal reason for each.
type protectedIDs struct {
	objects   map[fileID]string // the protected path itself
	ancestors map[fileID]string // directories that contain a protected path
}

// collectProtectedIDs lstats every non-glob denylist prefix under home and
// every Env.Protect entry, component by component. The walk never follows a
// symlink: it records the symlink's own identity and stops there.
func collectProtectedIDs(home string, protect []string) *protectedIDs {
	ids := &protectedIDs{
		objects:   map[fileID]string{},
		ancestors: map[fileID]string{},
	}
	// Denylist first, so a path that is both is reported as denylisted.
	for _, entry := range denyPrefixes {
		ids.addChain(home, strings.Split(entry, "/"), ReasonDenylisted, true)
	}
	for _, p := range protect {
		abs, ok := protectPath(home, p)
		if !ok || abs == "/" {
			continue
		}
		ids.addChain("", strings.Split(abs[1:], "/"), ReasonUserProtected, false)
	}
	return ids
}

// addChain lstats base/segs[0], base/segs[0]/segs[1], ... recording the last
// as an object and the rest as ancestors. With globs set, a segment with
// glob metacharacters ends the walk and everything recorded is an ancestor.
func (ids *protectedIDs) addChain(base string, segs []string, reason string, globs bool) {
	cur := base
	for i, seg := range segs {
		if seg == "" || (globs && hasMeta(seg)) {
			return
		}
		cur += "/" + seg
		fi, err := lstat(cur)
		if err != nil {
			return
		}
		id, ok := identityOf(fi)
		if !ok {
			return
		}
		last := i == len(segs)-1
		if last {
			if _, dup := ids.objects[id]; !dup {
				ids.objects[id] = reason
			}
		} else if _, dup := ids.ancestors[id]; !dup {
			ids.ancestors[id] = reason
		}
		if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return
		}
	}
}

// refusal returns the reason a path component with identity id must be
// refused, if any. final is true for the candidate itself, which is also
// refused when it contains a protected object.
func (ids *protectedIDs) refusal(id fileID, final bool) (string, bool) {
	if r, ok := ids.objects[id]; ok {
		return r, true
	}
	if final {
		if r, ok := ids.ancestors[id]; ok {
			return r, true
		}
	}
	return "", false
}

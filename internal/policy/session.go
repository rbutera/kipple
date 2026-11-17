package policy

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RefusedError is returned by Session methods that open or inspect a path
// when the walk refuses it. Reason is one of the Reason* constants; Err, when
// set, is the underlying system error (errors.Is(err, fs.ErrNotExist) tells a
// missing path from an unreadable one).
type RefusedError struct {
	Path   string
	Reason string
	Err    error
}

func (e *RefusedError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Path, e.Reason, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Reason)
}

func (e *RefusedError) Unwrap() error { return e.Err }

// Entry is a path opened by Session.Open: its lstat-equivalent info and, for
// a directory, an open descriptor (O_DIRECTORY|O_NOFOLLOW) that the caller
// must close.
type Entry struct {
	Info fs.FileInfo
	// Dir is nil unless Info.IsDir().
	Dir *os.File
}

// Guard reports protected objects found inside one item during its size
// walk. It never authorises anything; it only makes scan refuse items that
// contain something the policy protects. Build one with Session.Guard.
type Guard struct {
	ids *protectedIDs
	// rems are the denylist prefixes and user protections that lie strictly
	// below the item, as item-relative folded segments, with their reason.
	rems []guardRem
}

type guardRem struct {
	segs   []string
	reason string
}

// foldedComponents are denyComponents folded once for Guard.Check.
var foldedComponents = func() []string {
	out := make([]string, len(denyComponents))
	for i, c := range denyComponents {
		out[i] = foldName(c)
	}
	return out
}()

// newGuard builds the Guard for item (absolute, clean, below home).
func newGuard(env Env, ids *protectedIDs, item string) *Guard {
	g := &Guard{ids: ids}
	if !strings.HasPrefix(item, env.Home+"/") {
		return g
	}
	rel := strings.Split(item[len(env.Home)+1:], "/")
	abs := strings.Split(item[1:], "/")
	add := func(icomps, segs []string, reason string) {
		if len(segs) <= len(icomps) {
			return
		}
		for i, c := range icomps {
			x, y := foldName(segs[i]), foldName(c)
			if x != y && !globOverlap(x, y) {
				return
			}
		}
		rem := make([]string, 0, len(segs)-len(icomps))
		for _, s := range segs[len(icomps):] {
			rem = append(rem, foldName(s))
		}
		g.rems = append(g.rems, guardRem{segs: rem, reason: reason})
	}
	for _, entry := range denyPrefixes {
		add(rel, strings.Split(entry, "/"), ReasonDenylisted)
	}
	for _, p := range env.Protect {
		if p == "/" || !isCanonicalAbs(p) {
			continue
		}
		add(abs, strings.Split(p[1:], "/"), ReasonUserProtected)
	}
	return g
}

// Check reports whether an entry inside the item is protected: rel is its
// item-relative slash path, name its last component, and dev and ino its
// identity. It returns the refusal reason (denylisted or user_protected).
func (g *Guard) Check(rel, name string, dev, ino uint64) (string, bool) {
	if g == nil {
		return "", false
	}
	fn := foldName(name)
	for _, pat := range foldedComponents {
		if ok, err := filepath.Match(pat, fn); err != nil || ok {
			return ReasonDenylisted, true
		}
	}
	if len(g.rems) > 0 {
		segs := strings.Split(rel, "/")
		for _, r := range g.rems {
			if len(segs) < len(r.segs) {
				continue
			}
			match := true
			for i, p := range r.segs {
				s := foldName(segs[i])
				if hasMeta(p) {
					if ok, err := filepath.Match(p, s); err == nil && !ok {
						match = false
					}
				} else if p != s {
					match = false
				}
				if !match {
					break
				}
			}
			if match {
				return r.reason, true
			}
		}
	}
	if g.ids != nil {
		if r, ok := g.ids.objects[fileID{dev: dev, ino: ino}]; ok {
			return r, true
		}
	}
	return "", false
}

//go:build unix

package policy

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Session is an amortised, descriptor-relative form of Authorize for one
// scan (design D16). NewSession opens home once with O_DIRECTORY|O_NOFOLLOW
// and collects protected identities once. Session.Authorize then walks each
// candidate from that descriptor with fstatat and openat (O_NOFOLLOW|
// O_DIRECTORY), fstat-ing every opened descriptor and checking that it is the
// object just inspected, so each step resolves relative to an already
// verified directory and a swapped ancestor cannot redirect the walk.
// Directory listings for the stored-name check are cached by (dev, ino) and
// reused only while the directory's mtime and ctime (nanoseconds) are
// unchanged.
//
// Session.Authorize returns exactly the decision Authorize returns for the
// same Env, rule and path, given an unchanged filesystem; the policy tests run
// every case through both. Protected identities are a snapshot taken when the
// session starts, so a session must not outlive the scan it was made for:
// scan creates one, uses it, and closes it. Apply (wave 3) walks fresh,
// without a session cache.
//
// A Session is safe for concurrent use. Methods called after Close refuse
// everything with ReasonInvalidEnv.
type Session struct {
	env      Env
	homeFd   int
	homeDev  uint64
	homeFail string // filesystem refusal for every path when home is unusable
	ids      *protectedIDs

	// life guards closed and homeFd: every walk holds the read lock from
	// start to finish and Close takes the write lock, so Close can never
	// close (and the process reuse) homeFd while a walk is using it.
	life   sync.RWMutex
	closed bool

	mu   sync.Mutex // guards dirs
	dirs map[fileID]*dirNames
}

// dirNames is one directory's stored-name set, valid while the directory's
// mtime and ctime are unchanged.
type dirNames struct {
	mtime, ctime unix.Timespec
	names        map[string]struct{}
}

// HomeError reports that NewSession could not use Env.Home. The Session it
// is returned with is still valid and refuses every path that Authorize
// would refuse, with Reason.
type HomeError struct {
	Home   string
	Reason string
	Err    error
}

func (e *HomeError) Error() string {
	msg := "home " + e.Home + " cannot be used: " + e.Reason
	if e.Err != nil {
		msg += " (" + e.Err.Error() + ")"
	}
	return msg
}

func (e *HomeError) Unwrap() error { return e.Err }

// NewSession opens env.Home and collects protected identities. It always
// returns a usable Session; the error, a *HomeError, says the environment is
// invalid or home cannot be walked, in which case the Session refuses every
// path with the reason Authorize would give. Close the Session when the scan
// ends.
func NewSession(env Env) (*Session, error) {
	s := &Session{
		env:    Env{Home: env.Home, Protect: append([]string(nil), env.Protect...)},
		homeFd: -1,
		dirs:   map[fileID]*dirNames{},
	}
	home := env.Home
	if !isCanonicalAbs(home) || home == "/" {
		return s, &HomeError{Home: home, Reason: ReasonNotAbsolute}
	}
	for _, p := range env.Protect {
		if !isCanonicalAbs(p) {
			return s, &HomeError{Home: home, Reason: ReasonInvalidEnv}
		}
	}
	fail := func(reason string, err error) (*Session, error) {
		s.homeFail = reason
		return s, &HomeError{Home: home, Reason: reason, Err: err}
	}
	hfi, err := lstat(home)
	switch {
	case err != nil:
		return fail(ReasonMissing, err)
	case hfi.Mode()&fs.ModeSymlink != 0:
		return fail(ReasonSymlinkInPath, nil)
	case !hfi.IsDir():
		return fail(ReasonMissing, nil)
	}
	dev, ok := deviceOf(home, hfi)
	if !ok {
		return fail(ReasonDifferentDevice, nil)
	}
	hid, ok := identityOf(hfi)
	if !ok {
		return fail(ReasonDifferentDevice, nil)
	}
	fd, err := openDir(unix.AT_FDCWD, home)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return fail(ReasonSymlinkInPath, err)
		}
		return fail(ReasonMissing, err)
	}
	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil || (fileID{dev: uint64(st.Dev), ino: st.Ino}) != hid {
		_ = unix.Close(fd)
		return fail(ReasonMissing, err)
	}
	s.homeFd = fd
	s.homeDev = dev
	s.ids = collectIDs(home, s.env.Protect)
	return s, nil
}

// Close releases the home descriptor. Later calls refuse everything.
func (s *Session) Close() error {
	s.life.Lock()
	defer s.life.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.mu.Lock()
	s.dirs = nil
	s.mu.Unlock()
	if s.homeFd >= 0 {
		err := unix.Close(s.homeFd)
		s.homeFd = -1
		return err
	}
	return nil
}

// Home returns the session's home directory.
func (s *Session) Home() string { return s.env.Home }

// Authorize decides whether rule may move path, exactly as Authorize does.
func (s *Session) Authorize(rule Rule, path string) Decision {
	lx := lexical(s.env, rule, path)
	if lx.done {
		return lx.d
	}
	s.life.RLock()
	defer s.life.RUnlock()
	if s.closed {
		return refuse(ReasonInvalidEnv)
	}
	if s.homeFail != "" {
		return refuse(s.homeFail)
	}
	w := s.walk(lx.comps, lx.want, s.ids, false, true)
	defer w.close(s.homeFd)
	if !w.d.Allowed {
		return w.d
	}
	if lx.want == directory {
		return s.checkMarkers(w.parent, rule.RuleMatchMarkers(), true)
	}
	return Decision{Allowed: true}
}

// Open walks from home to path with the same descriptor-relative checks as
// Authorize (no symlink anywhere, every component on home's device, every
// name byte-identical to its stored name) but without the denylist, rule or
// identity checks. It authorises nothing: scan uses it to list rule roots
// and to measure items, including info-tier items whose roots are
// denylisted. Refusals are *RefusedError. For a directory, Entry.Dir is open
// and the caller closes it.
func (s *Session) Open(path string) (Entry, error) {
	home := s.env.Home
	if !isCanonicalAbs(path) || !isCanonicalAbs(home) || home == "/" {
		return Entry{}, &RefusedError{Path: path, Reason: ReasonNotAbsolute}
	}
	if !strings.HasPrefix(path, home+"/") {
		return Entry{}, &RefusedError{Path: path, Reason: ReasonOutsideHome}
	}
	s.life.RLock()
	defer s.life.RUnlock()
	if s.closed {
		return Entry{}, &RefusedError{Path: path, Reason: ReasonInvalidEnv}
	}
	if s.homeFail != "" {
		return Entry{}, &RefusedError{Path: path, Reason: s.homeFail}
	}
	comps := strings.Split(path[len(home)+1:], "/")
	w := s.walk(comps, anyType, nil, true, true)
	defer w.close(s.homeFd)
	if !w.d.Allowed {
		return Entry{}, &RefusedError{Path: path, Reason: w.d.Reason, Err: w.err}
	}
	e := Entry{Info: w.final}
	if w.finalFd >= 0 {
		e.Dir = os.NewFile(uintptr(w.finalFd), path)
		w.finalFd = -1 // owned by e.Dir now
	}
	return e, nil
}

// Guard returns the protected-descendant checker for item (an absolute
// path below home) of rule, used while measuring the item.
func (s *Session) Guard(rule Rule, item string) *Guard {
	return newGuard(s.env, s.ids, rule, item)
}

// Fresh is an approved path with its verified parent directory held open,
// returned by AuthorizeFresh. Wave 3 apply moves the item with
// renameat(ParentFd, Name, trashFd, newName) (or renameatx_np with
// RENAME_EXCL), so the move acts on exactly the object that was approved
// and a swapped ancestor cannot redirect it. Close releases ParentFd.
type Fresh struct {
	// ParentFd is an O_DIRECTORY|O_NOFOLLOW descriptor of the item's parent,
	// reached by the verified walk. The caller owns it.
	ParentFd int
	// Name is the item's name as stored in that directory.
	Name string
	// Info is the item's lstat-equivalent info, taken during the walk.
	Info fs.FileInfo
}

// Close releases the parent descriptor.
func (f *Fresh) Close() error {
	if f == nil || f.ParentFd < 0 {
		return nil
	}
	err := unix.Close(f.ParentFd)
	f.ParentFd = -1
	return err
}

// AuthorizeFresh decides like Authorize, but with protected identities
// collected again for this call and without the directory-listing cache, so
// the decision reflects the filesystem now rather than when the session
// started. When the path is allowed it also returns the verified parent
// descriptor and stored name for the move (see Fresh); the caller must
// Close it. Wave 3 apply calls it immediately before each move.
//
// TODO(wave 3): apply uses Fresh with renameat; not wired yet.
func (s *Session) AuthorizeFresh(rule Rule, path string) (*Fresh, Decision) {
	lx := lexical(s.env, rule, path)
	if lx.done {
		return nil, lx.d
	}
	s.life.RLock()
	defer s.life.RUnlock()
	if s.closed {
		return nil, refuse(ReasonInvalidEnv)
	}
	if s.homeFail != "" {
		return nil, refuse(s.homeFail)
	}
	ids := collectIDs(s.env.Home, s.env.Protect)
	w := s.walk(lx.comps, lx.want, ids, false, false)
	defer w.close(s.homeFd)
	if !w.d.Allowed {
		return nil, w.d
	}
	if lx.want == directory {
		if d := s.checkMarkers(w.parent, rule.RuleMatchMarkers(), false); !d.Allowed {
			return nil, d
		}
	}
	parent := w.parent
	if parent == s.homeFd {
		fd, err := unix.Dup(parent)
		if err != nil {
			return nil, refuse(ReasonMissing)
		}
		unix.CloseOnExec(fd)
		parent = fd
	} else {
		w.parent = -1 // ownership passes to Fresh
	}
	return &Fresh{ParentFd: parent, Name: lx.comps[len(lx.comps)-1], Info: w.final}, Decision{Allowed: true}
}

// Identity returns the (st_dev, st_ino) of info from os.Lstat or a Session.
func Identity(info fs.FileInfo) (dev, ino uint64, ok bool) {
	id, ok := identityOf(info)
	return id.dev, id.ino, ok
}

// walkResult holds the descriptors a walk leaves open.
type walkResult struct {
	d       Decision
	err     error
	parent  int // descriptor of the final component's parent
	finalFd int // descriptor of the final component, when opened
	final   fs.FileInfo
}

func (w *walkResult) close(homeFd int) {
	if w.parent >= 0 && w.parent != homeFd {
		_ = unix.Close(w.parent)
	}
	if w.finalFd >= 0 {
		_ = unix.Close(w.finalFd)
	}
}

// walk runs filesystem checks 8 to 10 component by component from the home
// descriptor, in the same order as checkFilesystem. With ids non-nil it
// applies the protected-identity check; with openFinal set it also opens the
// final component when it is a directory; with cache set it reuses
// directory listings for the stored-name check.
func (s *Session) walk(comps []string, want itemType, ids *protectedIDs, openFinal, cache bool) walkResult {
	w := walkResult{parent: s.homeFd, finalFd: -1}
	stop := func(reason string, err error) walkResult {
		w.d, w.err = refuse(reason), err
		return w
	}
	cur := s.env.Home
	for i, c := range comps {
		last := i == len(comps)-1
		cur = cur + "/" + c
		var st unix.Stat_t
		if err := fstatat(w.parent, c, &st); err != nil {
			return stop(ReasonMissing, err)
		}
		fi := statInfo{name: c, st: st}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return stop(ReasonSymlinkInPath, nil)
		}
		dev, ok := deviceOf(cur, fi)
		if !ok || dev != s.homeDev {
			return stop(ReasonDifferentDevice, nil)
		}
		id, ok := identityOf(fi)
		if !ok {
			return stop(ReasonDifferentDevice, nil)
		}
		if ids != nil {
			if reason, hit := ids.refusal(id, last); hit {
				return stop(reason, nil)
			}
		}
		stored, err := s.stored(w.parent, c, cache)
		if err != nil {
			return stop(ReasonMissing, err)
		}
		if !stored {
			return stop(ReasonNameMismatch, nil)
		}
		if last {
			if (want == regularFile && !fi.Mode().IsRegular()) || (want == directory && !fi.IsDir()) {
				return stop(ReasonNotRuleItem, nil)
			}
			w.final = fi
			if openFinal && fi.IsDir() {
				fd, reason, err := openVerified(w.parent, c, id)
				if reason != "" {
					return stop(reason, err)
				}
				w.finalFd = fd
			}
			break
		}
		if !fi.IsDir() {
			return stop(ReasonMissing, nil)
		}
		fd, reason, err := openVerified(w.parent, c, id)
		if reason != "" {
			return stop(reason, err)
		}
		if w.parent != s.homeFd {
			_ = unix.Close(w.parent)
		}
		w.parent = fd
	}
	w.d = Decision{Allowed: true}
	return w
}

// openVerified opens name in dirfd as a directory without following a
// symlink and checks that the descriptor is the object with identity want.
func openVerified(dirfd int, name string, want fileID) (int, string, error) {
	fd, err := openDir(dirfd, name)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return -1, ReasonSymlinkInPath, err
		}
		return -1, ReasonMissing, err
	}
	var st unix.Stat_t
	if err := fstat(fd, &st); err != nil || (fileID{dev: uint64(st.Dev), ino: st.Ino}) != want {
		_ = unix.Close(fd)
		return -1, ReasonMissing, err
	}
	return fd, "", nil
}

// checkMarkers is checkMarkers relative to the artifact's verified parent.
func (s *Session) checkMarkers(dirfd int, markers []string, cache bool) Decision {
	for _, m := range markers {
		if m == "" || m == "." || m == ".." || strings.Contains(m, "/") || hasMeta(m) {
			continue
		}
		var st unix.Stat_t
		if err := fstatat(dirfd, m, &st); err != nil {
			continue
		}
		if !(statInfo{name: m, st: st}).Mode().IsRegular() {
			continue
		}
		if stored, err := s.stored(dirfd, m, cache); err == nil && stored {
			return Decision{Allowed: true}
		}
	}
	return refuse(ReasonNotRuleItem)
}

// stored reports whether the directory open at dirfd holds an entry whose
// stored name is byte-identical to name, using the cached listing while the
// directory's mtime and ctime are unchanged.
func (s *Session) stored(dirfd int, name string, cache bool) (bool, error) {
	if !cache {
		names, err := readNames(dirfd)
		if err != nil {
			return false, err
		}
		for _, n := range names {
			if n == name {
				return true, nil
			}
		}
		return false, nil
	}
	var st unix.Stat_t
	if err := fstat(dirfd, &st); err != nil {
		return false, err
	}
	id := fileID{dev: uint64(st.Dev), ino: st.Ino}
	s.mu.Lock()
	if e, ok := s.dirs[id]; ok && e.mtime == st.Mtim && e.ctime == st.Ctim {
		_, hit := e.names[name]
		s.mu.Unlock()
		return hit, nil
	}
	s.mu.Unlock()

	names, err := readNames(dirfd)
	if err != nil {
		return false, err
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	// Cache only if the directory did not change while it was read.
	var after unix.Stat_t
	if err := fstat(dirfd, &after); err == nil && after.Mtim == st.Mtim && after.Ctim == st.Ctim {
		s.mu.Lock()
		if s.dirs != nil {
			s.dirs[id] = &dirNames{mtime: st.Mtim, ctime: st.Ctim, names: set}
		}
		s.mu.Unlock()
	}
	_, hit := set[name]
	return hit, nil
}

// readNames lists the directory open at dirfd through a fresh descriptor, so
// dirfd's own offset is untouched.
func readNames(dirfd int) ([]string, error) {
	fd, err := openDir(dirfd, ".")
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), ".")
	defer func() { _ = f.Close() }()
	return f.Readdirnames(-1)
}

// openDir opens name relative to dirfd as a directory, never following a
// symlink in the final component, retrying on EINTR.
func openDir(dirfd int, name string) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func fstatat(dirfd int, name string, st *unix.Stat_t) error {
	for {
		err := unix.Fstatat(dirfd, name, st, unix.AT_SYMLINK_NOFOLLOW)
		if err != unix.EINTR {
			return err
		}
	}
}

func fstat(fd int, st *unix.Stat_t) error {
	for {
		err := unix.Fstat(fd, st)
		if err != unix.EINTR {
			return err
		}
	}
}

// statInfo adapts a unix.Stat_t to fs.FileInfo, like os.Lstat's result.
type statInfo struct {
	name string
	st   unix.Stat_t
}

func (fi statInfo) Name() string       { return fi.name }
func (fi statInfo) Size() int64        { return fi.st.Size }
func (fi statInfo) ModTime() time.Time { return time.Unix(fi.st.Mtim.Unix()) }
func (fi statInfo) IsDir() bool        { return fi.Mode().IsDir() }
func (fi statInfo) Sys() any           { return &fi.st }

func (fi statInfo) Mode() fs.FileMode {
	return StatMode(uint32(fi.st.Mode))
}

// StatMode converts a raw st_mode to an fs.FileMode, as os.Lstat does.
func StatMode(m uint32) fs.FileMode {
	mode := fs.FileMode(m & 0o777)
	switch m & unix.S_IFMT {
	case unix.S_IFDIR:
		mode |= fs.ModeDir
	case unix.S_IFLNK:
		mode |= fs.ModeSymlink
	case unix.S_IFIFO:
		mode |= fs.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= fs.ModeSocket
	case unix.S_IFCHR:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case unix.S_IFBLK:
		mode |= fs.ModeDevice
	case unix.S_IFREG:
	default:
		mode |= fs.ModeIrregular
	}
	if m&unix.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if m&unix.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if m&unix.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	return mode
}

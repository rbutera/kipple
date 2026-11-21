//go:build !unix

package policy

import (
	"errors"
	"io/fs"
	"os"
)

// Session on non-unix platforms delegates to the stateless Authorize, which
// refuses every path there (device identity is unknown). kipple targets
// macOS; this exists so the module builds elsewhere.
type Session struct{ env Env }

// HomeError reports that NewSession could not use Env.Home.
type HomeError struct {
	Home   string
	Reason string
	Err    error
}

func (e *HomeError) Error() string { return "home " + e.Home + " cannot be used: " + e.Reason }

// NewSession returns a Session for env.
func NewSession(env Env) (*Session, error) { return &Session{env: env}, nil }

// Close is a no-op.
func (s *Session) Close() error { return nil }

// Home returns the session's home directory.
func (s *Session) Home() string { return s.env.Home }

// Authorize delegates to Authorize.
func (s *Session) Authorize(rule Rule, path string) Decision { return Authorize(s.env, rule, path) }

// Fresh is an approved path with its parent held open (unix only).
type Fresh struct {
	ParentFd int
	Name     string
	Info     fs.FileInfo
}

// Close is a no-op here.
func (f *Fresh) Close() error { return nil }

// AuthorizeFresh delegates to Authorize, which refuses every path here.
func (s *Session) AuthorizeFresh(rule Rule, path string) (*Fresh, Decision) {
	return nil, Authorize(s.env, rule, path)
}

// Identity returns the file identity of info, if known.
func Identity(info fs.FileInfo) (dev, ino uint64, ok bool) {
	id, ok := identityOf(info)
	return id.dev, id.ino, ok
}

// Open is unsupported on non-unix platforms.
func (s *Session) Open(path string) (Entry, error) {
	return Entry{}, &RefusedError{Path: path, Reason: ReasonDifferentDevice, Err: errors.New("unsupported platform")}
}

// Guard returns nil: Open always fails here, so nothing is measured.
func (s *Session) Guard(Rule, string) *Guard { return nil }

// Mkdir is unsupported here.
func (s *Session) Mkdir(path string, _ uint32) error {
	return &RefusedError{Path: path, Reason: ReasonInvalidEnv}
}

// VerifyParent refuses everything on non-unix platforms.
func (s *Session) VerifyParent(*Fresh, string) Decision { return refuse(ReasonParentChanged) }

// AuthorizeDestination delegates to Authorize, which refuses every path here.
func (s *Session) AuthorizeDestination(rule Rule, path string, _ fs.FileMode) (*Fresh, Decision) {
	return nil, Authorize(s.env, rule, path)
}

// OpenUnderHome is unsupported on non-unix platforms.
func (s *Session) OpenUnderHome(rel string) (*os.File, error) {
	return nil, &RefusedError{Path: rel, Reason: ReasonDifferentDevice, Err: errors.New("unsupported platform")}
}

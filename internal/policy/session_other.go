//go:build !unix

package policy

import "errors"

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

// Open is unsupported on non-unix platforms.
func (s *Session) Open(path string) (Entry, error) {
	return Entry{}, &RefusedError{Path: path, Reason: ReasonDifferentDevice, Err: errors.New("unsupported platform")}
}

// Guard returns nil: Open always fails here, so nothing is measured.
func (s *Session) Guard(Rule, string) *Guard { return nil }

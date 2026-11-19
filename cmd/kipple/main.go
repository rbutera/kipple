// Command kipple is a macOS disk cleaner for AI-tool and developer junk. It
// lists what it would move and why; nothing moves without --apply.
package main

import (
	"errors"
	"io"
	"os"
	"os/user"

	"github.com/rbutera/kipple/internal/homeguard"
)

// Set with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, system()))
}

// system is the real process environment. Only cmd/ resolves the real home.
func system() runtimeEnv {
	return runtimeEnv{
		getenv:    os.Getenv,
		geteuid:   os.Geteuid,
		userHome:  os.UserHomeDir,
		realHomes: realHomes,
		guardFS:   homeguard.System(),
		isTTY:     isTerminal,
	}
}

// realHomes returns every place the real home might be: $HOME and the user
// database entry, so overriding HOME cannot hide it from the KIPPLE_HOME
// guard.
func realHomes() ([]string, error) {
	var out []string
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		out = append(out, h)
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		out = append(out, u.HomeDir)
	}
	if len(out) == 0 {
		return nil, errors.New("neither $HOME nor the user database names a home folder")
	}
	return out, nil
}

// isTerminal reports whether w is a character device (a terminal).
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

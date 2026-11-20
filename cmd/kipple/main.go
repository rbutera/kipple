// Command kipple is a macOS disk cleaner for AI-tool and developer junk. It
// lists what it would move and why; nothing moves without --apply.
package main

import (
	"io"
	"os"

	"github.com/rbutera/kipple/cmd/internal/launch"
	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/procs"
)

// Set with -ldflags "-X main.version=... -X main.commit=... -X main.date=..."
// (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = ""
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
		stdin:     os.Stdin,
		procs:     procs.New(),
	}
}

// realHomes returns every place the real home might be (launch.RealHomes).
func realHomes() ([]string, error) { return launch.RealHomes() }

// isTerminal reports whether w is a character device (a terminal).
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

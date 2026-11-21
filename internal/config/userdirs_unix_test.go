//go:build unix

package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/testutil"
)

// Wave 2 re-attack F5: a user-dirs.dirs linked to a FIFO must not block:
// it is refused with a warning, and the built-in protections stay.
func TestUserDirsLinkedToAFIFO(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Dirs: []testutil.Dir{{Path: ".config"}, {Path: "elsewhere"}}})
	fifo := filepath.Join(home, "elsewhere", "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fifo, filepath.Join(home, ".config", "user-dirs.dirs")); err != nil {
		t.Fatal(err)
	}
	done := make(chan UserDirs, 1)
	go func() {
		done <- LoadUserDirs(home, func(rel string) (*os.File, error) { return openConfig(filepath.Join(home, rel)) })
	}()
	select {
	case u := <-done:
		if len(u.Protect) != 0 || len(u.Warnings) != 1 {
			t.Fatalf("%+v", u)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a user-dirs.dirs linked to a FIFO blocked")
	}
}

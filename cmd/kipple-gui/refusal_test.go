//go:build gui

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// GUI started on Linux (gui spec, "GUI platform availability"): the app
// exits 1 before opening a window, naming kipple tui, and reads nothing
// under home. On Linux the real main runs in a child process with an empty
// stand-in HOME and KIPPLE_HOME; elsewhere only the message is checked.
func TestLinuxRefusal(t *testing.T) {
	msg := platformRefusal("linux")
	if !strings.Contains(msg, "kipple tui") || platformRefusal("darwin") != "" || platformRefusal("freebsd") == "" {
		t.Fatalf("refusal messages: linux %q, darwin %q", msg, platformRefusal("darwin"))
	}
	if runtime.GOOS != "linux" {
		return
	}
	home, khome := t.TempDir(), t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxRefusalMain$")
	cmd.Env = []string{"KIPPLE_GUI_RUN_MAIN=1", "HOME=" + home, "KIPPLE_HOME=" + khome, "KIPPLE_STATE_DIR=" + khome + "/state", "PATH=/usr/bin:/bin", "TMPDIR=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &ee) || ee.ExitCode() != 1 || !strings.Contains(string(out), msg) {
		t.Fatalf("kipple-gui on Linux: err %v, output %q", err, out)
	}
	t.Logf("kipple-gui on Linux exited 1: %s", strings.TrimSpace(string(out)))
	for _, d := range []string{home, khome} {
		if es, err := os.ReadDir(d); err != nil || len(es) != 0 {
			t.Fatalf("%s changed: %v %v", d, es, err)
		}
	}
}

// TestLinuxRefusalMain runs main in the child process of TestLinuxRefusal.
func TestLinuxRefusalMain(t *testing.T) {
	if os.Getenv("KIPPLE_GUI_RUN_MAIN") != "1" {
		t.Skip("runs only as the child process of TestLinuxRefusal")
	}
	main()
	t.Fatal("main returned instead of exiting")
}

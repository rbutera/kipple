//go:build darwin

package procs

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

type exitErr int

func (e exitErr) Error() string { return "exit status" }
func (e exitErr) ExitCode() int { return int(e) }

// fake answers commands from a table keyed by the full command line.
func fake(t *testing.T, table map[string]func() ([]byte, error)) System {
	return System{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := strings.Join(append([]string{name}, args...), " ")
		f, ok := table[key]
		if !ok {
			t.Errorf("unexpected command %q", key)
			return nil, exec.ErrNotFound
		}
		return f()
	}}
}

func out(s string) func() ([]byte, error) { return func() ([]byte, error) { return []byte(s), nil } }
func exit(n int) func() ([]byte, error)   { return func() ([]byte, error) { return nil, exitErr(n) } }
func missing() ([]byte, error)            { return nil, exec.ErrNotFound }

// Fake runners for found, not found, and command missing (task 3.4).
func TestRunningBundleIDs(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		ids, err := fake(t, map[string]func() ([]byte, error){"lsappinfo list": out(`bundleID="com.apple.dt.Xcode"`)}).RunningBundleIDs(context.Background())
		if err != nil || !ids["com.apple.dt.Xcode"] {
			t.Fatalf("%v %v", ids, err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		ids, err := fake(t, map[string]func() ([]byte, error){"lsappinfo list": out(`bundleID="com.apple.finder"`)}).RunningBundleIDs(context.Background())
		if err != nil || ids["com.apple.dt.Xcode"] {
			t.Fatalf("%v %v", ids, err)
		}
	})
	t.Run("command missing is unknown", func(t *testing.T) {
		_, err := fake(t, map[string]func() ([]byte, error){"lsappinfo list": missing}).RunningBundleIDs(context.Background())
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
	})
}

func TestRunningNames(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		got, err := fake(t, map[string]func() ([]byte, error){"pgrep -x Xcode": out("42\n")}).RunningNames(context.Background(), []string{"Xcode"})
		if err != nil || !got["Xcode"] {
			t.Fatalf("%v %v", got, err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		got, err := fake(t, map[string]func() ([]byte, error){"pgrep -x Xcode": exit(1)}).RunningNames(context.Background(), []string{"Xcode"})
		if err != nil || got["Xcode"] {
			t.Fatalf("%v %v", got, err)
		}
	})
	t.Run("command missing is unknown", func(t *testing.T) {
		_, err := fake(t, map[string]func() ([]byte, error){"pgrep -x Xcode": missing}).RunningNames(context.Background(), []string{"Xcode"})
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
	})
	t.Run("pgrep error is unknown", func(t *testing.T) {
		_, err := fake(t, map[string]func() ([]byte, error){"pgrep -x Xcode": exit(3)}).RunningNames(context.Background(), []string{"Xcode"})
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
	})
	t.Run("regex characters are escaped", func(t *testing.T) {
		got, err := fake(t, map[string]func() ([]byte, error){`pgrep -x a\.b`: exit(1)}).RunningNames(context.Background(), []string{"a.b"})
		if err != nil || got["a.b"] {
			t.Fatalf("%v %v", got, err)
		}
	})
}

func TestProcessCwds(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		s := fake(t, map[string]func() ([]byte, error){
			"pgrep -x claude":             out("10\n11\n"),
			"pgrep -x codex":              exit(1),
			"lsof -a -d cwd -Fn -p 10,11": out("p10\nfcwd\nn/fx/dev/app\np11\nfcwd\nn/fx/dev/other/sub\n"),
		})
		cwds, err := s.ProcessCwds(context.Background(), []string{"claude", "codex"})
		if err != nil || !slices.Equal(cwds, []string{"/fx/dev/app", "/fx/dev/other/sub"}) {
			t.Fatalf("%v %v", cwds, err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		s := fake(t, map[string]func() ([]byte, error){"pgrep -x claude": exit(1)})
		cwds, err := s.ProcessCwds(context.Background(), []string{"claude"})
		if err != nil || len(cwds) != 0 {
			t.Fatalf("%v %v", cwds, err)
		}
	})
	t.Run("lsof missing is unknown", func(t *testing.T) {
		s := fake(t, map[string]func() ([]byte, error){"pgrep -x claude": out("10\n"), "lsof -a -d cwd -Fn -p 10": missing})
		if _, err := s.ProcessCwds(context.Background(), []string{"claude"}); !errors.Is(err, ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
	})
	t.Run("lsof partial answer stands", func(t *testing.T) {
		s := fake(t, map[string]func() ([]byte, error){
			"pgrep -x claude":             out("10\n11\n"),
			"lsof -a -d cwd -Fn -p 10,11": func() ([]byte, error) { return []byte("p10\nfcwd\nn/fx/a\n"), exitErr(1) },
		})
		cwds, err := s.ProcessCwds(context.Background(), []string{"claude"})
		if err != nil || !slices.Equal(cwds, []string{"/fx/a"}) {
			t.Fatalf("%v %v", cwds, err)
		}
	})
}

func TestTimeoutIsUnknown(t *testing.T) {
	s := System{Timeout: 10 * time.Millisecond, Run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	if _, err := s.RunningBundleIDs(context.Background()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("err = %v, want ErrUnknown", err)
	}
}

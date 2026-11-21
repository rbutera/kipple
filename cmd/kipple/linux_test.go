//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/testutil"
)

// Spec scenarios of platform-support and clean-apply that run the CLI on
// Linux against a fixture.
func TestLinuxCLI(t *testing.T) {
	cli := func(t *testing.T, args ...string) (int, string, string) {
		t.Helper()
		rt := testEnv(t)
		rt.goos = "linux"
		return cliWith(t, rt, args...)
	}
	cleanFixture := linuxFixture
	t.Run("Linux state directory", func(t *testing.T) {
		home := cleanFixture(t)
		t.Setenv("KIPPLE_STATE_DIR", "")
		code, out, _ := cli(t, "clean", "--apply", "--json")
		var res plan.Result
		exactlyOneJSON(t, out, &res)
		if code != exitOK || len(res.Moved) != 2 {
			t.Fatalf("exit %d result %+v", code, res)
		}
		if dir := filepath.Join(home, ".local", "state", "kipple", "receipts"); filepath.Dir(res.ReceiptPath) != dir {
			t.Fatalf("receipt at %s, want it in %s", res.ReceiptPath, dir)
		}
		for _, m := range res.Moved {
			if filepath.Dir(m.TrashPath) != filepath.Join(home, ".local/share/Trash/files") {
				t.Fatalf("%s went to %s", m.Path, m.TrashPath)
			}
		}
	})
	t.Run("Fixture Trash on Linux", func(t *testing.T) {
		home := cleanFixture(t)
		state := testutil.StateDir(home)
		// The real home's Trash and state are only looked at (stat), never
		// written; they must be exactly as they were.
		watch := testutil.RealHomePaths(".local/share/Trash", ".local/share/Trash/files", ".local/share/Trash/info", ".local/state/kipple")
		before := statAll(watch)
		outside := testutil.Snapshot(t, filepath.Dir(home))
		code, out, _ := cli(t, "clean", "--apply", "--json")
		var res plan.Result
		exactlyOneJSON(t, out, &res)
		if code != exitOK || len(res.Moved) != 2 {
			t.Fatalf("exit %d result %+v", code, res)
		}
		for _, m := range res.Moved {
			if !strings.HasPrefix(m.TrashPath, home+"/") {
				t.Fatalf("%s left the fixture: %s", m.Path, m.TrashPath)
			}
		}
		if !strings.HasPrefix(res.ReceiptPath, state+"/") {
			t.Fatalf("receipt outside the state directory: %s", res.ReceiptPath)
		}
		if statAll(watch) != before {
			t.Fatal("the real home's Trash or state changed")
		}
		// Outside the fixture home and its state directory, nothing in
		// the test's folder changed.
		inside := func(p string) bool {
			for _, d := range []string{filepath.Base(home), filepath.Base(state)} {
				if p == d || strings.HasPrefix(p, d+"/") {
					return true
				}
			}
			return false
		}
		var after, was []string
		for _, e := range testutil.Snapshot(t, filepath.Dir(home)) {
			if !inside(e.Path) {
				after = append(after, e.Path+" "+e.MTime.String())
			}
		}
		for _, e := range outside {
			if !inside(e.Path) {
				was = append(was, e.Path+" "+e.MTime.String())
			}
		}
		if strings.Join(after, "\n") != strings.Join(was, "\n") {
			t.Fatalf("entries outside the fixture changed:\n%q\n%q", was, after)
		}
	})
	t.Run("Linux receipt validates", func(t *testing.T) {
		cleanFixture(t)
		code, out, _ := cli(t, "clean", "--apply", "--json")
		var res plan.Result
		exactlyOneJSON(t, out, &res)
		if code != exitOK {
			t.Fatalf("exit %d", code)
		}
		data, err := os.ReadFile(res.ReceiptPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"mover": "freedesktop"`) {
			t.Fatalf("receipt mover is not freedesktop:\n%s", data)
		}
		_, schema, _ := cli(t, "schema", "receipt")
		if err := plantest.ValidateWith([]byte(schema), "receipt", data); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Restore removes the info file through the CLI", func(t *testing.T) {
		home := cleanFixture(t)
		code, out, _ := cli(t, "clean", "--apply", "--json")
		var res plan.Result
		exactlyOneJSON(t, out, &res)
		if code != exitOK || len(res.Moved) != 2 {
			t.Fatalf("exit %d %+v", code, res)
		}
		if entries, _ := os.ReadDir(filepath.Join(home, ".local/share/Trash/info")); len(entries) != 2 {
			t.Fatalf("info files %v, want 2", entries)
		}
		code, out, _ = cli(t, "restore", res.ReceiptID, "--apply", "--json")
		var rr plan.Result
		exactlyOneJSON(t, out, &rr)
		if code != exitOK || len(rr.Restored) != 2 {
			t.Fatalf("exit %d %+v", code, rr)
		}
		for _, d := range []string{"files", "info"} {
			if entries, _ := os.ReadDir(filepath.Join(home, ".local/share/Trash", d)); len(entries) != 0 {
				t.Fatalf("%s left: %v", d, entries)
			}
		}
	})
}

// statAll describes each path's existence, identity and modification
// time, read with lstat only.
func statAll(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		fi, err := os.Lstat(p)
		if err != nil {
			b.WriteString(p + " absent\n")
			continue
		}
		b.WriteString(p + " " + fi.Mode().String() + " " + fi.ModTime().String() + "\n")
	}
	return b.String()
}

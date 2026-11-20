package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/testutil"
)

// evilName retitles the terminal (OSC 0 ... BEL), clears the screen,
// recolours text, starts a new row and reverses what follows when printed
// raw.
const evilName = "evil\x1b]0;PWNED\x07\x1b[2J\x1b[31mred\x1b[0m\nrow\u202etxt"

func assertTerminalSafe(t *testing.T, name, text string) {
	t.Helper()
	if !utf8.ValidString(text) {
		t.Errorf("%s: invalid UTF-8", name)
	}
	for i, r := range text {
		if r != '\n' && output.Unsafe(r) {
			t.Fatalf("%s: unsafe character %U at byte %d:\n%q", name, r, i, text)
		}
	}
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "row") {
			t.Errorf("%s: a name's newline started a row: %q", name, l)
		}
	}
}

// Must-fix 1 (wave 4 review): a fixture folder whose name carries terminal
// escapes, a newline and a bidi override is listed, explained, moved,
// listed in history and restored with every text output escaped; JSON
// keeps the exact name.
func TestTerminalEscapesInPaths(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/" + evilName + "/blob", Size: 4096, MTime: old},
			{Path: "Library/Logs/app/log.txt", Size: 1024, MTime: old},
		},
		Dirs: []testutil.Dir{{Path: ".Trash", Mode: 0o700}, {Path: "Library/Caches/" + evilName, MTime: old}},
	})
	evilPath := filepath.Join(home, "Library/Caches", evilName)
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	var id string
	for _, it := range p.Items {
		if it.Path == evilPath {
			id = it.ID
		}
	}
	if id == "" {
		t.Fatalf("the scan did not list the folder (JSON keeps the exact name): %+v", p.Items)
	}
	text := func(args ...string) string {
		t.Helper()
		code, stdout, stderr := cli(t, args...)
		if code != exitOK {
			t.Fatalf("%v: exit %d\n%s", args, code, stderr)
		}
		assertTerminalSafe(t, strings.Join(args, " ")+" (stdout)", stdout)
		assertTerminalSafe(t, strings.Join(args, " ")+" (stderr)", stderr)
		return stdout
	}
	if s := text("scan"); !strings.Contains(s, `evil\x1b]0;PWNED\x07\x1b[2J\x1b[31mred\x1b[0m\nrow\u202etxt`) {
		t.Fatalf("the name is not shown with visible escapes:\n%s", s)
	}
	text("clean")
	text("explain", id)
	text("clean", "--apply", "--only", id)
	if _, err := os.Lstat(evilPath); !os.IsNotExist(err) {
		t.Fatal("the folder was not moved")
	}
	text("history")
	var h plan.History
	_, out, _ = cli(t, "history", "--json")
	exactlyOneJSON(t, out, &h)
	rid := h.Receipts[0].ID

	// Must-fix 5: a dry run for one item suggests restoring that item, not
	// the whole receipt.
	if s := text("restore", id); !strings.Contains(s, "put the items back: kipple restore "+id+" --apply") || strings.Contains(s, rid+" --apply") {
		t.Fatalf("item restore hint:\n%s", s)
	}
	if s := text("restore", rid, "--only", id); !strings.Contains(s, "kipple restore "+rid+" --only "+id+" --apply") {
		t.Fatalf("--only restore hint:\n%s", s)
	}
	if s := text("restore", rid); !strings.Contains(s, "kipple restore "+rid+" --apply") {
		t.Fatalf("receipt restore hint:\n%s", s)
	}
	text("restore", rid, "--apply")
	if _, err := os.Lstat(filepath.Join(evilPath, "blob")); err != nil {
		t.Fatalf("not restored: %v", err)
	}
}

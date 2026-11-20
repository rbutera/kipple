package output

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
)

// evilName is a folder name that, printed raw, retitles the terminal
// window (OSC 0 ... BEL), clears the screen, recolours text, starts a new
// row, and reverses what follows (U+202E).
const evilName = "evil\x1b]0;PWNED\x07\x1b[2J\x1b[31mred\x1b[0m\nrow\u202etxt\u2028\x9b\x7f"

// AssertTerminalSafe fails if text holds any control character other than
// the newlines between rows, any C1 control, DEL, bidi control or line
// separator, or invalid UTF-8.
func assertTerminalSafe(t *testing.T, name, text string) {
	t.Helper()
	if !utf8.ValidString(text) {
		t.Errorf("%s: invalid UTF-8", name)
	}
	for i, r := range text {
		if r != '\n' && Unsafe(r) {
			t.Fatalf("%s: unsafe character %U at byte %d:\n%q", name, r, i, text)
		}
	}
}

func TestSafeText(t *testing.T) {
	for in, want := range map[string]string{
		"plain/path":               "plain/path",
		"café ☕ 日本":                "café ☕ 日本",
		"a\x1b]0;x\x07b":           `a\x1b]0;x\x07b`,
		"line\nbreak\ttab\r":       `line\nbreak\ttab\r`,
		"del\x7f c1\u009b":         `del\x7f c1\x9b`,
		"rtl\u202eabc\u2066\u200f": `rtl\u202eabc\u2066\u200f`,
		"sep\u2028\u2029":          `sep\u2028\u2029`,
		"bad\xffbyte":              `bad\xffbyte`,
	} {
		if got := SafeText(in); got != want {
			t.Errorf("SafeText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := safeBlock("row one\nrow\x1b two\n"); got != "row one\nrow\\x1b two\n" {
		t.Errorf("safeBlock = %q", got)
	}
}

// Must-fix 1 (wave 4 review): a path with terminal escapes and a newline is
// printed escaped by every text renderer, on exactly the rows it would have
// had with a plain name.
func TestTextOutputEscapesPaths(t *testing.T) {
	home := "/tmp/fx/home"
	path := home + "/Library/Caches/" + evilName
	it := plan.Item{ID: plan.ID("caches.user", "Library/Caches/"+evilName), Rule: "caches.user", Tier: "safe", Path: path,
		AllocatedBytes: 4096, ScanStatus: plan.ScanComplete, Selected: true, Reasons: []string{},
		Git: &plan.Git{State: plan.GitDirty, Detail: "branch \x1b[2Jmain"}, ProtectedPaths: []string{".env\x1b[2J"},
		Warnings: []string{"warn\x07"}}
	p := plan.Plan{Home: home, Level: "safe", Items: []plan.Item{it}, Warnings: []string{"plan \x1b]0;w\x07"},
		Rules: []plan.RuleStatus{{ID: "caches.user", Tier: "safe", Status: plan.RulePresent, ItemCount: 1, Bytes: 4096}}}
	moved := plan.MovedItem{ID: it.ID, Rule: it.Rule, Path: path, TrashPath: home + "/.Trash/" + evilName, Bytes: 4096}
	res := plan.Result{Command: "clean", Home: home, ReceiptID: "20260101T000000Z-aaaaaa", ReceiptPath: "/r/x.json",
		Moved: []plan.MovedItem{moved}, Displaced: []plan.MovedItem{moved},
		Skipped:  []plan.SkippedItem{{ID: it.ID, Rule: it.Rule, Path: path, Reason: plan.SkipMoveFailed, Detail: "rename " + evilName}},
		Warnings: []string{"w " + evilName}}
	restored := plan.Result{Command: "restore", Home: home, ReceiptID: res.ReceiptID, DryRun: true,
		Restored: []plan.RestoredItem{{ID: it.ID, Rule: it.Rule, Path: path, TrashPath: moved.TrashPath}}, Skipped: res.Skipped}
	r, _ := rules.Lookup("caches.user")
	renders := map[string]func(*bytes.Buffer) error{
		"plan":    func(b *bytes.Buffer) error { return Plan(b, p, nil) },
		"explain": func(b *bytes.Buffer) error { return ExplainItem(b, it, r) },
		"clean":   func(b *bytes.Buffer) error { return CleanResult(b, res) },
		"dry run": func(b *bytes.Buffer) error { d := res; d.DryRun = true; return CleanResult(b, d) },
		"restore": func(b *bytes.Buffer) error { return RestoreResult(b, restored) },
		"history": func(b *bytes.Buffer) error {
			return History(b, plan.History{Receipts: []plan.Receipt{{ID: "x\x1b[2J"}}, Warnings: []string{evilName}})
		},
	}
	for name, render := range renders {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		assertTerminalSafe(t, name, out)
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "row") {
				t.Errorf("%s: the name's newline started a row: %q", name, l)
			}
		}
		if strings.Contains(out, "evil") && !strings.Contains(out, `evil\x1b]0;PWNED\x07\x1b[2J`) {
			t.Errorf("%s: the name is not shown with visible escapes:\n%s", name, out)
		}
	}
}

// The restore dry run suggests the command that repeats the request: the
// receipt form by default, the caller's form (an item ID) when given.
func TestRestoreHintRepeatsRequest(t *testing.T) {
	r := plan.Result{Command: "restore", DryRun: true, ReceiptID: "20260101T000000Z-aaaaaa"}
	var b bytes.Buffer
	_ = RestoreResult(&b, r)
	if !strings.Contains(b.String(), "kipple restore 20260101T000000Z-aaaaaa --apply") {
		t.Fatal(b.String())
	}
	b.Reset()
	_ = RestoreResultWith(&b, r, "kipple restore abcdefghijkl --apply")
	if !strings.Contains(b.String(), "put the items back: kipple restore abcdefghijkl --apply") || strings.Contains(b.String(), "20260101T000000Z-aaaaaa --apply") {
		t.Fatal(b.String())
	}
}

package words

import (
	"os"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

func TestEveryReasonHasWordsAndAChip(t *testing.T) {
	for _, r := range plan.Reasons() {
		if Reason(r) == "" || Reason(r) == r {
			t.Errorf("reason %q has no words", r)
		}
		if Chip(r) == "" || (Chip(r) == r && !strings.HasPrefix(r, plan.RefusedPrefix)) {
			t.Errorf("reason %q has no chip", r)
		}
		if strings.HasPrefix(r, plan.RefusedPrefix) && Chip(r) != "refused" {
			t.Errorf("refusal %q chip %q", r, Chip(r))
		}
	}
}

func TestEverySkipReasonHasASentence(t *testing.T) {
	for _, r := range plan.SkipReasons() {
		s, next := Skip(r)
		if s == "" || !strings.HasSuffix(s, ".") || strings.Contains(s, "_") && !strings.Contains(s, "(") {
			t.Errorf("skip reason %q: sentence %q", r, s)
		}
		if next != "" && !strings.HasSuffix(next, ".") {
			t.Errorf("skip reason %q: next step %q", r, next)
		}
	}
}

func TestHostileReasonIsEscaped(t *testing.T) {
	r := plan.RefusedPrefix + "x\x1b[31mred"
	if strings.ContainsRune(Reason(r), '\x1b') || strings.ContainsRune(Chip("\x1b]0;t\a"), '\x1b') {
		t.Fatal("control characters reached the words")
	}
}

func TestBlockersSkipTierReasons(t *testing.T) {
	got := Blockers([]string{plan.ReasonTierReview, plan.ReasonGitDirty, plan.ReasonTierManual})
	if len(got) != 1 || got[0] != "the project has uncommitted changes" {
		t.Fatalf("%q", got)
	}
	if FirstBlocker([]string{plan.ReasonTierManual}) != "" || FirstBlocker([]string{plan.ReasonTierReview, plan.ReasonTooRecent}) != plan.ReasonTooRecent {
		t.Fatal("FirstBlocker")
	}
}

func TestMoverAndPlural(t *testing.T) {
	if Mover(plan.MoverRename) == "" || Mover(plan.MoverNSFileManager) == "" || Mover(plan.MoverNone) != "" {
		t.Fatal("mover words")
	}
	if Items(1) != "1 item" || Items(3) != "3 items" || Sentence("abc") != "Abc." {
		t.Fatal("plural or sentence")
	}
	for _, tier := range []string{"safe", "review", "manual", "info"} {
		if TierLine(tier) == "" {
			t.Errorf("tier %s has no line", tier)
		}
	}
}

// No Finder, macOS or ~/.Trash wording for Linux users.
func TestPhrasesPerPlatform(t *testing.T) {
	l, d := PhrasesFor("linux"), PhrasesFor("darwin")
	for _, s := range []string{l.TrashFolder, l.YourTrash, l.OtherRestore, l.SystemRefused} {
		for _, bad := range []string{"Finder", "macOS", "~/.Trash", "Put Back"} {
			if strings.Contains(s, bad) {
				t.Errorf("Linux phrase %q mentions %q", s, bad)
			}
		}
	}
	if d.TrashFolder != "~/.Trash" || d.OtherRestore != "Finder's Put Back" || !strings.HasPrefix(d.SystemRefused, "macOS") {
		t.Errorf("macOS phrases changed: %+v", d)
	}
	if !strings.Contains(l.YourTrash, "~/.local/share/Trash") {
		t.Errorf("Linux Trash %q", l.YourTrash)
	}
}

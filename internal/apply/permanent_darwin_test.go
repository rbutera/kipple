//go:build darwin && kipple_permanent

package apply

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
)

// swapFx is permanentFx plus a precious folder outside every rule, which
// the tests swap with the item's subfolder zzz (RENAME_SWAP, as the red
// team did).
func swapFx(t *testing.T) (*fx, Options, string, func()) {
	f, o, item := permanentFx(t)
	if err := os.MkdirAll(f.abs("precious"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.abs("precious/thesis.txt"), []byte("four years of work"), 0o600); err != nil {
		t.Fatal(err)
	}
	swap := func() {
		if err := unix.RenameatxNp(unix.AT_FDCWD, item+"/zzz", unix.AT_FDCWD, f.abs("precious"), unix.RENAME_SWAP); err != nil {
			t.Errorf("RENAME_SWAP: %v", err)
		}
	}
	return f, o, item, swap
}

// thesisIntact reports whether the precious file survives, wherever the
// swaps left its folder.
func thesisIntact(f *fx, item string) bool {
	for _, p := range []string{f.abs("precious/thesis.txt"), item + "/zzz/thesis.txt"} {
		if b, err := os.ReadFile(p); err == nil && string(b) == "four years of work" {
			return true
		}
	}
	return false
}

// Must-fix 1 (HIGH): a folder swapped into the item at any point of a
// permanent delete is never deleted; the item is refused with
// identity_changed and the run is partial.
func TestPermanentNeverDeletesASwappedInFolder(t *testing.T) {
	cases := []struct {
		name  string
		setup func(o *Options, swap func())
	}{
		{"after the checks, before the delete", func(o *Options, swap func()) {
			o.beforeMove = func(plan.Item) { swap() }
		}},
		{"between the check pass and the delete pass", func(_ *Options, swap func()) {
			afterPrecheck = swap
		}},
		{"during the delete, before descending", func(_ *Options, swap func()) {
			beforeDescend = func(rel string) {
				if rel == "zzz" {
					swap()
				}
			}
		}},
		{"during the delete, after opening the folder", func(_ *Options, swap func()) {
			afterDescend = func(rel string) {
				if rel == "zzz" {
					swap()
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() { afterPrecheck, beforeDescend, afterDescend = nil, nil, nil }()
			f, o, item, swap := swapFx(t)
			tc.setup(&o, swap)
			res := f.run(o)
			if !thesisIntact(f, item) {
				t.Fatal("the swapped-in folder's file was deleted")
			}
			if _, ok := movedFor(res, item); ok || ExitCode(res) != ExitPartial {
				t.Fatalf("exit %d moved %+v skipped %+v", ExitCode(res), res.Moved, res.Skipped)
			}
			s, _ := skipFor(res, item)
			if s.Reason != plan.SkipIdentityChanged {
				t.Fatalf("reason %q (%s), want %s", s.Reason, s.Detail, plan.SkipIdentityChanged)
			}
		})
	}
}

// The red team's loop: RENAME_SWAP of zzz and the precious folder runs
// continuously through the whole scan and permanent delete. The precious
// file must survive every run.
func TestPermanentUnderASwapLoop(t *testing.T) {
	for i := 0; i < 25; i++ {
		f, _, item, _ := swapFx(t)
		var stop atomic.Bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_ = unix.RenameatxNp(unix.AT_FDCWD, item+"/zzz", unix.AT_FDCWD, f.abs("precious"), unix.RENAME_SWAP)
				time.Sleep(time.Duration(i%5) * 20 * time.Microsecond)
			}
		}()
		o := f.opts(f.scan(""))
		o.Permanent = true
		_ = f.run(o)
		stop.Store(true)
		wg.Wait()
		if !thesisIntact(f, item) {
			t.Fatalf("run %d: the precious file was deleted", i)
		}
	}
}

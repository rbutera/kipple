package apply

import (
	"errors"
	"os"
	"testing"

	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/trash"
)

// probeCase is one TestProbeTrash case; platformProbeCases adds the
// platform's own (probe_darwin_test.go, probe_linux_test.go).
type probeCase struct {
	name    string
	lock    bool // make the fixture's Trash unreadable (chmod 000)
	setup   func(f *fx, o *Options)
	want    TrashMode
	wantErr error
}

// ProbeTrash makes Run's choice of mover and changes nothing: the fixture's
// checksum is the same afterwards and no receipt is written.
func TestProbeTrash(t *testing.T) {
	cases := append([]probeCase{
		{"an openable Trash is used directly", false, func(*fx, *Options) {}, TrashRename, nil},
		{"an unreadable Trash without the fallback (KIPPLE_HOME) is unavailable", true, func(_ *fx, o *Options) {
			o.Finder = nil
		}, TrashUnavailable, ErrTrashAccess},
		{"a Trash that cannot be opened for lack of permission is unavailable", false, func(f *fx, o *Options) {
			o.openTrash = errTrashEPERM(f.home)
			o.Finder = func() (trash.ExpectTrasher, error) { return nil, errors.New("no cgo") }
		}, TrashUnavailable, ErrTrashAccess},
	}, platformProbeCases...)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.GuardRealHome(t)
			f := newFx(t, threeSafe())
			o := f.opts(f.scan(""))
			c.setup(f, &o)
			before := testutil.Checksum(t, f.home)
			if c.lock {
				chmod(t, f.abs(testutil.TrashRel), 0o000)
			}
			got, err := ProbeTrash(o)
			if got != c.want {
				t.Fatalf("mode %q (%v), want %q", got, err, c.want)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("error %v, want %v", err, c.wantErr)
			}
			if c.want != TrashUnavailable && err != nil {
				t.Fatalf("error %v", err)
			}
			chmod(t, f.abs(testutil.TrashRel), 0o700)
			if testutil.Checksum(t, f.home) != before {
				t.Fatal("ProbeTrash changed the fixture")
			}
			if es, _ := f.store.List(); len(es) != 0 {
				t.Fatalf("ProbeTrash wrote %d receipts", len(es))
			}
		})
	}
	t.Run("an explicit Trash is used directly", func(t *testing.T) {
		testutil.GuardRealHome(t)
		f := newFx(t, threeSafe())
		o := f.opts(f.scan(""))
		tr, err := trash.OpenDirTrasher(f.abs(testutil.TrashRel))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tr.Close() }()
		o.Trash = tr
		if got, err := ProbeTrash(o); got != TrashRename || err != nil {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("an unpinned home is refused", func(t *testing.T) {
		testutil.GuardRealHome(t)
		if got, err := ProbeTrash(Options{Receipts: receipt.Store{}}); got != TrashUnavailable || err == nil {
			t.Fatalf("%q %v", got, err)
		}
	})
}

// chmod changes a fixture folder's mode, ignoring a folder that is gone.
func chmod(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(p, mode); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

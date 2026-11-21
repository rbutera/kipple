//go:build darwin

// macOS protects ~/.Trash: only apps with Full Disk Access may open it, so
// apply falls back to NSFileManager's Trash call. Linux has no such
// protection and no system mover to fall back on (finderFallback is
// false there), so these tests are macOS-only.

package apply

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/trash"
)

// deniedTrasher is a mover NSFileManager refuses.
type deniedTrasher struct{}

func (deniedTrasher) Trash(int, string) (string, error) { return "", fs.ErrPermission }
func (deniedTrasher) TrashExpect(int, string, uint64, uint64) (string, error) {
	return "", fmt.Errorf("NSFileManager: %w", fs.ErrPermission)
}

// Must-fix 1: ~/.Trash cannot be opened without Full Disk Access. apply
// falls back to the NSFileManager mover; when that is missing or refused it
// fails with ErrTrashAccess and moves nothing.
func TestTrashFallback(t *testing.T) {
	t.Run("EPERM on the Trash falls back to NSFileManager", func(t *testing.T) {
		f := newFx(t, threeSafe())
		o := f.opts(f.scan(""))
		o.openTrash = errTrashEPERM(f.home)
		other := filepath.Join(filepath.Dir(f.home), "finder-trash")
		if err := os.Mkdir(other, 0o700); err != nil {
			t.Fatal(err)
		}
		o.Finder = func() (trash.ExpectTrasher, error) { return trash.OpenDirTrasher(other) } // stands in for NSFileManager
		res := f.run(o)
		if len(res.Moved) != 3 || len(res.Warnings) == 0 {
			t.Fatalf("%+v", res)
		}
		e, err := f.store.Load(res.ReceiptID)
		if err != nil || e.Receipt.Mover != plan.MoverNSFileManager || e.Receipt.Moved[0].Mover != plan.MoverNSFileManager {
			t.Fatalf("receipt %+v %v", e.Receipt, err)
		}
	})
	t.Run("no fallback: trash_access", func(t *testing.T) {
		f := newFx(t, threeSafe())
		o := f.opts(f.scan(""))
		o.openTrash = errTrashEPERM(f.home)
		if _, err := Run(context.Background(), o); !errors.Is(err, ErrTrashAccess) {
			t.Fatalf("err = %v, want ErrTrashAccess", err)
		}
		if n, _ := os.ReadDir(f.abs("Library/Caches")); len(n) != 1 {
			t.Fatal("something moved")
		}
	})
	t.Run("fallback unavailable: trash_access", func(t *testing.T) {
		f := newFx(t, threeSafe())
		o := f.opts(f.scan(""))
		o.openTrash = errTrashEPERM(f.home)
		o.Finder = func() (trash.ExpectTrasher, error) { return nil, trash.ErrUnsupported }
		if _, err := Run(context.Background(), o); !errors.Is(err, ErrTrashAccess) {
			t.Fatalf("err = %v, want ErrTrashAccess", err)
		}
	})
	// Should-fix 7: a refusal on an item is that item's permission_denied;
	// only failing to open the Trash is trash_access.
	t.Run("fallback refused on items: permission_denied per item, run continues", func(t *testing.T) {
		f := newFx(t, threeSafe())
		p := f.scan("")
		o := f.opts(p)
		o.openTrash = errTrashEPERM(f.home)
		o.Finder = func() (trash.ExpectTrasher, error) { return deniedTrasher{}, nil }
		res, err := Run(context.Background(), o)
		if err != nil || len(res.Moved) != 0 || len(res.Skipped) != 3 || ExitCode(res) != ExitPartial {
			t.Fatalf("err %v result %+v", err, res)
		}
		for _, s := range res.Skipped {
			if s.Reason != plan.SkipPermissionDenied {
				t.Errorf("%+v", s)
			}
		}
		for _, it := range p.Items {
			if !exists(it.Path) {
				t.Errorf("%s moved", it.Path)
			}
		}
	})
	t.Run("other errors opening the Trash are not a fallback", func(t *testing.T) {
		f := newFx(t, threeSafe())
		o := f.opts(f.scan(""))
		o.openTrash = func() (*homeTrash, error) { return nil, errors.New("no such folder") }
		o.Finder = func() (trash.ExpectTrasher, error) { t.Fatal("fell back"); return nil, nil }
		if _, err := Run(context.Background(), o); err == nil || errors.Is(err, ErrTrashAccess) {
			t.Fatalf("err = %v", err)
		}
	})
}

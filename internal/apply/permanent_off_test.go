//go:build !kipple_permanent

package apply

import (
	"context"
	"errors"
	"testing"
)

// Decision for v0.1.0: --permanent does not ship. Run refuses it before
// touching anything, and the items stay where they are.
func TestPermanentUnavailableInThisRelease(t *testing.T) {
	f := newFx(t, threeSafe())
	p := f.scan("")
	o := f.opts(p)
	o.Permanent = true
	res, err := Run(context.Background(), o)
	if !errors.Is(err, ErrPermanentUnavailable) || PermanentAvailable || res.ReceiptID != "" {
		t.Fatalf("err %v result %+v", err, res)
	}
	for _, it := range p.Items {
		if !exists(it.Path) {
			t.Fatalf("%s moved", it.Path)
		}
	}
}

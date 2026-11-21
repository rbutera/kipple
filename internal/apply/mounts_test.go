//go:build unix

package apply

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/testutil"
)

func fileIdentity(t *testing.T, p string) policy.FileIdentity {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, ok := policy.Identity(fi)
	if !ok {
		t.Fatal("no identity")
	}
	return policy.FileIdentity{Dev: dev, Ino: ino}
}

// Spec scenario "Bind mount inside a cache folder" (safety-policy), with an
// injected mount oracle standing in for a same-device bind mount of
// Documents: the item holding it is contains_mount, never selected, and
// apply skips it even when named; nothing under it or Documents moves.
func TestBindMountInsideACacheFolder(t *testing.T) {
	f := newFx(t, testutil.Spec{Files: []testutil.File{
		{Path: "Library/Caches/foo/docs/letter.txt", Content: "dear", MTime: old},
		{Path: "Library/Caches/foo/blob", Content: "x", MTime: old},
		{Path: "Documents/letter.txt", Content: "dear"},
	}})
	f.env.Mounts = policy.StubMounts{Roots: map[policy.FileIdentity]bool{fileIdentity(t, f.abs("Library/Caches/foo/docs")): true}}
	before := testutil.Checksum(t, f.home)
	p := f.scan("")
	it := itemByRel(t, p, "Library/Caches/foo")
	if it.Selected || !slices.Contains(it.Reasons, plan.ReasonContainsMount) {
		t.Fatalf("item %+v", it)
	}
	o := f.opts(p)
	o.Only = []string{it.ID}
	res := f.run(o)
	if s, ok := skipFor(res, it.Path); !ok || s.Reason != plan.ReasonContainsMount || len(res.Moved) != 0 {
		t.Fatalf("%+v", res)
	}
	if testutil.Checksum(t, f.home) != before {
		// Only the Trash folders (created for the run on Linux) may differ.
		for _, e := range []string{"Library/Caches/foo/docs/letter.txt", "Library/Caches/foo/blob", "Documents/letter.txt"} {
			if !exists(f.abs(e)) {
				t.Fatalf("%s moved", e)
			}
		}
	}
}

// Spec scenario "Mount table unavailable": scan warns and marks every item
// contains_mount; apply returns ErrMountsUnavailable and nothing changes.
func TestMountTableUnavailable(t *testing.T) {
	f := newFx(t, threeSafe())
	f.env.Mounts = policy.StubMounts{Err: policy.ErrMountsUnavailable}
	before := testutil.Checksum(t, f.home)
	p := f.scan("")
	if len(p.Warnings) == 0 {
		t.Fatal("no warning")
	}
	for _, it := range p.Items {
		if it.Selected || !slices.Contains(it.Reasons, plan.ReasonContainsMount) {
			t.Errorf("item %+v", it)
		}
	}
	o := f.opts(p)
	var ids []string
	for _, it := range p.Items {
		ids = append(ids, it.ID)
	}
	o.Only = ids
	if _, err := Run(context.Background(), o); !errors.Is(err, ErrMountsUnavailable) {
		t.Fatalf("err = %v, want ErrMountsUnavailable", err)
	}
	if testutil.Checksum(t, f.home) != before {
		t.Fatal("the fixture changed")
	}
	if es, _ := f.store.List(); len(es) != 0 {
		t.Fatal("a receipt was written")
	}
}

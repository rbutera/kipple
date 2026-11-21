//go:build darwin

package apply

import (
	"os"

	"github.com/rbutera/kipple/internal/trash"
)

// macOS: the NSFileManager fallback (TCC) and a missing ~/.Trash, which
// kipple never creates.
var platformProbeCases = []probeCase{
	{"an unreadable Trash with the fallback uses NSFileManager", true, func(_ *fx, o *Options) {
		o.Finder = func() (trash.ExpectTrasher, error) { return nil, nil }
	}, TrashFallback, nil},
	{"a missing Trash is unavailable", false, func(f *fx, o *Options) {
		if err := os.Remove(f.abs(".Trash")); err != nil {
			f.t.Fatal(err)
		}
		o.Finder = func() (trash.ExpectTrasher, error) { return trash.NewFinderTrasher(nil) }
	}, TrashUnavailable, nil},
}

// symlinkedTrashIsTrashAccess: on macOS a symlinked ~/.Trash is an
// ordinary error (kipple refuses it; Full Disk Access would not help).
const symlinkedTrashIsTrashAccess = false

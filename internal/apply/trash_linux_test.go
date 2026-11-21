//go:build linux

package apply

import (
	"os"
)

// Linux: a missing freedesktop Trash is created by the first move, so
// ProbeTrash reports rename mode and creates nothing ("Dry run creates
// nothing"; the checksum check in TestProbeTrash proves it).
var platformProbeCases = []probeCase{
	{"a missing Trash would be created", false, func(f *fx, o *Options) {
		if err := os.RemoveAll(f.abs(".local")); err != nil {
			f.t.Fatal(err)
		}
	}, TrashRename, nil},
	{"a world-writable Trash is unavailable", false, func(f *fx, o *Options) {
		if err := os.Chmod(f.abs(".local/share/Trash/info"), 0o777); err != nil {
			f.t.Fatal(err)
		}
	}, TrashUnavailable, ErrTrashAccess},
}

// symlinkedTrashIsTrashAccess: on Linux every unusable part of the
// freedesktop Trash is trash_access ("Trash folder is a symlink").
const symlinkedTrashIsTrashAccess = true

package apply

import "github.com/rbutera/kipple/internal/trash"

// homeTrash is the home folder's Trash, opened through the policy session:
// where a run moves items, and where restore takes them back from. On
// macOS it is ~/.Trash (trash_darwin.go); on Linux the freedesktop.org home
// Trash ~/.local/share/Trash, whose items are in files/ and described by
// info/<name>.trashinfo (trash_linux.go).
type homeTrash struct {
	// dir is the folder the Trash entries are in.
	dir *trash.DirTrasher
	// mover moves an approved item into dir.
	mover trash.ExpectTrasher
	// name is the receipt's mover (plan.MoverRename, plan.MoverFreedesktop).
	name string
	// info removes a restored entry's .trashinfo; nil without info files.
	info infoRemover
	// close releases everything.
	close func() error
}

// infoRemover is trash.FreedesktopTrasher's RemoveInfo.
type infoRemover interface {
	RemoveInfo(name, original string) (bool, error)
}

// Close releases the Trash's descriptors.
func (h *homeTrash) Close() error {
	if h == nil || h.close == nil {
		return nil
	}
	return h.close()
}

// HomeTrashDir is the folder of home's Trash that holds the entries kipple
// moves there: ~/.Trash on macOS, ~/.local/share/Trash/files on Linux.
func HomeTrashDir(home string) string { return home + "/" + homeTrashRel }

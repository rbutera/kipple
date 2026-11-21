package output

import "runtime"

// Platform phrases: what the user's Trash is called and how they put
// things back without kipple, per operating system. Every user-facing
// string that names Finder, ~/.Trash or macOS goes through Phrases, so
// Linux users never read macOS instructions.
type Phrases struct {
	// TrashFolder is the Trash folder kipple moves items into.
	TrashFolder string
	// YourTrash names it in a sentence: "your Trash (~/.local/share/Trash,
	// visible in your file manager)".
	YourTrash string
	// OtherRestore is how the user's desktop puts an item back.
	OtherRestore string
	// SystemRefused starts the permission_denied sentence.
	SystemRefused string
}

// PhrasesFor returns the phrases for goos ("darwin", "linux"); anything
// else gets neutral wording.
func PhrasesFor(goos string) Phrases {
	switch goos {
	case "darwin":
		return Phrases{
			TrashFolder:   "~/.Trash",
			YourTrash:     "your Trash",
			OtherRestore:  "Finder's Put Back",
			SystemRefused: "macOS refused the move.",
		}
	case "linux":
		return Phrases{
			TrashFolder:   "~/.local/share/Trash",
			YourTrash:     "your Trash (~/.local/share/Trash, visible in your file manager)",
			OtherRestore:  "Restore in your file manager's Trash",
			SystemRefused: "The system refused the move.",
		}
	}
	return Phrases{TrashFolder: "the Trash folder", YourTrash: "your Trash", OtherRestore: "your desktop's restore", SystemRefused: "The system refused the move."}
}

// Here are the phrases for the platform kipple runs on.
var Here = PhrasesFor(runtime.GOOS)

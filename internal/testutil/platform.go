package testutil

import "runtime"

// TrashRel is the home-relative folder that apply moves items into on this
// platform: the macOS Trash, or the files folder of the freedesktop.org
// home Trash on Linux. Tests use it instead of spelling out ".Trash".
var TrashRel = trashRelFor(runtime.GOOS)

// TrashRoot is the home-relative Trash folder itself: ".Trash" on macOS,
// ".local/share/Trash" (holding files and info) on Linux.
var TrashRoot = trashRootFor(runtime.GOOS)

func trashRelFor(goos string) string {
	if goos == "linux" {
		return ".local/share/Trash/files"
	}
	return ".Trash"
}

func trashRootFor(goos string) string {
	if goos == "linux" {
		return ".local/share/Trash"
	}
	return ".Trash"
}

// TrashDirs are the fixture folders that make up this platform's Trash,
// mode 0700 as the system creates them: .Trash on macOS; on Linux
// .local/share/Trash with files and info.
func TrashDirs() []Dir {
	if runtime.GOOS == "linux" {
		return []Dir{
			{Path: ".local/share/Trash", Mode: 0o700},
			{Path: ".local/share/Trash/files", Mode: 0o700},
			{Path: ".local/share/Trash/info", Mode: 0o700},
		}
	}
	return []Dir{{Path: ".Trash", Mode: 0o700}}
}

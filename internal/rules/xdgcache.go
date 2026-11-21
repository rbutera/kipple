package rules

// XDGCacheExclude is one child of ~/.cache that the xdg.cache rule never
// offers, with the reason in one sentence. docs/safety.md lists them, and a
// test checks the two agree (linux-port design L8).
type XDGCacheExclude struct {
	Pattern string
	Why     string
}

// ownedElsewhere is the reason for children another rule owns.
const ownedElsewhere = "Another rule owns it, with its own tier and conditions."

// xdgCacheExcludes are the children of ~/.cache that are not plain caches
// or belong to another rule. Every child the wave 2 red team finds that is
// not a cache is added here with its reason and a test.
var xdgCacheExcludes = []XDGCacheExclude{
	{"pip", ownedElsewhere},
	{"uv", ownedElsewhere},
	{"go-build", ownedElsewhere},
	{"yarn", ownedElsewhere},
	{"pnpm", ownedElsewhere},
	{"pypoetry", ownedElsewhere},
	{"huggingface", ownedElsewhere},
	{"JetBrains", ownedElsewhere},
	{"Homebrew", ownedElsewhere},
	{"keyring*", "Older GNOME keyring versions keep their control directory here; it is session state for the password store, not a cache."},
	{"gnome-keyring", "Older GNOME keyring versions keep their control directory here; it is session state for the password store, not a cache."},
	{"sessions", "Xfce and LXDE save the open-application session here; removing it loses the saved session."},
	{"evolution", "Evolution's offline mail store can hold messages and drafts not yet synchronised to the server."},
	{"rclone", "rclone's mount write-back cache can hold file changes not yet uploaded."},
	{"zellij", "zellij keeps session-resurrection layouts here, which are user state."},
	{"keepassxc", "KeePassXC keeps its recent-database and key-file list here, which belongs to a password manager."},
	{"ibus", "Some input methods (ibus-libpinyin, ibus-typing-booster) keep the phrases they learned from your typing here."},
	{"torch", ownedElsewhere},
	{"whisper", ownedElsewhere},
	{"llama.cpp", ownedElsewhere},
	{"lm-studio", ownedElsewhere},
	{"obexd", "BlueZ saves files received over Bluetooth here, and they may be the only copy."},
	{"zsh", "Common zsh setups (oh-my-zsh, zinit, prezto) keep your shell history and completion state here."},
	{"onedriver", "onedriver's OneDrive file cache can hold changes not yet uploaded."},
	{"yay", "AUR helpers keep their PKGBUILD clones here, and you may have edited them."},
	{"paru", "AUR helpers keep their PKGBUILD clones here, and you may have edited them."},
	{"pikaur", "AUR helpers keep their PKGBUILD clones here, and you may have edited them."},
	{"aurutils", "AUR helpers keep their PKGBUILD clones here, and you may have edited them."},
	{"vim", "Some Vim setups keep undo history and swap files here, which hold edits not yet saved."},
	{"SpaceVim", "SpaceVim keeps undo history and swap files here, which hold edits not yet saved."},
	{"vdirsyncer", "vdirsyncer keeps its sync state here; losing it makes the next sync treat every contact and event as changed."},
	{"cliphist", "cliphist keeps your clipboard history here."},
	{"kioexec", "KDE keeps local copies of remote files you are editing here until they are uploaded."},
}

// XDGCacheExcludes returns the xdg.cache exclude table.
func XDGCacheExcludes() []XDGCacheExclude {
	return append([]XDGCacheExclude(nil), xdgCacheExcludes...)
}

// XDGCacheExcludePatterns returns the patterns of the exclude table.
func XDGCacheExcludePatterns() []string {
	out := make([]string, len(xdgCacheExcludes))
	for i, e := range xdgCacheExcludes {
		out[i] = e.Pattern
	}
	return out
}

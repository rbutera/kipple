// Command mkfixture writes a small fixture home folder for
// scripts/agent-flow-check.sh: a few caches and logs, a Go module cache, an
// Xcode archive, an old installer, an Ollama model and an empty .Trash, all
// with modification times 200 days ago. On Linux it writes the Linux
// catalogue's twin of the same layout (~/.cache folders, a .deb installer,
// no Xcode, and no Trash, which the first clean --apply creates). It writes only inside the folder it
// is given, which must not exist yet or be empty.
//
// With -gui it writes a larger, realistic home for scripts/gui-screenshots.sh
// (about 70 MB of app, package-manager and Xcode caches, project folders,
// and a git repository with uncommitted work), so every kind of row in the
// GUI appears with believable sizes.
//
// Usage: go run ./tools/mkfixture [-gui] <new-folder>
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type fixtureFile struct {
	path string
	size int
}

// linuxFiles is the Linux layout: two safe items (a ~/.cache folder and a
// pip cache entry), a review item, a manual item and an info item.
var linuxFiles = []fixtureFile{
	{".cache/com.example.app/blob", 64 << 10},
	{".cache/pip/http/ab/blob", 4 << 10},
	{"go/pkg/mod/github.com/x/y.go", 4 << 10},
	{"Downloads/old.deb", 16 << 10},
	{".ollama/models/blobs/sha256-aa", 16 << 10},
}

var files = []fixtureFile{
	{"Library/Caches/com.example.app/blob", 64 << 10},
	{"Library/Logs/app/log.txt", 4 << 10},
	{"Library/Developer/Xcode/DerivedData/MyApp-bxkq/Build/out.o", 32 << 10},
	{"go/pkg/mod/github.com/x/y.go", 4 << 10},
	{"Library/Developer/Xcode/Archives/2026-03-01/MyApp.xcarchive/Info.plist", 8 << 10},
	{"Downloads/Old.dmg", 16 << 10},
	{".ollama/models/blobs/sha256-aa", 16 << 10},
}

func main() {
	args := os.Args[1:]
	gui := len(args) == 2 && args[0] == "-gui"
	if gui {
		args = args[1:]
	}
	if len(args) != 1 || !filepath.IsAbs(args[0]) {
		fmt.Fprintln(os.Stderr, "usage: mkfixture [-gui] <absolute path of a new or empty folder>")
		os.Exit(2)
	}
	b := build
	if gui {
		b = buildGUI
	}
	if err := b(filepath.Clean(args[0])); err != nil {
		fmt.Fprintln(os.Stderr, "mkfixture:", err)
		os.Exit(1)
	}
}

func build(root string) error {
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", root)
	}
	layout := files
	if runtime.GOOS == "linux" {
		layout = linuxFiles
	} else if err := os.MkdirAll(filepath.Join(root, ".Trash"), 0o700); err != nil {
		return err
	}
	old := time.Now().Add(-200 * 24 * time.Hour)
	var dirs []string
	for _, f := range layout {
		p := filepath.Join(root, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(p, make([]byte, f.size), 0o600); err != nil {
			return err
		}
		if err := os.Chtimes(p, old, old); err != nil {
			return err
		}
		for d := filepath.Dir(p); d != root; d = filepath.Dir(d) {
			dirs = append(dirs, d)
		}
	}
	// Folders last, so writing the files does not refresh their times.
	for _, d := range dirs {
		if err := os.Chtimes(d, old, old); err != nil {
			return err
		}
	}
	return nil
}

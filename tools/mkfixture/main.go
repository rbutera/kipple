// Command mkfixture writes a small fixture home folder for
// scripts/agent-flow-check.sh: a few caches and logs, a Go module cache, an
// Xcode archive, an old installer, an Ollama model and an empty .Trash, all
// with modification times 200 days ago. It writes only inside the folder it
// is given, which must not exist yet or be empty.
//
// Usage: go run ./tools/mkfixture <new-folder>
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var files = []struct {
	path string
	size int
}{
	{"Library/Caches/com.example.app/blob", 64 << 10},
	{"Library/Logs/app/log.txt", 4 << 10},
	{"Library/Developer/Xcode/DerivedData/MyApp-bxkq/Build/out.o", 32 << 10},
	{"go/pkg/mod/github.com/x/y.go", 4 << 10},
	{"Library/Developer/Xcode/Archives/2026-03-01/MyApp.xcarchive/Info.plist", 8 << 10},
	{"Downloads/Old.dmg", 16 << 10},
	{".ollama/models/blobs/sha256-aa", 16 << 10},
}

func main() {
	if len(os.Args) != 2 || !filepath.IsAbs(os.Args[1]) {
		fmt.Fprintln(os.Stderr, "usage: mkfixture <absolute path of a new or empty folder>")
		os.Exit(2)
	}
	if err := build(filepath.Clean(os.Args[1])); err != nil {
		fmt.Fprintln(os.Stderr, "mkfixture:", err)
		os.Exit(1)
	}
}

func build(root string) error {
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", root)
	}
	if err := os.MkdirAll(filepath.Join(root, ".Trash"), 0o700); err != nil {
		return err
	}
	old := time.Now().Add(-200 * 24 * time.Hour)
	var dirs []string
	for _, f := range files {
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

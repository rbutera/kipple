package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const mb = 1 << 20

// guiFiles is the -gui fixture: what a developer's home might hold, scaled
// down. Sizes are bytes of non-zero filler, so they are really allocated.
var guiFiles = []struct {
	path string
	size int
}{
	{"Library/Caches/com.spotify.client/Data/0a/blob", 9 * mb},
	{"Library/Caches/Google/Chrome/Default/Cache/data_1", 6 * mb},
	{"Library/Caches/com.tinyspeck.slackmacgap/Cache.db", 3 * mb},
	{"Library/Caches/com.microsoft.VSCode.ShipIt/update.zip", 2 * mb},
	{".npm/_cacache/content-v2/sha512/ab/cd/blob", 8 * mb},
	{"Library/Developer/Xcode/DerivedData/Kipple-hdzvbqsxgmbn/Build/Products/app.o", 7 * mb},
	{"Library/Developer/Xcode/DerivedData/Weather-agqkwmlqdbez/Build/Products/app.o", 3 * mb},
	{"Library/Logs/DiagnosticReports/app.ips", 256 << 10},
	{"Library/Caches/Homebrew/downloads/node--22.bottle.tar.gz", 4 * mb},
	{"go/pkg/mod/github.com/charmbracelet/lipgloss@v1.1.0/style.go", 5 * mb},
	{"dev/old-api/target/debug/old-api", 4 * mb},
	{"dev/old-api/Cargo.toml", 1 << 10},
	{"dev/scratch/node_modules/react/index.js", 3 * mb},
	{"dev/scratch/package.json", 1 << 10},
	{"Downloads/Docker.dmg", 3 * mb},
	{".cache/huggingface/hub/models--mistralai--Mistral-7B/blobs/aa", 7 * mb},
	{".ollama/models/blobs/sha256-8eeb52dfb3bb", 5 * mb},
	{"Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw", 6 * mb},
}

// buildGUI writes the -gui fixture into root.
func buildGUI(root string) error {
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", root)
	}
	if err := os.MkdirAll(filepath.Join(root, ".Trash"), 0o700); err != nil {
		return err
	}
	for _, f := range guiFiles {
		if err := filler(filepath.Join(root, filepath.FromSlash(f.path)), f.size); err != nil {
			return err
		}
	}
	// A project with uncommitted work: its node_modules is listed at risk
	// and stays unselectable.
	repo := filepath.Join(root, "dev", "kipple-demo")
	for p, n := range map[string]int{"package.json": 1 << 10, "src/index.js": 4 << 10, "node_modules/left-pad/index.js": 2 * mb} {
		if err := filler(filepath.Join(repo, filepath.FromSlash(p)), n); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n"), 0o600); err != nil {
		return err
	}
	old := time.Now().Add(-200 * 24 * time.Hour)
	if err := git(repo, old, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	if err := git(repo, old, "add", "-A"); err != nil {
		return err
	}
	if err := git(repo, old, "commit", "-q", "-m", "first"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "index.js"), []byte("// work in progress\n"), 0o600); err != nil {
		return err
	}
	// Everything looks untouched for 200 days; folders last.
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		return os.Chtimes(p, old, old) //nolint:gosec // G122: a fresh folder this tool just wrote; nothing else writes there.
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chtimes(dirs[i], old, old); err != nil {
			return err
		}
	}
	return nil
}

// filler writes n bytes of non-zero filler, so the file is not sparse.
func filler(p string, n int) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return os.WriteFile(p, b, 0o600)
}

// git runs git in dir with no user or system configuration.
func git(dir string, at time.Time, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G702: fixed arguments from this file, no shell.
	stamp := at.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=kipple", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_AUTHOR_DATE="+stamp,
		"GIT_COMMITTER_NAME=kipple", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "GIT_COMMITTER_DATE="+stamp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %v: %s", args, err, out)
	}
	return nil
}

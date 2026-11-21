package config

import (
	"os"
	"path/filepath"
	"slices"
		"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

// specs/safety-policy "XDG user directories are protected on Linux".
func TestParseUserDirs(t *testing.T) {
	const home = "/home/me"
	for _, tc := range []struct {
		name, data string
		want       []string
		warnings   int
	}{
		{"Localised Documents folder", `XDG_DOCUMENTS_DIR="$HOME/Dokumente"` + "\n" + `XDG_PICTURES_DIR="$HOME/Bilder"`, []string{"/home/me/Bilder", "/home/me/Dokumente"}, 0},
		{"Download dir stays cleanable", `XDG_DOWNLOAD_DIR="$HOME/Downloads"`, nil, 0},
		{"User dir set to home", `XDG_DESKTOP_DIR="$HOME"` + "\n" + `XDG_MUSIC_DIR="$HOME/Musik"`, []string{"/home/me/Musik"}, 1},
		{"Absolute path under home", `XDG_VIDEOS_DIR="/home/me/Filme"`, []string{"/home/me/Filme"}, 0},
		{"Outside home ignored", `XDG_VIDEOS_DIR="/srv/videos"`, nil, 1},
		{"Escaping home with dot-dot ignored", `XDG_VIDEOS_DIR="$HOME/../other"`, nil, 1},
		{"Comments and blank lines", "# written by xdg-user-dirs-update\n\nXDG_TEMPLATES_DIR=\"$HOME/Vorlagen\"\n", []string{"/home/me/Vorlagen"}, 0},
		{"Malformed lines warn, good ones count", "garbage\nXDG_Y_DIR=\"$HOME/a\\\"b\"\nXDG_Z_DIR=\"$HOME/two words\"\nXDG_PUBLICSHARE_DIR=\"$HOME/Öffentlich\"", []string{"/home/me/Öffentlich"}, 3},
		{"Shell forms the desktop accepts", "XDG_MUSIC_DIR=$HOME/Musik\nexport XDG_VIDEOS_DIR=\"${HOME}/Filme\"\nXDG_TEMPLATES_DIR=${HOME}/Vorlagen", []string{"/home/me/Filme", "/home/me/Musik", "/home/me/Vorlagen"}, 0},
		{"Relative value", `XDG_DOCUMENTS_DIR="Documents"`, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := ParseUserDirs(home, []byte(tc.data))
			if got := u.Paths(); !slices.Equal(got, tc.want) {
				t.Fatalf("protect %q, want %q", got, tc.want)
			}
			if len(u.Warnings) != tc.warnings {
				t.Fatalf("warnings %q, want %d", u.Warnings, tc.warnings)
			}
		})
	}
}

// The file is read without following a symlink; a symlinked or unreadable
// file is a warning, and a missing one is nothing.
func TestLoadUserDirs(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{
		Files:    []testutil.File{{Path: "elsewhere/dirs", Content: `XDG_DOCUMENTS_DIR="$HOME/Docs"`}},
		Symlinks: []testutil.Symlink{{Path: ".config/user-dirs.dirs", Target: "~/elsewhere/dirs"}},
	})
	open := func(rel string) (*os.File, error) { return openConfig(filepath.Join(home, rel)) }
	u := LoadUserDirs(home, open)
	if len(u.Warnings) != 0 || u.Protect["XDG_DOCUMENTS_DIR"] != filepath.Join(home, "Docs") {
		t.Fatalf("a symlinked user-dirs.dirs (stow, yadm) must still protect its folders: %+v", u)
	}
	fifo := testutil.NewHome(t, testutil.Spec{Symlinks: []testutil.Symlink{{Path: ".config/user-dirs.dirs", Target: "~/elsewhere"}}, Dirs: []testutil.Dir{{Path: "elsewhere"}}})
	if u := LoadUserDirs(fifo, func(rel string) (*os.File, error) { return openConfig(filepath.Join(fifo, rel)) }); len(u.Protect) != 0 || len(u.Warnings) != 1 {
		t.Fatalf("a link to something that is not a regular file: %+v", u)
	}
	empty := testutil.NewHome(t, testutil.Spec{})
	if u := LoadUserDirs(empty, func(rel string) (*os.File, error) { return openConfig(filepath.Join(empty, rel)) }); len(u.Protect) != 0 || len(u.Warnings) != 0 {
		t.Fatalf("missing file: %+v", u)
	}
}

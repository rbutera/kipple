//go:build gui

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rbutera/kipple/internal/testutil"
)

// Fyne writes outside the app: go-text's font scanner caches an index in
// os.UserCacheDir() ($HOME/Library/Caches/font_index_v6.cache), and a Fyne
// app keeps preferences under $HOME/Library/Preferences/fyne. Tests must
// never write in the real home folder (AGENTS.md rule 1), so TestMain
// points HOME and the XDG folders at a temp folder before any Fyne call,
// and fails the run if either real file changed while the tests ran.

// realHomeFiles are the files Fyne would write in the real home.
func realHomeFiles() []string {
	return testutil.RealHomePaths(
		"Library/Caches/font_index_v6.cache",
		"Library/Preferences/fyne/com.rbutera.kipple",
		"Library/Caches/fyne/com.rbutera.kipple")
}

// stamps reads (stat only) each file's modification time; zero when absent.
func stamps(paths []string) map[string]time.Time {
	out := map[string]time.Time{}
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			out[p] = fi.ModTime()
		}
	}
	return out
}

// redirectHome points HOME and the XDG folders at a new temp folder and
// returns a check to run after the tests: it removes the folder and turns
// the exit code into 1 if Fyne's files in the real home changed.
func redirectHome() func(code int) int {
	watched := realHomeFiles()
	if len(watched) == 0 {
		fmt.Fprintln(os.Stderr, "kipple-gui tests: the real home folder cannot be determined, so nothing runs")
		os.Exit(1)
	}
	before := stamps(watched)
	tmp, err := os.MkdirTemp("", "kipple-gui-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "kipple-gui tests: cannot make a temp HOME:", err)
		os.Exit(1)
	}
	for k, v := range map[string]string{
		"HOME": tmp, "XDG_CACHE_HOME": filepath.Join(tmp, ".cache"),
		"XDG_CONFIG_HOME": filepath.Join(tmp, ".config"), "XDG_DATA_HOME": filepath.Join(tmp, ".local", "share"),
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "kipple-gui tests:", err)
			os.Exit(1)
		}
	}
	return func(code int) int {
		_ = os.RemoveAll(tmp)
		for _, p := range changed(watched, before, stamps(watched)) {
			fmt.Fprintf(os.Stderr, "kipple-gui tests: %s changed in the real home folder during the run\n", p)
			code = 1
		}
		return code
	}
}

// changed lists the paths whose modification time differs (or that
// appeared or vanished) between two stamps.
func changed(paths []string, before, after map[string]time.Time) []string {
	var out []string
	for _, p := range paths {
		if !before[p].Equal(after[p]) {
			out = append(out, p)
		}
	}
	return out
}

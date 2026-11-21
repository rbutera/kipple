package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/tui"
)

// readmeFixture is the fixture the README's usage examples come from.
func readmeFixture(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.spotify.client/Data/blob", Size: 3 << 20, MTime: old},
			{Path: "Library/Caches/com.example.editor/cache.db", Size: 600 << 10, MTime: old},
			{Path: "Library/Logs/DiagnosticReports/app.ips", Size: 90 << 10, MTime: old},
			{Path: "Library/Developer/Xcode/DerivedData/MyApp-bxkq/Build/out.o", Size: 2 << 20, MTime: old},
			{Path: ".npm/_cacache/content-v2/sha512/aa", Size: 1 << 20, MTime: old},
			{Path: "go/pkg/mod/github.com/spf13/cobra@v1.10.2/cobra.go", Size: 400 << 10, MTime: old},
			{Path: "Library/Developer/Xcode/Archives/2026-03-01/MyApp.xcarchive/Info.plist", Size: 700 << 10, MTime: old},
			{Path: "Downloads/Installer.dmg", Size: 1 << 20, MTime: old},
			{Path: ".ollama/models/blobs/sha256-aa", Size: 2 << 20, MTime: old},
		},
		Dirs: testutil.TrashDirs(),
	})
}

// TestCaptureReadme prints the README's usage examples, run on a fixture
// with the fixture's paths shown as /Users/you. It runs only with
// KIPPLE_README_CAPTURE set.
func TestCaptureReadme(t *testing.T) {
	if os.Getenv("KIPPLE_README_CAPTURE") == "" {
		t.Skip("set KIPPLE_README_CAPTURE to print the README examples")
	}
	home := readmeFixture(t)
	state := os.Getenv("KIPPLE_STATE_DIR")
	clean := func(s string) string {
		s = strings.ReplaceAll(s, state, "/Users/you/Library/Application Support/kipple")
		return strings.ReplaceAll(s, home, "/Users/you")
	}
	show := func(args ...string) string {
		code, out, errOut := cli(t, args...)
		fmt.Printf("$ kipple %s    # exit %d\n%s%s\n", strings.Join(args, " "), code, clean(out), clean(errOut))
		return out
	}
	var p plan.Plan
	out := func() string { _, o, _ := cli(t, "scan", "--json"); return o }()
	exactlyOneJSON(t, out, &p)
	idOf := func(suffix string) string {
		for _, it := range p.Items {
			if strings.HasSuffix(it.Path, suffix) {
				return it.ID
			}
		}
		t.Fatalf("no %s", suffix)
		return ""
	}
	show("scan")
	show("explain", "xcode.deriveddata")
	show("explain", idOf("/DerivedData/MyApp-bxkq"))
	show("clean")
	show("clean", "--apply", "--only", idOf("/DerivedData/MyApp-bxkq")+","+idOf("/Xcode/Archives/2026-03-01"))
	show("history")
	var h plan.History
	exactlyOneJSON(t, func() string { _, o, _ := cli(t, "history", "--json"); return o }(), &h)
	show("restore", h.Receipts[0].ID)
	show("restore", h.Receipts[0].ID, "--apply")

	rt := testEnv(t)
	rt.isTTY = func(io.Writer) bool { return true }
	var view string
	var cfg tui.Config
	saved := tuiSize
	tuiSize = [2]int{110, 22}
	t.Cleanup(func() { tuiSize = saved })
	rt.tui = headlessTUI(&view, &cfg, "q")
	cliWith(t, rt, "tui")
	fmt.Printf("$ kipple tui\n%s\n", clean(view))
}

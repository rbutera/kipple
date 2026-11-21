package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

// linuxFixture is cleanFixture's Linux twin: two selected safe items (an
// old ~/.cache folder and a pip cache entry), a review item, a manual item
// and an info item, and a freedesktop Trash.
func linuxFixture(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: ".cache/com.example.app/blob", Size: 64 << 10, MTime: old},
			{Path: ".cache/pip/http/blob", Size: 1024, MTime: old},
			{Path: "go/pkg/mod/github.com/x/y.go", Size: 1024, MTime: old},
			{Path: "Downloads/Old.iso", Size: 4096, MTime: old},
			{Path: "snap/firefox/common/x", Size: 4096, MTime: old},
		},
		Dirs: append(testutil.TrashDirs(), testutil.Dir{Path: ".cache/com.example.app", MTime: old}, testutil.Dir{Path: ".cache/pip/http", MTime: old}),
	})
}

// linuxCLI runs kipple as on Linux: its catalogue, state folder and user
// folders (the Trash follows the build, which is the host's).
func linuxCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	rt := testEnv(t)
	rt.goos = rules.PlatformLinux
	return cliWith(t, rt, args...)
}

func itemAt(p plan.Plan, rel string) (plan.Item, bool) {
	for _, it := range p.Items {
		if it.Path == p.Home+"/"+rel {
			return it, true
		}
	}
	return plan.Item{}, false
}

// Spec scenarios of the Linux catalogue, through the CLI on any host.
func TestLinuxCatalogueScenarios(t *testing.T) {
	t.Run("Linux is supported", func(t *testing.T) {
		linuxFixture(t)
		code, out, _ := linuxCLI(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if code != exitOK || len(p.Items) == 0 {
			t.Fatalf("exit %d items %d", code, len(p.Items))
		}
		for _, it := range p.Items {
			if _, ok := rules.LookupFor(rules.PlatformLinux, it.Rule); !ok {
				t.Errorf("item from rule %s, not in the Linux catalogue", it.Rule)
			}
		}
	})
	t.Run("Shared rule ID", func(t *testing.T) {
		fixture(t)
		_, out, _ := linuxCLI(t, "rules", "--json")
		var doc plan.RulesDocument
		exactlyOneJSON(t, out, &doc)
		found := false
		for _, r := range doc.Rules {
			if r.ID == "pip.cache" {
				found = r.Roots[0] == ".cache/pip"
			}
			for _, root := range r.Roots {
				if strings.HasPrefix(root, "Library/") {
					t.Errorf("%s lists %s on Linux", r.ID, root)
				}
			}
		}
		if !found {
			t.Fatal("pip.cache with root .cache/pip not listed")
		}
	})
	t.Run("macOS-only rule on Linux", func(t *testing.T) {
		fixture(t)
		code, _, errOut := linuxCLI(t, "explain", "xcode.deriveddata")
		if code != exitUsage || !strings.Contains(errOut, "macOS only") {
			t.Fatalf("exit %d stderr %q", code, errOut)
		}
	})
	t.Run("Old cache folder pre-selected", func(t *testing.T) {
		linuxFixture(t)
		_, out, _ := linuxCLI(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		it, ok := itemAt(p, ".cache/com.example.app")
		if !ok || it.Rule != "xdg.cache" || !it.Selected {
			t.Fatalf("item %+v %v", it, ok)
		}
	})
	t.Run("Recent cache folder held back", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{
			Files: []testutil.File{{Path: ".cache/some-app/blob", Content: "x", MTime: time.Now().Add(-48 * time.Hour)}},
			Dirs:  []testutil.Dir{{Path: ".cache/some-app", MTime: time.Now().Add(-48 * time.Hour)}},
		})
		_, out, _ := linuxCLI(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		it, ok := itemAt(p, ".cache/some-app")
		if !ok || it.Selected || !strings.Contains(strings.Join(it.Reasons, ","), plan.ReasonTooRecent) {
			t.Fatalf("%s: item %+v %v", home, it, ok)
		}
	})
	t.Run("Excluded folder never offered", func(t *testing.T) {
		spec := testutil.Spec{}
		for _, e := range rules.XDGCacheExcludes() {
			name := strings.ReplaceAll(e.Pattern, "*", "-x")
			spec.Files = append(spec.Files, testutil.File{Path: ".cache/" + name + "/blob", Content: "x", MTime: old})
			spec.Dirs = append(spec.Dirs, testutil.Dir{Path: ".cache/" + name, MTime: old})
		}
		spec.Files = append(spec.Files, testutil.File{Path: ".cache/pip/http/blob", Content: "x", MTime: old})
		linuxHome := testutil.NewHome(t, spec)
		_, out, _ := linuxCLI(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		for _, it := range p.Items {
			if it.Rule == "xdg.cache" {
				t.Errorf("xdg.cache offers %s", strings.TrimPrefix(it.Path, linuxHome+"/"))
			}
		}
		if it, ok := itemAt(p, ".cache/pip/http"); !ok || it.Rule != "pip.cache" {
			t.Fatalf("pip's child not under pip.cache: %+v %v", it, ok)
		}
	})
	t.Run("Model caches are manual, like the Hugging Face cache", func(t *testing.T) {
		testutil.NewHome(t, testutil.Spec{
			Files: []testutil.File{
				{Path: ".cache/torch/hub/checkpoints/resnet.pth", Content: "w", MTime: old},
				{Path: ".cache/whisper/large-v3.pt", Content: "w", MTime: old},
				{Path: ".cache/lm-studio/models/qwen/q.gguf", Content: "w", MTime: old},
			},
			Dirs: []testutil.Dir{{Path: ".cache/torch/hub", MTime: old}, {Path: ".cache/lm-studio/models/qwen", MTime: old}},
		})
		_, out, _ := linuxCLI(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		for _, rel := range []string{".cache/torch/hub", ".cache/whisper/large-v3.pt", ".cache/lm-studio/models/qwen"} {
			it, ok := itemAt(p, rel)
			if !ok || it.Rule != "models.downloaded" || it.Tier != string(rules.TierManual) || it.Selected {
				t.Errorf("%s: %+v %v", rel, it, ok)
			}
		}
		for _, it := range p.Items {
			if it.Rule == "xdg.cache" {
				t.Errorf("xdg.cache offers %s", it.Path)
			}
		}
	})
	t.Run("Snap data is info only", func(t *testing.T) {
		home := linuxFixture(t)
		_, out, _ := linuxCLI(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		it, ok := itemAt(p, "snap/firefox")
		if !ok || it.Rule != "snap.data" || it.Tier != string(rules.TierInfo) {
			t.Fatalf("item %+v %v", it, ok)
		}
		before := testutil.Checksum(t, home)
		code, out, _ := linuxCLI(t, "clean", "--only", it.ID, "--apply", "--json")
		var res plan.Result
		exactlyOneJSON(t, out, &res)
		if code != exitPartial || len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipInfoTier {
			t.Fatalf("exit %d %+v", code, res)
		}
		if _, err := os.Stat(filepath.Join(home, "snap/firefox/common/x")); err != nil || testutil.Checksum(t, home) != before {
			t.Fatal("something moved")
		}
	})
}

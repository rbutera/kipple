package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

// Spec scenario "Unsupported platform refused" (platform-support), with
// the platform injected: every command but version and help exits 1 with
// unsupported_platform and changes nothing.
func TestUnsupportedPlatformRefused(t *testing.T) {
	home := cleanFixture(t)
	before := testutil.Checksum(t, home)
	rt := testEnv(t)
	rt.goos = "freebsd"
	for _, args := range [][]string{{"clean", "--apply", "--json"}, {"scan", "--json"}, {"rules", "--json"}, {"restore", "20990101T000000Z-aaaaaa", "--json"}} {
		code, out, _ := cliWith(t, rt, args...)
		var env errorEnvelope
		exactlyOneJSON(t, out, &env)
		if code != exitError || env.Error.Code != codeUnsupportedPlatform {
			t.Fatalf("%v: exit %d %+v", args, code, env)
		}
	}
	if code, _, _ := cliWith(t, rt, "version", "--json"); code != exitOK {
		t.Fatalf("version: exit %d", code)
	}
	if testutil.Checksum(t, home) != before {
		t.Fatal("the fixture changed")
	}
}

// Spec scenarios "Version JSON" and "Platform in version"
// (cli-agent-interface): os and arch are the binary's, in Go's names, and
// schema_version stays 1.
func TestVersionPlatform(t *testing.T) {
	code, out, _ := cli(t, "version", "--json")
	var doc map[string]any
	exactlyOneJSON(t, out, &doc)
	if code != exitOK || doc["version"] == nil || doc["commit"] == nil || doc["schema_version"] != float64(1) {
		t.Fatalf("exit %d %v", code, doc)
	}
	if doc["os"] != runtime.GOOS || doc["arch"] != runtime.GOARCH {
		t.Fatalf("os %v arch %v, want %s/%s", doc["os"], doc["arch"], runtime.GOOS, runtime.GOARCH)
	}
	_, text, _ := cli(t, "version")
	if !strings.Contains(text, "Platform: "+runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("human version lacks the platform:\n%s", text)
	}
}

// realHomeEnv runs kipple on Linux for a stand-in real home (no
// KIPPLE_HOME), with the given environment variables.
func realHomeEnv(t *testing.T, home string, env map[string]string) runtimeEnv {
	t.Helper()
	t.Setenv("KIPPLE_HOME", "")
	rt := testEnv(t)
	rt.goos = rules.PlatformLinux
	rt.userHome = func() (string, error) { return home, nil }
	rt.realHomes = func() ([]string, error) { return []string{home}, nil }
	rt.getenv = func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
	return rt
}

// Spec requirement "XDG environment variables are not followed".
func TestXDGVariablesNotFollowed(t *testing.T) {
	t.Run("XDG_CACHE_HOME outside home", func(t *testing.T) {
		home := linuxFixture(t)
		outside := filepath.Join(filepath.Dir(home), "rai-cache")
		if err := os.MkdirAll(filepath.Join(outside, "big"), 0o755); err != nil {
			t.Fatal(err)
		}
		rt := realHomeEnv(t, home, map[string]string{"XDG_CACHE_HOME": outside})
		_, out, _ := cliWith(t, rt, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		for _, it := range p.Items {
			if strings.HasPrefix(it.Path, outside) {
				t.Fatalf("scanned outside home: %s", it.Path)
			}
		}
		if !slices.ContainsFunc(p.Warnings, func(w string) bool {
			return strings.Contains(w, "XDG_CACHE_HOME") && strings.Contains(w, "never looks outside")
		}) {
			t.Fatalf("warnings %q", p.Warnings)
		}
	})
	t.Run("XDG_CACHE_HOME elsewhere under home", func(t *testing.T) {
		home := linuxFixture(t)
		rt := realHomeEnv(t, home, map[string]string{"XDG_CACHE_HOME": home + "/.xdg-cache", "XDG_DATA_HOME": home + "/.local/share"})
		_, out, _ := cliWith(t, rt, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "XDG_CACHE_HOME") && strings.Contains(w, "~/.cache") }) {
			t.Fatalf("warnings %q", p.Warnings)
		}
		if slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "XDG_DATA_HOME") }) {
			t.Fatalf("a variable at its default location warned: %q", p.Warnings)
		}
		if _, ok := itemAt(p, ".cache/com.example.app"); !ok {
			t.Fatal("~/.cache was not scanned")
		}
	})
	t.Run("The default spelled through a symlinked home is not warned about", func(t *testing.T) {
		home := linuxFixture(t)
		link := filepath.Join(filepath.Dir(home), "homelink")
		if err := os.Symlink(home, link); err != nil {
			t.Fatal(err)
		}
		rt := realHomeEnv(t, link, map[string]string{"XDG_CACHE_HOME": link + "/.cache"})
		_, out, _ := cliWith(t, rt, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "XDG_CACHE_HOME") }) {
			t.Fatalf("warnings %q", p.Warnings)
		}
	})
	t.Run("Fixture ignores XDG variables", func(t *testing.T) {
		home := linuxFixture(t)
		rt := testEnv(t)
		rt.goos = rules.PlatformLinux
		real := filepath.Join(filepath.Dir(home), "real", ".local", "share")
		rt.getenv = func(k string) string {
			if k == "XDG_DATA_HOME" {
				return real
			}
			return os.Getenv(k)
		}
		code, out, errOut := cliWith(t, rt, "clean", "--apply", "--json")
		var res plan.Result
		exactlyOneJSON(t, out, &res)
		if code != exitOK || len(res.Warnings) != 0 || strings.Contains(errOut, "XDG") {
			t.Fatalf("exit %d warnings %q stderr %q", code, res.Warnings, errOut)
		}
		for _, m := range res.Moved {
			if !strings.HasPrefix(m.TrashPath, home+"/"+testutil.TrashRel+"/") {
				t.Fatalf("%s went to %s", m.Path, m.TrashPath)
			}
		}
	})
}

// Spec requirement "Home resolved once above home".
func TestHomeResolvedOnceAboveHome(t *testing.T) {
	t.Run("Symlinked /home", func(t *testing.T) {
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		real := filepath.Join(base, "var", "home", "me")
		if err := os.MkdirAll(filepath.Join(real, ".cache", "pip", "http"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("var/home", filepath.Join(base, "home")); err != nil {
			t.Fatal(err)
		}
		rt := realHomeEnv(t, filepath.Join(base, "home", "me"), nil)
		_, out, _ := cliWith(t, rt, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		if p.Home != real {
			t.Fatalf("plan home %q, want %q", p.Home, real)
		}
		for _, it := range p.Items {
			if !strings.HasPrefix(it.Path, real+"/") {
				t.Fatalf("item path %s does not start with the resolved home", it.Path)
			}
		}
	})
	t.Run("Symlink below home still refused", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{
			Files:    []testutil.File{{Path: "elsewhere/http/blob", Content: "x", MTime: old}},
			Symlinks: []testutil.Symlink{{Path: ".cache/pip", Target: "~/elsewhere"}},
		})
		_, out, _ := linuxCLI(t, "scan", "--json")
		var p plan.Plan
		exactlyOneJSON(t, out, &p)
		for _, it := range p.Items {
			if strings.HasPrefix(it.Path, home+"/.cache/pip/") || strings.HasPrefix(it.Path, home+"/elsewhere") {
				t.Fatalf("an item through the symlink: %+v", it)
			}
		}
		if !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "symlink_in_path") }) {
			t.Fatalf("warnings %q", p.Warnings)
		}
	})
}

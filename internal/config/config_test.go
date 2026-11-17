package config_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/config"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

func homeWithConfig(t *testing.T, toml string, extra ...testutil.File) string {
	t.Helper()
	files := append([]testutil.File{}, extra...)
	if toml != "" {
		files = append(files, testutil.File{Path: ".config/kipple/config.toml", Content: toml})
	}
	return testutil.NewHome(t, testutil.Spec{Files: files})
}

// Spec scenario "Config cannot shrink the denylist".
func TestConfigCannotShrinkTheDenylist(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		wantKey string
	}{
		{"Allow key naming Documents", `allow = ["Documents"]`, "allow"},
		{"Unprotect key", `unprotect = ["~/Documents"]`, "unprotect"},
		{"Denylist override table", "[denylist]\nremove = [\"Documents\"]", "denylist"},
		{"Project root in Documents", `project_roots = ["~/Documents/code"]`, "project_roots"},
		{"Project root is Documents", `project_roots = ["Documents"]`, "project_roots"},
		{"Project root contains agent memory", `project_roots = ["~/.claude"]`, "project_roots"},
		{"Project root in Documents by case", `project_roots = ["~/documents/code"]`, "project_roots"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := homeWithConfig(t, tc.toml, testutil.File{Path: "Documents/code/app/package.json", Content: "{}"})
			_, err := config.Load(home)
			var cerr *config.Error
			if !errors.As(err, &cerr) {
				t.Fatalf("want a *config.Error, got %v", err)
			}
			if cerr.Key != tc.wantKey || !strings.Contains(err.Error(), `"`+tc.wantKey+`"`) {
				t.Fatalf("error %q should name key %q", err, tc.wantKey)
			}
			// Documents stays refused regardless.
			rule := policy.StaticRule{ID: "probe", Roots: []string{"Documents"}}
			if d := policy.Authorize(policy.Env{Home: home}, rule, home+"/Documents/code"); d.Reason != policy.ReasonDenylisted {
				t.Fatalf("Documents/code should stay denylisted, got %+v", d)
			}
		})
	}
}

// Spec scenario "Extra protection honoured" (the apply half; scan is wave 2).
func TestExtraProtectionHonoured(t *testing.T) {
	home := homeWithConfig(t, `protect = ["~/Library/Caches/com.example.keep"]`,
		testutil.File{Path: "Library/Caches/com.example.keep/blob", Size: 32},
		testutil.File{Path: "Library/Caches/com.example.other/blob", Size: 32},
	)
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	env := cfg.PolicyEnv(home)
	caches, ok := rules.Lookup("caches.user")
	if !ok {
		t.Fatal("caches.user missing")
	}
	for _, p := range []string{"Library/Caches/com.example.keep", "Library/Caches/com.example.keep/blob"} {
		if d := policy.Authorize(env, caches, home+"/"+p); d.Reason != policy.ReasonUserProtected {
			t.Errorf("%s: want user_protected, got %+v", p, d)
		}
	}
	if d := policy.Authorize(env, caches, home+"/Library/Caches/com.example.other"); !d.Allowed {
		t.Errorf("unprotected sibling should be allowed, got %+v", d)
	}
}

func TestLoad(t *testing.T) {
	t.Run("Missing file gives defaults", func(t *testing.T) {
		home := homeWithConfig(t, "")
		cfg, err := config.Load(home)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Level != rules.LevelSafe || cfg.Protect != nil || cfg.ProjectRoots != nil {
			t.Fatalf("defaults: %+v", cfg)
		}
		if strings.Join(cfg.EffectiveProjectRoots(), ",") != strings.Join(rules.DefaultProjectRoots(), ",") {
			t.Fatalf("effective roots: %v", cfg.EffectiveProjectRoots())
		}
		if config.Path(home) != home+"/.config/kipple/config.toml" {
			t.Fatalf("path: %s", config.Path(home))
		}
	})

	t.Run("All keys parse and normalise", func(t *testing.T) {
		home := homeWithConfig(t, `
protect = ["~/Library/Caches/a", "Library/Caches/b", "/opt/elsewhere", "~/Library/Caches/c/../d"]
project_roots = ["~/work", "clients/acme", "~/dev/"]
level = "review"
`)
		cfg, err := config.Load(home)
		if err != nil {
			t.Fatal(err)
		}
		wantProtect := []string{home + "/Library/Caches/a", home + "/Library/Caches/b", "/opt/elsewhere", home + "/Library/Caches/d"}
		if strings.Join(cfg.Protect, "|") != strings.Join(wantProtect, "|") {
			t.Errorf("protect = %v, want %v", cfg.Protect, wantProtect)
		}
		if strings.Join(cfg.ProjectRoots, ",") != "work,clients/acme,dev" {
			t.Errorf("project_roots = %v", cfg.ProjectRoots)
		}
		if cfg.Level != rules.LevelReview {
			t.Errorf("level = %q", cfg.Level)
		}
		if env := cfg.PolicyEnv(home); env.Home != home || len(env.Protect) != 4 {
			t.Errorf("env = %+v", env)
		}
	})

	t.Run("Empty project_roots disables project scanning", func(t *testing.T) {
		home := homeWithConfig(t, `project_roots = []`)
		cfg, err := config.Load(home)
		if err != nil {
			t.Fatal(err)
		}
		if roots := cfg.EffectiveProjectRoots(); len(roots) != 0 {
			t.Fatalf("want no project roots, got %v", roots)
		}
	})

	errCases := []struct {
		name, toml, key string
	}{
		{"Invalid TOML", `protect = [`, ""},
		{"Unknown key", `colour = "blue"`, "colour"},
		{"Bad level", `level = "manual"`, "level"},
		{"Protect wrong type", `protect = "~/x"`, ""},
		{"Empty protect entry", `protect = [""]`, "protect"},
		{"Other user's home", `protect = ["~bob/x"]`, "protect"},
		{"Project root outside home", `project_roots = ["/opt/src"]`, "project_roots"},
		{"Project root is home", `project_roots = ["~"]`, "project_roots"},
		{"Project root escapes home", `project_roots = ["~/../other"]`, "project_roots"},
		{"Project root with glob", `project_roots = ["~/dev/*"]`, "project_roots"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			home := homeWithConfig(t, tc.toml)
			_, err := config.Load(home)
			var cerr *config.Error
			if !errors.As(err, &cerr) {
				t.Fatalf("want a *config.Error, got %v", err)
			}
			if cerr.Key != tc.key {
				t.Fatalf("key = %q, want %q (%v)", cerr.Key, tc.key, err)
			}
		})
	}
}

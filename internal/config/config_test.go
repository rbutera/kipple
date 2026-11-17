package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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
			rule := policy.StaticRule{ID: "probe", Roots: []string{"Documents"}, Kind: policy.MatchChildren}
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
protect = ["~/Library/Caches/a", "Library/Caches/b", "~/Library/Caches/c/../d"]
project_roots = ["~/work", "clients/acme", "~/dev/"]
level = "review"
`)
		cfg, err := config.Load(home)
		if err != nil {
			t.Fatal(err)
		}
		wantProtect := []string{home + "/Library/Caches/a", home + "/Library/Caches/b", home + "/Library/Caches/d"}
		if strings.Join(cfg.Protect, "|") != strings.Join(wantProtect, "|") {
			t.Errorf("protect = %v, want %v", cfg.Protect, wantProtect)
		}
		if strings.Join(cfg.ProjectRoots, ",") != "work,clients/acme,dev" {
			t.Errorf("project_roots = %v", cfg.ProjectRoots)
		}
		if cfg.Level != rules.LevelReview {
			t.Errorf("level = %q", cfg.Level)
		}
		if env := cfg.PolicyEnv(home); env.Home != home || len(env.Protect) != 3 {
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
		{"Project root is filesystem root", `project_roots = ["/"]`, "project_roots"},
		{"Project root is home via path", `project_roots = ["~/"]`, "project_roots"},
		{"Project root contains Xcode folders", `project_roots = ["Library/Developer"]`, "project_roots"},
		{"Project root contains the Go module cache", `project_roots = ["go"]`, "project_roots"},
		{"Project root is Downloads", `project_roots = ["Downloads"]`, "project_roots"},
		{"Project root is Downloads by case", `project_roots = ["~/downloads"]`, "project_roots"},
		{"Project root contains Ollama models", `project_roots = [".ollama"]`, "project_roots"},
		{"Project root inside the caches folder", `project_roots = ["Library/Caches/proj"]`, "project_roots"},
		{"Project root matches a glob rule root", `project_roots = ["Library/Developer/Xcode/iOS DeviceSupport"]`, "project_roots"},
		{"Project root with long s aliasing Documents", `project_roots = ["Documentſ"]`, "project_roots"},
		{"Project root with long s aliasing .ssh", `project_roots = ["~/.ſſh"]`, "project_roots"},
		{"Project root with long s aliasing Library/Messages", `project_roots = ["Library/Meſſages"]`, "project_roots"},
		{"Project root with ligature aliasing Library/CloudStorage", `project_roots = ["Library/Cloudﬆorage"]`, "project_roots"},
		{"Project root with sharp s", `project_roots = ["Library/Meßages"]`, "project_roots"},
		{"Protect with long s", `protect = ["~/Library/Caches/ſensitive"]`, "protect"},
		{"Protect filesystem root", `protect = ["/"]`, "protect"},
		{"Protect with NUL", `protect = ["~/Library/Caches/a\u0000b"]`, "protect"},
		{"Protect with a newline", `protect = ["~/Library/Caches/a\n"]`, "protect"},
		{"Protect with a tab", `protect = ["~/Library/Caches/\ta"]`, "protect"},
		{"Protect with DEL", `protect = ["~/Library/Caches/a\u007f"]`, "protect"},
		{"Protect with a C1 control", `protect = ["~/Library/Caches/a\u0085"]`, "protect"},
		{"Project root with NUL", `project_roots = ["work\u0000"]`, "project_roots"},
		{"Project root with a carriage return", `project_roots = ["work\r"]`, "project_roots"},
		{"Protect filesystem root via dot-dot", `protect = ["/opt/.."]`, "protect"},
		{"Protect with a glob", `protect = ["~/Library/Caches/sens*"]`, "protect"},
		{"Protect with a trailing glob", `protect = ["~/Library/Caches/com.example.*"]`, "protect"},
		{"Protect outside home", `protect = ["/opt/elsewhere"]`, "protect"},
		{"Protect escaping home", `protect = ["~/../other"]`, "protect"},
		{"Protect with ligature", `protect = ["~/Library/Caches/ﬁle"]`, "protect"},
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

// Load never follows a symlinked config, never blocks on a FIFO, and never
// reads more than MaxSize bytes.
func TestLoadRefusesUnsafeFiles(t *testing.T) {
	cfgRel := ".config/kipple/config.toml"
	cases := []struct {
		name  string
		spec  testutil.Spec
		setup func(t *testing.T, path string)
		want  string
	}{
		{"Symlinked config", testutil.Spec{
			Files:    []testutil.File{{Path: "dotfiles/kipple.toml", Content: `level = "review"`}},
			Symlinks: []testutil.Symlink{{Path: cfgRel, Target: "~/dotfiles/kipple.toml"}},
		}, nil, "symbolic link"},
		{"Config symlinked to an endless device", testutil.Spec{
			Symlinks: []testutil.Symlink{{Path: cfgRel, Target: "/dev/zero"}},
		}, nil, "symbolic link"},
		{"FIFO at the config path", testutil.Spec{Dirs: []testutil.Dir{{Path: ".config/kipple"}}},
			func(t *testing.T, path string) {
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			}, "not a regular file"},
		{"Directory at the config path", testutil.Spec{Dirs: []testutil.Dir{{Path: cfgRel}}}, nil, "not a regular file"},
		{"Oversized config", testutil.Spec{Files: []testutil.File{{Path: cfgRel, Content: strings.Repeat("#", config.MaxSize) + "\n"}}}, nil, "larger than 64 KiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := testutil.NewHome(t, tc.spec)
			path := filepath.Join(home, cfgRel)
			if tc.setup != nil {
				tc.setup(t, path)
			}
			done := make(chan error, 1)
			go func() {
				_, err := config.Load(home)
				done <- err
			}()
			select {
			case err := <-done:
				var cerr *config.Error
				if !errors.As(err, &cerr) || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want a config error containing %q, got %v", tc.want, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("config.Load did not return")
			}
		})
	}

	t.Run("Config of exactly the maximum size loads", func(t *testing.T) {
		home := homeWithConfig(t, `level = "review"`+"\n"+strings.Repeat("#", config.MaxSize-len(`level = "review"`)-1))
		cfg, err := config.Load(home)
		if err != nil || cfg.Level != rules.LevelReview {
			t.Fatalf("got %+v, %v", cfg, err)
		}
	})
}

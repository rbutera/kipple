// Package config loads kipple's optional user configuration from
// <home>/.config/kipple/config.toml. Home is injected; this package never
// resolves the real home directory.
//
// Configuration is additive only: it can protect more paths, choose project
// roots and set the default level, but nothing in it can relax a built-in
// protection. Recognised keys:
//
//	protect       = ["~/Library/Caches/com.example.keep"]  # refused with user_protected
//	project_roots = ["~/work"]                              # replaces the default project roots
//	level         = "safe"                                  # or "review"
//
// Any other key is an error that names the key. A project root on, inside or
// containing a denylisted location is an error naming project_roots.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
)

// Config is the validated configuration.
type Config struct {
	// Protect holds absolute, cleaned paths refused like denylist entries.
	Protect []string
	// ProjectRoots holds home-relative, slash-separated project roots, or nil
	// for rules.DefaultProjectRoots.
	ProjectRoots []string
	// Level is the default selection level, "safe" or "review".
	Level string
}

// Error is a configuration error tied to one key.
type Error struct {
	Key string
	Msg string
}

func (e *Error) Error() string {
	if e.Key == "" {
		return "config: " + e.Msg
	}
	return fmt.Sprintf("config: key %q: %s", e.Key, e.Msg)
}

type rawFile struct {
	Protect      []string `toml:"protect"`
	ProjectRoots []string `toml:"project_roots"`
	Level        string   `toml:"level"`
}

// Default is the configuration used when no file exists.
func Default() Config { return Config{Level: rules.LevelSafe} }

// Path returns the config file location for home.
func Path(home string) string {
	return filepath.Join(home, ".config", "kipple", "config.toml")
}

// Load reads and validates Path(home). A missing file yields Default().
func Load(home string) (Config, error) {
	data, err := os.ReadFile(Path(home))
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, &Error{Msg: err.Error()}
	}
	return Parse(home, data)
}

// Parse validates TOML config data for home.
func Parse(home string, data []byte) (Config, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return Config{}, &Error{Msg: fmt.Sprintf("home %q is not an absolute clean path", home)}
	}
	var raw rawFile
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return Config{}, &Error{Msg: "invalid TOML: " + err.Error()}
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		key := undec[0].String()
		return Config{}, &Error{Key: key, Msg: unknownKeyMsg(key)}
	}

	cfg := Default()
	for _, p := range raw.Protect {
		abs, err := expand(home, p)
		if err != nil {
			return Config{}, &Error{Key: "protect", Msg: err.Error()}
		}
		cfg.Protect = append(cfg.Protect, abs)
	}

	if raw.ProjectRoots != nil {
		cfg.ProjectRoots = []string{}
	}
	for _, p := range raw.ProjectRoots {
		rel, err := projectRoot(home, p)
		if err != nil {
			return Config{}, &Error{Key: "project_roots", Msg: err.Error()}
		}
		cfg.ProjectRoots = append(cfg.ProjectRoots, rel)
	}

	if raw.Level != "" {
		ok := false
		for _, l := range rules.Levels() {
			ok = ok || raw.Level == l
		}
		if !ok {
			return Config{}, &Error{Key: "level", Msg: fmt.Sprintf("%q is not one of %s", raw.Level, strings.Join(rules.Levels(), ", "))}
		}
		cfg.Level = raw.Level
	}
	return cfg, nil
}

// unknownKeyMsg explains an unrecognised key; keys that read like an attempt
// to relax protections get a pointed message.
func unknownKeyMsg(key string) string {
	k := strings.ToLower(key)
	for _, hint := range []string{"allow", "unprotect", "unblock", "deny", "exclude", "remove", "override", "unsafe"} {
		if strings.Contains(k, hint) {
			return "kipple config can only add protections; the built-in denylist and rule roots cannot be relaxed"
		}
	}
	return "unknown key (valid keys: protect, project_roots, level)"
}

// expand turns "~", "~/x", an absolute path or a home-relative path into a
// clean absolute path.
func expand(home, p string) (string, error) {
	p = strings.TrimSpace(p)
	switch {
	case p == "":
		return "", errors.New("empty path")
	case p == "~":
		return home, nil
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:]), nil
	case strings.HasPrefix(p, "~"):
		return "", fmt.Errorf("%q: only ~/ (your own home) is supported", p)
	case filepath.IsAbs(p):
		return filepath.Clean(p), nil
	default:
		return filepath.Join(home, p), nil
	}
}

// projectRoot validates a project root and returns it home-relative.
func projectRoot(home, p string) (string, error) {
	abs, err := expand(home, p)
	if err != nil {
		return "", err
	}
	if abs == home || !strings.HasPrefix(abs, home+string(filepath.Separator)) {
		return "", fmt.Errorf("%q must be a folder inside the home folder", p)
	}
	rel := filepath.ToSlash(abs[len(home)+1:])
	segs, ok := policy.ParseRoot(rel)
	if !ok {
		return "", fmt.Errorf("%q is not a plain folder path", p)
	}
	for _, s := range segs {
		if strings.ContainsAny(s, `*?[\`) {
			return "", fmt.Errorf("%q: globs are not allowed in project roots", p)
		}
	}
	if policy.IsDenylisted(rel) {
		return "", fmt.Errorf("%q is on, inside, or contains a location on the built-in denylist; config can only add protections", p)
	}
	return rel, nil
}

// EffectiveProjectRoots returns the configured project roots or the defaults.
func (c Config) EffectiveProjectRoots() []string {
	if c.ProjectRoots == nil {
		return rules.DefaultProjectRoots()
	}
	return append([]string(nil), c.ProjectRoots...)
}

// PolicyEnv builds the policy environment for home with this config's
// protections added.
func (c Config) PolicyEnv(home string) policy.Env {
	return policy.Env{Home: home, Protect: append([]string(nil), c.Protect...)}
}

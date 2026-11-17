// Package config loads kipple's optional user configuration from
// <home>/.config/kipple/config.toml. Home is injected; this package never
// resolves the real home directory.
//
// Configuration is additive only: it can protect more paths, choose project
// roots and set the default level, but nothing in it can relax a built-in
// protection. Recognised keys:
//
//	protect       = ["~/Library/Caches/com.example.keep"]  # refused with user_protected
//	                                                        # (inside home; relative = home-relative; no patterns)
//	project_roots = ["~/work"]                              # replaces the default project roots
//	level         = "safe"                                  # or "review"
//
// The file must be a regular file of at most 64 KiB. A symlinked
// config.toml is refused (it is opened with O_NOFOLLOW), as is a FIFO,
// device or directory at that path, so loading can never hang or read
// without bound.
//
// Any other key is an error that names the key. A project root must be a
// folder inside home; one on, inside or containing a denylisted location, or
// a root of any other catalogue rule, is an error naming project_roots.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// MaxSize is the largest config file Load reads.
const MaxSize = 64 << 10

// Load reads and validates Path(home). A missing file yields Default().
func Load(home string) (Config, error) {
	data, err := readConfig(Path(home))
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, &Error{Msg: err.Error()}
	}
	return Parse(home, data)
}

// readConfig reads a regular, non-symlinked file of at most MaxSize bytes.
func readConfig(path string) ([]byte, error) {
	f, err := openConfig(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (%s); kipple only reads a regular config file", path, fi.Mode().Type())
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSize {
		return nil, fmt.Errorf("%s is larger than %d KiB", path, MaxSize>>10)
	}
	return data, nil
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
		abs, err := protectEntry(home, p)
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
	for _, r := range p {
		if r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			return "", fmt.Errorf("%q contains the control character %U; write the path without it", p, r)
		}
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("empty path")
	}
	if r, bad := policy.ConfusableRune(p); bad {
		return "", fmt.Errorf("%q contains %U, which macOS treats as a different letter when comparing names; write the path with plain letters", p, r)
	}
	switch {
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

// protectEntry validates a protect entry and returns it absolute. Globs are
// rejected rather than matched literally, so a pattern never looks like a
// protection it is not; "/" is rejected because it would refuse everything.
func protectEntry(home, p string) (string, error) {
	if strings.ContainsAny(p, "*?[") {
		return "", fmt.Errorf("%q: patterns are not supported; list each folder or file to protect", p)
	}
	abs, err := expand(home, p)
	if err != nil {
		return "", err
	}
	if abs == "/" {
		return "", fmt.Errorf("%q would protect the whole disk and leave kipple nothing to do; list the folders to protect", p)
	}
	if abs != home && !strings.HasPrefix(abs, home+string(filepath.Separator)) {
		return "", fmt.Errorf("%q is outside the home folder, where kipple never acts; list folders inside it", p)
	}
	return abs, nil
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
	for _, r := range rules.Catalogue() {
		if r.Match.Kind == rules.MatchProjectArtifact {
			continue
		}
		for _, root := range r.Roots {
			if policy.Overlaps(rel, root) {
				return "", fmt.Errorf("%q is on, inside, or contains %q, the folder of rule %s; project roots must be separate from the folders other rules clean", p, root, r.ID)
			}
		}
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

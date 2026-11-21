package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// UserDirsRelPath is where xdg-user-dirs records the user's folders,
// relative to home. kipple reads the default location only (it does not
// follow XDG_CONFIG_HOME; design L6).
const UserDirsRelPath = ".config/user-dirs.dirs"

// UserDirs are the protections and warnings from user-dirs.dirs (design L9
// of linux-port). They only add to the built-in protections.
type UserDirs struct {
	// Protect holds the absolute, cleaned folders to protect, by variable.
	Protect map[string]string
	// Warnings explain every entry or line that was ignored.
	Warnings []string
}

// Paths returns the protected folders, sorted.
func (u UserDirs) Paths() []string {
	var out []string
	for _, p := range u.Protect {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// LoadUserDirs reads <home>/.config/user-dirs.dirs through open (never
// following a symlink, policy.Session.OpenUnderHome) and parses it. A
// missing file has no entries; a symlink, a non-regular file or any read
// error is a warning, and the built-in protections stay as they are.
func LoadUserDirs(home string, open func(rel string) (*os.File, error)) UserDirs {
	data, err := readConfig(filepath.Join(home, UserDirsRelPath), func() (*os.File, error) { return open(UserDirsRelPath) })
	if errors.Is(err, fs.ErrNotExist) {
		return UserDirs{}
	}
	if err != nil {
		return UserDirs{Warnings: []string{fmt.Sprintf("~/%s was not read (%v); only the built-in folders are protected", UserDirsRelPath, err)}}
	}
	return ParseUserDirs(home, data)
}

// ParseUserDirs parses user-dirs.dirs for home: lines
// XDG_<NAME>_DIR="$HOME/<path>" or XDG_<NAME>_DIR="/absolute/path", blank
// lines and # comments. Every folder named becomes a protection except
// XDG_DOWNLOAD_DIR, the root of downloads.installers. A value equal to home
// or outside home, and any malformed line, are ignored with a warning;
// every well-formed entry still counts.
func ParseUserDirs(home string, data []byte) UserDirs {
	u := UserDirs{Protect: map[string]string{}}
	warn := func(format string, args ...any) {
		u.Warnings = append(u.Warnings, fmt.Sprintf("~/"+UserDirsRelPath+": "+format, args...))
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || !strings.HasPrefix(key, "XDG_") || !strings.HasSuffix(key, "_DIR") || len(key) <= len("XDG__DIR") {
			warn("line %d is not XDG_<NAME>_DIR=\"...\"; ignored", i+1)
			continue
		}
		if len(val) < 2 || val[0] != '"' || val[len(val)-1] != '"' || strings.ContainsAny(val[1:len(val)-1], "\"\\`") {
			warn("%s has a value kipple does not understand (%s); ignored", key, val)
			continue
		}
		v := val[1 : len(val)-1]
		var p string
		switch {
		case v == "$HOME":
			p = home
		case strings.HasPrefix(v, "$HOME/"):
			p = filepath.Join(home, v[len("$HOME/"):])
		case strings.HasPrefix(v, "/"):
			p = filepath.Clean(v)
		default:
			warn("%s is neither $HOME/... nor an absolute path (%s); ignored", key, v)
			continue
		}
		if key == "XDG_DOWNLOAD_DIR" {
			continue // downloads.installers offers old installers there
		}
		switch {
		case p == home:
			warn("%s is your home folder itself; ignored (everything else stays protected)", key)
			continue
		case !strings.HasPrefix(p, home+"/"):
			warn("%s is outside your home folder (%s); ignored, kipple never looks there", key, p)
			continue
		}
		u.Protect[key] = p
	}
	return u
}

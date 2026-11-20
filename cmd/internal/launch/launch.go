// Package launch is what every kipple front end (the CLI, the TUI it starts,
// and the GUI) does before it scans: resolve and guard the home folder, load
// the config, open the receipt store, and build the scan and apply options.
// It lives under cmd/ because it is the one place allowed to read the real
// home folder; the library never does (tools/checkhome).
package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/config"
	"github.com/rbutera/kipple/internal/gitstate"
	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/procs"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/trash"
)

// Error codes, as in the CLI's JSON error envelope.
const (
	CodeHome    = "home"
	CodeConfig  = "config"
	CodeReceipt = "receipt"
)

// Error is a launch failure with a machine-readable code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func fail(code, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Runtime is the process environment, injectable for tests.
type Runtime struct {
	Getenv   func(string) string
	UserHome func() (string, error)
	// RealHomes lists the real home folder's possible locations; with
	// KIPPLE_HOME set, kipple refuses a KIPPLE_HOME that is, is inside, or
	// contains any of them.
	RealHomes func() ([]string, error)
	// GuardFS looks up directory identities for that guard; the zero value
	// means the real file system.
	GuardFS homeguard.FS
}

// System is the real process environment.
func System() Runtime {
	return Runtime{Getenv: os.Getenv, UserHome: os.UserHomeDir, RealHomes: RealHomes, GuardFS: homeguard.System()}
}

// RealHomes returns every place the real home might be: $HOME and the user
// database entry, so overriding HOME cannot hide it from the KIPPLE_HOME
// guard.
func RealHomes() ([]string, error) {
	var out []string
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		out = append(out, h)
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		out = append(out, u.HomeDir)
	}
	if len(out) == 0 {
		return nil, errors.New("neither $HOME nor the user database names a home folder")
	}
	return out, nil
}

// fs returns the identity lookups, the real file system by default.
func (rt Runtime) fs() homeguard.FS {
	if rt.GuardFS.Chain == nil || rt.GuardFS.Stat == nil || rt.GuardFS.MountPoint == nil {
		return homeguard.System()
	}
	return rt.GuardFS
}

// Home is the resolved, guarded home folder and its config.
type Home struct {
	// Path is the home folder, symlinks resolved and spelled as on disk.
	Path string
	// FromEnv is set when Path came from KIPPLE_HOME.
	FromEnv bool
	// ID is the identity of the approved home directory; the scan's
	// policy session refuses home if its path names another directory by
	// then.
	ID homeguard.ID
	// Config is the config file under home (defaults when absent).
	Config config.Config
}

// Resolve resolves home and loads config.
func Resolve(rt Runtime) (Home, error) {
	home, fromEnv, id, err := ResolveHome(rt)
	if err != nil {
		return Home{}, err
	}
	cfg, err := LoadConfig(home, id)
	if err != nil {
		return Home{}, err
	}
	return Home{Path: home, FromEnv: fromEnv, ID: id, Config: cfg}, nil
}

// LoadConfig reads the config file relative to the approved home's
// descriptor (a policy session pinned to its identity), so a home swapped
// after it was approved cannot supply the config.
func LoadConfig(home string, id homeguard.ID) (config.Config, error) {
	sess, err := policy.NewSession(policy.Env{Home: home, HomeIdentity: &policy.FileIdentity{Dev: id.Dev, Ino: id.Ino}})
	defer func() { _ = sess.Close() }()
	if err != nil {
		return config.Config{}, fail(CodeHome, "%v", err)
	}
	cfg, err := config.LoadWith(home, sess.OpenUnderHome)
	if err != nil {
		return config.Config{}, fail(CodeConfig, "%v", err)
	}
	return cfg, nil
}

// PolicyEnv is the policy environment for h: config protections and the
// pinned home identity.
func (h Home) PolicyEnv() policy.Env {
	env := h.Config.PolicyEnv(h.Path)
	env.HomeIdentity = &policy.FileIdentity{Dev: h.ID.Dev, Ino: h.ID.Ino}
	return env
}

// Rules is the effective catalogue: the built-in rules with the config's
// project roots.
func (h Home) Rules() []rules.Rule {
	return scan.EffectiveRules(rules.Catalogue(), h.Config.EffectiveProjectRoots())
}

// ScanOptions are the scan options every front end starts from.
func (h Home) ScanOptions(level, version string) scan.Options {
	return scan.Options{
		Env:           h.PolicyEnv(),
		Rules:         h.Rules(),
		Level:         level,
		KippleVersion: version,
	}
}

// ApplyOptions are the apply options every apply path (clean, tui, gui)
// starts from; the caller sets the plan and what to act on. pi answers
// owner and agent checks (procs.New() when nil); save, in tests, replaces
// saving the receipt.
func (h Home) ApplyOptions(store receipt.Store, version string, pi procs.Inspector, save func(plan.Receipt) error) apply.Options {
	opts := apply.Options{
		Env:           h.PolicyEnv(),
		Rules:         h.Rules(),
		Receipts:      store,
		KippleVersion: version,
		Procs:         pi,
		SaveReceipt:   save,
	}
	// The NSFileManager fallback moves items into the real Trash, so it is
	// used only for the real home, never for a KIPPLE_HOME.
	if !h.FromEnv {
		opts.Finder = func() (trash.ExpectTrasher, error) { return trash.NewFinderTrasher(nil) }
	}
	return opts
}

// StateDir is $KIPPLE_STATE_DIR, or ~/Library/Application Support/kipple
// under the effective home.
func StateDir(rt Runtime, h Home) (string, error) {
	if d := rt.Getenv("KIPPLE_STATE_DIR"); d != "" {
		if !filepath.IsAbs(d) {
			return "", fail(CodeReceipt, "KIPPLE_STATE_DIR %q is not an absolute path", d)
		}
		return filepath.Clean(d), nil
	}
	return filepath.Join(h.Path, "Library", "Application Support", "kipple"), nil
}

// Store opens the receipt store in the state directory.
func Store(rt Runtime, h Home) (receipt.Store, error) {
	dir, err := StateDir(rt, h)
	if err != nil {
		return receipt.Store{}, err
	}
	s, err := receipt.Open(dir)
	if err != nil {
		return receipt.Store{}, fail(CodeReceipt, "%v", err)
	}
	return s, nil
}

// HomeLabel is how a front end's header names the home folder: "~" for the
// real home, and for a KIPPLE_HOME its last two folders, marked as such.
func HomeLabel(home string, override bool) string {
	if !override {
		return "~"
	}
	parts := strings.Split(strings.Trim(home, "/"), "/")
	if len(parts) > 2 {
		return "KIPPLE_HOME …/" + strings.Join(parts[len(parts)-2:], "/")
	}
	return "KIPPLE_HOME " + home
}

// ResolveHome returns KIPPLE_HOME, or the real home, with symlinks resolved
// and each component spelled as stored on disk, so the policy's
// byte-identical name checks match (design D3). This is the only place
// kipple reads the real home.
//
// The respelling happens before the KIPPLE_HOME guard, and the guard checks
// the exact path returned, so nothing after the check changes what was
// checked. It also returns the identity of the directory it approved (for
// KIPPLE_HOME, the very one the guard checked), which the scan pins in
// policy.Env.HomeIdentity so a later swap of home or an ancestor is
// refused.
func ResolveHome(rt Runtime) (string, bool, homeguard.ID, error) {
	var none homeguard.ID
	h := rt.Getenv("KIPPLE_HOME")
	fromEnv := h != ""
	if !fromEnv {
		var err error
		if h, err = rt.UserHome(); err != nil {
			return "", false, none, fail(CodeHome, "cannot determine the home folder: %v", err)
		}
	}
	if !filepath.IsAbs(h) {
		return "", fromEnv, none, fail(CodeHome, "home %q is not an absolute path", h)
	}
	resolved, err := filepath.EvalSymlinks(h)
	if err != nil {
		return "", fromEnv, none, fail(CodeHome, "home %q cannot be resolved: %v", h, err)
	}
	resolved = filepath.Clean(resolved)
	final := onDiskCase(resolved)
	fi, err := os.Lstat(final)
	if err != nil || !fi.IsDir() {
		return "", fromEnv, none, fail(CodeHome, "home %q is not a directory", h)
	}
	if fromEnv {
		id, err := guardRealHome(rt, h, resolved, final)
		if err != nil {
			return "", fromEnv, none, err
		}
		return final, fromEnv, id, nil
	}
	c, err := rt.fs().Chain(final, true)
	if err != nil || len(c) == 0 {
		return "", fromEnv, none, fail(CodeHome, "home %q cannot be identified: %v", h, err)
	}
	return final, fromEnv, c[0], nil
}

// guardRealHome refuses a KIPPLE_HOME that is the real home folder, lies
// inside it, or contains it, and returns the identity of the directory it
// approved. KIPPLE_HOME exists for fixtures and agents' sandboxes; pointed
// into the real home (say ~/Documents/OldMac), it would sidestep the
// denylist, which is relative to home.
//
// Identity decides, not spelling: final (the path kipple will use) and
// resolved (before respelling) must be the same directory, and neither may
// relate to any real home location by (dev, ino) (see homeguard.Relate).
// Every check must have seen the same directory, or it is refused as
// changed mid-check. The case-insensitive string comparison is kept as an
// extra refusal; it can only refuse more. It fails closed when the real
// home cannot be determined or any identity lookup fails.
func guardRealHome(rt Runtime, given, resolved, final string) (homeguard.ID, error) {
	var pin homeguard.ID
	reals, err := rt.RealHomes()
	if err != nil || len(reals) == 0 {
		return pin, fail(CodeHome, "KIPPLE_HOME is set but the real home folder cannot be determined to guard against it: %v", err)
	}
	fsys := rt.fs()
	pinned := false
	for _, r := range reals {
		for _, p := range []string{final, resolved} {
			rel, id, err := homeguard.RelateID(fsys, p, r)
			if err != nil {
				return pin, fail(CodeHome, "KIPPLE_HOME %q cannot be checked against the real home folder, so it is refused: %v", given, err)
			}
			if rel != homeguard.Separate {
				return pin, fail(CodeHome, "KIPPLE_HOME %q %s (%s); point it at a separate fixture folder", given, rel, r)
			}
			// The respelled path must be the very directory that was
			// resolved, and nothing may swap it while it is checked.
			if !pinned {
				pin, pinned = id, true
			} else if id != pin {
				return pin, fail(CodeHome, "KIPPLE_HOME %q changed identity while it was checked (or when respelled as %q); refusing", given, final)
			}
		}
		for _, p := range []string{given, resolved, final} {
			if within(p, r) || within(r, p) {
				return pin, fail(CodeHome, "KIPPLE_HOME %q is the real home folder, inside it, or contains it (%s); point it at a separate fixture folder", given, r)
			}
		}
	}
	return pin, nil
}

// within reports whether p is root or below it, comparing the given and the
// symlink-resolved forms case-insensitively. It is a second, spelling-based
// refusal; homeguard.Relate is the identity check that decides.
func within(p, root string) bool {
	forms := func(x string) []string {
		out := []string{filepath.Clean(x)}
		if r, err := filepath.EvalSymlinks(x); err == nil {
			out = append(out, r)
		}
		return out
	}
	for _, a := range forms(p) {
		for _, b := range forms(root) {
			al, bl := strings.ToLower(a), strings.ToLower(strings.TrimSuffix(b, "/"))
			if al == bl || bl == "" || strings.HasPrefix(al, bl+"/") {
				return true
			}
		}
	}
	return false
}

// onDiskCase respells each component of an existing absolute path as it is
// stored in its parent directory. On a case-insensitive volume /users/me
// becomes /Users/me. A component with no unique case-insensitive match is
// kept as given.
func onDiskCase(p string) string {
	cur := "/"
	for _, c := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if c == "" {
			continue
		}
		cur = filepath.Join(cur, storedSpelling(cur, c))
	}
	return cur
}

// storedSpelling returns the name under which dir stores the entry that
// name opens. A byte-identical entry wins. Otherwise the candidates are
// entries that fold to the same name (policy.FoldName: APFS case folding,
// long s, sharp s, ligatures) or, for names with non-ASCII letters, any
// non-ASCII entry, since NFC and NFD spellings of a name differ in bytes but
// open the same entry. A candidate counts only if it is the very object name
// opens (same dev and inode, checked with lstat), so the result is the
// stored spelling of that object, never a look-alike. Without exactly one
// such entry the name is kept as given.
func storedSpelling(dir, name string) string {
	f, err := os.Open(dir)
	if err != nil {
		return name
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return name
	}
	target, err := os.Lstat(filepath.Join(dir, name))
	if err != nil {
		return name
	}
	nonASCII := func(s string) bool {
		for i := 0; i < len(s); i++ {
			if s[i] >= 0x80 {
				return true
			}
		}
		return false
	}
	match := ""
	for _, n := range names {
		if n == name {
			return name
		}
		if policy.FoldName(n) != policy.FoldName(name) && (!nonASCII(name) || !nonASCII(n)) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(dir, n))
		if err != nil || !os.SameFile(fi, target) {
			continue
		}
		if match != "" {
			return name // two entries for one object: keep the given spelling
		}
		match = n
	}
	if match == "" {
		return name
	}
	return match
}

// CommandLineTools reports whether Apple's Command Line Tools (or Xcode)
// are installed. Without them /usr/bin/git is a shim that pops up an
// install dialog every time it runs, so a front end started from Finder
// checks first and, when they are missing, scans with NoGit. It runs
// /usr/bin/xcode-select -p, which only prints a folder path.
func CommandLineTools() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/xcode-select", "-p").Run() == nil
}

// NoGit is a git inspector that never runs git: every repository is
// git_unavailable, so project items stay unselectable.
type NoGit struct{}

// Inspect implements scan.GitInspector.
func (NoGit) Inspect(context.Context, string) gitstate.Result {
	return gitstate.Result{State: gitstate.StateUnavailable, Detail: "Apple's Command Line Tools are not installed, so kipple did not run git"}
}

// InsideRealHome reports whether p is, is inside, or contains a real home
// location, comparing the given and symlink-resolved spellings
// case-insensitively. It fails closed: when the real home cannot be
// determined, every path counts as inside it.
func InsideRealHome(rt Runtime, p string) bool {
	reals, err := rt.RealHomes()
	if err != nil || len(reals) == 0 {
		return true
	}
	for _, r := range reals {
		if within(p, r) || within(r, p) {
			return true
		}
	}
	return false
}

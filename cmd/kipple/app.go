package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rbutera/kipple/internal/config"
	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
)

// Exit codes (design D11). 3 (nothing to do) and 4 (partial) are produced
// by clean and restore in wave 3.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// Error codes in the JSON error envelope.
const (
	codeUsage    = "usage"
	codeRoot     = "root"
	codeHome     = "home"
	codeConfig   = "config"
	codeNotFound = "not_found"
	codeScan     = "scan"
	codeInternal = "internal"
)

// runtimeEnv is the process environment, injectable for tests.
type runtimeEnv struct {
	getenv   func(string) string
	geteuid  func() int
	userHome func() (string, error)
	// realHomes lists the real home folder's possible locations; with
	// KIPPLE_HOME set, kipple refuses a KIPPLE_HOME that is, is inside, or
	// contains any of them.
	realHomes func() ([]string, error)
	// guardFS looks up directory identities for that guard; the zero value
	// means the real file system.
	guardFS homeguard.FS
	isTTY   func(io.Writer) bool
}

// cliError carries an exit code and a machine-readable code.
type cliError struct {
	exit int
	code string
	msg  string
}

func (e *cliError) Error() string { return e.msg }

func usageErr(format string, args ...any) error {
	return &cliError{exit: exitUsage, code: codeUsage, msg: fmt.Sprintf(format, args...)}
}

func fail(code, format string, args ...any) error {
	return &cliError{exit: exitError, code: code, msg: fmt.Sprintf(format, args...)}
}

// errorEnvelope is the JSON error document: {"error":{"code","message"}}.
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// helpDocument is what --help prints on stdout under --json.
type helpDocument struct {
	Command string `json:"command"`
	Help    string `json:"help"`
}

// app holds per-invocation state shared by the commands.
type app struct {
	rt     runtimeEnv
	stdout io.Writer
	stderr io.Writer
	json   bool
	level  string
	ctx    context.Context
}

const rootHelp = `kipple lists rebuildable caches and developer junk under your home folder,
explains each item, and (from v0.1 clean) moves only what you approve to the
Trash. Every command is a dry run unless --apply is given; scan never moves
anything.

Exit codes:
  0  success
  1  error
  2  usage error (unknown flag, command or argument)
  3  nothing to do
  4  partial: some items were skipped or failed

With --json, stdout carries exactly one JSON document and everything else
goes to stderr. Errors are also reported on stdout as
{"error":{"code":"...","message":"..."}}. JSON Schemas: kipple schema plan.

Environment:
  KIPPLE_HOME       use this folder as home (for tests and agents)
  KIPPLE_STATE_DIR  where receipts are kept`

// run executes kipple with args and returns the exit code.
func run(args []string, stdout, stderr io.Writer, rt runtimeEnv) int {
	a := &app{rt: rt, stdout: stdout, stderr: stderr}
	if err := policy.CheckNotRoot(rt.geteuid()); err != nil {
		a.json = jsonRequested(args)
		return a.report(&cliError{exit: exitError, code: codeRoot, msg: err.Error()})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a.ctx = ctx

	root := a.rootCommand()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var ce *cliError
		if !errors.As(err, &ce) {
			// Cobra's own errors (unknown command, bad arguments) are usage errors.
			ce = &cliError{exit: exitUsage, code: codeUsage, msg: err.Error()}
		}
		// Cobra sets a.json when it parses --json in any spelling. A flag
		// error can stop its parse before --json is reached, so parse again,
		// leniently, with the same flag parser.
		a.json = a.json || jsonRequested(args)
		return a.report(ce)
	}
	return exitOK
}

// jsonRequested reports whether args set --json, parsed by pflag (the
// parser cobra uses) with unknown flags ignored, so every spelling pflag
// accepts (--json, --json=1, --json=t, --json=TRUE) counts and parsing
// stops at "--". It is used only when cobra's own parse did not finish:
// the root refusal runs before it, and a flag error can end it early.
func jsonRequested(args []string) bool {
	fs := pflag.NewFlagSet("kipple", pflag.ContinueOnError)
	fs.ParseErrorsWhitelist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var j bool
	fs.BoolVar(&j, "json", false, "")
	fs.String("level", "", "")
	_ = fs.Parse(args)
	return j
}

// report prints err to stderr and, with --json, the envelope to stdout.
func (a *app) report(ce *cliError) int {
	fmt.Fprintf(a.stderr, "kipple: %s\n", ce.msg)
	if a.json {
		var env errorEnvelope
		env.Error.Code, env.Error.Message = ce.code, ce.msg
		_ = plan.Encode(a.stdout, env)
	}
	return ce.exit
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "kipple",
		Short:         "Find and safely clear AI-tool and developer junk on macOS",
		Long:          rootHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if a.json {
				return usageErr("no command given (see kipple --help)")
			}
			return c.Help()
		},
		PersistentPreRunE: func(*cobra.Command, []string) error {
			if a.level != "" && a.level != rules.LevelSafe && a.level != rules.LevelReview {
				return usageErr("--level must be %q or %q, not %q", rules.LevelSafe, rules.LevelReview, a.level)
			}
			return nil
		},
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	// With --json, help text goes to stderr and stdout carries one JSON
	// document holding the same text, so stdout is never anything but JSON.
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(c *cobra.Command, args []string) {
		if !a.json {
			defaultHelp(c, args)
			return
		}
		var buf strings.Builder
		c.SetOut(&buf)
		defaultHelp(c, args)
		c.SetOut(nil)
		_, _ = io.WriteString(a.stderr, buf.String())
		_ = plan.Encode(a.stdout, helpDocument{Command: c.CommandPath(), Help: buf.String()})
	})
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr("%v", err) })
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print one JSON document on stdout")
	root.PersistentFlags().StringVar(&a.level, "level", "", `selection level: "safe" (default, or config level) or "review"`)
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(a.scanCommand(), a.rulesCommand(), a.explainCommand(), a.schemaCommand(), a.versionCommand())
	return root
}

func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErr("unknown command or argument %q (see kipple --help)", args[0])
	}
	return nil
}

func exactArgs(n int, what string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErr("expected %s, got %d arguments", what, len(args))
		}
		return nil
	}
}

// emit writes v as JSON (with --json) or calls human.
func (a *app) emit(v any, human func(io.Writer) error) error {
	if a.json {
		return plan.Encode(a.stdout, v)
	}
	return human(a.stdout)
}

// homeEnv resolves home and loads config.
type homeEnv struct {
	home    string
	fromEnv bool
	cfg     config.Config
}

func (a *app) resolve() (homeEnv, error) {
	home, fromEnv, err := resolveHome(a.rt)
	if err != nil {
		return homeEnv{}, err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return homeEnv{}, fail(codeConfig, "%v", err)
	}
	return homeEnv{home: home, fromEnv: fromEnv, cfg: cfg}, nil
}

// effectiveLevel is --level, else the config level, else safe.
func (a *app) effectiveLevel(cfg config.Config) string {
	if a.level != "" {
		return a.level
	}
	if cfg.Level != "" {
		return cfg.Level
	}
	return rules.LevelSafe
}

func effectiveRules(cfg config.Config) []rules.Rule {
	return scan.EffectiveRules(rules.Catalogue(), cfg.EffectiveProjectRoots())
}

// resolveHome returns KIPPLE_HOME, or the real home, with symlinks resolved
// and each component spelled as stored on disk, so the policy's
// byte-identical name checks match (design D3). This is the only place
// kipple reads the real home.
//
// The respelling happens before the KIPPLE_HOME guard, and the guard checks
// the exact path returned, so nothing after the check changes what was
// checked.
func resolveHome(rt runtimeEnv) (string, bool, error) {
	h := rt.getenv("KIPPLE_HOME")
	fromEnv := h != ""
	if !fromEnv {
		var err error
		if h, err = rt.userHome(); err != nil {
			return "", false, fail(codeHome, "cannot determine the home folder: %v", err)
		}
	}
	if !filepath.IsAbs(h) {
		return "", fromEnv, fail(codeHome, "home %q is not an absolute path", h)
	}
	resolved, err := filepath.EvalSymlinks(h)
	if err != nil {
		return "", fromEnv, fail(codeHome, "home %q cannot be resolved: %v", h, err)
	}
	resolved = filepath.Clean(resolved)
	final := onDiskCase(resolved)
	fi, err := os.Lstat(final)
	if err != nil || !fi.IsDir() {
		return "", fromEnv, fail(codeHome, "home %q is not a directory", h)
	}
	if fromEnv {
		if err := guardRealHome(rt, h, resolved, final); err != nil {
			return "", fromEnv, err
		}
	}
	return final, fromEnv, nil
}

// guardRealHome refuses a KIPPLE_HOME that is the real home folder, lies
// inside it, or contains it. KIPPLE_HOME exists for fixtures and agents'
// sandboxes; pointed into the real home (say ~/Documents/OldMac), it would
// sidestep the denylist, which is relative to home.
//
// Identity decides, not spelling: final (the path kipple will use) and
// resolved (before respelling) must be the same directory, and neither may
// relate to any real home location by (dev, ino) (see homeguard.Relate).
// The case-insensitive string comparison is kept as an extra refusal; it
// can only refuse more. It fails closed when the real home cannot be
// determined or any identity lookup fails.
func guardRealHome(rt runtimeEnv, given, resolved, final string) error {
	reals, err := rt.realHomes()
	if err != nil || len(reals) == 0 {
		return fail(codeHome, "KIPPLE_HOME is set but the real home folder cannot be determined to guard against it: %v", err)
	}
	fsys := rt.guardFS
	if fsys.Chain == nil || fsys.Stat == nil {
		fsys = homeguard.System()
	}
	// The respelled path must be the very directory that was resolved.
	if final != resolved {
		a, errA := fsys.Chain(resolved, true)
		b, errB := fsys.Chain(final, true)
		if errA != nil || errB != nil || len(a) == 0 || len(b) == 0 || a[0] != b[0] {
			return fail(codeHome, "KIPPLE_HOME %q changed identity when respelled as %q; refusing", given, final)
		}
	}
	for _, r := range reals {
		for _, p := range []string{final, resolved} {
			rel, err := homeguard.Relate(fsys, p, r)
			if err != nil {
				return fail(codeHome, "KIPPLE_HOME %q cannot be checked against the real home folder, so it is refused: %v", given, err)
			}
			if rel != homeguard.Separate {
				return fail(codeHome, "KIPPLE_HOME %q %s (%s); point it at a separate fixture folder", given, rel, r)
			}
		}
		for _, p := range []string{given, resolved, final} {
			if within(p, r) || within(r, p) {
				return fail(codeHome, "KIPPLE_HOME %q is the real home folder, inside it, or contains it (%s); point it at a separate fixture folder", given, r)
			}
		}
	}
	return nil
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
	match := ""
	for _, n := range names {
		if n == name {
			return name
		}
		if strings.EqualFold(n, name) {
			if match != "" {
				return name // ambiguous on a case-sensitive volume
			}
			match = n
		}
	}
	if match == "" {
		return name
	}
	return match
}

// runScan scans home at the effective level.
func (a *app) runScan(h homeEnv) (plan.Plan, error) {
	opts := scan.Options{
		Env:           h.cfg.PolicyEnv(h.home),
		Rules:         effectiveRules(h.cfg),
		Level:         a.effectiveLevel(h.cfg),
		KippleVersion: version,
	}
	if a.rt.isTTY(a.stderr) {
		opts.Progress = func(msg string) { fmt.Fprintf(a.stderr, "\r\033[K%s", msg) }
	}
	p, err := scan.Run(a.ctx, opts)
	if opts.Progress != nil {
		fmt.Fprint(a.stderr, "\r\033[K")
	}
	if err != nil {
		return plan.Plan{}, fail(codeScan, "%v", err)
	}
	return p, nil
}

func titles() map[string]string {
	out := map[string]string{}
	for _, r := range rules.Catalogue() {
		out[r.ID] = r.Title
	}
	return out
}

func (a *app) scanCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "List what kipple would move, with sizes and reasons (never moves anything)",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			h, err := a.resolve()
			if err != nil {
				return err
			}
			p, err := a.runScan(h)
			if err != nil {
				return err
			}
			return a.emit(p, func(w io.Writer) error { return output.Plan(w, p, titles()) })
		},
	}
}

func (a *app) rulesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rules",
		Short: "List the rule catalogue grouped by tier",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			h, err := a.resolve()
			if err != nil {
				return err
			}
			rs := effectiveRules(h.cfg)
			doc := plan.RulesDocument{SchemaVersion: plan.SchemaVersion, Home: h.home, Rules: rs}
			return a.emit(doc, func(w io.Writer) error {
				if h.fromEnv {
					fmt.Fprintf(w, "Home: %s\n\n", h.home)
				}
				return output.Rules(w, rs)
			})
		},
	}
}

var itemIDPattern = regexp.MustCompile(`^[a-z2-7]{12}$`)

// explainDocument is the JSON output of kipple explain.
type explainDocument struct {
	SchemaVersion int        `json:"schema_version"`
	Home          string     `json:"home"`
	Rule          rules.Rule `json:"rule"`
	Item          *plan.Item `json:"item,omitempty"`
}

func (a *app) explainCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "explain <item-id|rule-id>",
		Short: "Explain a rule, or an item from a fresh scan",
		Args:  exactArgs(1, "one item ID or rule ID"),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			h, err := a.resolve()
			if err != nil {
				return err
			}
			rs := effectiveRules(h.cfg)
			for _, r := range rs {
				if r.ID == id {
					doc := explainDocument{SchemaVersion: plan.SchemaVersion, Home: h.home, Rule: r}
					return a.emit(doc, func(w io.Writer) error {
						if h.fromEnv {
							fmt.Fprintf(w, "Home: %s\n\n", h.home)
						}
						return output.ExplainRule(w, r)
					})
				}
			}
			if !itemIDPattern.MatchString(id) {
				return fail(codeNotFound, "no rule or item %q (rule IDs are listed by kipple rules; item IDs by kipple scan)", id)
			}
			p, err := a.runScan(h)
			if err != nil {
				return err
			}
			for i := range p.Items {
				it := p.Items[i]
				if it.ID != id {
					continue
				}
				for _, r := range rs {
					if r.ID == it.Rule {
						doc := explainDocument{SchemaVersion: plan.SchemaVersion, Home: h.home, Rule: r, Item: &it}
						return a.emit(doc, func(w io.Writer) error { return output.ExplainItem(w, it, r) })
					}
				}
			}
			return fail(codeNotFound, "no item %q in a scan of %s", id, h.home)
		},
	}
}

func (a *app) schemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:       "schema <" + strings.Join(plan.Schemas(), "|") + ">",
		Short:     "Print the JSON Schema of a kipple document",
		ValidArgs: plan.Schemas(),
		Args:      exactArgs(1, "one of "+strings.Join(plan.Schemas(), ", ")),
		RunE: func(_ *cobra.Command, args []string) error {
			known := false
			for _, s := range plan.Schemas() {
				known = known || s == args[0]
			}
			if !known {
				return usageErr("unknown schema %q (want one of %s)", args[0], strings.Join(plan.Schemas(), ", "))
			}
			b, err := plan.Schema(args[0])
			if err != nil {
				return fail(codeInternal, "%v", err)
			}
			_, err = a.stdout.Write(b)
			return err
		},
	}
}

// versionDocument is the JSON output of kipple version.
type versionDocument struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	SchemaVersion int    `json:"schema_version"`
	Home          string `json:"home,omitempty"`
}

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and document schema version",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			doc := versionDocument{Version: version, Commit: commit, SchemaVersion: plan.SchemaVersion}
			if a.rt.getenv("KIPPLE_HOME") != "" {
				if home, _, err := resolveHome(a.rt); err == nil {
					doc.Home = home
				}
			}
			return a.emit(doc, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "kipple %s (commit %s, schema_version %d)\n", doc.Version, doc.Commit, doc.SchemaVersion)
				if err == nil && doc.Home != "" {
					_, err = fmt.Fprintf(w, "home %s\n", doc.Home)
				}
				return err
			})
		},
	}
}

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

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/config"
	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/procs"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/tui"
)

// Exit codes (design D11). 3 (nothing to do) and 4 (partial) come from
// clean and restore (apply.ExitCode).
const (
	exitOK      = 0
	exitError   = 1
	exitUsage   = 2
	exitNothing = apply.ExitNothing
	exitPartial = apply.ExitPartial
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
	codePlan     = "plan"
	codeApply    = "apply"
	codeReceipt  = "receipt"
	// codeTrashAccess: the Trash folder cannot be used (no Full Disk Access
	// and no usable fallback).
	codeTrashAccess = "trash_access"
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
	// stdin is read only for the clean --apply confirmation on a TTY.
	stdin io.Reader
	// procs answers owner and agent checks (procs.New() when nil).
	procs procs.Inspector
	// saveReceipt, in tests, replaces saving the receipt (apply.Options.SaveReceipt).
	saveReceipt func(plan.Receipt) error
	// beforeScan, when set, runs after home is resolved and guarded and
	// before the scan opens it. Tests use it to swap the directory.
	beforeScan func(home string)
	// tui runs the interactive front end (tui.Run when nil). Tests drive
	// the model headlessly instead.
	tui func(cfg tui.Config, in io.Reader, out io.Writer) (tui.Outcome, error)
}

// fs returns the identity lookups, the real file system by default.
func (rt runtimeEnv) fs() homeguard.FS {
	if rt.guardFS.Chain == nil || rt.guardFS.Stat == nil || rt.guardFS.MountPoint == nil {
		return homeguard.System()
	}
	return rt.guardFS
}

// cliError carries an exit code and a machine-readable code.
type cliError struct {
	exit int
	code string
	msg  string
	// result, when set, is what the command had already done; it goes into
	// the JSON error envelope so nothing that happened is hidden.
	result *plan.Result
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
	// Result is present when the command had already done something before
	// the error (for example items moved before a receipt save failed).
	Result *plan.Result `json:"result,omitempty"`
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
	// exit is the code a command that succeeded asks run to return (3 or
	// 4 from clean and restore).
	exit int
}

const rootHelp = `kipple lists rebuildable caches and developer junk under your home folder,
explains each item, and moves only what you approve to the Trash. Every
command is a dry run unless --apply is given; scan never moves anything.
clean --apply re-checks every item immediately before moving it and writes a
receipt; restore puts items back from a receipt.

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
  KIPPLE_STATE_DIR  where receipts are kept, in its receipts folder
                    (default ~/Library/Application Support/kipple)`

// run executes kipple with args and returns the exit code.
func run(args []string, stdout, stderr io.Writer, rt runtimeEnv) int {
	a := &app{rt: rt, stdout: stdout, stderr: stderr}
	root := a.rootCommand()
	if err := policy.CheckNotRoot(rt.geteuid()); err != nil {
		a.json = jsonRequested(root, args)
		return a.report(&cliError{exit: exitError, code: codeRoot, msg: err.Error()})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a.ctx = ctx

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
		a.json = a.json || jsonRequested(root, args)
		return a.report(ce)
	}
	return a.exit
}

// jsonRequested reports whether args set --json, parsed by pflag (the
// parser cobra uses) with unknown flags ignored, so every spelling pflag
// accepts (--json, --json=1, --json=t, --json=TRUE) counts and parsing
// stops at "--". It is used only when cobra's own parse did not finish:
// the root refusal runs before it, and a flag error can end it early.
//
// The flag set is cloned from the command cobra resolves for args (its
// local and inherited flags, including help), keeping each flag's name,
// shorthand and no-value default, so a flag that takes a value consumes it
// exactly as cobra would and a new flag cannot make this parse drift.
func jsonRequested(root *cobra.Command, args []string) bool {
	c := root
	if found, _, err := root.Find(args); err == nil && found != nil {
		c = found
	}
	c.InitDefaultHelpFlag()
	fs := pflag.NewFlagSet("kipple", pflag.ContinueOnError)
	fs.ParseErrorsWhitelist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var j bool
	clone := func(f *pflag.Flag) {
		if fs.Lookup(f.Name) != nil {
			return
		}
		if f.Name == "json" {
			fs.BoolVarP(&j, f.Name, f.Shorthand, false, "")
			return
		}
		nf := fs.VarPF(anyValue{typ: f.Value.Type()}, f.Name, f.Shorthand, "")
		nf.NoOptDefVal = f.NoOptDefVal
	}
	c.LocalFlags().VisitAll(clone)
	c.InheritedFlags().VisitAll(clone)
	_ = fs.Parse(args)
	return j
}

// anyValue accepts any value for a cloned flag; only its arity matters.
type anyValue struct{ typ string }

func (anyValue) Set(string) error { return nil }
func (anyValue) String() string   { return "" }
func (v anyValue) Type() string   { return v.typ }

// report prints err to stderr and, with --json, the envelope to stdout.
func (a *app) report(ce *cliError) int {
	fmt.Fprintf(a.stderr, "kipple: %s\n", ce.msg)
	if a.json {
		var env errorEnvelope
		env.Error.Code, env.Error.Message = ce.code, ce.msg
		env.Result = ce.result
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
	root.SetHelpCommand(a.helpCommand())
	root.AddCommand(a.scanCommand(), a.cleanCommand(), a.historyCommand(), a.restoreCommand(),
		a.rulesCommand(), a.explainCommand(), a.schemaCommand(), a.versionCommand(), a.tuiCommand())
	return root
}

// helpCommand replaces cobra's help command, which prints the main help and
// exits 0 for an unknown topic. Here an unknown topic is a usage error.
func (a *app) helpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		RunE: func(c *cobra.Command, args []string) error {
			target, rest, err := c.Root().Find(args)
			if err != nil || target == nil || len(rest) > 0 {
				return usageErr("unknown help topic %q (see kipple --help)", strings.Join(args, " "))
			}
			target.InitDefaultHelpFlag()
			return target.Help()
		},
	}
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
	// id is the identity of the approved home directory; the scan's policy
	// session refuses home if its path names another directory by then.
	id  homeguard.ID
	cfg config.Config
}

func (a *app) resolve() (homeEnv, error) {
	home, fromEnv, id, err := resolveHome(a.rt)
	if err != nil {
		return homeEnv{}, err
	}
	cfg, err := loadConfig(home, id)
	if err != nil {
		return homeEnv{}, err
	}
	return homeEnv{home: home, fromEnv: fromEnv, id: id, cfg: cfg}, nil
}

// loadConfig reads the config file relative to the approved home's
// descriptor (a policy session pinned to its identity), so a home swapped
// after it was approved cannot supply the config.
func loadConfig(home string, id homeguard.ID) (config.Config, error) {
	sess, err := policy.NewSession(policy.Env{Home: home, HomeIdentity: &policy.FileIdentity{Dev: id.Dev, Ino: id.Ino}})
	defer func() { _ = sess.Close() }()
	if err != nil {
		return config.Config{}, fail(codeHome, "%v", err)
	}
	cfg, err := config.LoadWith(home, sess.OpenUnderHome)
	if err != nil {
		return config.Config{}, fail(codeConfig, "%v", err)
	}
	return cfg, nil
}

// policyEnv is the policy environment for h: config protections and the
// pinned home identity.
func policyEnv(h homeEnv) policy.Env {
	env := h.cfg.PolicyEnv(h.home)
	env.HomeIdentity = &policy.FileIdentity{Dev: h.id.Dev, Ino: h.id.Ino}
	return env
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
// checked. It also returns the identity of the directory it approved (for
// KIPPLE_HOME, the very one the guard checked), which the scan pins in
// policy.Env.HomeIdentity so a later swap of home or an ancestor is
// refused.
func resolveHome(rt runtimeEnv) (string, bool, homeguard.ID, error) {
	var none homeguard.ID
	h := rt.getenv("KIPPLE_HOME")
	fromEnv := h != ""
	if !fromEnv {
		var err error
		if h, err = rt.userHome(); err != nil {
			return "", false, none, fail(codeHome, "cannot determine the home folder: %v", err)
		}
	}
	if !filepath.IsAbs(h) {
		return "", fromEnv, none, fail(codeHome, "home %q is not an absolute path", h)
	}
	resolved, err := filepath.EvalSymlinks(h)
	if err != nil {
		return "", fromEnv, none, fail(codeHome, "home %q cannot be resolved: %v", h, err)
	}
	resolved = filepath.Clean(resolved)
	final := onDiskCase(resolved)
	fi, err := os.Lstat(final)
	if err != nil || !fi.IsDir() {
		return "", fromEnv, none, fail(codeHome, "home %q is not a directory", h)
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
		return "", fromEnv, none, fail(codeHome, "home %q cannot be identified: %v", h, err)
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
func guardRealHome(rt runtimeEnv, given, resolved, final string) (homeguard.ID, error) {
	var pin homeguard.ID
	reals, err := rt.realHomes()
	if err != nil || len(reals) == 0 {
		return pin, fail(codeHome, "KIPPLE_HOME is set but the real home folder cannot be determined to guard against it: %v", err)
	}
	fsys := rt.fs()
	pinned := false
	for _, r := range reals {
		for _, p := range []string{final, resolved} {
			rel, id, err := homeguard.RelateID(fsys, p, r)
			if err != nil {
				return pin, fail(codeHome, "KIPPLE_HOME %q cannot be checked against the real home folder, so it is refused: %v", given, err)
			}
			if rel != homeguard.Separate {
				return pin, fail(codeHome, "KIPPLE_HOME %q %s (%s); point it at a separate fixture folder", given, rel, r)
			}
			// The respelled path must be the very directory that was
			// resolved, and nothing may swap it while it is checked.
			if !pinned {
				pin, pinned = id, true
			} else if id != pin {
				return pin, fail(codeHome, "KIPPLE_HOME %q changed identity while it was checked (or when respelled as %q); refusing", given, final)
			}
		}
		for _, p := range []string{given, resolved, final} {
			if within(p, r) || within(r, p) {
				return pin, fail(codeHome, "KIPPLE_HOME %q is the real home folder, inside it, or contains it (%s); point it at a separate fixture folder", given, r)
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

// runScan scans home at the effective level.
func (a *app) runScan(h homeEnv) (plan.Plan, error) {
	return a.runScanAt(h, a.effectiveLevel(h.cfg))
}

// runScanAt scans home at level.
// scanOptions are the scan options every front end (scan, clean, tui)
// starts from.
func scanOptions(h homeEnv, level string) scan.Options {
	return scan.Options{
		Env:           policyEnv(h),
		Rules:         effectiveRules(h.cfg),
		Level:         level,
		KippleVersion: version,
	}
}

func (a *app) runScanAt(h homeEnv, level string) (plan.Plan, error) {
	if a.rt.beforeScan != nil {
		a.rt.beforeScan(h.home)
	}
	opts := scanOptions(h, level)
	if a.rt.isTTY(a.stderr) {
		opts.Progress = func(msg string) { fmt.Fprintf(a.stderr, "\r\033[K%s", msg) }
	}
	p, err := scan.Run(a.ctx, opts)
	if opts.Progress != nil {
		fmt.Fprint(a.stderr, "\r\033[K")
	}
	if err != nil {
		// Home itself unusable, including a home that is no longer the
		// directory approved above, is a home error like the guard's.
		return plan.Plan{}, scanError(err)
	}
	return p, nil
}

// scanError maps an error from scan.Run to a CLI error.
func scanError(err error) error {
	var he *policy.HomeError
	if errors.As(err, &he) {
		return fail(codeHome, "%v", err)
	}
	return fail(codeScan, "%v", err)
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
						return a.emit(doc, func(w io.Writer) error {
							if h.fromEnv {
								fmt.Fprintf(w, "Home: %s\n\n", h.home)
							}
							return output.ExplainItem(w, it, r)
						})
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
			// version needs no home, so without KIPPLE_HOME it prints none.
			// With KIPPLE_HOME set every output names the effective home
			// (spec "Test home override"), so a refused one is an error here
			// as in every other command.
			if a.rt.getenv("KIPPLE_HOME") != "" {
				home, _, _, err := resolveHome(a.rt)
				if err != nil {
					return err
				}
				doc.Home = home
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

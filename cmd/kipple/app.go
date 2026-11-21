package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rbutera/kipple/cmd/internal/launch"
	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/config"
	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/procs"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/trash"
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
	// codeMountsUnavailable: mount points cannot be determined (Linux), so
	// apply moves nothing.
	codeMountsUnavailable = "mounts_unavailable"
	// codeUnsupportedPlatform: kipple runs on macOS and Linux only.
	codeUnsupportedPlatform = "unsupported_platform"
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
	// mounts, in tests, replaces the platform's mount oracle.
	mounts policy.MountOracle
	// goos, in tests, chooses the platform whose catalogue, state folder
	// and user folders kipple uses ("" means the running one).
	goos string
	// beforeScan, when set, runs after home is resolved and guarded and
	// before the scan opens it. Tests use it to swap the directory.
	beforeScan func(home string)
	// tui runs the interactive front end (tui.Run when nil). Tests drive
	// the model headlessly instead.
	tui func(cfg tui.Config, in io.Reader, out io.Writer) (tui.Outcome, error)
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
                    (default ~/Library/Application Support/kipple on macOS,
                    ~/.local/state/kipple on Linux)`

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
	fmt.Fprintf(a.stderr, "kipple: %s\n", output.SafeText(ce.msg))
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
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			if goos := a.goos(); !launch.Supported(goos) && c.Name() != "version" && c.Name() != "help" {
				return fail(codeUnsupportedPlatform, "kipple runs on macOS and Linux only, not on %s; nothing was read or changed", goos)
			}
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

// homeEnv is the resolved home and its config (launch.Home).
type homeEnv = launch.Home

// launch is the part of the runtime environment home resolution uses.
func (rt runtimeEnv) launch() launch.Runtime {
	return launch.Runtime{Getenv: rt.getenv, UserHome: rt.userHome, RealHomes: rt.realHomes, GuardFS: rt.guardFS, Mounts: rt.mounts, GOOS: rt.goos}
}

// launchErr maps a launch error to a CLI error with the same code.
func launchErr(err error) error {
	var le *launch.Error
	if errors.As(err, &le) {
		return fail(le.Code, "%s", le.Msg)
	}
	return err
}

// goos is the platform kipple acts as: the running one, or a test's.
func (a *app) goos() string {
	if a.rt.goos != "" {
		return a.rt.goos
	}
	return rules.Platform()
}

func (a *app) resolve() (homeEnv, error) {
	h, err := launch.Resolve(a.rt.launch())
	if err != nil {
		return homeEnv{}, launchErr(err)
	}
	return h, nil
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

// runScan scans home at the effective level.
func (a *app) runScan(h homeEnv) (plan.Plan, error) {
	return a.runScanAt(h, a.effectiveLevel(h.Config))
}

// runScanAt scans home at level.
func (a *app) runScanAt(h homeEnv, level string) (plan.Plan, error) {
	if a.rt.beforeScan != nil {
		a.rt.beforeScan(h.Path)
	}
	opts := h.ScanOptions(level, version)
	if a.rt.isTTY(a.stderr) {
		opts.Progress = func(msg string) { fmt.Fprintf(a.stderr, "\r\033[K%s", output.SafeText(msg)) }
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
	for _, r := range append(rules.CatalogueFor(rules.PlatformDarwin), rules.CatalogueFor(rules.PlatformLinux)...) {
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
			rs := h.Rules()
			doc := plan.RulesDocument{SchemaVersion: plan.SchemaVersion, Home: h.Path, Rules: rs}
			return a.emit(doc, func(w io.Writer) error {
				if h.FromEnv {
					fmt.Fprintf(w, "Home: %s\n\n", output.SafeText(h.Path))
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
			rs := h.Rules()
			for _, r := range rs {
				if r.ID == id {
					doc := explainDocument{SchemaVersion: plan.SchemaVersion, Home: h.Path, Rule: r}
					return a.emit(doc, func(w io.Writer) error {
						if h.FromEnv {
							fmt.Fprintf(w, "Home: %s\n\n", output.SafeText(h.Path))
						}
						return output.ExplainRule(w, r)
					})
				}
			}
			if ps := rules.PlatformsOf(id); len(ps) > 0 {
				var names []string
				for _, p := range ps {
					names = append(names, rules.PlatformName(p))
				}
				return usageErr("rule %s exists on %s only, not on %s (kipple rules lists this platform's rules)", id, strings.Join(names, " and "), rules.PlatformName(h.GOOS))
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
						doc := explainDocument{SchemaVersion: plan.SchemaVersion, Home: h.Path, Rule: r, Item: &it}
						return a.emit(doc, func(w io.Writer) error {
							if h.FromEnv {
								fmt.Fprintf(w, "Home: %s\n\n", output.SafeText(h.Path))
							}
							return output.ExplainItem(w, it, r)
						})
					}
				}
			}
			return fail(codeNotFound, "no item %q in a scan of %s", id, h.Path)
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
	Date          string `json:"date,omitempty"`
	SchemaVersion int    `json:"schema_version"`
	// TrashFallback is set when the build has Apple's Trash API fallback
	// (cgo on macOS), used when ~/.Trash cannot be opened.
	TrashFallback bool   `json:"trash_fallback"`
	Home          string `json:"home,omitempty"`
	// OS and Arch are the platform the binary was built for, in Go's
	// names (darwin, linux; amd64, arm64).
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and document schema version",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			doc := versionDocument{Version: version, Commit: commit, Date: date, SchemaVersion: plan.SchemaVersion, TrashFallback: trash.FinderAvailable, OS: runtime.GOOS, Arch: runtime.GOARCH}
			// version needs no home, so without KIPPLE_HOME it prints none.
			// With KIPPLE_HOME set every output names the effective home
			// (spec "Test home override"), so a refused one is an error here
			// as in every other command.
			if a.rt.getenv("KIPPLE_HOME") != "" {
				home, _, _, err := launch.ResolveHome(a.rt.launch())
				if err != nil {
					return launchErr(err)
				}
				doc.Home = home
			}
			return a.emit(doc, func(w io.Writer) error {
				built := ""
				if doc.Date != "" {
					built = ", built " + doc.Date
				}
				var trashLine string
				switch {
				case doc.OS != "darwin":
					trashLine = "Trash: ~/.local/share/Trash (freedesktop.org)"
				case doc.TrashFallback:
					trashLine = "Trash fallback (Apple's Trash API): built in"
				default:
					trashLine = "Trash fallback (Apple's Trash API): not built in (built without cgo): without Full Disk Access, clean --apply stops with trash_access"
				}
				_, err := fmt.Fprintf(w, "kipple %s (commit %s%s, schema_version %d)\nPlatform: %s/%s\n%s\n", doc.Version, doc.Commit, built, doc.SchemaVersion, doc.OS, doc.Arch, trashLine)
				if err == nil && doc.Home != "" {
					_, err = fmt.Fprintf(w, "home %s\n", output.SafeText(doc.Home))
				}
				return err
			})
		},
	}
}

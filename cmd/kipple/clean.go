package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rbutera/kipple/cmd/internal/launch"
	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
)

func (a *app) store(h homeEnv) (receipt.Store, error) {
	s, err := launch.Store(a.rt.launch(), h)
	return s, launchErr(err)
}

// parseIDs splits --only values on commas and checks each is an item ID.
func parseIDs(values []string) ([]string, error) {
	var ids []string
	for _, v := range values {
		for _, id := range strings.Split(v, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if !itemIDPattern.MatchString(id) {
				return nil, usageErr("--only: %q is not an item ID (12 characters a-z and 2-7, as printed by kipple scan)", id)
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// readPlan reads a plan document from path, or from stdin for "-". It must
// be a regular file (or stdin), at most plan.MaxDocumentSize bytes.
func (a *app) readPlan(path string) (plan.Plan, error) {
	var r io.Reader
	if path == "-" {
		if a.rt.stdin == nil {
			return plan.Plan{}, fail(codePlan, "no standard input to read the plan from")
		}
		r = a.rt.stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return plan.Plan{}, fail(codePlan, "cannot read the plan: %v", err)
		}
		defer func() { _ = f.Close() }()
		if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
			return plan.Plan{}, fail(codePlan, "%s is not a regular file", path)
		}
		r = f
	}
	p, err := plan.Decode(r)
	if err != nil {
		return plan.Plan{}, fail(codePlan, "%v", err)
	}
	return p, nil
}

func (a *app) cleanCommand() *cobra.Command {
	var (
		doApply, yes, permanent bool
		planFile                string
		only                    []string
	)
	c := &cobra.Command{
		Use:   "clean",
		Short: "Move the selected items to the Trash (a dry run unless --apply)",
		Long: `clean lists exactly what scan lists and moves nothing, unless --apply is
given. With --apply every item is authorised again, its conditions are
re-checked and it is confirmed to be the object that was scanned,
immediately before it is moved to the Trash; anything that fails a check is
skipped with a reason and the run exits 4. A receipt is written for every
--apply run (see kipple history and kipple restore).

--only <id,...> acts only on those items, including manual and review items
(never info items). --plan <file|-> acts on a plan from kipple scan --json;
it is untrusted: items are matched by ID against a scan made now, and an
item whose path, identity or rule root changed is refused. --permanent
(delete instead of trashing) is not available in this release.

On a terminal, --apply asks for confirmation unless --yes is given. With
--json, or when stdout is not a terminal, it never prompts: --apply is the
consent.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			if permanent && !apply.PermanentAvailable {
				return usageErr("%v", apply.ErrPermanentUnavailable)
			}
			if permanent && !doApply {
				return usageErr("--permanent needs --apply")
			}
			ids, err := parseIDs(only)
			if err != nil {
				return err
			}
			h, err := a.resolve()
			if err != nil {
				return err
			}
			level := a.effectiveLevel(h.Config)
			var src *plan.Plan
			if planFile != "" {
				p, err := a.readPlan(planFile)
				if err != nil {
					return err
				}
				if p.Home != h.Path {
					return fail(codePlan, "the plan is for home %s, not %s", p.Home, h.Path)
				}
				// A plan file never raises the level: items it selects at a
				// higher level than this invocation allows are skipped with
				// their tier reason by the fresh scan at this level.
				if p.Level != rules.LevelSafe && p.Level != rules.LevelReview {
					return fail(codePlan, "the plan's level %q is not safe or review", p.Level)
				}
				src = &p
			}
			scanned := time.Now()
			fresh, err := a.runScanAt(h, level)
			if err != nil {
				return err
			}
			target := fresh
			if src != nil {
				target = *src
			}
			store, err := a.store(h)
			if err != nil {
				return err
			}
			opts := a.applyOptions(h, store)
			opts.Plan = target
			opts.Only = ids
			opts.Permanent = permanent
			opts.ChecksSince = scanned
			if src != nil {
				opts.Fresh = &fresh
			}
			// A plan file is re-derived and re-checked: its dry run shows
			// what apply would really do, not the file's selection.
			if !doApply && src != nil {
				dry := opts
				dry.DryRun = true
				res, err := apply.Run(a.ctx, dry)
				if err != nil {
					return a.applyError(err)
				}
				return a.emit(res, func(w io.Writer) error { return output.CleanResult(w, res) })
			}
			if !doApply {
				p := apply.Preview(target, ids)
				return a.emit(p, func(w io.Writer) error {
					if err := output.Plan(w, p, titles()); err != nil {
						return err
					}
					_, err := fmt.Fprintln(w, output.DryRunLine)
					return err
				})
			}
			if a.rt.isTTY(a.stdout) && !a.json && !yes {
				dry := opts
				dry.DryRun = true
				would, err := apply.Run(a.ctx, dry)
				if err != nil {
					return a.applyError(err)
				}
				ok, err := a.confirm(would, permanent)
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(a.stdout, "Nothing was moved.")
					a.exit = exitNothing
					return nil
				}
			}
			if a.rt.isTTY(a.stderr) {
				opts.Progress = func(msg string) { fmt.Fprintf(a.stderr, "\r\033[K%s", output.SafeText(msg)) }
			}
			res, err := apply.Run(a.ctx, opts)
			if opts.Progress != nil {
				fmt.Fprint(a.stderr, "\r\033[K")
			}
			if err != nil {
				ae := a.applyError(err)
				var ce *cliError
				if errors.As(ae, &ce) && errors.Is(err, apply.ErrReceipt) {
					ce.result = &res
					if !a.json {
						_ = output.CleanResult(a.stderr, res)
					}
				}
				return ae
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(a.stderr, "kipple: warning: %s\n", output.SafeText(w))
			}
			a.exit = apply.ExitCode(res)
			return a.emit(res, func(w io.Writer) error { return output.CleanResult(w, res) })
		},
	}
	f := c.Flags()
	f.BoolVar(&doApply, "apply", false, "move the selected items (without it, clean is a dry run)")
	f.BoolVar(&yes, "yes", false, "do not ask for confirmation on a terminal")
	f.BoolVar(&permanent, "permanent", false, "not available in this release (items always go to the Trash)")
	f.StringVar(&planFile, "plan", "", "act on this plan (from kipple scan --json; - reads stdin)")
	f.StringSliceVar(&only, "only", nil, "act only on these item IDs (comma-separated or repeated)")
	return c
}

// applyOptions are the apply options every apply path (clean, tui)
// starts from; the caller sets the plan and what to act on.
func (a *app) applyOptions(h homeEnv, store receipt.Store) apply.Options {
	return h.ApplyOptions(store, version, a.rt.procs, a.rt.saveReceipt)
}

// trashAccessHelp is the message for apply.ErrTrashAccess.
const trashAccessHelp = "kipple cannot use your Trash folder: macOS lets only apps with Full Disk Access open it, so nothing was moved. " +
	"Grant Full Disk Access to your terminal (System Settings > Privacy & Security > Full Disk Access) or run kipple from a terminal that has it"

// applyError maps an error from apply.Run or apply.Restore to a CLI error.
func (a *app) applyError(err error) error {
	if errors.Is(err, apply.ErrTrashAccess) {
		return fail(codeTrashAccess, "%s (%v)", trashAccessHelp, err)
	}
	if errors.Is(err, apply.ErrReceipt) {
		return fail(codeReceipt, "%v", err)
	}
	return fail(codeApply, "%v", err)
}

// confirm shows what apply would move (from a dry run of the same
// pipeline) and asks for y on the terminal.
func (a *app) confirm(would plan.Result, permanent bool) (bool, error) {
	var bytes int64
	for _, m := range would.Moved {
		bytes += m.Bytes
	}
	n, total := len(would.Moved), output.Bytes(bytes)
	if permanent {
		fmt.Fprintf(a.stdout, "Delete %d safe items (%s) permanently, not to the Trash? This cannot be undone. [y/N] ", n, total)
	} else {
		fmt.Fprintf(a.stdout, "Move %d items (%s) to the Trash? [y/N] ", n, total)
	}
	if a.rt.stdin == nil {
		return false, nil
	}
	line, err := bufio.NewReader(a.rt.stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
}

func (a *app) historyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "List clean receipts, newest first",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			h, err := a.resolve()
			if err != nil {
				return err
			}
			store, err := a.store(h)
			if err != nil {
				return err
			}
			list, errs := store.List()
			doc := plan.History{SchemaVersion: plan.SchemaVersion, Home: h.Path, ReceiptsDir: store.Dir(), Receipts: []plan.Receipt{}, Warnings: []string{}}
			for _, e := range list {
				doc.Receipts = append(doc.Receipts, e.Receipt)
			}
			for _, err := range errs {
				doc.Warnings = append(doc.Warnings, err.Error())
				fmt.Fprintf(a.stderr, "kipple: warning: %s\n", output.SafeText(err.Error()))
			}
			return a.emit(doc, func(w io.Writer) error {
				if h.FromEnv {
					fmt.Fprintf(w, "Home: %s\n\n", output.SafeText(h.Path))
				}
				return output.History(w, doc)
			})
		},
	}
}

func (a *app) restoreCommand() *cobra.Command {
	var (
		doApply bool
		only    []string
	)
	c := &cobra.Command{
		Use:   "restore <receipt-id|item-id>",
		Short: "Put items from a receipt back where they were (a dry run unless --apply)",
		Long: `restore moves items recorded in a receipt from the Trash back to their
original paths. It is a dry run unless --apply is given. An item is put back
only if its original folder still exists, the original path is free, the
path passes the safety policy for the item's rule, and the Trash entry is
the very item kipple moved there; otherwise it is skipped with a reason
(destination_exists, trash_item_missing, ...) and the run exits 4.

The argument is a receipt ID (from kipple history) or an item ID, which
restores that item from the newest receipt that moved it. Only receipts in
kipple's own receipts folder are used; receipt files elsewhere are not
accepted.`,
		Args: exactArgs(1, "one receipt ID or item ID"),
		RunE: func(_ *cobra.Command, args []string) error {
			ids, err := parseIDs(only)
			if err != nil {
				return err
			}
			h, err := a.resolve()
			if err != nil {
				return err
			}
			store, err := a.store(h)
			if err != nil {
				return err
			}
			e, inStore, extra, err := a.findReceipt(store, args[0])
			if err != nil {
				return err
			}
			asked := append([]string(nil), ids...)
			ids = append(ids, extra...)
			opts := apply.RestoreOptions{
				Env:     h.PolicyEnv(),
				Rules:   h.Rules(),
				Receipt: e,
				Only:    ids,
				Apply:   doApply,
			}
			if inStore {
				opts.Store = &store
			}
			res, err := apply.Restore(a.ctx, opts)
			if err != nil {
				return a.applyError(err)
			}
			a.exit = apply.ExitCode(res)
			// The dry run's hint repeats this request, so restoring one item
			// never suggests a command that restores the whole receipt.
			again := "kipple restore " + args[0]
			if len(asked) > 0 {
				again += " --only " + strings.Join(asked, ",")
			}
			again += " --apply"
			return a.emit(res, func(w io.Writer) error { return output.RestoreResultWith(w, res, again) })
		},
	}
	c.Flags().BoolVar(&doApply, "apply", false, "move the items back (without it, restore is a dry run)")
	c.Flags().StringSliceVar(&only, "only", nil, "restore only these item IDs (comma-separated or repeated)")
	return c
}

// findReceipt resolves restore's argument. It returns the receipt, whether
// it came from the store (and may be annotated), and the item IDs an item-ID
// argument adds to --only.
func (a *app) findReceipt(store receipt.Store, arg string) (receipt.Entry, bool, []string, error) {
	switch {
	case receipt.ValidID(arg):
		e, err := store.Load(arg)
		if err != nil {
			return e, false, nil, fail(codeNotFound, "%v", err)
		}
		return e, true, nil, nil
	case itemIDPattern.MatchString(arg):
		list, _ := store.List()
		for _, e := range list {
			for _, entries := range [][]plan.MovedItem{e.Receipt.Moved, e.Receipt.Pending, e.Receipt.Displaced} {
				for _, m := range entries {
					if m.ID == arg && m.RestoredAt == nil && !m.Permanent {
						return e, true, []string{arg}, nil
					}
				}
			}
		}
		return receipt.Entry{}, false, nil, fail(codeNotFound, "no receipt in %s has item %s waiting to be restored", store.Dir(), arg)
	default:
		return receipt.Entry{}, false, nil, usageErr("restore takes a receipt ID (from kipple history) or an item ID; receipt files are not accepted, only receipts in %s", store.Dir())
	}
}

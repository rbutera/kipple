package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/tui"
)

func (a *app) tuiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Review the scan and move items to the Trash interactively",
		Long: `tui scans, then shows every item grouped by rule with its tier and size,
and explains the highlighted one. Space selects or unselects an item, a shows
the total and asks for confirmation, y moves the selection to the Trash, and
any other key cancels. Info items cannot be selected; review and manual items
can be chosen as with kipple clean --only, and anything else that blocks an
item (too recent, uncommitted work, protected files inside) keeps it
unselectable. Items are moved by the same code as kipple clean --apply, which
checks each one again just before it moves and writes a receipt. There is no
permanent delete in the TUI.

tui needs a terminal. It has no JSON output: agents use kipple scan --json
and kipple clean --apply --json. NO_COLOR turns colours off.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.json {
				return usageErr("kipple tui is interactive and has no JSON output; use kipple scan --json and kipple clean --apply --json")
			}
			if !a.rt.isTTY(a.stdout) {
				return usageErr("kipple tui needs a terminal; use kipple scan and kipple clean --apply instead")
			}
			h, err := a.resolve()
			if err != nil {
				return err
			}
			store, err := a.store(h)
			if err != nil {
				return err
			}
			cfg := tui.Config{
				Ctx:       a.ctx,
				Scan:      scanOptions(h, a.effectiveLevel(h.cfg)),
				Apply:     a.applyOptions(h, store),
				Color:     a.rt.getenv("NO_COLOR") == "" && a.rt.getenv("TERM") != "dumb",
				Version:   version,
				HomeLabel: homeLabel(h.home, a.rt.getenv("KIPPLE_HOME") != ""),
			}
			runTUI := a.rt.tui
			if runTUI == nil {
				runTUI = tui.Run
			}
			out, err := runTUI(cfg, a.rt.stdin, a.stdout)
			if err != nil {
				return fail(codeInternal, "%v", err)
			}
			switch {
			case out.Err != nil && out.Applied:
				return a.applyError(out.Err)
			case out.Err != nil:
				return scanError(out.Err)
			case !out.Applied:
				_, err := fmt.Fprintln(a.stdout, "Nothing was moved.")
				return err
			}
			// The full-screen view is gone once the TUI exits, so the result
			// is printed again for the scrollback.
			for _, w := range out.Result.Warnings {
				fmt.Fprintf(a.stderr, "kipple: warning: %s\n", output.SafeText(w))
			}
			a.exit = apply.ExitCode(out.Result)
			return output.CleanResult(a.stdout, out.Result)
		},
	}
}

// homeLabel is how the TUI header names the home folder: "~" for the real
// home, and for a KIPPLE_HOME its last two folders, marked as such.
func homeLabel(home string, override bool) string {
	if !override {
		return "~"
	}
	parts := strings.Split(strings.Trim(home, "/"), "/")
	if len(parts) > 2 {
		return "KIPPLE_HOME …/" + strings.Join(parts[len(parts)-2:], "/")
	}
	return "KIPPLE_HOME " + home
}

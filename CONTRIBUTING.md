# Contributing to kipple

Thanks for helping. kipple moves files out of people's home folders, so the
bar for changes is "provably safe", and the rules below exist to keep it
there. Please read [docs/safety.md](docs/safety.md) and
[AGENTS.md](AGENTS.md) (the same rules, written for coding agents) before
you start.

## The one rule: fixtures only

**No test, benchmark, script or manual check may create, change, move or
delete anything in a real home folder, yours included.**

- Every test that touches the file system builds a fixture home with
  `testutil.NewHome(t, spec)`, which creates it under `t.TempDir()` and sets
  `KIPPLE_HOME` and `KIPPLE_STATE_DIR` to it.
- Every package with tests has a `TestMain` that calls
  `testutil.GuardedMain`, which refuses to run if `KIPPLE_HOME` is not set
  to a fixture. `make check-no-real-home` fails the build if a package
  forgets it, or if library code (anything outside `cmd/`) looks up the real
  home folder.
- Never run `kipple clean --apply`, `kipple restore --apply` or the TUI's
  apply against your real home "to check it works". Point `KIPPLE_HOME` at a
  fixture instead: `scripts/agent-flow-check.sh` shows how.
- Looking is fine. `ls` and `stat` on a real cache folder to see what it
  holds are read-only.

## Building and checking

You need Go 1.23 and the Xcode command line tools (the Trash fallback uses
cgo), plus [golangci-lint](https://golangci-lint.run) v2.

```sh
command make check   # gofmt, go vet, golangci-lint, no-real-home, tests with -race (both builds), build
make agent-flow-check  # the README's agent commands against a fixture
make release-snapshot  # the release archives into dist/, unsigned (needs goreleaser)
```

`make check` must pass before you open a pull request. Use
`GOTOOLCHAIN=local` with `go get` and `go mod tidy` so the module stays on
`go 1.23`. New dependencies need a maintainer's agreement first: the
approved set is cobra, Bubble Tea, bubbles, lipgloss, BurntSushi/toml,
invopop/jsonschema, santhosh-tekuri/jsonschema (tests) and golang.org/x/sys.

Tests are table-driven where it helps, and a test that covers a spec
scenario (under `openspec/changes/*/specs/`) is named after it. Golden files
live in `testdata/` beside their test.

## Adding a rule

A rule is a Go value in the catalogue, not configuration: that keeps the
list reviewable and impossible to extend at run time.

1. **Add the rule** to `internal/rules/catalogue.go`, in its tier's section:

   ```go
   {
       ID:        "example.cache",            // tool.thing, lower case
       Title:     "Example tool cache",
       Tier:      TierSafe,                   // safe, review, manual or info
       Roots:     []string{".example/cache"}, // home-relative; the root itself is never moved
       Match:     children,                   // every direct child is an item
       Owner:     Owner{Name: "Example"},
       What:      "Downloads the example tool keeps so its next run starts faster.",
       IfRemoved: "The example tool downloads them again the next time it needs them.",
   },
   ```

   `What` and `IfRemoved` are one sentence each, written for someone who has
   never heard of the tool. Add `Conditions` (minimum age, idle project,
   clean and pushed git, owner not running) when the item is only safe under
   them, and `OwnerCommand` for an `info` rule. Pick the tier honestly:
   anything that is expensive to rebuild is `review`, anything that holds
   work or irreplaceable data is `manual` or `info`.
2. **Run `go test ./internal/rules`.** `rules.Validate` checks every rule: a
   valid ID and tier, a title and owner, `What` and `IfRemoved` as single
   sentences ending in a full stop, roots under home and off the denylist,
   no overlap with another rule's roots, and for an info rule an owner
   command and roots that are on the denylist (so no other rule can move
   them). Add the new ID and tier to `TestCatalogueCount`.
3. **Add a scan test** in `internal/scan` with a fixture that holds an item
   for the rule (and, if it has conditions, one that fails each condition),
   asserting it is listed with the right tier, selection and reasons.
   `TestAddARuleSteps` in `internal/scan/addrule_test.go` walks these steps
   on the example rule above.
4. **Update the golden catalogue** with
   `go test ./internal/rules -run TestGoldenRulesJSON -update`, and read the
   diff.
5. Mention the location in the README's "What it cleans" table if people
   would look for it there, then run `command make check`.

## Reporting a path that escaped

If kipple lists, moves or restores something the policy should have refused,
that is a security bug. Report it privately as [SECURITY.md](SECURITY.md)
describes; use the "Path escaped the allowlist" issue template only for
cases that cannot cause data loss or are already public.

## Review process

1. Open a pull request against `main` and fill in the template. It asks
   whether any test or script touches a real home folder; the answer must be
   no.
2. CI runs lint, vet, the full test suite with the race detector, the
   no-real-home check, the README agent flow and a build on macOS. It must be
   green.
3. A maintainer reviews every change to `internal/policy`, `internal/apply`,
   `internal/trash`, `internal/receipt` or the rule catalogue line by line,
   with the safety model in mind, and may ask for a red-team pass (someone
   trying to make the change move something it should not) before merging.
4. Small, focused commits with descriptive messages are easier to review
   than one large one.

By contributing you agree that your work is released under the
[MIT licence](LICENSE).

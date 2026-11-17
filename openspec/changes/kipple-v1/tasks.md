# Tasks

Waves for the /wave skill. Each wave ends with `make check` green (lint, vet, race tests, no-real-home check, build). Every filesystem test runs against a `testutil.NewHome` fixture; nothing ever touches the real home.

## 1. Wave 1: scaffold, policy, catalogue, fixtures, CI

- [ ] 1.1 Initialise `go.mod` (`github.com/rbutera/kipple`, Go 1.23), `Makefile` (`build`, `test`, `lint`, `check`, `check-no-real-home`), `.golangci.yml`, `.gitignore`, `.editorconfig`; verify `make check` passes on an empty tree
- [ ] 1.2 Add `LICENSE` (MIT, Rai Butera 2026), stub `README.md`, `SECURITY.md`; verify files exist and licence text is the OSI MIT text
- [ ] 1.3 Implement `internal/testutil`: `NewHome(t, Spec)` building files with size and mtime, symlinks, `git init` repos with commits and optional bare upstream; `DirTrasher`; `GuardRealHome(t)`; verify with `testutil` self-tests that a spec round-trips and the guard fails when `KIPPLE_HOME` equals the real home
- [ ] 1.4 Implement `internal/policy` (`Env`, `Decision`, `Authorize`) with the full denylist from design D3, home-only, different-device, symlink chain, rule-root and allowlist-descent checks, euid-0 refusal helper; verify with table tests covering every refusal reason in `specs/safety-policy` (one test case per scenario, plus symlink-in-middle, symlink-as-item, `..` segments, trailing slash)
- [ ] 1.5 Implement `internal/rules` types and the full v1 catalogue from `specs/rule-catalogue` with explanations and owners; verify with `TestCatalogueInvariants` (roots under home, not denylisted, no duplicate roots, non-empty explanations) and a golden `rules.json`
- [ ] 1.6 Implement `internal/config` (TOML at `~/.config/kipple/config.toml`, keys `protect`, `project_roots`, `level`), additive-only; verify a test that an attempt to allow a denylisted path errors and that `protect` entries are refused by `policy.Authorize` with `user_protected`
- [ ] 1.7 Add `.github/workflows/ci.yml` (macos-latest: golangci-lint, vet, `go test -race ./...`, no-real-home check, `go build ./...`, `go build -tags gui ./...` as a separate job); verify the workflow file passes `actionlint` locally
- [ ] 1.8 Add `make check-no-real-home` script that fails if `os.UserHomeDir` appears outside `cmd/` or if any test lacks the guard; verify by introducing a violation in a temp copy and seeing it fail

## 2. Wave 2: scan, plan, sizing, read-only CLI

- [ ] 2.1 Implement `internal/sizes` (lstat walk, logical and allocated bytes, inode dedupe, partial status on permission errors, context cancellation); verify with fixture tests for hardlink dedupe, symlink not followed, unreadable subdir yields `partial`
- [ ] 2.2 Implement `internal/plan` types (Plan, Item, RuleStatus, Totals, `SchemaVersion = 1`), stable ID function, JSON encode/decode, JSON Schema generation via `invopop/jsonschema`; verify with golden plan JSON, ID determinism test, and a test that a generated plan validates against the generated schema (`santhosh-tekuri/jsonschema`)
- [ ] 2.3 Implement `internal/scan` (per-rule workers, root globbing one level, exclusions, children-only, age conditions, `absent` status, selection by level, reasons); verify with fixture tests: default level selects only safe, review level selects review items whose conditions pass, info never selected, absent roots exit 0, fixture checksum unchanged after scan
- [ ] 2.4 Implement `internal/output` (byte formatting, grouped table, dry-run footer naming `kipple clean --apply`); verify with golden text output for a fixture plan
- [ ] 2.5 Implement `cmd/kipple` with cobra: root (`--json`, `--level`, euid check, `KIPPLE_HOME`/`KIPPLE_STATE_DIR`), `scan`, `rules`, `explain`, `schema`, `version`, exit code mapping (0/1/2/3/4), JSON error envelope, stdout/stderr discipline; verify with CLI tests that `scan --json` parses, `--bogus` exits 2, stdout has nothing but JSON under `--json`, `explain xcode.deriveddata` shows both sentences
- [ ] 2.6 Document the plan schema and item reasons in `docs/plan-schema.md`; verify the doc's example validates against `kipple schema plan`

## 3. Wave 3: apply, Trash, receipts, restore, git and process conditions

- [ ] 3.1 Implement `internal/trash`: `Trasher` interface, darwin cgo `NSFileManager trashItemAtURL` implementation returning the destination, `DirTrasher`, non-cgo fallback error; verify `DirTrasher` with fixture tests including name collision, and a darwin-only test that trashes a file created inside a fixture under `KIPPLE_HOME` and finds it at the returned path
- [ ] 3.2 Implement `internal/receipt` (write, list newest first, annotate restored, `KIPPLE_STATE_DIR`); verify with fixture tests and a golden receipt JSON that validates against `kipple schema receipt`
- [ ] 3.3 Implement `internal/gitstate` (`Inspect` with 5 s timeout, states `clean_pushed|dirty|unpushed|no_upstream|not_a_repo|git_unavailable`, worktree listing); verify with fixture repos for every state including a bare upstream for `clean_pushed` and a PATH without git for `git_unavailable`
- [ ] 3.4 Implement `internal/procs` (`RunningBundleIDs` via `lsappinfo`, `RunningNames` via `pgrep -x`, `ProcessCwds` via `lsof -p -Fn` for known agent names, all with injectable runners); verify with fake runners for found, not found, and command missing (unknown)
- [ ] 3.5 Implement project artifact rules in `internal/scan` (marker adjacency, project roots, bounded depth, idle 30 days, git condition, `at_risk`, reasons `project_active|git_dirty|...`); verify with fixture tests for every scenario in `specs/project-artifacts` including `target` without `Cargo.toml` and a project under `Desktop`
- [ ] 3.6 Implement `internal/apply` (re-authorise, re-check conditions, lstat type check, owner running skip, `info_tier`, `permanent_not_allowed`, `--only`, plan-file input, receipt, result document with `moved|skipped|untouched`, exit code mapping); verify with fixture tests for every scenario in `specs/clean-apply` using `DirTrasher`, plus a TOCTOU test that modifies a project between scan and apply
- [ ] 3.7 Wire `kipple clean` (dry run default, `--apply`, `--yes`, `--permanent`, `--plan`, `--only`, TTY confirmation only when TTY and not `--json`), `kipple history`, `kipple restore` (dry run default, `destination_exists`, `trash_item_missing`); verify with CLI tests against fixtures, including piped `clean --apply --json` not prompting and `restore --apply` round trip
- [ ] 3.8 Add `docs/safety.md` describing the gate, denylist, tiers, re-check, receipts, and what kipple refuses to do; verify the denylist in the doc is generated from `policy.Denylist()` by a test that diffs them

## 4. Wave 4: TUI, docs, release pipeline, v0.1.0

- [ ] 4.1 Implement `internal/tui` Bubble Tea model (grouped list, detail pane, space toggle with tier rules, `a`/`y` confirm, receipt summary, `q`) and `kipple tui`; verify with headless model tests sending key messages for toggle, info-item refusal, cancel, and apply via `DirTrasher`
- [ ] 4.2 Write the full `README.md` (pitch, safety model, install via release binary and `go install`, usage, "For agents" section with scan/inspect/subset-apply/restore flow, exit codes, schema, verification of signatures, comparison table to Mole/Dusty/ElfClean, roadmap) and `llms.txt`; verify by running the agent-section commands against a fixture in a CI step (`scripts/agent-flow-check.sh`)
- [ ] 4.3 Add `.goreleaser.yaml` (darwin arm64/amd64, universal binary, ldflags version/commit, checksums, archives, ad-hoc codesign hook) and `.github/workflows/release.yml` (on `v*` tags, waits for CI green on the same SHA, goreleaser, cosign keyless signing of `checksums.txt`); verify with `goreleaser release --snapshot --clean` locally and `actionlint`
- [ ] 4.4 Add `CONTRIBUTING.md` (fixture-only testing rule, how to add a rule with tests), issue templates for "path escaped the allowlist" and "new rule"; verify files exist and the add-a-rule steps work on a scratch rule in a test
- [ ] 4.5 Integration check: on a fixture home, run `kipple scan --json`, `kipple clean --apply --json --only <two ids>`, `kipple history --json`, `kipple restore <id> --apply`, assert exit codes 0/0/0/0 and the fixture equals its original checksum at the end; verify as `TestEndToEnd` in `cmd/kipple`

## 5. Wave 5: optional GUI

- [ ] 5.1 Implement `cmd/kipple-gui` with Fyne behind `//go:build gui`: scan view with progress, review tree by rule with checkboxes, sizes, explanation, apply button with confirmation showing total and Trash, done view with receipt summary and "Open Trash"; verify `go build -tags gui ./cmd/kipple-gui` succeeds and `go build ./...` without the tag pulls no Fyne packages (`go list -deps` check in test)
- [ ] 5.2 Share selection logic with the CLI (pre-selected set equals `scan --level safe`, info items disabled); verify with a non-UI test on the GUI view-model against a fixture
- [ ] 5.3 Add a GUI job to CI and a GUI `.app` zip to the release workflow; verify with `actionlint` and a snapshot release
- [ ] 5.4 Document the GUI in the README with a screenshot taken from a fixture scan; verify the image is committed and referenced

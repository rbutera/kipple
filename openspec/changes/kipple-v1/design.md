# Design

## Context

Greenfield Go project. See `proposal.md` for motivation and `research/elfclean.md` for the competitor analysis that shaped the safety posture. The development machine (latios) is shared with live agents and has a near-full disk: no test, benchmark or dev command may modify the real home folder. Everything runs against temp fixture homes.

## Goals / Non-Goals

**Goals:**
- One library, three front ends. The CLI, TUI and GUI contain no deletion logic of their own.
- A plan an agent can read, reason about, and hand back partially approved.
- Safety invariants that are enforced in one place and proven by table tests, not by convention.
- Deterministic, versioned JSON output.

**Non-Goals:**
- Reaching every last gigabyte. Refusing to delete something safe is the preferred failure.
- Replacing Mole's breadth (uninstaller, optimize, status). kipple is the narrow, trustworthy core.

## Decisions

### D1. Go over Rust
Go compiles in seconds, which matters when Opus agents iterate in waves; Bubble Tea is the most mature TUI toolkit on either side; cobra gives free completions and help; goreleaser produces universal macOS binaries and checksums with almost no config; one language covers CLI, TUI and GUI (Fyne). Rust's advantage was the `trash` crate; a 40-line cgo shim around `NSFileManager.trashItemAtURL` closes that gap and additionally returns the Trash location, which we need for receipts. Alternatives: Rust (slower agent loops, Tauri GUI needs a JS front end), Swift (not cross-buildable, no Rai familiarity).

### D2. Module layout
```
cmd/kipple/            cobra CLI; `kipple tui` lives here too (one binary to install)
cmd/kipple-gui/        Fyne app, //go:build gui, separate release artifact
internal/policy/       Authorize(root, rule, path) — the only gate
internal/rules/        Rule types + Catalogue() + per-rule explanations
internal/scan/         walks rule roots, sizes, applies conditions, emits plan.Items
internal/plan/         Plan/Item types, schema v1, JSON encode/decode, JSON Schema export
internal/apply/        executes a plan: re-validate, re-check, trash, receipt
internal/trash/        Trasher interface; darwin cgo NSFileManager impl; DirTrasher for tests
internal/receipt/      receipt files, listing, restore
internal/gitstate/     git status/upstream classification via exec git
internal/procs/        running-app detection (lsappinfo, pgrep)
internal/sizes/        allocated-size walker with inode dedupe
internal/config/       ~/.config/kipple/config.toml (additive only)
internal/output/       human renderer (tables, byte formatting)
internal/tui/          Bubble Tea model
internal/testutil/     fixture home builder, fake trasher, guards
```
Everything under `internal/` is importable by the three `cmd/` targets; a public `pkg/kipple` facade is deferred until the API has settled (v0.2).

### D3. The policy is the only gate
`policy.Authorize(ctx Env, rule Rule, path string) (Decision)` is called by scan (to decide what is listable) and again by apply immediately before each move. It checks, in order: path is absolute and clean; `path` descends from `Env.Home`; every component from `Env.Home` down to `path` is a real directory (lstat, never follow symlinks; a symlink anywhere in the chain is a refusal); `path` is not the rule root itself (roots are kept, children are moved); `path` does not match the denylist; `path` descends from one of the rule's roots. The denylist is a Go constant and cannot be shrunk by config; config adds entries. `Env.Home` is injected, never read from `os.UserHomeDir()` inside the library; the CLI sets it from `$KIPPLE_HOME` (hidden, for tests) or the real home. The policy package has no dependencies other than the standard library so it can be exhaustively table-tested.

Denylist (relative to home, prefix matches): `Documents`, `Desktop`, `Pictures`, `Movies`, `Music`, `Library/Mobile Documents`, `Library/CloudStorage`, `Library/Mail`, `Library/Messages`, `Library/Photos`, `Pictures/Photos Library.photoslibrary`, `Library/Keychains`, `Library/Preferences`, `Library/Application Support/MobileSync`, `Library/Containers` (whole), `Library/Group Containers`, `.ssh`, `.gnupg`, `.aws`, `.config/op`, `.ark`, `.claude/projects`, `.claude/memory`, `.claude/plans`, `.claude/skills`, `.claude/settings*.json`, `.codex/sessions`, `.codex/memory`, any path component equal to `.env` or starting with `.env.`, any `*.keychain*`, any `*.photoslibrary`. Also refused: the home directory itself, any path on a different device than home (external volumes, APFS volumes mounted under home).

### D4. Tiers and selection
Four tiers. `safe`: regenerates itself, pre-selected at every level. `review`: regenerates but costs time or needs a condition (idle project, clean git, 90-day DeviceSupport); listed at every level, pre-selected only with `--level review`. `manual`: never pre-selected, cleanable only by passing its ID to `--only` (Xcode Archives, Hugging Face hub, agent worktrees, old installers in Downloads). `info`: measured and reported, never movable by kipple, with the owner's command shown (Ollama models, Docker.raw, agent sessions, simulator runtimes). `--level safe` is the default.

### D5. Rule catalogue in Go, not YAML
Rules are Go values with typed fields: `ID`, `Title`, `Tier`, `Roots []string` (home-relative globs, one level of `*` allowed), `Match` (children, or extension filter for Downloads), `Conditions` (min age, requires idle project, requires clean git, skip if owner app running), `Owner` (tool name and process names), `What` and `IfRemoved` (one sentence each). A test asserts every root is under home, no root is on the denylist, no two rules claim the same root, every rule has non-empty explanations. `kipple rules --json` dumps the catalogue so agents and docs stay in sync. YAML would allow runtime extension, which is exactly the attack surface we do not want in v1.

### D6. Stable item IDs
`item.id = base32(sha256(rule.id + "\x00" + home-relative path))[:12]`. Deterministic across runs and machines with the same layout, so an agent can scan, decide, and pass IDs to `clean --only`. Items also carry `path`, `size_bytes`, `allocated_bytes`, `file_count`, `newest_mtime`, `tier`, `selected`, `reasons[]` (why it is or is not selected), and for project items a `git` block.

### D7. Sizing
Walk with `lstat`, never follow symlinks, sum `st_blocks*512` for allocated bytes and `st_size` for logical bytes, dedupe by `(dev, ino)` within a scan so hardlinked stores (pnpm, uv, Nix-style) are counted once. Scans run concurrently per rule with a bounded worker pool; a per-item `scan_status` of `complete` or `partial` records permission errors instead of failing the whole plan.

### D8. Apply pipeline
For each selected item: `policy.Authorize` again; re-run the rule's conditions (idle, git, running owner); `lstat` and refuse if the path is now a symlink or missing; call `Trasher.Trash(path)` which returns the Trash location; append to the receipt. Any refusal becomes a `skipped` entry with a machine-readable reason and the run exits 4 (partial). The receipt (`~/Library/Application Support/kipple/receipts/<RFC3339>-<short>.json`, dir overridable via `$KIPPLE_STATE_DIR`) records plan metadata, each moved item's original and trashed paths, sizes, and skips. `--permanent` is accepted only together with `--apply` and only acts on `safe` tier items; anything else is skipped with reason `permanent_not_allowed`.

### D9. Trash implementation
`trash.Trasher` interface; every implementation takes the item as a verified parent descriptor and its stored name, never a path. Wave 3 review changed the original plan (NSFileManager first) to a hybrid, because two facts pull in opposite directions: the move must act on the approved object (D16), which a path-based API cannot promise, and macOS protects `~/.Trash` with TCC, so without Full Disk Access (the default for a terminal) opening it fails with EPERM.
- First choice: `DirTrasher`, `renameatx_np(RENAME_EXCL)` from the verified parent descriptor into the verified `~/.Trash` descriptor (opened through the policy session), Finder-style collision names, never replacing, never copy+delete on EXDEV.
- When opening `~/.Trash` fails with EPERM/EACCES: `FinderTrasher`, a cgo shim around `NSFileManager trashItemAtURL`, made to act on the object: the path comes from `F_GETPATH` on the verified parent plus the stored name, is converted to a file reference URL, which must resolve back to that path with the approved (dev, ino) immediately before the call, and is trashed through the reference; afterwards the parent descriptor must no longer hold the object and the reference must resolve to the returned path. Only for the real home: with `KIPPLE_HOME` set there is no fallback (it would move fixture items into the user's real Trash).
- If both are refused, apply stops with error code `trash_access` and asks the user to grant Full Disk Access to their terminal. Restore likewise reaches Trash entries by descriptor, or by path when the Trash cannot be opened, with identity checks before and after.
- The receipt records the mover (`rename`, `nsfilemanager`, `delete` for `--permanent`) and each item's dev, ino, size and mtime, which restore requires the Trash entry to match.
- Builds without cgo or off macOS: `NewFinderTrasher` returns `ErrUnsupported` (no osascript fallback).

### D10. Git and process checks
`gitstate.Inspect(dir)` runs `git -C dir status --porcelain=v2 --branch` and `git -C dir rev-list --count @{upstream}..HEAD`, 5-second timeout, and classifies: `clean_pushed`, `dirty`, `unpushed`, `no_upstream`, `not_a_repo`, `git_unavailable`. Only `clean_pushed` satisfies the `requires clean git` condition. Worktrees: `git worktree list --porcelain` from the main repo. `procs.RunningBundleIDs()` parses `lsappinfo list`; `procs.RunningNames()` uses `pgrep -x` for CLI tools (`claude`, `codex`, `cursor-agent`). Both degrade to "unknown" which counts as running (refuse) for `review` items and as a warning for `safe` items.

### D11. CLI conventions
`--json` on every command: JSON on stdout only, progress and warnings on stderr, nothing else on stdout. Exit codes: 0 success, 1 error, 2 usage, 3 nothing to do, 4 partial (some items skipped or failed). No interactive prompt when stdout is not a TTY or `--json` is set; `--apply` is the consent, `--yes` only suppresses the TTY confirmation. `kipple schema plan|receipt|rules` prints JSON Schema for the documents, generated from the Go types via struct tags (`invopop/jsonschema`), so the schema cannot drift from the encoder. `kipple version --json` includes `schema_version`.

### D12. TUI
Bubble Tea with bubbles `list` and `viewport`: left pane items grouped by rule, right pane explanation and path; `space` toggles selection, `a` opens an apply confirmation showing the total and the word "Trash", `y` confirms, `q` quits. The TUI builds a plan via the same `scan` package and executes via `apply`; it cannot bypass tiers (`info` items are not selectable).

### D13. GUI
Fyne, `//go:build gui`, `cmd/kipple-gui`. Three views: scan (progress), review (checkbox tree by rule, sizes, explanation), done (receipt summary with "Open Trash"). Released as a separate `.app` zip; its CI job runs on macOS. Chosen over Wails (needs a JS front end) and SwiftUI (second language). It is last in the task order and v0.1.0 may ship without it.

### D14. Testing discipline
`testutil.NewHome(t, spec)` builds a fixture home in `t.TempDir()` from a declarative tree (files with sizes and mtimes, symlinks, `git init` repos with commits, optional fake upstream). Every test uses `KIPPLE_HOME` set to the fixture. `TestMain` in every package asserts `os.Getenv("KIPPLE_HOME") != realHome` and that `KIPPLE_STATE_DIR` is set. A `make check-no-real-home` target greps the source for `os.UserHomeDir` outside `cmd/` and fails CI if found elsewhere. Golden files for plan JSON and human output.

### D15. Release
goreleaser: `darwin/arm64` and `darwin/amd64` plus a universal binary via `universal_binaries`; `checksums.txt` signed with cosign keyless (GitHub OIDC, `sigstore/cosign-installer`), producing `checksums.txt.sig` and `.pem`. Release workflow triggers on `v*` tags and requires the CI workflow to be green on the same commit. Ad-hoc codesigning of the binary (`codesign -s -`) so Gatekeeper does not quarantine the download in the obvious way; Developer ID notarisation is a later step and documented as such.

## Risks / Trade-offs

- [Deleting a running app's cache causes glitches] → `safe` items whose owner bundle ID is running are skipped with reason `owner_running`; unknown owner is allowed with a warning.
- [`~/Library/Caches` contains things that are not caches (some apps misuse it)] → explicit exclusion list in the `caches.user` rule (`com.apple.bird`, `CloudKit`, `com.apple.cloudkit`, `com.apple.icloud*`, `Homebrew` which has its own rule) and the whole folder is `safe` only because Apple's own guidance says apps must tolerate its removal.
- [TOCTOU between scan and apply] → re-validate and re-check conditions immediately before each move; refuse if the path became a symlink or vanished; Trash makes the residual gap recoverable.
- [Go module cache is read-only and fails to move] → `pkg.go-mod` is `review` tier and the apply step chmods only within the authorised path after a second authorisation.
- [cgo complicates cross-compilation] → we only build on macOS runners; `DirTrasher` keeps `go test` cgo-free where possible.
- [Fyne build flakiness on CI] → GUI is a separate job and separate artifact; a GUI failure blocks the release workflow but not the CLI's correctness, and v0.1.0 can be cut from a commit without the GUI job if needed.
- [Catalogue paths that do not exist on a machine] → scanning a missing root is not an error; the rule is reported `absent`.

## Open Questions

- Whether to expose a public `pkg/kipple` facade in v0.1.0 or wait for v0.2.0 (deferred; does not change v1 behaviour).
- Homebrew tap naming (`rbutera/homebrew-tap`), after v0.1.0.

### D16. Amortised, race-free gate walks (added after Wave 1 review)
Wave 1 review found a race-only bypass: a same-user process swapping a directory two levels above the item mid-call could make a later `lstat` follow a symlink, and each `Authorize` call costs about 60 lstats plus one readdir per component. Wave 2 adds `policy.Session`: open home once with `O_DIRECTORY|O_NOFOLLOW`; collect protected identities once per session; cache each directory's stored-name set keyed by `(dev, ino)` and validated by `fstat` mtime/ctime before reuse; walk candidates with `openat(parentfd, name, O_NOFOLLOW|O_DIRECTORY)` and `fstat` each fd for type, device and identity, so every step resolves relative to an already-verified descriptor. A session never outlives one scan. Wave 3 apply runs the same walk fresh with no cache and moves with `renameat` (or `renameatx_np` with `RENAME_EXCL`) from the verified parent fd into a verified Trash fd, closing the check-then-move gap. This needs `golang.org/x/sys/unix` (approved).

### D17. Portability seams for a later Linux port (added before Wave 3)
Rai asked whether kipple can support Linux. Almost all of it already can: policy, Session, scan, plan, output, the CLI contract, the TUI and Fyne are pure Go. The platform-specific pieces are small and Wave 3 must keep them behind seams so a Linux port is new files, not a refactor:
- `internal/trash`: `Trasher` interface; `trash_darwin.go` (cgo `NSFileManager`), `trash_dir.go` (`DirTrasher`, all platforms). A later `trash_linux.go` implements the freedesktop Trash spec (`$XDG_DATA_HOME/Trash/{files,info}`) in pure Go.
- `internal/procs`: `Inspector` interface with injectable runners; `procs_darwin.go` (`lsappinfo`, `pgrep`, `lsof`). A later `procs_linux.go` reads `/proc`.
- `internal/apply`: the no-clobber move goes through one function `moveNoReplace(parentfd, name, destfd, destname)`; `move_darwin.go` uses `renameatx_np` with `RENAME_EXCL`, a later `move_linux.go` uses `renameat2` with `RENAME_NOREPLACE`.
- `internal/rules`: the catalogue stays one file for v1, but every rule root is relative to home and the macOS-only rules (`Library/...`) are tagged so a Linux build can swap in XDG roots later.
No `GOOS=linux` build is required to pass in v1, but `go vet` with `GOOS=linux` must not fail on package structure (a stub returning `ErrUnsupported` is fine). The port itself is a follow-up change (target v0.3.0): Linux rule set, freedesktop Trash, `/proc` checks, ubuntu CI job, linux/amd64+arm64 release artifacts.

# Proposal

## Why

AI coding tools, local models and developer toolchains quietly fill a Mac with tens of gigabytes of caches, build folders, worktrees and session logs. The tools that clean them are either closed and GUI-only (ElfClean, CleanMyMac), permanently delete by default and are hard to embed (Mole: GPL, Bash, `mo clean` empties your Trash), or ship their CLI inside an app bundle (Dusty). None offers a contract an agent can rely on: a versioned JSON plan with stable item IDs, dry-run as the only default, Trash as the only default, receipts that restore, and an importable MIT library. See `research/elfclean.md`.

## What Changes

- New Go module `github.com/rbutera/kipple` with one core library under `internal/` and three front ends that all call it: a CLI (`kipple`), a TUI (`kipple tui`), and an optional GUI (`kipple-gui`, separate build target).
- A rule catalogue of allowlisted cache, log and leftover locations under `$HOME`, each with a risk tier (`safe`, `review`, `manual`, `info`) and a plain-language explanation.
- A single safety policy that is the only code path allowed to authorise a move: hard-coded denylist, home-only, no symlink traversal, refuses to run as root, user config can only add protections.
- `kipple scan` produces a plan (human table or `--json`, schema v1, stable item IDs, sizes and paths). `kipple clean` is a dry run unless `--apply` is given; it moves to Trash, re-validates every item immediately before moving, and writes a receipt. `kipple restore` puts receipts back. `kipple rules`, `kipple explain`, `kipple history`, `kipple schema`, `kipple version` round out the agent surface.
- Git-aware project artifact rules (`node_modules`, `target`, `.venv`, `.next`) that only fire for idle projects with clean, pushed git state, re-checked before the move.
- GitHub Actions CI (lint, test, build on macOS) and a tagged release workflow producing universal macOS binaries with signed checksums. MIT licence, README with install and an agent-usage section.

## Out of scope for v1

- Anything outside `$HOME`, external volumes, system caches, Time Machine snapshots, `sudo`.
- App uninstaller and leftover-of-uninstalled-apps detection.
- Deleting models (Ollama, Hugging Face), Docker/OrbStack/Colima images, simulator runtimes, agent session history, agent memory/plans/skills. These are measured and reported (`info` tier) with the owning tool's own command printed.
- Duplicate finder, storage treemap, memory/CPU monitoring, scheduled or background cleaning, menu bar presence.
- Homebrew tap (planned after v0.1.0), Windows/Linux builds, localisation.

## Capabilities

### New Capabilities
- `safety-policy`: the single path-authorisation gate, its denylist and invariants.
- `rule-catalogue`: the allowlist of cleanable locations, tiers, explanations and listing.
- `scan-plan`: scanning, sizing, selection levels and the versioned plan document.
- `clean-apply`: dry-run default, `--apply`, Trash default, re-validation, receipts, skip reasons.
- `history-restore`: listing receipts and restoring items from the Trash.
- `project-artifacts`: git-aware and idle-aware rules for build artifacts inside projects.
- `cli-agent-interface`: JSON everywhere, stdout/stderr discipline, exit codes, non-interactive behaviour, schema command.
- `tui`: interactive terminal review and apply.
- `gui`: optional graphical front end as a separate build target.
- `distribution`: versioning, CI gates and signed releases.

### Modified Capabilities
None (greenfield).

## Impact

New repository. External runtime dependencies at run time: `git` (optional; absence makes every project "at risk"), `lsappinfo` (macOS built-in, optional). Build dependencies: Go 1.23+, cgo for the Trash shim on darwin, cobra, Bubble Tea, goreleaser in CI. No network access at run time, no telemetry.

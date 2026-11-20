# Spec Delta

## Purpose

Clean turns a plan into moves to the Trash, only with explicit consent, re-checking every item at the moment of action and leaving a receipt.

## ADDED Requirements

### Requirement: Dry run by default
`kipple clean` SHALL behave exactly like `kipple scan` (listing the plan, moving nothing) unless `--apply` is given. The dry-run output SHALL say so explicitly.

#### Scenario: No flag
- **WHEN** `kipple clean` runs without `--apply`
- **THEN** nothing under home changes and the output ends with a line stating this was a dry run and `--apply` is required

### Requirement: Trash is the only default destination
With `--apply`, every selected item SHALL be moved to the user's Trash. v0.1 SHALL NOT delete permanently: `--permanent` is parsed but refused with a usage error (exit 2) naming docs/safety.md#permanent. (Builds with the `kipple_permanent` tag implement it for `safe` tier items only, skipping other tiers with reason `permanent_not_allowed`; it ships once the staging design in docs/safety.md closes its remaining race.)

#### Scenario: Apply to Trash
- **WHEN** `kipple clean --apply` runs on a fixture
- **THEN** each selected item is absent from its original path and present in the Trash location recorded in the receipt

#### Scenario: Permanent refused in v0.1
- **WHEN** `kipple clean --apply --permanent --level review` runs
- **THEN** it exits 2 with a usage error saying permanent deletion is not available in this release, and nothing under home changes

### Requirement: Re-check before every move
Immediately before moving each item, apply SHALL re-run authorisation and the rule's conditions (idle project, clean git, owner not running) and SHALL refuse the item if any check fails or the path has changed type or vanished.

#### Scenario: Project became dirty
- **WHEN** a project `node_modules` item was selected and a file is modified in the project between scan and apply
- **THEN** the item is skipped with reason `git_dirty` and the rest of the plan proceeds

### Requirement: Plan input and subset selection
`clean` SHALL accept `--plan <file>` to act on a previously produced plan and `--only <id,...>` to act only on those item IDs. Items named in `--only` SHALL be moved even if `manual` tier, but never if `info` tier.

#### Scenario: Agent approves a subset
- **WHEN** `kipple clean --plan plan.json --only a1b2c3d4e5f6 --apply --json` runs
- **THEN** only that item is moved and the JSON result lists it under `moved` and every other item under `untouched`

### Requirement: Receipts
Every `--apply` run SHALL write a receipt file recording kipple version, time, home, every moved item (original path, trash path, bytes), every object moved in place of an item (displaced), and every skipped item with its reason. The receipt SHALL be write-ahead: an item is recorded as pending before it moves, and does not move if that record cannot be saved.

#### Scenario: Receipt written
- **WHEN** `kipple clean --apply` moves two items and skips one
- **THEN** a receipt exists in the state directory listing two `moved` and one `skipped` entries

### Requirement: Exit codes
Apply SHALL exit 0 when every selected item moved, 3 when nothing was selected, 4 when at least one item was skipped or failed, 1 on error.

#### Scenario: Partial run
- **WHEN** one of three items is skipped for `owner_running`
- **THEN** exit code is 4 and the JSON result names the skip reason

### Requirement: Consent is non-interactive for agents
When `--json` is set or stdout is not a TTY, apply SHALL NOT prompt; `--apply` alone is consent. On a TTY without `--yes`, apply SHALL show the selected total and the word Trash and wait for `y`.

#### Scenario: Piped apply
- **WHEN** `kipple clean --apply --json | cat` runs
- **THEN** no prompt is shown and the run proceeds

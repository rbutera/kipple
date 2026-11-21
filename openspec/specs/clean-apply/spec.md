# clean-apply Specification

## Purpose

Clean turns a plan into moves to the Trash, only with explicit consent, re-checking every item at the moment of action and leaving a receipt.

## Requirements

### Requirement: Dry run by default
`kipple clean` SHALL behave exactly like `kipple scan` (listing the plan, moving nothing) unless `--apply` is given. The dry-run output SHALL say so explicitly.

#### Scenario: No flag
- **WHEN** `kipple clean` runs without `--apply`
- **THEN** nothing under home changes and the output ends with a line stating this was a dry run and `--apply` is required

### Requirement: Trash is the only default destination
With `--apply`, every selected item SHALL be moved to the user's Trash: `~/.Trash` on macOS, and the freedesktop.org home Trash `~/.local/share/Trash` on Linux. Without `--apply` nothing moves (dry run is the default). An item SHALL never be copied and deleted instead of moved; an item on another device than its Trash is skipped with reason `different_device`. v0.1 SHALL NOT delete permanently: `--permanent` is parsed but refused with a usage error (exit 2) naming docs/safety.md#permanent. (Builds with the `kipple_permanent` tag implement it for `safe` tier items only, skipping other tiers with reason `permanent_not_allowed`; it ships once the staging design in docs/safety.md closes its remaining race.)

#### Scenario: Apply to Trash
- **WHEN** `kipple clean --apply` runs on a fixture
- **THEN** each selected item is absent from its original path and present in the Trash location recorded in the receipt

#### Scenario: Permanent refused in v0.1
- **WHEN** `kipple clean --apply --permanent --level review` runs
- **THEN** it exits 2 with a usage error saying permanent deletion is not available in this release, and nothing under home changes

#### Scenario: Apply to the freedesktop Trash on Linux
- **WHEN** `kipple clean --apply` runs on Linux against a fixture home
- **THEN** each moved item is under `$KIPPLE_HOME/.local/share/Trash/files`, and the receipt's `mover` is `freedesktop`

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

#### Scenario: Receipt save fails before a move
- **WHEN** the receipt cannot be saved with an item as pending (for example the disk is full)
- **THEN** that item does not move, no further item moves, the run exits 1 with error code `receipt`, and the JSON error includes what was already moved

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

### Requirement: Freedesktop Trash entries on Linux
On Linux, apply SHALL follow the freedesktop.org Trash specification 1.0 for the home Trash. For each moved item it SHALL first reserve a name by creating `info/<name>.trashinfo` exclusively, write `[Trash Info]` with `Path` (the absolute original path, percent-encoded) and `DeletionDate` (local time, `YYYY-MM-DDThh:mm:ss`), and only then rename the item to `files/<name>` without replacing anything. Name collisions SHALL be resolved with Finder-style names (`name 2`, `report 2.pdf`). If the move does not happen, apply SHALL remove the info file it wrote. apply SHALL never remove an info file it did not write. Dry run is the default and the Trash the only destination: without `--apply`, nothing, including the Trash folders, is created.

#### Scenario: Trash info written
- **WHEN** `kipple clean --apply` moves `~/.cache/pip/http` on Linux
- **THEN** `~/.local/share/Trash/files/http` exists and `~/.local/share/Trash/info/http.trashinfo` holds `Path=<fixture>/.cache/pip/http` and a `DeletionDate` in local time

#### Scenario: Name collision in the Trash
- **WHEN** `files/http` and `info/http.trashinfo` already exist in the Trash
- **THEN** the item moves to `files/http 2` with `info/http 2.trashinfo`, and the existing entry is untouched

#### Scenario: Orphan file without info
- **WHEN** `files/http` exists but `info/http.trashinfo` does not
- **THEN** the item moves to `files/http 2`, the orphan is untouched, and no `info/http.trashinfo` is left behind

#### Scenario: Unusual bytes in the path
- **WHEN** the moved item's name contains a space, `%` and a non-UTF-8 byte
- **THEN** the `Path` line percent-encodes them and decodes back to the original bytes

### Requirement: Trash folders on Linux are created only when needed and checked
On Linux, if `.local`, `.local/share`, `Trash`, `files` or `info` is missing, apply SHALL create it with mode 0700 from its verified parent, only in an `--apply` run with at least one item to move. Before moving, apply SHALL check that `Trash`, `files` and `info` are real directories (not symlinks), on home's device, owned by the user and not writable by group or others. Otherwise it SHALL stop with error code `trash_access` and move nothing. Dry run is the default, and the Trash is the only destination.

#### Scenario: Missing Trash created on apply
- **WHEN** the fixture home has no `.local/share/Trash` and `kipple clean --apply` moves one item
- **THEN** `Trash`, `files` and `info` exist with mode 0700 and hold the item and its info file

#### Scenario: Dry run creates nothing
- **WHEN** the fixture home has no `.local` folder and `kipple clean` runs without `--apply`
- **THEN** no folder is created and the fixture checksum is unchanged

#### Scenario: Trash folder is a symlink
- **WHEN** `~/.local/share/Trash` is a symlink
- **THEN** `kipple clean --apply` exits 1 with error code `trash_access` and nothing moves

#### Scenario: World-writable Trash
- **WHEN** `~/.local/share/Trash/files` has mode 0777
- **THEN** `kipple clean --apply` exits 1 with error code `trash_access` and nothing moves

### Requirement: Restore tag on Linux
On Linux, apply SHALL tag each item with the user extended attribute `user.com.rbutera.kipple` (receipt, item and home identity) before moving it, as on macOS. If the file system does not support user extended attributes, the item SHALL be skipped with reason `move_failed` and a detail naming the file system, and SHALL NOT move. Device nodes, FIFOs and sockets SHALL be refused without being opened. Dry run is the default, and the Trash is the only destination.

#### Scenario: Read-only package folder tagged
- **WHEN** a `go.mod-cache` item and its folder are read-only and `kipple clean --apply --level review` runs on Linux
- **THEN** the item moves to the Trash carrying the tag and its permissions are as they were

#### Scenario: No user extended attributes
- **WHEN** setting the tag fails because the file system does not support user extended attributes
- **THEN** the item is skipped with reason `move_failed`, it stays at its original path, and the run exits 4

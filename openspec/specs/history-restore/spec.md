# history-restore Specification

## Purpose

History lists past receipts, and restore moves items from the Trash back to their original paths.

## Requirements

### Requirement: History listing
`kipple history` SHALL list receipts newest first with time, items moved, bytes, and skips; `--json` SHALL emit one object (schema `kipple schema history`) whose `receipts` field is the receipts array, newest first, alongside `schema_version`, `home`, `receipts_dir` and `warnings`.

#### Scenario: Empty history
- **WHEN** no receipts exist
- **THEN** `kipple history` prints that nothing has been cleaned yet and exits 0

### Requirement: Restore is dry run by default
`kipple restore <receipt-id> [--only ids]` SHALL list what would be put back and move nothing unless `--apply` is given. Restored items SHALL be moved from their recorded Trash path to their original path only if the original path's parent still exists, the original path is vacant, and the original path passes authorisation for the item's rule.

#### Scenario: Restore to vacant path
- **WHEN** `kipple restore <id> --apply` runs and the original path is vacant
- **THEN** the item is moved back and the receipt is annotated as restored

#### Scenario: Original path occupied
- **WHEN** something now exists at the original path
- **THEN** the item is skipped with reason `destination_exists` and exit code is 4

#### Scenario: Trash emptied
- **WHEN** the recorded Trash path no longer exists
- **THEN** the item is skipped with reason `trash_item_missing`

#### Scenario: Restore a pending item
- **WHEN** a clean run stopped after moving an item but before recording the move, so the receipt lists it as pending
- **THEN** `kipple history` shows the pending entry, and `kipple restore <item-id> --apply` finds it in the Trash by its identity and tag and puts it back

#### Scenario: Receipt file path refused
- **WHEN** `kipple restore` is given the path of a receipt file instead of a receipt ID or item ID
- **THEN** it exits 2 with a usage error and moves nothing; only receipts in kipple's own receipts folder are used

#### Scenario: Restore finds a renamed Trash entry by identity
- **WHEN** an entry kipple moved was renamed inside the Trash (by Finder, or by a restore interrupted midway)
- **THEN** restore finds it by its identity and tag and puts it back

### Requirement: Restore removes the freedesktop trash info
On Linux, `kipple restore` SHALL be a dry run unless `--apply` is given, and SHALL move items only from kipple's Trash back to their original paths, never anywhere else. For a receipt whose mover is `freedesktop`, after an item is moved back, restore SHALL remove `info/<name>.trashinfo` for the Trash name the item was found under, but only if that file is a regular file whose decoded `Path` equals the item's original path. Otherwise it SHALL leave the file and add a warning. The info file SHALL be removed after the move, never before.

#### Scenario: Restore removes the info file
- **WHEN** `kipple restore <id> --apply` puts back an item kipple moved to `files/http` on Linux
- **THEN** the item is at its original path, `info/http.trashinfo` no longer exists, and the receipt is annotated as restored

#### Scenario: Info file describes something else
- **WHEN** `info/http.trashinfo` exists but its `Path` names a different location
- **THEN** the item is restored, the info file is left in place, and the result carries a warning naming it

#### Scenario: Restore dry run on Linux
- **WHEN** `kipple restore <id>` runs without `--apply`
- **THEN** it lists what would be put back and nothing in the Trash or under home changes

### Requirement: Items put back by a file manager
When an item's Trash entry is missing but its original path holds an object with the identity recorded in the receipt and kipple's tag for that receipt and item, restore SHALL skip it with reason `already_restored` and SHALL move nothing. This happens when GNOME's or KDE's "Restore" put it back. Dry run is the default, and the Trash is the only destination.

#### Scenario: Restored in the file manager first
- **WHEN** an item kipple moved was restored with the file manager and then `kipple restore <id> --apply` runs
- **THEN** the item is skipped with reason `already_restored` and nothing moves

#### Scenario: Trash emptied in the file manager
- **WHEN** the file manager emptied the Trash after kipple moved an item
- **THEN** restore skips it with reason `trash_item_missing`

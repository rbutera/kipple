# Spec Delta

## ADDED Requirements

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

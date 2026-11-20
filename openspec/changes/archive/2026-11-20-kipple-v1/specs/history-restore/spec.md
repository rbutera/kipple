# Spec Delta

## Purpose

History lists past receipts, and restore moves items from the Trash back to their original paths.

## ADDED Requirements

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

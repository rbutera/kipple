# Spec Delta

## Purpose

Scanning measures every candidate the catalogue allows and produces a plan document that humans and agents can review before anything moves.

## ADDED Requirements

### Requirement: Scan is read-only
`kipple scan` SHALL never create, modify, move or delete anything under home. It is always a dry run; there is no `--apply` on scan.

#### Scenario: Fixture unchanged
- **WHEN** scan runs against a fixture home
- **THEN** a recursive checksum of the fixture before and after is identical

### Requirement: Plan document
Scan SHALL emit a plan with `schema_version` (integer, 1 for v1), `generated_at`, `kipple_version`, `home`, `level`, `totals` (selected bytes, selected count, listed bytes, listed count), `rules[]` with per-rule status, and `items[]`.

#### Scenario: JSON plan
- **WHEN** `kipple scan --json` runs
- **THEN** stdout is exactly one JSON document matching the published plan schema and stderr carries any progress

### Requirement: Item fields
Each item SHALL carry `id`, `rule`, `tier`, `path` (absolute), `size_bytes`, `allocated_bytes`, `file_count`, `newest_mtime`, `scan_status` (`complete` or `partial`), `selected`, `reasons[]`, and for project items a `git` object with `state`.

#### Scenario: Partial scan recorded
- **WHEN** a subdirectory of an item is unreadable
- **THEN** the item's `scan_status` is `partial`, its sizes cover what was readable, and exit code stays 0

### Requirement: Stable item IDs
Item IDs SHALL be a deterministic function of rule ID and home-relative path so that the same layout yields the same ID across runs and machines.

#### Scenario: Two scans, same IDs
- **WHEN** scan runs twice on an unchanged fixture
- **THEN** the sets of item IDs are identical

### Requirement: Selection levels
`--level safe` (default) SHALL pre-select only `safe` items. `--level review` SHALL pre-select `safe` and `review` items whose conditions pass. `manual` and `info` items are never pre-selected.

#### Scenario: Default level
- **WHEN** `kipple scan` runs without `--level`
- **THEN** every `review`, `manual` and `info` item has `selected: false` with a reason naming the tier

### Requirement: Honest sizes
Sizes SHALL be measured with lstat, never following symlinks, counting hardlinked inodes once per scan, and SHALL report both logical and allocated bytes. kipple SHALL NOT display a health score or claim performance gains.

#### Scenario: Hardlinked store
- **WHEN** a fixture has two directory entries pointing at the same inode of 1 MiB
- **THEN** the containing item reports about 1 MiB allocated, not 2 MiB

### Requirement: Human output shows paths and sizes
Human output SHALL show, for every listed item, its path and size, grouped by rule with the tier and whether it is selected, and SHALL end with the selected total and the sentence that nothing has been moved.

#### Scenario: Dry-run footer
- **WHEN** `kipple scan` runs on a TTY
- **THEN** the last line states the selected total and that nothing was moved, and names `kipple clean --apply` as the next step

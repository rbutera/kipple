# Spec Delta

## Purpose

An optional graphical front end for people who do not use a terminal, built as a separate target over the same library.

## ADDED Requirements

### Requirement: Separate build target
The GUI SHALL build only with the `gui` build tag as `cmd/kipple-gui` and SHALL contain no deletion logic of its own; it calls the shared scan and apply packages.

#### Scenario: Default build excludes GUI
- **WHEN** `go build ./...` runs without `-tags gui`
- **THEN** no GUI dependencies are compiled

### Requirement: Scan, review, apply
The GUI SHALL offer a scan action, a review list grouped by rule with checkboxes, sizes and explanations, and an apply button that moves selected items to the Trash after a confirmation showing the total. Nothing moves without the confirmation.

#### Scenario: Confirmation required
- **WHEN** the apply button is pressed and the confirmation is dismissed
- **THEN** nothing under home changes

### Requirement: Tier parity
The GUI SHALL pre-select the same items as `kipple scan --level safe` and SHALL not allow selecting `info` items.

#### Scenario: Same selection as CLI
- **WHEN** the GUI and CLI scan the same fixture
- **THEN** the sets of pre-selected item IDs are equal

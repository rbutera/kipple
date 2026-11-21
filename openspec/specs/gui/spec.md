# gui Specification

## Purpose

An optional graphical front end for people who do not use a terminal, built as a separate target over the same library.

## Requirements

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

### Requirement: GUI platform availability
In v0.3.x until a Linux GUI ships, the GUI SHALL be released for macOS only. On Linux, `kipple-gui` SHALL compile with the `gui` tag (checked in CI). When started, it SHALL exit 1 before opening a window, printing that the Linux app is not available yet and that `kipple tui` offers the same review and apply. Nothing under home is read or changed. Dry run is the default and the Trash the only destination, as in the CLI and TUI.

#### Scenario: GUI started on Linux
- **WHEN** a `kipple-gui` built with the `gui` tag starts on Linux
- **THEN** it exits 1 with a message naming `kipple tui` and opens no window

#### Scenario: No Linux GUI artifact
- **WHEN** the v0.3.0 release is built
- **THEN** it contains no `kipple-gui` artifact for Linux

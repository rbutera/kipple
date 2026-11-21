# Spec Delta

## ADDED Requirements

### Requirement: GUI platform availability
In v0.3.x until a Linux GUI ships, the GUI SHALL be released for macOS only. On Linux, `kipple-gui` SHALL compile with the `gui` tag (checked in CI). When started, it SHALL exit 1 before opening a window, printing that the Linux app is not available yet and that `kipple tui` offers the same review and apply. Nothing under home is read or changed. Dry run is the default and the Trash the only destination, as in the CLI and TUI.

#### Scenario: GUI started on Linux
- **WHEN** a `kipple-gui` built with the `gui` tag starts on Linux
- **THEN** it exits 1 with a message naming `kipple tui` and opens no window

#### Scenario: No Linux GUI artifact
- **WHEN** the v0.3.0 release is built
- **THEN** it contains no `kipple-gui` artifact for Linux

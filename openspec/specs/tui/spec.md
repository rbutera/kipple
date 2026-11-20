# tui Specification

## Purpose

The TUI lets a person review the plan interactively in the terminal and apply it to the Trash with the same safety rules as the CLI.

## Requirements

### Requirement: Review view
`kipple tui` SHALL scan on start and show items grouped by rule with tier, size and selection state in one pane and the highlighted item's path and explanation in another.

#### Scenario: Launch
- **WHEN** `kipple tui` runs on a TTY against a fixture
- **THEN** every rule with items appears with its total and each item shows its size

### Requirement: Selection respects tiers
Space SHALL toggle selection of `safe`, `review` and `manual` items. `info` items SHALL not be selectable and SHALL show the owner's command instead.

#### Scenario: Toggle info item
- **WHEN** space is pressed on an `info` item
- **THEN** its selection does not change and a hint names the owner's command

### Requirement: Apply with confirmation
Pressing `a` SHALL show the selected total and that items go to the Trash; `y` SHALL run apply through the shared apply path and show the receipt summary; any other key cancels.

#### Scenario: Cancel
- **WHEN** `a` then `n` are pressed
- **THEN** nothing moves and the review view returns

### Requirement: Headless test
The TUI model SHALL be testable without a terminal by sending key messages and asserting the rendered view.

#### Scenario: Model test
- **WHEN** the test sends space on a safe item then renders
- **THEN** the view shows the item unselected and the total reduced

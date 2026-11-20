# rule-catalogue Specification

## Purpose

The rule catalogue is the allowlist of locations kipple knows how to clean, with a risk tier and a plain-language explanation for each.

## Requirements

### Requirement: Rules have tiers and explanations
Every rule SHALL have a stable ID, a title, a tier of `safe`, `review`, `manual` or `info`, one or more home-relative roots, a one-sentence `what` and a one-sentence `if_removed` explanation, and an owner tool name.

#### Scenario: Catalogue listing
- **WHEN** `kipple rules --json` runs
- **THEN** stdout is a JSON document with `schema_version` and an array of rules each carrying id, title, tier, roots, what, if_removed and owner

#### Scenario: Human listing
- **WHEN** `kipple rules` runs on a TTY
- **THEN** rules are grouped by tier with their explanations and roots

### Requirement: Catalogue invariants
Every rule root SHALL be under home, SHALL NOT be on the denylist, and no two rules SHALL claim the same root. Every rule SHALL have non-empty explanations.

No root SHALL have a glob in its first segment (such as `*` or `*/Cache`), and every exclusion pattern SHALL be a valid pattern.

Two exemptions apply, and only these:
- `info` rule roots SHALL be on the denylist (for example `.claude/projects`, `.codex/sessions`, `.ollama`, Docker's disk image under `Library/Containers`, `Library/Developer/CoreSimulator/Devices`), so that no other rule can ever move them. Info roots are measured and reported only and are never movable; the policy refuses every `info` rule with reason `info_tier` before any other check.
- Project artifact rules MAY share roots with each other, because they all search the configured project roots. They SHALL instead have disjoint artifact names, so no path is offered by two of them, and their roots SHALL NOT overlap any other rule's root.

#### Scenario: Invariant test
- **WHEN** the catalogue test suite runs
- **THEN** it fails if any rule violates an invariant, naming the rule

### Requirement: Tier semantics
`safe` rules regenerate without cost and are pre-selected at every level. `review` rules are listed at every level and pre-selected only at level `review`. `manual` rules are never pre-selected and are movable only by explicit item ID. `info` rules are measured and reported but SHALL never be movable by kipple; their output includes the owning tool's own cleanup command.

#### Scenario: Info rule in plan
- **WHEN** a plan contains an item from an `info` rule and `clean --only <that id> --apply` runs
- **THEN** the item is skipped with reason `info_tier` and nothing moves

### Requirement: v1 catalogue contents
The v1 catalogue SHALL include at least: `safe`: user caches (`Library/Caches/*` with exclusions for `com.apple.bird`, `CloudKit`, `com.apple.cloudkit`, `com.apple.icloud*`, `Homebrew`), user logs, Xcode DerivedData, CoreSimulator caches, SwiftPM cache, Homebrew download cache, npm, pnpm, Yarn classic, Bun, pip, uv, Poetry, Cargo registry cache and src, Go build cache, Gradle caches, CocoaPods cache, Cursor/VS Code/Windsurf `Cache`, `CachedData`, `Code Cache`, `GPUCache`, Claude Code `shell-snapshots` and `debug`, Codex `log`. `review`: Go module download cache, Xcode iOS DeviceSupport older than 90 days, project `node_modules`, `target`, `.venv`/`venv`, `.next`. `manual`: Xcode Archives, Hugging Face hub cache, Codex worktrees, `.dmg`/`.pkg`/`.iso` in Downloads older than 14 days. `info`: Ollama models, Claude Code projects, Codex sessions, Docker.raw, CoreSimulator runtimes.

#### Scenario: Catalogue count
- **WHEN** `kipple rules --json` runs
- **THEN** every rule listed above is present with the stated tier

### Requirement: Absent roots are not errors
A rule whose roots do not exist on this machine SHALL be reported as `absent` and SHALL not cause a non-zero exit.

#### Scenario: No Xcode installed
- **WHEN** `~/Library/Developer` does not exist
- **THEN** scan completes with exit 0 and Xcode rules carry `status: absent`

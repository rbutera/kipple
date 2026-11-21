# Spec Delta

## MODIFIED Requirements

### Requirement: Catalogue invariants
Every platform's catalogue (macOS and Linux) SHALL satisfy these invariants, and the test suite SHALL check both catalogues on any host. Every rule root SHALL be under home, SHALL NOT be on the denylist, and no two rules SHALL claim the same root. Every rule SHALL have non-empty explanations.

No root SHALL have a glob in its first segment (such as `*` or `*/Cache`), and every exclusion pattern SHALL be a valid pattern. A root nested inside another rule's root SHALL be excluded by that other rule.

On Linux, no rule SHALL name an app bundle ID, and every rule that skips while its owner runs SHALL name at least one owner process.

Two exemptions apply, and only these:
- `info` rule roots SHALL be on the denylist (for example `.claude/projects`, `.codex/sessions`, `.ollama`, Docker's disk image under `Library/Containers` or `.docker`, `Library/Developer/CoreSimulator/Devices`, `.local/share/containers`, `snap`), so that no other rule can ever move them. Info roots are measured and reported only and are never movable; the policy refuses every `info` rule with reason `info_tier` before any other check.
- Project artifact rules MAY share roots with each other, because they all search the configured project roots. They SHALL instead have disjoint artifact names, so no path is offered by two of them, and their roots SHALL NOT overlap any other rule's root.

#### Scenario: Invariant test
- **WHEN** the catalogue test suite runs
- **THEN** it fails if any rule of either platform's catalogue violates an invariant, naming the rule and the platform

#### Scenario: Nested root not excluded
- **WHEN** a Linux rule claims `.cache/pip` and `xdg.cache` does not exclude `pip`
- **THEN** the invariant test fails naming `xdg.cache` and `linux`

### Requirement: v1 catalogue contents
The macOS catalogue SHALL include at least: `safe`: user caches (`Library/Caches/*` with exclusions for `com.apple.bird`, `CloudKit`, `com.apple.cloudkit`, `com.apple.icloud*`, `Homebrew`), user logs, Xcode DerivedData, CoreSimulator caches, SwiftPM cache, Homebrew download cache, npm, pnpm, Yarn classic, Bun, pip, uv, Poetry, Cargo registry cache and src, Go build cache, Gradle caches, CocoaPods cache, Cursor/VS Code/Windsurf `Cache`, `CachedData`, `Code Cache`, `GPUCache`, Claude Code `shell-snapshots` and `debug`, Codex `log`. `review`: Go module download cache, Xcode iOS DeviceSupport older than 90 days, project `node_modules`, `target`, `.venv`/`venv`, `.next`. `manual`: Xcode Archives, Hugging Face hub cache, Codex worktrees, `.dmg`/`.pkg`/`.iso` in Downloads older than 14 days. `info`: Ollama models, Claude Code projects, Codex sessions, Docker.raw, CoreSimulator runtimes. In v0.3.0 the macOS catalogue SHALL be exactly the v0.2.0 catalogue.

#### Scenario: Catalogue count
- **WHEN** `kipple rules --json` runs on macOS
- **THEN** every rule listed above is present with the stated tier

#### Scenario: macOS catalogue unchanged by the port
- **WHEN** the macOS catalogue is rendered with `kipple rules --json` against a fixture home
- **THEN** it is byte-identical to the v0.2.0 golden file

## ADDED Requirements

### Requirement: Per-platform catalogue
kipple SHALL offer only the running platform's catalogue. A rule that exists on both platforms SHALL keep the same ID on both, with roots, extensions and owner processes that may differ by platform. `kipple rules` SHALL list only the running platform's rules, and `kipple explain` naming a rule that exists only on the other platform SHALL be a usage error (exit 2) that names the platform which has it. Dry run is the default, and the Trash is the only destination.

#### Scenario: Shared rule ID
- **WHEN** `kipple rules --json` runs on Linux
- **THEN** `pip.cache` is listed with root `.cache/pip`, and on macOS the same ID is listed with root `Library/Caches/pip`

#### Scenario: macOS-only rule on Linux
- **WHEN** `kipple explain xcode.deriveddata` runs on Linux
- **THEN** it exits 2 and says the rule exists on macOS only

### Requirement: Linux catalogue contents
The Linux catalogue SHALL include at least:
- `safe`: npm (`.npm/_cacache`), pnpm (`.local/share/pnpm/store`, `.cache/pnpm`), Yarn classic (`.cache/yarn`), Bun (`.bun/install/cache`), pip (`.cache/pip`), uv (`.cache/uv`), Poetry (`.cache/pypoetry/cache`, `.cache/pypoetry/artifacts`), Cargo registry cache and src, Go build cache (`.cache/go-build`), Gradle caches, Homebrew cache (`.cache/Homebrew`), `xdg.cache` (children of `.cache` untouched for 7 days, with an explicit exclude list: every other rule's root under `.cache`, keyring and session state (`keyring*`, `gnome-keyring`, `sessions`), stores that can hold unsynchronised user data (`evolution`, `rclone`, `zellij`), password-manager state (`keepassxc`), and denylisted names, each exclude documented with its reason), VS Code, Cursor and Windsurf `Cache`, `CachedData`, `Code Cache` and `GPUCache` under `.config/<App>` (skipped while the editor runs), Claude Code `shell-snapshots` and `debug`, Codex `log`.
- `review`: `jetbrains.caches` (children of `.cache/JetBrains` untouched for 30 days, skipped while an IDE runs), `maven.repository` (children of `.m2/repository`), `flatpak.app-cache` (children of `.var/app/*/cache`), Go module download cache, project `node_modules`, `target`, `.venv`/`venv`, `.next`.
- `manual`: Hugging Face hub cache, Codex worktrees, `.deb`/`.rpm`/`.AppImage`/`.iso`/`.run` in Downloads older than 14 days.
- `info`: Ollama models, Claude Code projects, Codex sessions, Docker Desktop's disk image (`.docker/desktop/vms/0/data`), rootless Docker (`.local/share/docker`), Podman storage (`.local/share/containers/storage`), Flatpak user installation (`.local/share/flatpak`), Snap data (`snap`), each with the owner's own cleanup command.

Every rule SHALL carry `what` and `if_removed` sentences written for a non-expert. Dry run is the default and the Trash the only destination; `info` rules are never movable.

#### Scenario: Linux catalogue count
- **WHEN** `kipple rules --json` runs on Linux
- **THEN** every rule listed above is present with the stated tier and no rule has a root under `Library/`

#### Scenario: Old cache folder pre-selected
- **WHEN** `kipple scan` runs on Linux at the default level and `~/.cache/some-app` was last modified 30 days ago
- **THEN** the item is listed under `xdg.cache` with `selected: true`

#### Scenario: Recent cache folder held back
- **WHEN** `kipple scan` runs on Linux and `~/.cache/some-app` was modified 2 days ago
- **THEN** the item is listed with `selected: false` and reason `too_recent`

#### Scenario: Excluded folder never offered
- **WHEN** `~/.cache/rclone` and `~/.cache/pip` exist and are 30 days old
- **THEN** no `xdg.cache` item is listed for either; `~/.cache/pip`'s children appear only under `pip.cache`

#### Scenario: Excludes documented
- **WHEN** the docs test runs
- **THEN** every `xdg.cache` exclude appears in docs/safety.md with its reason, and the test fails if the two differ

#### Scenario: Snap data is info only
- **WHEN** `kipple clean --only <id of an item under ~/snap> --apply` runs
- **THEN** the item is skipped with reason `info_tier` and nothing moves

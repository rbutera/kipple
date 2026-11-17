# Spec Delta

## Purpose

The safety policy is the single gate that decides whether kipple may move a given path. Nothing is listed, moved or restored without its approval.

## ADDED Requirements

### Requirement: Single authorisation gate
Every path kipple lists as movable, moves, or restores SHALL pass the policy's authorisation check, and the apply step SHALL call it again immediately before each move. Dry run is the default for every command; the policy applies identically to dry runs and `--apply` runs.

#### Scenario: Apply re-authorises before moving
- **WHEN** a plan item passed authorisation at scan time but its path has since become a symlink
- **THEN** apply refuses the item with reason `symlink_in_path` and moves nothing for that item

### Requirement: Home-only
The policy SHALL refuse any path that does not descend from the configured home directory, and SHALL refuse the home directory itself.

#### Scenario: Path outside home
- **WHEN** a rule root resolves to `/Library/Caches/x`
- **THEN** authorisation is refused with reason `outside_home`

#### Scenario: Different device
- **WHEN** a path under home is on a different device than home (a mounted volume)
- **THEN** authorisation is refused with reason `different_device`

### Requirement: No symlink traversal
The policy SHALL inspect every path component from home down to the candidate with lstat and SHALL refuse if any component is a symlink. Symlinks are never followed anywhere in kipple.

#### Scenario: Symlinked cache directory
- **WHEN** `~/Library/Caches/foo` is a symlink to `~/Documents/important`
- **THEN** authorisation of `~/Library/Caches/foo` is refused with reason `symlink_in_path`

#### Scenario: Symlink as the item itself
- **WHEN** the candidate path is itself a symlink
- **THEN** it is refused, even though moving a symlink would not follow it

### Requirement: Hard-coded denylist
The policy SHALL refuse any path that is, or descends from, a denylisted location. The denylist includes at least: Documents, Desktop, Pictures, Movies, Music, Library/Mobile Documents, Library/CloudStorage, Library/Mail, Library/Messages, Library/Photos, Library/Keychains, Library/Preferences, Library/Containers, Library/Group Containers, Library/Application Support/MobileSync, .ssh, .gnupg, .aws, .config/op, .ark, .claude/projects, .claude/memory, .claude/plans, .claude/skills, .codex/sessions, .codex/memory, any `.env` or `.env.*` component, any `*.keychain*`, any `*.photoslibrary`.

#### Scenario: Rule points into denylist
- **WHEN** a (hypothetical) rule root is `Library/Mail/V10`
- **THEN** authorisation is refused with reason `denylisted` regardless of the rule

#### Scenario: Config cannot shrink the denylist
- **WHEN** user config lists `Documents` under a key intended to allow it
- **THEN** config loading fails with an error naming the key, and Documents stays refused

### Requirement: Rule roots are kept
The policy SHALL refuse to authorise a rule's root directory itself; only children of the root are movable.

#### Scenario: Root is the candidate
- **WHEN** the candidate is `~/Library/Caches`
- **THEN** authorisation is refused with reason `is_rule_root`

### Requirement: Allowlist descent
The policy SHALL authorise a path only if it descends from one of the roots of the rule that claims it.

#### Scenario: Path not under rule root
- **WHEN** rule `xcode.deriveddata` claims `~/Library/Logs/foo`
- **THEN** authorisation is refused with reason `outside_rule_roots`

### Requirement: Never as root
kipple SHALL refuse to run when the effective user ID is 0 and SHALL never invoke sudo.

#### Scenario: Run as root
- **WHEN** any kipple command starts with euid 0
- **THEN** it exits with code 1 and the message `kipple will not run as root`

### Requirement: User protections are additive
User configuration SHALL be able to add protected paths that are treated like denylist entries, and SHALL NOT be able to remove or weaken any built-in protection.

#### Scenario: Extra protection honoured
- **WHEN** config protects `~/Library/Caches/com.example.keep`
- **THEN** scan lists nothing under it and apply refuses it with reason `user_protected`

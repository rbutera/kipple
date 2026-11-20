# safety-policy Specification

## Purpose

The safety policy is the single gate that decides whether kipple may move a given path. Nothing is listed, moved or restored without its approval.

## Requirements

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
The policy SHALL refuse any path that is, or descends from, a denylisted location. Names SHALL be compared the way APFS compares them (case and Unicode case folding, such as long s, sharp s and the `ﬀ`..`ﬆ` ligatures), and the policy SHALL also refuse a path whose components share a file identity (device and inode) with an existing denylisted location or user protection, so that no alternative spelling reaches a protected object. The denylist includes at least: Documents, Desktop, Pictures, Movies, Music, Library/Mobile Documents, Library/CloudStorage, Library/Mail, Library/Messages, Library/Photos, Library/Keychains, Library/Preferences, Library/Containers, Library/Group Containers, Library/Application Support/MobileSync, .ssh, .gnupg, .aws, .config/op, .ark, .claude/projects, .claude/memory, .claude/plans, .claude/skills, .codex/sessions, .codex/memory, any `.env` or `.env.*` component, any `*.keychain*`, any `*.photoslibrary`. It also covers browser profiles and cookies, password managers, account and privacy databases, editor `User` settings folders, developer credentials (`.docker`, cloud CLIs, package registry tokens, `.cargo/credentials*`), shell history, agent configuration (`.claude/agents`, `.claude/commands`, `.claude/CLAUDE.md`, `.codex/config.toml`, `.codex/prompts`), info-tier locations (`.ollama`, CoreSimulator `Devices` and `Profiles/Runtimes`), and any `.git`, `.envrc`, `*.kdbx`, `*.pem`, `*.p12`, `id_rsa*` or `id_ed25519*` component. The constant in `internal/policy/denylist.go` is the complete list.

#### Scenario: Rule points into denylist
- **WHEN** a (hypothetical) rule root is `Library/Mail/V10`
- **THEN** authorisation is refused with reason `denylisted` regardless of the rule

#### Scenario: Unicode alias of a denylisted folder
- **WHEN** a rule root or candidate is spelled `Documentſ` (long s), which APFS opens as `Documents`
- **THEN** authorisation is refused with reason `denylisted`, and config rejects such a spelling in `project_roots` or `protect`

#### Scenario: Config cannot shrink the denylist
- **WHEN** user config lists `Documents` under a key intended to allow it
- **THEN** config loading fails with an error naming the key, and Documents stays refused

### Requirement: Rule roots are kept
The policy SHALL refuse to authorise a rule's root directory itself; only children of the root are movable.

#### Scenario: Root is the candidate
- **WHEN** the candidate is `~/Library/Caches`
- **THEN** authorisation is refused with reason `is_rule_root`

### Requirement: Allowlist descent
The policy SHALL authorise a path only if it descends from one of the roots of the rule that claims it, and only if the path has the shape of that rule's items: a direct child of a root for `children` rules; a direct child that is a regular file with one of the rule's extensions for `extensions` rules; and for project artifact rules a directory named one of the rule's artifact names, inside a project folder below a project root, not inside another artifact directory, and beside at least one of the rule's marker files (a regular file, not a symlink, whose name on disk matches the marker exactly). Marker adjacency is enforced by the policy; scan and apply still re-check the idle and git conditions.

#### Scenario: Path not under rule root
- **WHEN** rule `xcode.deriveddata` claims `~/Library/Logs/foo`
- **THEN** authorisation is refused with reason `outside_rule_roots`

#### Scenario: Path is not an item of the rule
- **WHEN** rule `downloads.installers` claims `~/Downloads/taxes-2025.pdf`, rule `project.node-modules` claims `~/dev/proj/src`, or rule `project.target` claims `~/dev/notes/target` with no `Cargo.toml` file beside it
- **THEN** authorisation is refused with reason `not_rule_item`

### Requirement: Info rules are never authorised
The policy SHALL refuse every path claimed by an `info` tier rule with reason `info_tier`, before any other rule or filesystem check.

#### Scenario: Info rule item
- **WHEN** rule `ollama.models` claims `~/.ollama/models/blobs`
- **THEN** authorisation is refused with reason `info_tier`

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

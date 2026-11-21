# Spec Delta

## MODIFIED Requirements

### Requirement: Hard-coded denylist
The policy SHALL refuse any path that is, or descends from, a denylisted location. The denylist SHALL be one constant applied on every platform: an entry that matters on Linux is refused on macOS too, and the reverse. Names SHALL be compared the way APFS compares them (case and Unicode case folding, such as long s, sharp s and the `ﬀ`..`ﬆ` ligatures) on every platform, even on file systems that compare bytes, and the policy SHALL also refuse a path whose components share a file identity (device and inode) with an existing denylisted location or user protection, so that no alternative spelling or second mount reaches a protected object. The denylist includes at least: Documents, Desktop, Pictures, Movies, Music, Videos, Templates, Public, Library/Mobile Documents, Library/CloudStorage, Library/Mail, Library/Messages, Library/Photos, Library/Keychains, Library/Preferences, Library/Containers, Library/Group Containers, Library/Application Support/MobileSync, .ssh, .gnupg, .aws, .config/op, .ark, .claude/projects, .claude/memory, .claude/plans, .claude/skills, .codex/sessions, .codex/memory, any `.env` or `.env.*` component, any `*.keychain*`, any `*.photoslibrary`. It also covers browser profiles and cookies, password managers, account and privacy databases, editor `User` settings folders, developer credentials (`.docker`, cloud CLIs, package registry tokens, `.cargo/credentials*`), shell history, agent configuration (`.claude/agents`, `.claude/commands`, `.claude/CLAUDE.md`, `.codex/config.toml`, `.codex/prompts`), info-tier locations (`.ollama`, CoreSimulator `Devices` and `Profiles/Runtimes`), and any `.git`, `.envrc`, `*.kdbx`, `*.pem`, `*.p12`, `id_rsa*` or `id_ed25519*` component. On Linux it additionally covers: `.local/share/keyrings`, `.local/share/kwalletd`, `.pki`, `.mozilla`, `.thunderbird`, the Chrome, Chromium, Brave, Edge and Vivaldi profiles under `.config`, `.config/1Password`, `.config/Bitwarden`, `.config/keepassxc`, `.config/sops`, `.config/rclone`, `.config/containers`, `.config/evolution`, `.local/share/evolution`, `.config/Signal`, `.local/share/TelegramDesktop`, `.config/Code/User`, `.config/Cursor/User`, `.config/Windsurf/User`, `.config/Claude`, `.local/state`, `.local/share/fish`, the Trash `.local/share/Trash` and any `.Trash-*` component, the info-tier locations `.local/share/docker`, `.local/share/containers`, `.local/share/flatpak` and `snap`, and Flatpak app data `.var/app/*/data` and `.var/app/*/config`. The constant in `internal/policy/denylist.go` is the complete list. Dry run is the default and the Trash the only destination; the denylist applies to both.

#### Scenario: Rule points into denylist
- **WHEN** a (hypothetical) rule root is `Library/Mail/V10`
- **THEN** authorisation is refused with reason `denylisted` regardless of the rule

#### Scenario: Unicode alias of a denylisted folder
- **WHEN** a rule root or candidate is spelled `Documentſ` (long s), which APFS opens as `Documents`
- **THEN** authorisation is refused with reason `denylisted`, and config rejects such a spelling in `project_roots` or `protect`

#### Scenario: Config cannot shrink the denylist
- **WHEN** user config lists `Documents` under a key intended to allow it
- **THEN** config loading fails with an error naming the key, and Documents stays refused

#### Scenario: Linux keyring refused
- **WHEN** a (hypothetical) rule root is `.local/share/keyrings`
- **THEN** authorisation is refused with reason `denylisted` on Linux and on macOS

#### Scenario: Trash is never a source
- **WHEN** a candidate lies under `~/.local/share/Trash/files` or under a `.Trash-1000` folder
- **THEN** authorisation is refused with reason `denylisted`

#### Scenario: Folding applies on Linux
- **WHEN** on Linux a candidate is `~/documents/x` (lower case), a different folder from `~/Documents` on ext4
- **THEN** authorisation is refused with reason `denylisted`

## ADDED Requirements

### Requirement: XDG user directories are protected on Linux
On Linux, the policy SHALL treat every directory named in `<home>/.config/user-dirs.dirs` as a user protection (reason `user_protected`), except `XDG_DOWNLOAD_DIR`. A value equal to home or outside home SHALL be ignored with a warning. A malformed file SHALL produce a warning and leave the built-in denylist in force. These protections SHALL only add to the built-in ones, never remove any. Dry run is the default, and the Trash is the only destination.

#### Scenario: Localised Documents folder
- **WHEN** `user-dirs.dirs` sets `XDG_DOCUMENTS_DIR="$HOME/Dokumente"` and a hypothetical rule claims `~/Dokumente/x`
- **THEN** authorisation is refused with reason `user_protected`

#### Scenario: Download dir stays cleanable
- **WHEN** `user-dirs.dirs` sets `XDG_DOWNLOAD_DIR="$HOME/Downloads"`
- **THEN** `downloads.installers` still offers old installers in `~/Downloads`

#### Scenario: User dir set to home
- **WHEN** `user-dirs.dirs` sets `XDG_DESKTOP_DIR="$HOME"`
- **THEN** that entry is ignored, the plan warns about it, and every other protection stays in force

### Requirement: Mount points are boundaries
The policy, the size walk and the apply walk SHALL treat every mount point under home as a boundary, including a mount that reports the same device number as home (a Linux bind mount). A candidate that is a mount point, or that has a mount point between home and itself, SHALL be refused with reason `different_device`. An item that contains a mount point SHALL be marked `contains_mount`, SHALL NOT be selected, and apply SHALL skip it. When kipple cannot determine mount points, scan SHALL warn and mark every item `contains_mount`, and apply SHALL exit 1 with error code `mounts_unavailable` before moving anything. Dry run is the default, the Trash is the only destination, and kipple never copies an item across a boundary.

#### Scenario: Bind mount inside a cache folder
- **WHEN** on Linux `~/.cache/foo/docs` is a bind mount of `~/Documents` on the same device and `kipple clean --apply --only <id of ~/.cache/foo>` runs
- **THEN** the item is skipped with reason `contains_mount` and nothing under `~/.cache/foo` or `~/Documents` moves

#### Scenario: Candidate is a bind mount point
- **WHEN** `~/.cache/bar` is itself a bind mount
- **THEN** authorisation of `~/.cache/bar` is refused with reason `different_device`

#### Scenario: btrfs subvolume under home
- **WHEN** `~/.cache` is a separate btrfs subvolume with its own device number
- **THEN** items under it are refused with reason `different_device` and the plan explains that items on another device are not moved

#### Scenario: Mount table unavailable
- **WHEN** neither the kernel's mount-root attribute nor `/proc/self/mountinfo` is available
- **THEN** `kipple clean --apply` exits 1 with error code `mounts_unavailable` and nothing moves

### Requirement: Fixture home never overlaps the real home
When `KIPPLE_HOME` is set, kipple SHALL refuse to run, with exit 1 and nothing read or changed, if `KIPPLE_HOME` is the real home folder, lies inside it, or contains it. This SHALL be decided by file identity, including every place the real home is visible: macOS firmlinks, and Linux bind mounts listed in `/proc/self/mountinfo`. If the Linux mount table cannot be read, `KIPPLE_HOME` SHALL be refused. Dry run is the default, and the Trash is the only destination.

#### Scenario: KIPPLE_HOME through a bind mount of a folder inside home
- **WHEN** `/mnt/x` is a bind mount of `~/sub` and `KIPPLE_HOME=/mnt/x`
- **THEN** kipple exits 1 saying the folder is inside the real home folder

#### Scenario: KIPPLE_HOME through a bind mount of the home's parent
- **WHEN** `/mnt/h` is a bind mount of `/home` and `KIPPLE_HOME=/mnt/h/me`
- **THEN** kipple exits 1 saying the folder is the real home folder

#### Scenario: Separate temp folder accepted
- **WHEN** `KIPPLE_HOME` is a fresh folder under `/tmp` on a different mount from home
- **THEN** kipple runs against it

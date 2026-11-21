# Proposal

## Why

Agents and developers on Linux collect the same junk as on a Mac: package caches under `~/.cache`, `~/.npm`, `~/.cargo`, Go and Gradle caches, editor caches under `~/.config/Code`, agent logs under `~/.claude` and `~/.codex`, and project build output. kipple v0.2.0 only runs on macOS. Design D17 of kipple-v1 kept every platform-specific piece (Trash, no-clobber move, process checks, mount handling) behind a seam so that a port is new files rather than a refactor; the owner asked for the port ("Linux port too plz"), targeting v0.3.0.

## What Changes

- kipple builds, tests and ships for `linux/amd64` and `linux/arm64` (CLI and TUI), pure Go, no cgo.
- A per-platform rule catalogue: shared rules are written once, rules whose roots differ by platform carry per-platform roots under the same rule ID, and Linux adds XDG-based rules (`~/.cache/*` as a safe-tier rule with a 7-day age condition and an explicit exclude list, mirroring macOS `Library/Caches`; JetBrains, Maven, Flatpak app caches) plus info-tier rules for rootless Docker, Podman, Flatpak and Snap. The macOS catalogue is unchanged in v0.3.0 (its golden file must stay byte-identical); `maven.repository` and `jetbrains.caches` are enabled on macOS in v0.3.1.
- The denylist grows Linux entries (keyrings, browser and mail profiles under `~/.mozilla`, `~/.thunderbird`, `~/.config/<browser>`, password managers, `~/.local/state`, the freedesktop Trash, container and Flatpak data). It stays one Go constant applied on every platform. On Linux, the XDG user directories named in `~/.config/user-dirs.dirs` are added as protections, which can only add, never remove.
- Mount points are boundaries on Linux even when they share the device number with home (bind mounts): an item that is or contains a mount point is never moved. The `KIPPLE_HOME` guard learns bind mounts from `/proc/self/mountinfo` instead of macOS firmlinks.
- The Trash on Linux is the freedesktop.org home Trash (`~/.local/share/Trash/{files,info}`), with a `.trashinfo` file per item so GNOME and KDE "Restore" work. Moves use `renameat2(RENAME_NOREPLACE)` from the verified parent descriptor; an item on another device is skipped, never copied. Restore puts items back and removes their `.trashinfo`.
- Process checks on Linux read `/proc` (`comm`, `cmdline`, `cwd`) instead of `lsappinfo`, `pgrep` and `lsof`.
- State moves to `~/.local/state/kipple` on Linux; config stays at `~/.config/kipple/config.toml`. kipple uses fixed XDG default locations and warns when `XDG_*_HOME` points elsewhere; it never scans outside home.
- `kipple version --json` gains `os` and `arch`. The receipt's `mover` field gains the value `freedesktop`. Both are additive; `schema_version` stays `1` (see below).
- A Docker-based `make test-linux` runs the whole suite in a Linux container (repo mounted read-only, non-root user, container-local home), and a privileged, fixture-only `make test-linux-mounts` exercises real bind and overlay mounts. CI gains an Ubuntu job with `-race`. Releases add Linux tarballs to the same signed `checksums.txt`.
- The GUI stays macOS-only in v0.3.0: it is compile-checked on Linux in CI, and on Linux it refuses to start and points to `kipple tui`. A Linux GUI is planned for v0.3.1.

### JSON contract and schema_version

`schema_version` stays `1`. No field is renamed, removed or changes meaning. Two additions: `os` and `arch` in `kipple version --json` (a document with no published schema), and the enum value `freedesktop` for `receipt.mover`, which tells restore that each Trash entry has a `.trashinfo` to remove. `docs/plan-schema.md` already says adding a field is compatible; this change extends that sentence to new enum values and new error codes (`unsupported_platform`, `mounts_unavailable`), so a consumer must tolerate unknown enum values. A consumer validating new receipts against a v0.2.0 receipt schema fails on `mover: "freedesktop"`; only Linux receipts carry it, and no v0.2.0 consumer ran on Linux.

## Out of scope for v0.3.0

- Linux GUI release (planned v0.3.1), `.desktop` file and Linux icon packaging.
- Linux packages: deb, rpm, AUR, Nix, Snap, Flatpak, Homebrew on Linux. Roadmap only; release tarballs and `go install` are the install paths.
- Trashing items that live on another device or btrfs subvolume (the freedesktop `$topdir/.Trash-$uid` scheme). Such items are skipped with `different_device`, as on macOS.
- Honouring `XDG_CACHE_HOME`, `XDG_DATA_HOME`, `XDG_CONFIG_HOME` or `XDG_STATE_HOME` set to non-default locations (warned about, not followed). Anything outside home stays refused forever.
- System locations (`/var/cache`, `/var/lib/docker`, journald, package manager caches), anything needing root, other users' homes.
- Windows, FreeBSD and other Unixes: kipple refuses with `unsupported_platform`.
- Updating the freedesktop `directorysizes` cache; file managers recompute it.
- Any change to the macOS behaviour beyond the larger shared denylist (which only refuses more) and the version fields.

## Capabilities

### New Capabilities
- `platform-support`: which platforms kipple runs on, the per-platform locations of state, config and Trash, the XDG environment policy, home resolution through a symlinked `/home`, and refusal on unsupported platforms.

### Modified Capabilities
- `safety-policy`: Linux denylist entries in the shared constant, XDG user directories as additive protections, mount points as boundaries even on the same device, and the `KIPPLE_HOME` guard decided with bind mounts.
- `rule-catalogue`: per-platform catalogue with shared rule IDs, Linux catalogue contents, invariants checked for every platform's catalogue.
- `clean-apply`: the Trash default on Linux is the freedesktop home Trash with `.trashinfo`, atomic no-clobber moves, fail-closed behaviour when the file system lacks `RENAME_NOREPLACE` or user extended attributes.
- `history-restore`: restore removes the `.trashinfo` it wrote, and recognises items already put back by a file manager.
- `project-artifacts`: the running-agent check reads `/proc` on Linux.
- `cli-agent-interface`: `kipple version` reports `os` and `arch`; rule IDs of another platform are a usage error.
- `distribution`: Ubuntu CI job, container test harness, Linux release archives in the signed checksums, Linux install docs.
- `gui`: macOS-only availability in v0.3.0.

## Impact

- New files: `internal/trash/{freedesktop_linux.go,move_linux.go}`, `internal/procs/procs_linux.go`, `internal/apply/{tag_linux.go,...}`, `internal/homeguard/mount_linux.go`, `internal/policy/mounts_linux.go`, `internal/config/userdirs.go`, Linux test fixtures, `test/linux/Dockerfile`, `scripts/linux-mount-check.sh`.
- Changed: `internal/rules` (per-platform catalogue), `internal/policy/denylist.go`, `internal/sizes` and the policy session (mount-point detection), `internal/apply` (Trash selection per platform, mover), `internal/plan` (mover enum), `cmd/internal/launch` (state dir, XDG warnings, platform check), `cmd/kipple` (version fields), `cmd/kipple-gui/main.go` (Linux refusal), `.goreleaser.yaml`, `.github/workflows/ci.yml`, `Makefile`, `scripts/agent-flow-check.sh`, README, `llms.txt`, `docs/safety.md`, `docs/plan-schema.md`.
- Dependencies: none new. `golang.org/x/sys/unix` (already approved) supplies `Renameat2`, `Statx`, `Fsetxattr`.
- Runtime requirements on Linux: kernel 3.15+ and a home file system supporting `RENAME_NOREPLACE` (ext4, btrfs, xfs, tmpfs, overlayfs upper layers) and user extended attributes for apply; scan works everywhere. `/proc` mounted for process checks (otherwise review items whose owner check applies are skipped). Mount-point detection uses `statx` `STATX_ATTR_MOUNT_ROOT` (5.8+) or `/proc/self/mountinfo`.

# platform-support Specification

## Purpose

Which operating systems kipple runs on, where it keeps its state, configuration and Trash on each, how it treats the XDG environment variables, and how it refuses on a platform it does not support.

## ADDED Requirements

### Requirement: Supported platforms
kipple SHALL support macOS (`darwin/arm64`, `darwin/amd64`) and Linux (`linux/amd64`, `linux/arm64`). On any other operating system every command except `version` and `help` SHALL exit 1 with error code `unsupported_platform` and SHALL read and change nothing under home. On every platform dry run is the default and the Trash is the only destination, and nothing moves without `--apply`.

#### Scenario: Linux is supported
- **WHEN** `kipple scan --json` runs on `linux/amd64` against a fixture home
- **THEN** it exits 0 and prints a plan built from the Linux catalogue

#### Scenario: Unsupported platform refused
- **WHEN** a kipple binary built for FreeBSD runs `kipple clean --apply`
- **THEN** it exits 1 with error code `unsupported_platform` and nothing under home changes

### Requirement: Per-platform locations
kipple SHALL use these locations, each relative to the effective home:

| | macOS | Linux |
|---|---|---|
| Trash | `.Trash` | `.local/share/Trash` (`files/` and `info/`) |
| Receipts (state) | `Library/Application Support/kipple` | `.local/state/kipple` |
| Config | `.config/kipple/config.toml` | `.config/kipple/config.toml` |

`KIPPLE_STATE_DIR` SHALL override the state location on both platforms. With `KIPPLE_HOME` set, the Trash SHALL be the one inside `KIPPLE_HOME`, so a fixture run never reaches the real Trash. Dry run is the default, and the Trash is the only destination.

#### Scenario: Linux state directory
- **WHEN** `kipple clean --apply` runs on Linux with `KIPPLE_HOME` set and `KIPPLE_STATE_DIR` unset
- **THEN** the receipt is written under `$KIPPLE_HOME/.local/state/kipple` and moved items are under `$KIPPLE_HOME/.local/share/Trash/files`

#### Scenario: Fixture Trash on Linux
- **WHEN** a test runs `kipple clean --apply` with `KIPPLE_HOME` pointing at a fixture
- **THEN** nothing is created or moved outside the fixture and the state directory

### Requirement: XDG environment variables are not followed
On Linux, kipple SHALL use the XDG default locations (`.cache`, `.local/share`, `.config`, `.local/state` under home) and SHALL NOT follow `XDG_CACHE_HOME`, `XDG_DATA_HOME`, `XDG_CONFIG_HOME` or `XDG_STATE_HOME`. When one is set to an absolute path other than its default, `scan` and `clean` SHALL add a plan warning naming the variable, saying that kipple scans only the default location, and for `XDG_DATA_HOME` that the desktop's Trash may be elsewhere and `kipple restore` puts items back. A location outside home SHALL never be scanned. With `KIPPLE_HOME` set, the variables SHALL be ignored without a warning. Dry run is the default, and the Trash is the only destination.

#### Scenario: XDG_CACHE_HOME outside home
- **WHEN** `XDG_CACHE_HOME=/var/tmp/rai-cache` and `kipple scan --json` runs for the real home
- **THEN** no path under `/var/tmp/rai-cache` appears in the plan and `warnings` names `XDG_CACHE_HOME`

#### Scenario: XDG_CACHE_HOME elsewhere under home
- **WHEN** `XDG_CACHE_HOME` is `~/.xdg-cache`
- **THEN** rules still scan `~/.cache` only and the plan warns that `XDG_CACHE_HOME` is not followed

#### Scenario: Fixture ignores XDG variables
- **WHEN** `KIPPLE_HOME` is set and `XDG_DATA_HOME` points at the real `~/.local/share`
- **THEN** the run uses `$KIPPLE_HOME/.local/share/Trash` and prints no XDG warning

### Requirement: Home resolved once above home
kipple SHALL resolve the home folder with symlinks resolved once, at start-up, before pinning its identity, so a symlinked `/home` (for example `/home -> var/home`) yields paths under the resolved location. Below home, no symlink SHALL ever be followed. Dry run is the default, and the Trash is the only destination.

#### Scenario: Symlinked /home
- **WHEN** `$HOME` is `/home/me` and `/home` is a symlink to `/var/home`
- **THEN** the plan's `home` is `/var/home/me` and every item path starts with it

#### Scenario: Symlink below home still refused
- **WHEN** `~/.cache/pip` is a symlink to another folder
- **THEN** its items are refused with reason `symlink_in_path` on Linux as on macOS

### Requirement: Filesystems without atomic no-clobber rename
On Linux, apply SHALL move items only with an atomic rename that never replaces an existing entry. If the home's file system cannot do that (for example NFS or some FUSE file systems), apply SHALL stop with error code `trash_access` before moving anything, name the file system in the message, and SHALL NOT fall back to copying, deleting or a non-atomic rename. Scan SHALL still work. Dry run is the default, and the Trash is the only destination.

#### Scenario: No RENAME_NOREPLACE
- **WHEN** the rename primitive reports that no-replace renames are unsupported on the first move
- **THEN** the run exits 1 with error code `trash_access`, the message names the file system, and nothing under home has moved

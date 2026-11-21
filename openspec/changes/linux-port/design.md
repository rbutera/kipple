# Design

## Context

See proposal.md for why. kipple v0.2.0 is macOS-only, but design D17 of kipple-v1 (archived at `openspec/changes/archive/2026-11-20-kipple-v1/design.md`) kept the platform pieces behind seams, and `internal/portability` tests that no file built for Linux refers to a darwin-only symbol. Today `GOOS=linux go vet ./...` passes and the CLI cross-compiles with `CGO_ENABLED=0`, but on Linux:

- `trash.MoveNoReplace` returns `ErrUnsupported` (`move_other.go`), so `DirTrasher` cannot move anything.
- `procs.New()` answers unknown for everything (`procs_other.go`).
- `apply` tags with `setTag`, which is darwin-only (`tag_other.go` returns `ErrUnsupported`), so every move fails before it starts.
- `apply` opens `<home>/.Trash`, which is the macOS location.
- `homeguard.mountPoint` returns `""` on Linux (`mount_unix_other.go`), which is right for firmlinks but blind to bind mounts.
- Mount detection inside items is by `st_dev` change only (`internal/sizes`), which misses a same-device bind mount.
- `rules.Catalogue()` is one macOS list; D17 promised tagging of macOS-only rules, which was not done.
- The state directory is `~/Library/Application Support/kipple`.

Baseline (team lead, 2026-11-21): the current suite run in Docker (`golang:1.23`, linux/arm64, repo read-only, `HOME=/tmp/h`) passes 14 packages and fails 10 (`cmd/kipple`, `internal/{apply,guimodel,policy,receipt,scan,sizes,testutil,trash,tui}`), 92 tests. Almost all of them fail with "trash: not supported on this platform or build" or on the missing move and procs seams. A few are policy and session device-identity tests, and the chmod-based permission tests (`TestPermissionDenied`, `TestUnreadable*`, `TestReadOnlyDirectoryItem`) failed because that container ran as root, where permission bits do not apply. Most failing CLI, TUI and restore tests go through apply into the platform Trash. So making the whole default suite pass on Linux needs the Trash, apply, restore and `/proc` mechanics, and the wave order follows from that (see Migration Plan).

Constraints: AGENTS.md hard rules apply unchanged on Linux. No test, script or harness may touch the real home of any machine, including the CI runner's `/home/runner` and the colima VM. kipple refuses to run as root, so the container harness must run tests as a non-root user. No new dependencies.

## Goals / Non-Goals

**Goals:**
- The same safety model on Linux: one gate (`policy.Authorize`), dry run by default, Trash only, receipts, restore, no symlink following, home only, never root.
- A Linux user's file manager sees kipple's Trash entries and can restore them.
- Both catalogues, both denylists and the Linux-specific logic are testable on a macOS host (pure functions with injected file-system facts), and the syscall paths are tested in a Linux container and on Ubuntu CI.
- macOS behaviour and output are unchanged except where the proposal says so.

**Non-Goals:**
- Feature parity with desktop-environment trash tools (no `directorysizes`, no top-directory trashes, no trash emptying).
- Running inside a sandbox (Flatpak or Snap packaging of kipple).
- Detecting agents in other PID namespaces.

## Decisions

### L1. Platform abstraction: build tags for syscalls, runtime parameters for data

Syscall code is selected by build tags, as today. Data that differs by platform (catalogue, Trash location, state dir, denylist documentation) is selected at run time by a `goos` parameter defaulting to `runtime.GOOS`, so both variants are tested on any host.

New or changed files:

| Package | darwin (unchanged) | linux (new) | other |
|---|---|---|---|
| `internal/trash` | `move_darwin.go`, `finder_darwin.go` | `move_linux.go` (`renameat2`), `freedesktop_linux.go` (`FreedesktopTrasher`) | `move_other.go` narrowed to `!darwin && !linux` |
| `internal/apply` | `tag_darwin.go` | `tag_linux.go` (`user.` xattr), `trash_linux.go` (open the freedesktop Trash) | `tag_other.go` narrowed |
| `internal/procs` | `procs_darwin.go` | `procs_linux.go` (`/proc`) | `procs_other.go` narrowed |
| `internal/homeguard` | `mount_darwin.go` | `mount_linux.go` (`/proc/self/mountinfo` aliases) | `mount_unix_other.go` narrowed |
| `internal/policy` | | `mounts_linux.go` (mount-root test per directory) | stub returns "not a mount root" by `st_dev` only |
| `internal/sizes` | | uses the policy mount test | |

`internal/rules` gets no build tags. `rules.Catalogue()` returns `CatalogueFor(runtime.GOOS)`; tests and the docs generator call `CatalogueFor("darwin")` and `CatalogueFor("linux")`.

Alternatives: runtime `if runtime.GOOS == "linux"` switches around syscalls (rejected: `x/sys/unix` symbols such as `Renameat2` and `RenameatxNp` exist only on their platform, so it would not compile); separate `catalogue_darwin.go` and `catalogue_linux.go` (rejected: duplicates every shared rule and makes the Linux catalogue untestable on macOS).

`internal/portability`'s test is extended to treat `linux` like `darwin`: no file built for both may refer to a symbol declared only for one of them.

### L2. Linux home guard and home resolution

Home resolution is unchanged in shape: `cmd/internal/launch` resolves `KIPPLE_HOME` or the real home with symlinks resolved, then pins the home's identity. A symlinked `/home` (Fedora Silverblue's `/home -> var/home`) is therefore resolved once, above home, and every reported path uses `/var/home/<user>`. Nothing below home is ever followed.

The `KIPPLE_HOME` guard (`homeguard.Relate`) already decides by identity along `..` chains, plus a tail probe and, on macOS, the firmlinked mount point. Linux has no firmlinks but has bind mounts, which break the `..` chain test in one direction: if `/mnt/x` is a bind mount of `/home/me/sub`, then `..` of `/mnt/x` is `/mnt`, so the real home's identity is not on the candidate's chain and "inside" goes undetected. Same-identity and contains checks still work, because a bind mount keeps `(st_dev, st_ino)`.

`mount_linux.go` replaces the `MountPoint` probe with an alias list built from `/proc/self/mountinfo`. For each mount whose source is the same file system as the real home (same `major:minor`, and the mount's `root` field is the path within that file system):
- if the mount's root is the real home's file-system path or an ancestor of it, the real home is also visible at `mountpoint + remainder`. That path is an alias, and the relation is computed against it as well.
- if the mount's root lies inside the real home, the mount point itself is inside the real home, so a candidate equal to it or below it is `Inside`.

The real home's file-system path comes from the mountinfo entry of the mount that holds it (longest mount-point prefix of the resolved real home, confirmed by comparing `st_dev` with the mount's `major:minor`). btrfs reports the subvolume in the `root` field (`/@home/me`), so the comparison works per subvolume. If mountinfo cannot be read or parsed, or an escape sequence is malformed, the guard fails closed: `KIPPLE_HOME` is refused. Containers: inside a container the real home is the container's home; the host's home is not visible and is not a concern.

Alternatives: `name_to_handle_at` file handles (rejected: not supported by every file system, overlayfs among them, and it still cannot list aliases); refusing any `KIPPLE_HOME` on a bind mount (rejected: CI and container harnesses commonly bind-mount temp folders).

### L3. Mount points are boundaries even on the same device

On macOS every mount has its own `st_dev`, so the existing rules work: a candidate on a different device is refused with `different_device`, and an item whose size walk meets an `st_dev` change is `contains_mount`. On Linux a bind mount of a directory from the same file system has the same `st_dev`. A bind mount of `~/Documents` at `~/.cache/foo/docs` would be walked, measured and moved along with `~/.cache/foo`. A file manager emptying the Trash would then recurse into Documents through the moved mount.

`policy` gains a mount-root test used by the session walk, the fresh apply walk and the size walk. On Linux it is `statx(fd, "", AT_EMPTY_PATH, STATX_BASIC_STATS)` with `STATX_ATTR_MOUNT_ROOT` in `stx_attributes_mask` (kernel 5.8+). Where that attribute is not reported, it falls back to a snapshot of mount points under home read from `/proc/self/mountinfo` at session start, compared by identity (each listed mount point is opened with `O_PATH|O_NOFOLLOW` and `fstat`ed, so spelling and escapes do not matter). Results:
- A candidate, or a component between home and the candidate, that is a mount root is refused with `different_device`.
- An item containing a mount root is `contains_mount`, as for an `st_dev` change today, so it is unselectable and apply skips it.
- If neither `statx` nor mountinfo can answer, kipple fails closed: scan warns and marks every item `contains_mount`, and apply stops with error code `mounts_unavailable` before moving anything.

btrfs subvolumes have their own `st_dev`, so a subvolume under home (for example `~/.cache` as its own subvolume) is `different_device`, exactly like an APFS volume mounted under home. That is reported per item; the docs explain why and the out-of-scope topdir Trash.

overlayfs: renaming a directory that comes from a lower layer fails with `EXDEV` unless `redirect_dir` is on, and with `xino` off non-directory objects may report the lower layer's `st_dev`. Both lead to `different_device` (never copy). Containers where the home is a volume (the usual case) are unaffected.

### L4. Trash: the freedesktop.org home Trash

**Location.** `<home>/.local/share/Trash`, with `files/` and `info/`. kipple does not honour `XDG_DATA_HOME` for the Trash in v0.3.0 (see L6). With `KIPPLE_HOME` set it is `$KIPPLE_HOME/.local/share/Trash`, inside the fixture.

**Opening.** `apply/trash_linux.go` opens `Trash`, `files` and `info` through the policy session (no symlink, same device as home, stored names). Each must be a directory owned by the effective user and not writable by group or others; otherwise apply stops with `trash_access` and says why. If any of `.local`, `.local/share`, `Trash`, `files` or `info` is missing, it is created with `mkdirat(parentfd, name, 0700)` from the verified parent descriptor, then reopened through the session. Creation happens only in an `--apply` run that has at least one item to move. A dry run, a scan and a probe never create anything; `ProbeTrash` reports `rename` mode when the Trash is missing but creatable.

**Moving one item** (`trash.FreedesktopTrasher.TrashExpect`, holding the `files` and `info` descriptors):
1. Check the item's identity by `fstatat` on the verified parent descriptor, as `DirTrasher` does, and that the parent, the item and `files` share a device. Otherwise return `ErrDifferentDevice` and move nothing.
2. Pick a name, starting with the item's name and then Finder-style `name 2`, `report 2.pdf`. Reserve it by creating `info/<name>.trashinfo` with `openat(infofd, ..., O_WRONLY|O_CREAT|O_EXCL|O_NOFOLLOW|O_CLOEXEC, 0600)`. `EEXIST` means try the next name. This is the spec's atomic reservation.
3. Write the info file, then `fsync` it:
   ```
   [Trash Info]
   Path=/home/me/.cache/pip/http
   DeletionDate=2026-11-21T10:30:00
   ```
   `Path` is the absolute original path, percent-encoded per RFC 3986 (every byte outside the unreserved set and `/`), so any byte sequence in a Linux name round-trips. `DeletionDate` is local time, `YYYY-MM-DDThh:mm:ss`, with no zone, as the spec requires.
4. `renameat2(parentfd, name, filesfd, cand, RENAME_NOREPLACE)`. On `EEXIST` (an orphan in `files/` with no info), remove the info file just written and go back to step 2. On `EXDEV`, remove it and return `ErrDifferentDevice`.
5. Check that the object that arrived is the one inspected, as `DirTrasher.checkArrived` does. On a mismatch, move the object back. If that fails, report `DisplacedError` and keep the info file, so a file manager can still restore it. Otherwise remove the info file and return `ErrItemChanged`.

The info file is removed only after `fstatat(infofd, name)` shows the inode written in step 3. A kill between steps 3 and 4 leaves an orphan `.trashinfo` with no file, which the spec tells implementations to ignore. kipple never removes info files it did not write.

**No-clobber moves.** `MoveNoReplace` on Linux is `renameat2` with `RENAME_NOREPLACE`. `EINVAL` or `ENOSYS` (NFS, some FUSE file systems, kernels before 3.15) maps to `trash.ErrNoReplaceUnsupported`. Nothing has moved at that point, and apply stops the run with `trash_access`, naming the file system. Alternatives rejected: `link` + `unlink` (cannot move directories, which are most items) and `renameat` with a pre-check (a check-then-replace race, the exact gap D16 closed).

**`openat2`/`O_PATH`.** Not used. The session already walks one component at a time with `O_NOFOLLOW|O_DIRECTORY` and checks every descriptor's identity, which `RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS` would only duplicate on 5.6+ kernels. `O_PATH` descriptors cannot get or set extended attributes, which the tag needs.

**Tag.** `tag_linux.go` sets `user.com.rbutera.kipple` (same value format, `kipple/1 receipt=... item=... home=dev:ino`) with `fsetxattr` on a read-write-capable descriptor, using the same temporary owner-write dance as darwin for read-only folders (the Go module cache). Linux allows `user.` attributes only on regular files and directories. Items are never symlinks (the policy refuses them), and device nodes, FIFOs and sockets are refused before any open, so they are never opened. If the file system rejects user attributes (`ENOTSUP`, or `EPERM` on tmpfs before 6.6), the item is skipped with `move_failed` and a detail naming the file system, and nothing moves. Restore requires the tag as on macOS, so moving without one would create entries restore refuses.

**Receipt.** `mover` is `freedesktop` and `trash_dir` is `<home>/.local/share/Trash/files`. Each moved item's `trash_path` is `files/<cand>`. The info path is derived as `info/<basename(trash_path)>.trashinfo`, so there is no new field.

**Other devices.** Skipped with `different_device`, never copied, as on macOS. The spec's `$topdir/.Trash-$uid` would mean writing to the top of another mount, checking sticky bits, and authorising a second Trash root for restore. That is deferred (roadmap), so v0.3.0 has one Trash per home, as on macOS.

**Interop with GNOME and KDE.** Entries appear in the file manager's Trash with their original location, and "Restore" (gio, Nautilus, Dolphin) renames them back and removes the info file. That keeps the identity and the tag, which restore uses (L5). Emptying the Trash in the file manager makes restore report `trash_item_missing`. kipple never empties the Trash and never writes `directorysizes`; consumers recompute it when an entry is missing from it.

### L5. Restore on Linux

Restore works as on macOS. It opens the Trash `files` directory only through the pinned home descriptor, finds the entry by recorded name or by identity and tag, authorises the destination, stages it under a random visible name inside `files/`, verifies it, and renames it to the destination with `RENAME_NOREPLACE`. For a `freedesktop` receipt it then removes `info/<name>.trashinfo`, but only when that file is a regular file whose decoded `Path` equals the receipt's original path. Otherwise it leaves the file and adds a warning. Removal happens after the move: a crash in between leaves an orphan info file, which is harmless. Removing it first could strand a file with no info, which a file manager cannot restore. If the entry was renamed in the Trash, the info file to remove is the one matching the name restore found it under. When the item is missing from the Trash but the original path holds an object with the recorded identity and kipple's tag, the entry is reported `already_restored`, an existing reason, because a file manager put it back.

### L6. XDG environment policy

kipple uses the XDG default locations (`~/.cache`, `~/.local/share`, `~/.config`, `~/.local/state`) and does not follow `XDG_CACHE_HOME`, `XDG_DATA_HOME`, `XDG_CONFIG_HOME` or `XDG_STATE_HOME`:
- If one is set, absolute, and not equal to its default, `scan` and `clean` add a plan warning naming the variable. The warning says kipple scans only the default location, and for `XDG_DATA_HOME` that the desktop's Trash may be elsewhere, so `kipple restore` (not the file manager) is the way back.
- A value outside home is never scanned; the home-only invariant is permanent.
- With `KIPPLE_HOME` set, `XDG_*` variables are ignored entirely and produce no warning, so the real environment can never steer a fixture run.

Alternatives: follow `XDG_CACHE_HOME` when it lies under home (rejected for v0.3.0: roots would come from the environment, which the catalogue invariant tests cannot check, and an environment variable could point a `safe` rule at `~/Documents`, refused only by the denylist; roadmap item with its own invariant design); follow it anywhere (rejected: home-only).

State: `~/.local/state/kipple` on Linux (receipts), `KIPPLE_STATE_DIR` still overrides. Config: `~/.config/kipple/config.toml` on both platforms, as today.

### L7. Process checks from /proc

`procs_linux.go` implements `Inspector` by reading `/proc` through an injectable `FS` (an `fs.FS` rooted at `/proc` by default), so tests feed synthetic trees on any host:
- `RunningNames(names)`: for each numeric `/proc/<pid>` owned by the effective UID (`fstat` of the directory), read `comm` (kernel-truncated to 15 bytes) and the first NUL-terminated field of `cmdline`. A process matches a name when `comm == name`. For names of 15 bytes or more, a match also needs `basename(argv0) == name`, because `comm` is the truncated prefix. It also matches when `basename(argv0) == name` alone, which catches Node's `process.title` (the npm `claude`), since Node rewrites the argv area.
- `ProcessCwds(names)`: `readlink /proc/<pid>/cwd` for matching processes, stripping a trailing ` (deleted)`.
- `RunningBundleIDs`: `ErrUnsupported`. Linux rules carry no bundle IDs (catalogue invariant), so apply never asks.
- Processes of other users are ignored: kipple cannot read their `cwd` and they are not the user's agents. A process that exits during the walk (`ENOENT`, `ESRCH`) is skipped. `/proc` not mounted, or unreadable for the user's own processes, is unknown (`ErrUnknown`), which apply already treats as running for review and manual items (skip) and as a warning for safe items.
- `hidepid=2` and PID namespaces: kipple sees only its own namespace. Inside a container, an agent running on the host in a bind-mounted project is invisible. This is documented as a known limit; the idle and git conditions still apply.

Owner processes on Linux: VS Code `code`, Cursor `cursor`, Windsurf `windsurf`, JetBrains IDEs `idea`, `pycharm`, `goland`, `webstorm`, `clion`, `rustrover`, `rider`, `phpstorm`, `datagrip`, `rubymine`; agents `claude`, `codex`, `cursor-agent` as today.

`procs.Within` keeps its ASCII case-insensitive comparison on Linux. It only decides "an agent may be in this project", so matching more is the safe direction.

### L8. Rule catalogue split and the Linux catalogue

The catalogue stays one Go list of `entry` values. An entry holds the rule, written once, and an optional per-platform override (`Roots`, `Exclude`, `Match.Extensions`, `Owner`) plus the platforms it exists on. `CatalogueFor(goos)` returns the resolved `[]Rule` for one platform, and `Catalogue()` calls it with `runtime.GOOS`. Rule IDs are shared across platforms (`pip.cache` is `pip.cache` everywhere), so an agent learns one vocabulary. Item IDs differ anyway, because they hash the home-relative path. `kipple rules --json` lists only the running platform's rules and its schema does not change.

The macOS catalogue must resolve to exactly today's list. The existing golden `rules.json` becomes `testdata/rules_darwin.json`, byte-identical, and `testdata/rules_linux.json` is new.

Linux catalogue (`shared` = same roots as macOS; others are Linux roots for the same or a new ID):

| ID | Tier | Linux roots | Notes |
|---|---|---|---|
| `npm.cache` | safe | `.npm/_cacache` | shared |
| `pnpm.store` | safe | `.local/share/pnpm/store`, `.cache/pnpm` | |
| `yarn.cache` | safe | `.cache/yarn` | |
| `bun.cache` | safe | `.bun/install/cache` | shared |
| `pip.cache` | safe | `.cache/pip` | |
| `uv.cache` | safe | `.cache/uv` | shared |
| `poetry.cache` | safe | `.cache/pypoetry/cache`, `.cache/pypoetry/artifacts` | |
| `cargo.registry` | safe | `.cargo/registry/cache`, `.cargo/registry/src` | shared |
| `go.build-cache` | safe | `.cache/go-build` | |
| `gradle.caches` | safe | `.gradle/caches` | shared |
| `homebrew.cache` | safe | `.cache/Homebrew` | Homebrew on Linux |
| `editor.vscode` / `editor.cursor` / `editor.windsurf` | safe | `.config/<App>/{Cache,CachedData,Code Cache,GPUCache}` | owner process `code` / `cursor` / `windsurf`; skip while running |
| `claude.shell-snapshots`, `claude.debug`, `codex.log` | safe | shared | |
| `xdg.cache` (new) | review | `.cache` children, min age 7 days | excludes every child that is another rule's root (`pip`, `uv`, `go-build`, `yarn`, `pnpm`, `pypoetry`, `huggingface`, `JetBrains`, `Homebrew`) and `sessions` (Xfce session state) |
| `jetbrains.caches` (new) | review | `.cache/JetBrains` children, min age 30 days | skip while an IDE runs |
| `maven.repository` (new) | review | `.m2/repository` children | locally installed snapshots need a rebuild |
| `flatpak.app-cache` (new) | review | `.var/app/*/cache` children | owner unknown per app, hence review |
| `go.mod-cache` | review | `go/pkg/mod` | shared |
| `project.*` | review | project roots | shared |
| `huggingface.hub` | manual | `.cache/huggingface/hub` | shared |
| `codex.worktrees` | manual | shared | |
| `downloads.installers` | manual | `Downloads`, `.deb .rpm .AppImage .iso .run`, older than 14 days | per-platform extensions |
| `ollama.models`, `claude.projects`, `codex.sessions` | info | shared | |
| `docker.raw` | info | `.docker/desktop/vms/0/data` | Docker Desktop for Linux |
| `docker.rootless` (new) | info | `.local/share/docker` | `docker system prune` |
| `podman.storage` (new) | info | `.local/share/containers/storage` | `podman system prune` |
| `flatpak.user` (new) | info | `.local/share/flatpak` | `flatpak uninstall --unused` |
| `snap.data` (new) | info | `snap` | `snap list --all`, then `snap remove --purge <name>` |

`xdg.cache` is `review`, not `safe` like macOS `Library/Caches`. macOS's guidance is that apps must tolerate removal of their caches; Linux has no equivalent contract, desktop components keep state there, and the owner of each child is unknown, so the owner-running check cannot apply. The 7-day age condition skips caches in active use. Rules for snap and Flatpak app data other than `cache` are deliberately absent: `.var/app/*/data`, `.var/app/*/config` and `snap` are on the denylist.

The new rules exist on Linux only in v0.3.0, so the macOS catalogue is unchanged. Whether to enable `maven.repository` and `jetbrains.caches` on macOS (with macOS roots) is a v0.3.1 decision.

Invariants (`TestCatalogueInvariants`) run for every platform in `{"darwin", "linux"}`: roots under home, info roots on the denylist and every other root off it, no duplicate roots, nested roots excluded by their parent rule, non-empty explanations, owner command on info rules. New invariant: Linux rules have no `BundleIDs`, and every Linux rule with `SkipIfOwnerRunning` names at least one process, or is `review`.

`kipple explain <id>` for a rule that exists only on the other platform is a usage error (exit 2) saying which platform has it.

### L9. Denylist on Linux

One constant for all platforms (union). Adding Linux entries refuses more on macOS too, which is harmless: no macOS rule root lies under them, the darwin golden catalogue is unchanged, and the denylist doc test regenerates `docs/safety.md`. Additions:

- Personal folders: `Videos`, `Templates`, `Public` (Documents, Desktop, Pictures, Music already there).
- Secrets and keyrings: `.local/share/keyrings`, `.local/share/kwalletd`, `.pki`, `.config/1Password`, `.config/Bitwarden`, `.config/keepassxc`, `.config/sops`, `.config/rclone`, `.config/containers` (registry `auth.json`).
- Browsers and mail: `.mozilla`, `.thunderbird`, `.config/google-chrome`, `.config/chromium`, `.config/BraveSoftware`, `.config/microsoft-edge`, `.config/vivaldi`, `.config/evolution`, `.local/share/evolution`, `.config/Signal`, `.local/share/TelegramDesktop`.
- Editor and app settings: `.config/Code/User`, `.config/Cursor/User`, `.config/Windsurf/User`, `.config/Claude`.
- State and history: `.local/state` (includes kipple's own receipts), `.local/share/fish`.
- Trash: `.local/share/Trash`, and the component pattern `.Trash-*` (top-directory trashes) at any depth.
- Info-tier roots: `.local/share/docker`, `.local/share/containers`, `.local/share/flatpak`, `snap`.
- Flatpak app data: `.var/app/*/data`, `.var/app/*/config`.

Name folding stays APFS-style on every platform. ext4 and btrfs compare bytes, so folding on Linux only refuses more (`~/documents` is refused too). One policy is simpler to test than two, and ext4 casefold directories and `ciopfs` exist.

**XDG user directories.** On Linux, `internal/config` parses `<home>/.config/user-dirs.dirs`, opened without following a symlink. Every `XDG_<NAME>_DIR="$HOME/<path>"` or absolute path under home becomes an extra protection (`Env.Protect`, reason `user_protected`), which covers localised names such as `~/Dokumente` and `~/Bilder`. Exceptions: `XDG_DOWNLOAD_DIR` is not protected (it is the root of `downloads.installers`, which offers only old installers), and a value equal to home or outside home is ignored with a warning. If a protection covers a rule root (someone set `XDG_PICTURES_DIR=$HOME/.cache`), the plan warns that the rule's items are protected. A malformed file gives a warning, and the built-in English names stay protected. This is additive only, consistent with rule 5 of AGENTS.md.

### L10. Sizes on Linux

`st_blocks*512` and `(dev, ino)` dedupe work unchanged. Known differences, documented in docs/safety.md "Known limits": reflinked extents (btrfs, XFS `cp --reflink`) and btrfs snapshots make allocated bytes an upper bound of what emptying the Trash frees; btrfs transparent compression makes `st_blocks` the compressed size. Moving to the Trash frees nothing until the Trash is emptied, as on macOS.

### L11. TUI and GUI

The TUI is unchanged apart from strings: the done view says items went to "your Trash (~/.local/share/Trash, visible in your file manager)" on Linux. Its key handling has no macOS dependence.

GUI: deferred to v0.3.1. v0.3.0 ships no Linux GUI artifact. CI compile-checks `go build -tags gui ./cmd/kipple-gui` on Ubuntu with the X11 and GL development packages, so the tree stays portable. On Linux `kipple-gui` exits 1 at start with "the Linux app arrives in v0.3.1; use kipple tui" before Fyne initialises. Reasons:
1. The app has macOS behaviour that has never run on Linux: SF fonts read from `/System/Library/Fonts`, "Open Trash" through `/usr/bin/open -a Finder`, ⌘ shortcuts and the ⌘⌫ repeat guard, the Full-Disk-Access-driven Put back sheet, and the `HOME` redirect for Fyne's files. Each needs a Linux design (`xdg-open trash:///`, Ctrl shortcuts, a font fallback) and screenshots.
2. A Linux GUI build needs cgo and X11/Wayland/GL libraries, so it must be built on the Ubuntu runner. The release runs goreleaser OSS once on macOS, and joining an artifact from a second runner into the same signed `checksums.txt` needs a staging step (`checksum.extra_files` from a downloaded artifact folder). That is doable, but it is a new release path that deserves its own review.
3. Nobody can verify a Linux desktop session on the dev machine (colima is headless); v0.3.0 should ship what has been verified.

v0.3.1 plan, recorded in the roadmap: the Ubuntu job builds `kipple-gui_<v>_linux_<arch>.tar.gz` (binary, `kipple.desktop`, the 512px PNG icon, LICENSE), uploads it as a workflow artifact, and the release job downloads it into `prebuilt/` before goreleaser, which lists it through `checksum.extra_files` and `release.extra_files`.

Alternative: ship the Linux GUI in v0.3.0 (rejected for the reasons above).

### L12. Testing

- **Fixtures only.** Every test still builds homes with `testutil.NewHome` in `t.TempDir()` and sets `KIPPLE_HOME` and `KIPPLE_STATE_DIR`. The Trash in every apply test is the fixture's own `.local/share/Trash` or a `DirTrasher` on a fixture folder.
- **Never as root.** kipple refuses euid 0, and chmod-based permission tests prove nothing as root, so the suite must run unprivileged. `testutil.GuardedMain`, which every package's `TestMain` already calls, fails the package before any test when the effective UID is 0, and `TestSuiteIsNotRoot` in `internal/testutil` asserts it by name. This holds on macOS, in the container and on CI.
- **`make test-linux`.** It builds `test/linux/Dockerfile`: `FROM golang:1.23-bookworm@sha256:<pinned>`, which already has git and gcc for `-race`, adds a user `kipple` (uid 1000) with home `/home/kipple`, and sets `USER kipple`. It then runs:
  ```
  docker run --rm \
    -v "$PWD":/src:ro -w /src \
    -v "$(go env GOMODCACHE)":/gomod:ro \
    -v kipple-linux-gocache:/cache \
    --tmpfs /tmp:rw,exec,mode=1777 \
    -e GOFLAGS="-buildvcs=false -mod=readonly" -e GOPROXY=off -e GOTOOLCHAIN=local \
    -e GOMODCACHE=/gomod -e GOCACHE=/cache/go-build -e HOME=/home/kipple -e TMPDIR=/tmp \
    kipple-linux-test test/linux/run.sh
  ```
  A Dockerfile user is preferred over `docker run --user $(id -u):$(id -g)`: the host UID (501 on macOS) has no passwd entry in the image, and the home must be writable and container-local. `run.sh` refuses to run as uid 0. It snapshots `find $HOME -printf '%p %s %T@ %i\n'`, runs `go vet ./...`, `go test -race -count=1 ./...`, `scripts/check-no-real-home.sh` and `scripts/agent-flow-check.sh`, then fails if the `$HOME` snapshot changed (hard rule 1, proven on Linux). The host home is never mounted, the repository is read-only, and the module cache is read-only with `GOPROXY=off`. If the read-only module cache turns out to need writes, `make test-linux-prime` copies it into the named volume once and the run uses that. A preflight in `run.sh` sets a `user.` attribute in `$TMPDIR` and fails with a clear message if tmpfs lacks support (kernel before 6.6). colima's Ubuntu 24.04 VM runs 6.8.
- **`make test-linux-mounts`.** Real mounts need privileges, which ordinary tests do not have. `scripts/linux-mount-check.sh` runs in a throwaway `--privileged` container of the same image. Only the repo is mounted (read-only); the host home is not, and on colima it lives in the VM anyway. As root, the script builds the `kipple` binary to `/tmp`, builds a fixture home under `/tmp/fx`, and creates real mounts inside the fixture only: a bind mount of the fixture's `Documents` at `.cache/foo/docs`, a bind mount at `.cache/bar` itself, a tmpfs at `.cache/baz/t`, and an overlay mount whose lower-layer directory is a cache item. It then drops to uid 1000 with `setpriv` and runs `kipple scan --json` and `kipple clean --apply --json --only ...` with `KIPPLE_HOME=/tmp/fx`. It asserts `contains_mount`, `different_device` and that nothing was copied or moved, and checks a sha256 tree of the fixture before and after.
- **Unit tests on any host.** The mountinfo parser, alias computation, `/proc` reader, `.trashinfo` encoder and decoder, user-dirs parser, catalogue resolution and denylist are pure functions with injected inputs, run on macOS too.
- **Ubuntu CI job** (L13) runs the suite with `-race` on amd64; colima covers arm64.
- **darwin-only tests.** Already tagged: `trash/volume_darwin_test.go`, `apply/{restore,permanent,volume}_darwin_test.go`, `procs/procs_darwin_test.go`. To tag or split: tests that create APFS aliases on disk (`policy/unicode_test.go` cases that open `Documentſ` and expect APFS to reach `Documents` go to `unicode_darwin_test.go`; the lexical folding cases stay portable), FinderTrasher tests, and the GUI tests (`cmd/kipple-gui`, `gui && darwin` in v0.3.0). To parameterise: tests and goldens that hard-code macOS rule IDs or `Library/...` paths (rules golden, scan fixtures, CLI tests such as `explain xcode.deriveddata`, `TestEndToEnd`, TUI model tests, `scripts/agent-flow-check.sh`), which pick a rule present on the running platform from a small per-platform table in `testutil`.
- Every new requirement scenario has one test case named after it.

### L13. Distribution

- **CI** (`ci.yml`): a new `linux` job on `ubuntu-latest` runs `make fmt-check`, `go vet ./...`, `golangci-lint` (with `GOOS=linux`), `make check-no-real-home`, `go test -race -count=1 ./...`, `go build ./...`, a `CGO_ENABLED=0` build of `cmd/kipple` with `scripts/agent-flow-check.sh` run against it (release binaries are cgo-free while race tests use cgo, so this proves the shipped flavour), and the GUI compile check with `libgl1-mesa-dev xorg-dev` installed. `GOCACHE` and `GOMODCACHE` point into `$RUNNER_TEMP`, and setup-go's cache is configured accordingly, so a snapshot of `$HOME` (`find ~ -xdev` listing) taken before and compared after the tests is meaningful. The job fails if it changed. The release gate already requires the whole CI run green, so Linux becomes a release gate without touching `release.yml`.
- **Release** (`.goreleaser.yaml`): a new build `kipple-linux` (`goos: [linux]`, `goarch: [amd64, arm64]`, `CGO_ENABLED=0`, no codesign hook, same ldflags and `-trimpath`) and its archives `kipple_<v>_linux_{amd64,arm64}.tar.gz` (LICENSE, README, docs/safety.md). They are listed in the same `checksums.txt`, which is signed once with cosign keyless as today. goreleaser still runs on the macOS runner, which cross-compiles Linux with cgo off.
- **`go install github.com/rbutera/kipple/cmd/kipple@latest`** works on Linux with or without cgo; nothing on Linux needs cgo.
- **Packaging**: no deb, rpm, AUR, Nix or Homebrew-on-Linux in v0.3.0. The README roadmap lists them, AUR first (the owner runs CachyOS).
- **Docs**: README "Install" gains Linux tarballs and `go install`, plus a "Linux" section (Trash location, file-manager restore, what differs). docs/safety.md gains "On Linux" subsections in "The Trash", "Receipts and restore", "Known limits" and "What the app writes", and the regenerated denylist. `llms.txt` mentions the platforms. `docs/plan-schema.md` documents `mover: freedesktop` and the compatibility rule for enum values and error codes.

## Risks / Trade-offs

- [A same-device bind mount inside a cache item drags a protected folder into the Trash] → mount-root detection by `statx` or mountinfo on every walk (L3), fail closed when unavailable, and the privileged mount check proves it with real mounts.
- [`renameat2` unsupported on the home file system (NFS, FUSE)] → the run stops with `trash_access` before any move. Scan still works. NFS homes are documented as scan-only.
- [User extended attributes unsupported (old tmpfs, some FUSE)] → item skipped with `move_failed` and an explanation; never moved untagged.
- [Users restore from the file manager and then run `kipple restore`] → `already_restored` by identity and tag; nothing moves twice.
- [The Trash created with 0700 surprises a user whose desktop never created one] → this is what GNOME does; documented.
- [`XDG_*` overrides mean kipple misses a tool's real cache] → plan warning naming the variable; following them is on the roadmap with its own invariant design.
- [Agents in another PID namespace (host vs container) are invisible] → documented; idle and git conditions still guard project artifacts.
- [`xdg.cache` is broad] → review tier, 7-day age, exclusions, every child shown with its What/IfRemoved, Trash recoverability.
- [Union denylist refuses Linux-looking paths on macOS] → acceptable: refusing more is the safe direction; the darwin golden catalogue proves no macOS rule lost a root.
- [Read-only module cache mount fails in the container] → `make test-linux-prime` fallback into a named volume.
- [Unprivileged user namespaces blocked on Ubuntu 24.04 runners (AppArmor)] → real-mount tests run only in the privileged container harness locally; CI covers them with stubs and runs the privileged script in a `docker run --privileged` step on the Ubuntu runner, where Docker is available.

## Migration Plan

Wave order (tasks.md):
- **Wave 1** ports the mechanics so the whole default suite passes on Linux in the container as a non-root user. That means the harness, the darwin-only tags, `renameat2`, the tag, the freedesktop Trash, apply and restore wiring, `/proc` and the Linux state dir. darwin-only tests are tagged with a build constraint, never skipped at run time.
- **Wave 2** adds what is specific to Linux safety and content: mount boundaries, home-guard aliases, denylist additions, user-dirs protections, the per-platform catalogue, the XDG policy, platform reporting, and real-mount checks.
- **Wave 3** covers CI, release, the GUI decision and docs.

Wave 1 is not releasable on its own, because same-device bind mounts are not yet a boundary. Nothing is tagged before wave 3.


No data migration. macOS users upgrading see the same catalogue and behaviour, plus `os` and `arch` in `kipple version`. Receipts written by v0.2.0 on macOS keep working (`mover: rename`). Rollback is installing v0.2.0; Linux receipts are only read by v0.3.0+.

## Open Questions

- Whether `maven.repository` and `jetbrains.caches` should also be enabled on macOS in v0.3.1 (macOS roots `~/.m2/repository`, `~/Library/Caches/JetBrains`). This does not affect v0.3.0's specs or tasks.

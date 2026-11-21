# How kipple stays safe

kipple moves files out of your home folder. This page says exactly what
decides whether it may, what it re-checks before every move, where things
go, how to get them back, and what it will never do. The denylist below is
generated from the program itself (`policy.Denylist()`), and a test fails if
this page and the program disagree.

## One gate

Every path kipple lists as movable, moves, or restores is approved by one
function: the safety policy in `internal/policy`. Nothing else in kipple
decides that a path may be moved. It checks, in order:

1. The path is absolute and clean, and the home folder is too.
2. The path is inside the home folder, and is not the home folder itself.
3. The rule is not an `info` rule (those are measured, never moved).
4. The path is not, is not inside, and does not contain a denylisted
   location (see the list below). Names are compared the way macOS compares
   them: case-insensitively, with Unicode case folding, so `Documentſ`
   (long s) is refused like `Documents`.
5. The same for the folders you protect in `~/.config/kipple/config.toml`.
6. The path is below one of the rule's own folders, is not that folder
   itself, and has the shape of the rule's items (for example a
   `node_modules` folder sitting directly beside a `package.json`).
7. Walking down from the home folder, no folder on the way is a symbolic
   link, every one is on the home folder's disk, every name is spelled
   exactly as stored on disk, and none is the same object (same device and
   inode) as a denylisted or protected location under another spelling.

Symbolic links are never followed, anywhere. A link is refused, not
resolved.

File names are data, and some are hostile: a folder can be named with
terminal escape sequences that retitle the window or clear the screen, a
newline that fakes an extra row, or a right-to-left override that reorders
what follows. Everything kipple prints for people (the text output of every
command, and the TUI) shows such characters as visible escapes such as
`\x1b`, `\n` or `\u202e`; JSON output carries the exact name.

## Dry run first, Trash by default

Every command that can change your files is a dry run unless you add
`--apply`. `kipple clean` without `--apply` lists exactly what it would move
and ends by saying nothing was moved. With `--apply`, items go to the Trash
(`~/.Trash`). There is no permanent deletion in this release: `--permanent`
is refused with a usage error (see [Permanent deletion](#permanent)).

On a terminal, `clean --apply` shows the total and asks before moving
anything. With `--json`, or when its output is not a terminal, it never
asks and never reads input: `--apply` itself is the consent. kipple refuses
to run as root and never uses `sudo`.

## Tiers

| Tier | Meaning |
|---|---|
| `safe` | Regenerates by itself. Selected at every level. |
| `review` | Regenerates, but costs time or needs a condition (an idle project with clean, pushed git). Selected only with `--level review`. |
| `manual` | Never selected. Moved only when you name its ID with `--only`. |
| `info` | Measured and reported, never moved. kipple shows the owning tool's own command instead. |

## Re-checked immediately before every move

A scan can be minutes old, and a plan file can be days old or edited. So
`kipple clean --apply` treats the plan as a list of candidates, not as
permission, and for each item, immediately before moving it:

- **Plan files are untrusted.** Items are matched by ID against a scan made
  now, which derives each path again from its rule. An item whose path,
  device and inode, or rule folder's device and inode differ from the plan
  is skipped (`identity_changed`, `root_changed`).
- **The policy runs again**, fresh: protected locations are looked up again,
  the home folder must still be the exact folder kipple approved at start
  (`home_identity_mismatch` otherwise), and the walk keeps every folder on
  the way open, so the move acts on the very object that was approved.
- **The item is measured again**, from that open folder, and refused if part
  of it cannot be read (`scan_partial`), another disk is mounted inside it
  (`contains_mount`), or something protected is inside it, such as a `.env`
  file, an SSH key or a `.git` folder (`contains_protected`).
- **Conditions are evaluated again:** minimum age (`too_recent`), git state
  (`git_dirty`, `git_unpushed`, ...), and whether the project was touched in
  the last 30 days (`project_active`). The git check is read-only and
  uses plumbing commands only (`rev-parse`, `ls-files`, `diff-index
  --cached`, `symbolic-ref`, `rev-list`), none of which produces a diff, so
  no textconv or external diff driver is invoked. The repository's hooks
  and fsmonitor are disabled, every filter driver it configures is set to
  "no filter", lazy fetching is disabled (`GIT_NO_LAZY_FETCH=1`,
  `protocol.allow=never`), and a partial clone is `git_unavailable`
  because its state cannot be proven without fetching. Global and system
  git config are not read, the index is never rewritten, and it gives up
  after 5 seconds. git is found only in `/usr/bin`, `/bin`, `/usr/sbin`,
  `/sbin`, `/opt/homebrew/bin` and `/usr/local/bin`, never through your
  `PATH`, and runs with a fixed environment.
- **The owner must not be running:** the app that owns a cache (checked with
  `/usr/bin/lsappinfo` and `/usr/bin/pgrep`) is `owner_running`, and an
  agent (`claude`, `codex`, `cursor-agent`) whose working directory is
  inside a project (checked with `/usr/sbin/lsof`) is `tool_running`. These
  tools are run by absolute path, never found through `PATH`. If kipple
  cannot tell, it skips `review` and `manual` items and only warns for
  `safe` ones. These are the last checks before the move.
- **The parent folder is confirmed in place:** the operating system is asked
  where the open parent folder is now (`F_GETPATH`); if it was renamed or
  replaced the item is skipped (`parent_changed`).
- **The move itself** is a single rename from that open parent folder into
  the open Trash folder, refusing to replace anything (`renameatx_np` with
  `RENAME_EXCL`), and only if the entry is still the approved object. If the
  Trash is on another disk the item is skipped (`different_device`); kipple
  never copies and deletes instead.

Anything that fails a check is skipped with its reason and the run exits 4
(partial). The rest of the plan proceeds.

A plan file never raises the level: items it selects at `review` are
skipped with `tier_review` unless this run says `--level review`. Without
`--apply`, `kipple clean --plan` runs every check above and reports what
would move and what would be skipped; it moves nothing and writes no
receipt. The confirmation on a terminal shows that same total.

## The Trash and Full Disk Access

macOS protects `~/.Trash`: only apps with Full Disk Access may open it, and
a terminal does not have that by default. kipple first tries to open the
Trash folder and move items into it by rename, as described above. When
macOS refuses, kipple falls back to the system's own Trash call
(NSFileManager `trashItemAtURL`). Immediately before the call kipple
confirms, through the open parent folder, that the name still holds the
approved item, and hands the system a file reference to it. The system
call itself turns that reference back into a path, so if another object is
renamed into the item's place at that very instant, the system can move
that object instead. kipple detects that afterwards (the approved item is still in its
folder, or the reference does not lead to what was moved) and then skips
the item with `identity_changed` and records the object that did move in
the receipt's `displaced` list, with where it is in the Trash and its
identity, so `kipple restore` can put it back. The receipt records which
way each item was moved (`mover`: `rename` or `nsfilemanager`).

If the fallback is not available, `kipple clean --apply` stops with error
code `trash_access` and moves nothing. If the system refuses one item, that
item is skipped with `permission_denied` and the run continues.
`kipple restore --apply` always needs to open the Trash folder itself, so
without Full Disk Access it stops with `trash_access`; items moved by the
fallback may also be put back with Finder's "Put Back". Grant Full Disk Access
to your terminal (System Settings > Privacy & Security > Full Disk Access),
or run kipple from a terminal that has it. When `KIPPLE_HOME` points at a
test folder there is no fallback, since it would put that folder's items in
your real Trash.

Go's module cache is read-only, and macOS will not move a read-only folder
to another folder. For such an item kipple adds write permission to that
one folder for the length of the rename and then sets it back; nothing
inside it is changed.

## Receipts and restore

Every `--apply` run writes a receipt, a JSON file in
`~/Library/Application Support/kipple/receipts` on macOS,
`~/.local/state/kipple/receipts` on Linux (or `$KIPPLE_STATE_DIR/receipts`).
It is written before the first move and saved again after every item, so
even an interrupted run leaves a record. It is a write-ahead record:
immediately before each move the item is saved as `pending`, and if that
save fails (a full disk) the item does not move and the run stops with
error code `receipt`. After the move it is saved again as moved (or, for
an object that was moved instead of it, displaced). If kipple is killed
or the disk fills between the move and that second save, the item stays
`pending`, and `kipple restore` finds it in the Trash by its tag and
identity. It lists every moved item's
original path, Trash path, size on disk, device, inode, its own size and
modification time, and how it was moved, and every skipped item with its
reason. `kipple history` lists receipts, newest first.

Before an item goes to the Trash, kipple tags it with an extended
attribute (`com.rbutera.kipple`) naming the receipt, the item and the home
folder.

`kipple restore <receipt-id>` puts items (and any pending or displaced
entries) back. It is a dry run unless `--apply`. It only uses receipts in
kipple's own receipts folder (read through that folder, never through a
symlink), named by their ID from `kipple history` or by an item ID, which
finds the newest receipt with that item moved, pending or displaced and not
yet restored; a receipt file elsewhere is refused. `kipple history` shows
how many entries each receipt has moved, pending and displaced. A displaced object
goes back to the item's path, if that is free, wherever it was before the
swap; Finder's "Put Back" may return it to where it came from instead. Receipts are treated as untrusted too: an
item's ID must be the one its rule and path give, and it is put back only
if its Trash entry is directly inside the Trash, is opened through the
open Trash folder (never by path, never through a link), is the very object
kipple moved there (same device, inode, size and modification time as the
receipt, and kipple's tag for this receipt and item), and has nothing
protected in its name or inside it (`trash_item_missing`,
`trash_item_mismatch`, `contains_protected` otherwise), and only if
its original folder still exists, the original path is free
(`destination_exists` otherwise, in any spelling), and the original path
passes the same policy checks for the item's rule. The move back refuses to
replace anything.

The move back is staged: the entry is first renamed inside the Trash to a
visible name, `<name> (kipple restoring <hex>)`, and checked there through
its descriptor (identity, size, modification time, tag); only then is it
renamed to the destination. If a different object is found at either step
(something else was renamed into the Trash name or the staging name at
that instant), that object is put back in the Trash, under its Trash name
or a visible `<name> (kipple returned <hex>)` name, and the receipt notes
which. If kipple stops between the two renames, the entry stays visible in
the Trash under its staging name, and the next restore finds it by its
identity and tag; restore does the same for any entry renamed inside the
Trash (by Finder, for example). Applying restores and saves of one receipt
take a lock on it, so two restores at once never lose each other's notes; a
dry-run restore takes no lock and writes nothing, so it also works with a
read-only receipts folder. The receipt's home is checked on the copy loaded
from the receipts folder, the one restore acts on.

What the tag and identity checks protect against is stale or mistaken
receipts: a receipt for another home, a Trash entry you emptied or
replaced, an entry that is not the object kipple moved, a destination the
policy no longer allows. They do not stop a program running as you that
can already edit kipple's receipts folder: the tag carries no secret, so
such a program could tag an entry and write a matching receipt. Even then
the entry is only put back at a path the policy approves for the item's
rule, and never if anything inside it is protected.

## Skip reasons

<!-- skip-reasons:start -->
| Reason | Meaning |
|---|---|
| `not_found` | An ID passed to `--only` is not in the plan, or a scan now no longer finds the item. |
| `info_tier` | An info item was named; kipple never moves those. |
| `permanent_not_allowed` | `--permanent` acts only on safe items (builds with permanent deletion only; not in this release). |
| `tool_running` | An agent process is working inside the project, or kipple could not tell. |
| `identity_changed` | The item is not the object the plan recorded, or it changed during the move. |
| `root_changed` | The rule's folder is not the one the plan recorded. |
| `parent_changed` | The item's folder moved or was replaced immediately before the move. |
| `different_device` | The item and the Trash (or the restore destination) are on different disks. |
| `trash_changed` | The Trash folder was replaced during the run. |
| `permission_denied` | macOS refused the move (for example a folder protected by System Integrity Protection or privacy settings). kipple never asks for more permissions. |
| `move_failed` | The move failed for another reason; see the detail. |
| `destination_exists` | Restore: something is already at the original path. |
| `trash_item_missing` | Restore: nothing is at the recorded Trash path. |
| `trash_item_mismatch` | Restore: the Trash entry is not the item kipple moved there (device, inode, size or modification time differ from the receipt). |
| `not_in_trash` | Restore: the receipt's Trash path is not directly inside the Trash. |
| `already_restored` | Restore: the receipt says the item was already put back. |
| `deleted_permanently` | Restore: the item was deleted with `--permanent`. |
<!-- skip-reasons:end -->

An item that is no longer selectable is skipped with the reason a scan
would give it (for example `git_dirty`, `too_recent`, `owner_running`, or
`refused:symlink_in_path`); those are documented in
[plan-schema.md](plan-schema.md).

## What kipple refuses to do

- Touch anything outside your home folder, on another disk, or behind a
  symbolic link.
- Touch anything on the denylist below or that you protect in config.
  Config can only add protections; it can never remove one.
- Move an `info` item, or delete anything permanently (not in this
  release).
- Move anything without `--apply`, or prompt when it is not on a terminal.
- Copy and delete when a rename across disks is not possible.
- Replace an existing file or folder, when moving to the Trash or when
  restoring.
- Run git hooks or fsmonitor, write to a repository, or use the network.
- Run as root or use `sudo`.

## What the app writes

The optional Mac app (`kipple-gui`) moves items and writes receipts exactly
as `kipple clean --apply` does. Fyne, the toolkit it is built on, also keeps
two things of its own in your home folder, and they are the only files the
app writes outside the Trash and the receipts folder:

- `~/Library/Preferences/fyne/com.rbutera.kipple/preferences.json`, Fyne's
  preferences for the app (kipple stores nothing in it);
- `~/Library/Caches/font_index_v6.cache`, an index of the system fonts that
  Fyne's text renderer builds so it can find them quickly.

When `KIPPLE_HOME` is set (a test folder or an agent's sandbox), the app
puts both under `<state folder>/fyne-home` instead, so a test run writes
nothing in the real home folder.

## Codex worktrees

Every git worktree has a `.git` file at its top that points at the main
repository, and `.git` is on the denylist. For the `codex.worktrees` rule
only, that single top-level `.git` file is not counted as protected, and in
exchange the worktree must be committed and pushed (`clean_pushed`) before it
can be moved; it is also `manual`, so it moves only when you name it. A
`.git` folder, or a `.git` anywhere deeper, still blocks it.

## Known limits

- Items moved by rename into `~/.Trash` are unknown to Finder's "Put Back";
  use `kipple restore`, which knows them.
- A receipt is a file you own, and so is the tag (see "Receipts and
  restore" above for what that does and does not protect).
- A pending entry for an object displaced by the fallback cannot be found
  by restore, since its identity was not known before the move; it is still
  in the Trash.
- git does not see uncommitted changes inside a submodule's own working
  tree, and files ignored only through your global git excludes count as
  untracked (which keeps the project).
- Between the last check and the rename, a program can still write a new
  file inside an item. Such a file goes to the Trash with the item and can
  be restored from there.

### On Linux

- A home on a file system without atomic no-replace renames (NFS, some
  FUSE file systems) is scan-only. kipple writes its receipt with the same
  kind of rename, so `clean --apply` stops when it cannot write the receipt,
  before anything moves; if the receipt can be written elsewhere
  (`KIPPLE_STATE_DIR`), the first move stops the run with `trash_access`.
  Nothing is ever moved with a rename that could replace something.
- A mount point is a boundary even when it shares its device with home (a
  bind mount): an item that is or contains one is never moved, and a Trash
  folder that is one is refused. (The v0.3.0 development snapshots before
  this check could move a bind mount that lived on home's device.)
- User extended attributes are required to move anything (see "On Linux,
  The Trash"): tmpfs before Linux 6.6 and some FUSE file systems lack them,
  so their items are skipped.
- overlayfs: a folder that comes from a lower layer cannot be renamed
  (`EXDEV`) unless `redirect_dir` is on, and is skipped with
  `different_device`, never copied. Containers whose home is a volume are
  unaffected.
- btrfs: a subvolume under home has its own device number, so its items are
  `different_device` (kipple has one Trash per home; the per-device
  `.Trash-<uid>` folders are not used). With reflinked files or snapshots,
  the sizes shown are an upper bound of what emptying the Trash frees, and
  with compression they are the compressed size.
- kipple sees only the processes in its own PID namespace (and none of
  another user's, or any hidden by `hidepid`): an agent running on the host
  in a project bind-mounted into a container is invisible to kipple in the
  container. The idle and git conditions still apply.
- Names of 246 to 255 bytes cannot get a `.trashinfo` file (its name would
  pass 255 bytes): such an item stays, with a detail saying the name is too
  long for the Trash.
- Without `/proc`, kipple cannot confirm an item's folder just before the
  move, so nothing moves (the detail says `/proc` is not available).
- A mount made inside a folder after kipple has walked it again in the
  Trash is not seen: it would need a privileged process mounting onto the
  folder's new place in the Trash in that instant. Before emptying the
  Trash, run `findmnt -R ~/.local/share/Trash` and unmount anything it
  lists. Do not rely on `rm -r --one-file-system`: a bind mount of a folder
  from the same file system (say `~/.ssh`) does not change the device, so
  it is followed and its contents deleted.
- Mounts made in another mount namespace (a sandboxed app, a container, a
  process started with `unshare -m`) are invisible to kipple, which sees
  only its own namespace's mounts. An item holding such a mount moves to
  the Trash, and emptying the Trash from inside that namespace would follow
  the mount.

## On Linux

kipple runs on Linux with the same model: one gate, dry run by default, the
Trash only, receipts and restore, no symlink followed, home only, never root.
What differs is described here.

### The Trash

On Linux the Trash is the freedesktop.org home Trash,
`~/.local/share/Trash`, with the items in `files/` and one
`info/<name>.trashinfo` file per item recording its original path and
when it was trashed, so GNOME Files, Dolphin and other file managers show
the item with its original location and can restore it. kipple uses this
default location and does not follow `XDG_DATA_HOME` (see below); with
`KIPPLE_HOME` it is the fixture's own `.local/share/Trash`.

For each item kipple first reserves `info/<name>.trashinfo` (created
exclusively, so it can never overwrite one), writes the original path
(percent-encoded, so any byte in a name survives) and the time, syncs it,
and only then renames the item into `files/<name>` with a rename that
never replaces anything (`renameat2` with `RENAME_NOREPLACE`), from the
folder descriptor the policy verified. A name already taken gets a
Finder-style name (`http 2`, `report 2.pdf`). If the move does not happen,
kipple removes the info file it wrote, and only that one (checked by
inode); it never removes an info file it did not write. An item on another
device than the Trash is skipped (`different_device`), never copied.

Before moving anything kipple checks the Trash: `Trash`, `files` and `info`
must be real folders (no symlink anywhere on the way, no mount point), on
home's device, owned by you, usable by you and not writable by group or
others; otherwise the run stops with `trash_access` and nothing moves. If
`.local`, `.local/share`, `Trash`, `files` or `info` is missing, kipple
creates it with mode 0700, as GNOME does, but only in a `clean --apply` run
that has something to move. A dry run, a scan and the GUI's checks never
create anything.

Every item kipple moves first gets the extended attribute
`user.com.rbutera.kipple` (the receipt, the item and the home), which
`kipple restore` requires. A file system without user extended attributes
(tmpfs before Linux 6.6, some FUSE file systems) cannot carry it: the item
is skipped with `move_failed` and stays where it is, because an untagged
Trash entry could not be restored by kipple. Device nodes, FIFOs and
sockets are refused without being opened.

### Putting things back

`kipple restore <receipt> --apply` works as on macOS (staging inside
`files/`, never replacing anything) and then removes the item's
`.trashinfo` file, after the move, and only if it is the file kipple wrote
(its inode is in the receipt) and still names the item's original path;
otherwise it leaves the file and says so. You can also use Restore in your
file manager's Trash: it renames the item back and removes the info file,
and the item keeps kipple's tag, so a later `kipple restore` reports it as
`already_restored` instead of moving anything. If you empty the Trash in
the file manager, restore reports `trash_item_missing`.

### Mount points

A mount point is a boundary even when it shares its device with home, as a
bind mount does. kipple asks the kernel whether each folder it passes is a
mount root (`statx` `STATX_ATTR_MOUNT_ROOT`, Linux 5.8 and later), or,
where the kernel does not say, compares every folder with the mount points
under home listed in `/proc/self/mountinfo`. A candidate that is a mount
point, or lies below one, is refused with `different_device`; an item that
contains one is `contains_mount` and never moves; a Trash folder that is a
mount point is `trash_access`. If neither source can answer, scan marks
every item `contains_mount` and `clean --apply` stops with
`mounts_unavailable` before moving anything. Where kipple reads the mount
table instead of asking the kernel, it reads it again before each item and
after each move, so a mount made during the run is seen. After moving a
folder, kipple walks it again in the Trash: if a mount appeared inside it
during the move, it puts the folder back where it was (`contains_mount`);
if that is impossible, the result and the receipt say so, and you should
not empty the Trash until that mount is gone. With `KIPPLE_HOME`, the check
that the folder is not your real home also looks through bind mounts
listed in `/proc/self/mountinfo`, and refuses if that table cannot be read.

### Your folders

On Linux the folders named in `~/.config/user-dirs.dirs` (for example a
localised `~/Dokumente` or `~/Bilder`) are protected like the built-in
Documents, Pictures and the rest (reason `user_protected`). The Downloads
folder is the exception, since `downloads.installers` offers old
installers there. A value that is your home folder itself or outside it is
ignored with a warning, a malformed file gives a warning, and the
built-in protections always stay; the file can only add protections. If a
protected folder covers a rule's folder, the plan says that rule's items
are never offered.

### XDG variables

kipple uses the XDG default locations (`~/.cache`, `~/.local/share`,
`~/.config`, `~/.local/state`) and does not follow `XDG_CACHE_HOME`,
`XDG_DATA_HOME`, `XDG_CONFIG_HOME` or `XDG_STATE_HOME`. When one is set to
another place, scan and clean warn: kipple scans only the default location
and never looks outside your home folder, and with `XDG_DATA_HOME` your
desktop's Trash may be elsewhere, so put kipple's items back with `kipple
restore`. With `KIPPLE_HOME` set the variables are ignored entirely.

### What kipple writes on Linux

Receipts go to `~/.local/state/kipple/receipts` (`KIPPLE_STATE_DIR`
overrides it), and `clean --apply` writes into the Trash as described
above, creating its folders 0700 when they are missing. Nothing else is
written; config stays at `~/.config/kipple/config.toml`. The GUI is not
available on Linux in v0.3.0 (use `kipple tui`).

### Running agents and apps

On Linux kipple reads `/proc` for your own processes only: a process
counts as running when its name (`comm`) or the base name of its first
command-line argument matches, which covers names longer than the kernel's
15 characters and Node programs such as the npm `claude` command. If
`/proc` cannot be read, review and manual items whose owner check applies
are skipped (`tool_running`, `owner_running`).

### The ~/.cache rule (`xdg.cache`)

On Linux, `~/.cache` is where the XDG standard puts data that apps can
recreate, and `xdg.cache` is the Linux twin of the macOS `caches.user`
rule: it is `safe`, so its items are pre-selected, but only a child of
`~/.cache` untouched for 7 days is offered, every item is checked again just
before it moves, it goes to the Trash, and the receipt lets you put it back.
These children of `~/.cache` are never offered by it, because another rule
owns them or because they hold state that is not a cache. The names match
direct children of `~/.cache` only (case folded): a folder of the same name
deeper inside another child, such as `~/.cache/foo/zsh`, goes with that
child.

<!-- xdg-cache-excludes:start -->
| Child of `~/.cache` | Why it is never offered |
|---|---|
| `pip` | Another rule owns it, with its own tier and conditions. |
| `uv` | Another rule owns it, with its own tier and conditions. |
| `go-build` | Another rule owns it, with its own tier and conditions. |
| `yarn` | Another rule owns it, with its own tier and conditions. |
| `pnpm` | Another rule owns it, with its own tier and conditions. |
| `pypoetry` | Another rule owns it, with its own tier and conditions. |
| `huggingface` | Another rule owns it, with its own tier and conditions. |
| `JetBrains` | Another rule owns it, with its own tier and conditions. |
| `Homebrew` | Another rule owns it, with its own tier and conditions. |
| `keyring*` | Older GNOME keyring versions keep their control directory here; it is session state for the password store, not a cache. |
| `gnome-keyring` | Older GNOME keyring versions keep their control directory here; it is session state for the password store, not a cache. |
| `sessions` | Xfce and LXDE save the open-application session here; removing it loses the saved session. |
| `evolution` | Evolution's offline mail store can hold messages and drafts not yet synchronised to the server. |
| `rclone` | rclone's mount write-back cache can hold file changes not yet uploaded. |
| `zellij` | zellij keeps session-resurrection layouts here, which are user state. |
| `keepassxc` | KeePassXC keeps its recent-database and key-file list here, which belongs to a password manager. |
| `ibus` | Some input methods (ibus-libpinyin, ibus-typing-booster) keep the phrases they learned from your typing here. |
| `torch` | Another rule owns it, with its own tier and conditions. |
| `whisper` | Another rule owns it, with its own tier and conditions. |
| `llama.cpp` | Another rule owns it, with its own tier and conditions. |
| `lm-studio` | Another rule owns it, with its own tier and conditions. |
| `obexd` | BlueZ saves files received over Bluetooth here, and they may be the only copy. |
| `zsh` | Common zsh setups (oh-my-zsh, zinit, prezto) keep your shell history and completion state here. |
| `onedriver` | onedriver's OneDrive file cache can hold changes not yet uploaded. |
| `yay` | AUR helpers keep their PKGBUILD clones here, and you may have edited them. |
| `paru` | AUR helpers keep their PKGBUILD clones here, and you may have edited them. |
| `pikaur` | AUR helpers keep their PKGBUILD clones here, and you may have edited them. |
| `aurutils` | AUR helpers keep their PKGBUILD clones here, and you may have edited them. |
| `vim` | Some Vim setups keep undo history and swap files here, which hold edits not yet saved. |
| `SpaceVim` | SpaceVim keeps undo history and swap files here, which hold edits not yet saved. |
| `vdirsyncer` | vdirsyncer keeps its sync state here; losing it makes the next sync treat every contact and event as changed. |
| `cliphist` | cliphist keeps your clipboard history here. |
| `kioexec` | KDE keeps local copies of remote files you are editing here until they are uploaded. |
<!-- xdg-cache-excludes:end -->

A child whose name is on the denylist (such as `.env*`, `.git`, `*.kdbx` or
`*.pem`) is refused by the policy anyway, and protected names inside an item
make it `contains_protected`, as everywhere.

<a id="permanent"></a>
## Permanent deletion

This release has no permanent deletion. `kipple clean --permanent` is
refused with a usage error and everything goes to the Trash. The
implementation exists behind the `kipple_permanent` build tag and is
tested there, but it still checks an entry by name and then removes it by
name, and a program swapping names at exactly that moment could make it
delete something else (the red-team review won that race once in 60
contended runs). It ships when that gap is closed.

## Roadmap

- **Permanent deletion with staging.** Inside the held item folder, create
  a kipple-owned 0700 staging folder; rename each entry into it with
  `renameatx_np(RENAME_EXCL)` under a random name, confirm through a
  descriptor at the staging name that it is the recorded object, and only
  then unlink it there. After each unlink, check that the held
  descriptor's link count dropped as expected (a tripwire for anything
  that slipped through), and stop if not.
- **Receipts and tags that resist a same-user forger.** Sign each receipt
  entry and tag with an HMAC whose key lives in the login Keychain with an
  access list limited to the kipple binary, so another program running as
  you has to pass a Keychain prompt to forge one.

## Denylist

These locations, relative to your home folder, are refused along with
everything inside them and every folder that contains them. Entries starting
with `**/` match a name at any depth.

<!-- denylist:start -->
- `Documents`
- `Desktop`
- `Pictures`
- `Pictures/Photos Library.photoslibrary`
- `Movies`
- `Music`
- `Videos`
- `Templates`
- `Public`
- `Library/Mobile Documents`
- `Library/CloudStorage`
- `Library/Mail`
- `Library/Messages`
- `Library/Photos`
- `Library/Application Support/MobileSync`
- `Library/Application Support/CloudDocs`
- `Library/Application Support/AddressBook`
- `Library/Calendars`
- `Library/Autosave Information`
- `Library/Keychains`
- `Library/Preferences`
- `Library/Containers`
- `Library/Group Containers`
- `Library/Accounts`
- `Library/IdentityServices`
- `Library/Application Support/com.apple.TCC`
- `Library/Safari`
- `Library/Cookies`
- `Library/HTTPStorages`
- `Library/Application Support/Google/Chrome`
- `Library/Application Support/BraveSoftware`
- `Library/Application Support/Microsoft Edge`
- `Library/Application Support/Arc`
- `Library/Application Support/Firefox`
- `Library/Application Support/1Password`
- `Library/Application Support/Bitwarden`
- `.password-store`
- `.local/share/keyrings`
- `.local/share/kwalletd`
- `.pki`
- `.config/1Password`
- `.config/Bitwarden`
- `.config/keepassxc`
- `.config/sops`
- `.config/rclone`
- `.config/containers`
- `.mozilla`
- `.thunderbird`
- `.config/google-chrome`
- `.config/chromium`
- `.config/BraveSoftware`
- `.config/microsoft-edge`
- `.config/vivaldi`
- `.config/evolution`
- `.local/share/evolution`
- `.config/Signal`
- `.local/share/TelegramDesktop`
- `Library/Application Support/Code/User`
- `Library/Application Support/Cursor/User`
- `Library/Application Support/Windsurf/User`
- `Library/Application Support/Claude`
- `.config/Code/User`
- `.config/Cursor/User`
- `.config/Windsurf/User`
- `.config/Claude`
- `Library/Developer/Xcode/UserData`
- `Library/MobileDevice/Provisioning Profiles`
- `Library/Developer/CoreSimulator/Devices`
- `Library/Developer/CoreSimulator/Profiles/Runtimes`
- `.ssh`
- `.gnupg`
- `.aws`
- `.config/op`
- `.config/gh`
- `.kube`
- `.netrc`
- `.npmrc`
- `.docker`
- `.config/gcloud`
- `.azure`
- `.terraform.d`
- `.git-credentials`
- `.pgpass`
- `.vault-token`
- `.pypirc`
- `.gem/credentials`
- `.cargo/credentials*`
- `.zsh_history`
- `.bash_history`
- `.local/state`
- `.local/share/fish`
- `.local/share/Trash`
- `.local/share/docker`
- `.local/share/containers`
- `.local/share/flatpak`
- `snap`
- `.var/app/*/data`
- `.var/app/*/config`
- `.ark`
- `.claude/projects`
- `.claude/memory`
- `.claude/plans`
- `.claude/skills`
- `.claude/agents`
- `.claude/commands`
- `.claude/CLAUDE.md`
- `.claude/settings*.json`
- `.claude/.credentials.json`
- `.claude.json`
- `.codex/sessions`
- `.codex/memory`
- `.codex/memories`
- `.codex/auth.json`
- `.codex/config.toml`
- `.codex/prompts`
- `.ollama`
- `**/.env`
- `**/.env.*`
- `**/*.env`
- `**/.envrc`
- `**/.git`
- `**/*.keychain*`
- `**/*.photoslibrary`
- `**/*.kdbx`
- `**/*.kdb`
- `**/*.pem`
- `**/*.p12`
- `**/id_rsa*`
- `**/id_ed25519*`
- `**/.Trash-*`
<!-- denylist:end -->

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

## Dry run first, Trash by default

Every command that can change your files is a dry run unless you add
`--apply`. `kipple clean` without `--apply` lists exactly what it would move
and ends by saying nothing was moved. With `--apply`, items go to the Trash
(`~/.Trash`). `--permanent` deletes instead, and only acts on `safe` items;
every other item is skipped with `permanent_not_allowed`.

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
  the last 30 days (`project_active`). git runs read-only and never runs
  repository code: kipple uses plumbing commands (`ls-files`, `diff-index
  --cached`, `rev-list`) that never produce a diff, disables the
  repository's hooks and fsmonitor, sets every filter driver the repository
  configures to "no filter", reads no global or system git config, never
  rewrites the index, and gives up after 5 seconds. git is found only in
  `/usr/bin`, `/bin`, `/usr/sbin`, `/sbin`, `/opt/homebrew/bin` and
  `/usr/local/bin`, never through your `PATH`.
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

`--permanent` deletes from the open item folder down, never following a
link and stopping at another disk. It checks every entry it is about to
delete against the protected names (`.env`, keys, a `.git` folder and the
rest): first in a pass that deletes nothing, then again as it deletes. On
the first protected entry it stops; the item is skipped with
`contains_protected`, and if anything was already deleted the skip is
marked `partial` with how many entries and bytes were removed. The same
`partial` marking applies to a permanent delete that fails partway.

## The Trash and Full Disk Access

macOS protects `~/.Trash`: only apps with Full Disk Access may open it, and
a terminal does not have that by default. kipple first tries to open the
Trash folder and move items into it by rename, as described above. When
macOS refuses, kipple falls back to the system's own Trash call
(NSFileManager `trashItemAtURL`), and still acts on the object, not the
name: it builds a file reference to the very item that passed every check,
confirms its device and inode immediately before the call, and afterwards
confirms the item is gone from its folder and is the object now in the
Trash. The receipt records which way each item was moved (`mover`:
`rename` or `nsfilemanager`).

If both are refused, `kipple clean --apply` and `kipple restore --apply`
stop with error code `trash_access` and move nothing. Grant Full Disk Access
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
`~/Library/Application Support/kipple/receipts` (or `$KIPPLE_STATE_DIR/receipts`).
It is written before the first move and saved again after every item, so
even an interrupted run leaves a record. It lists every moved item's
original path, Trash path, size on disk, device, inode, its own size and
modification time, and how it was moved, and every skipped item with its
reason. `kipple history` lists receipts, newest first.

`kipple restore <receipt>` puts items back. It is a dry run unless
`--apply`. Receipts are treated as untrusted too: an item is put back only
if its Trash entry is directly inside the Trash and is the very object
kipple moved there: same device, inode, size and modification time as the
receipt, read through the open Trash folder (`trash_item_missing`,
`trash_item_mismatch` otherwise). Without Full Disk Access the entry is
reached by its path, with the same checks before and after the move.
its original folder still exists, the original path is free
(`destination_exists` otherwise, in any spelling), and the original path
passes the same policy checks for the item's rule. The move back refuses to
replace anything.

## Skip reasons

<!-- skip-reasons:start -->
| Reason | Meaning |
|---|---|
| `not_found` | An ID passed to `--only` is not in the plan, or a scan now no longer finds the item. |
| `info_tier` | An info item was named; kipple never moves those. |
| `permanent_not_allowed` | `--permanent` acts only on safe items. |
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
- Move an `info` item, or permanently delete anything but a `safe` item.
- Move anything without `--apply`, or prompt when it is not on a terminal.
- Copy and delete when a rename across disks is not possible.
- Replace an existing file or folder, when moving to the Trash or when
  restoring.
- Run git hooks or fsmonitor, write to a repository, or use the network.
- Run as root or use `sudo`.

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
- A receipt is a file you own. Someone who can write it and can read a
  Trash entry's identity could forge a receipt that names that entry; it
  would still only be put back at a path the policy approves for the
  item's rule.
- git does not see uncommitted changes inside a submodule's own working
  tree, and files ignored only through your global git excludes count as
  untracked (which keeps the project).
- Between the last check and the rename, a program can still write a new
  file inside an item. Such a file goes to the Trash with the item and can
  be restored from there.

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
- `Library/Application Support/Code/User`
- `Library/Application Support/Cursor/User`
- `Library/Application Support/Windsurf/User`
- `Library/Application Support/Claude`
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
<!-- denylist:end -->

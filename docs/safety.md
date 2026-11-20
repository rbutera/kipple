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
`~/Library/Application Support/kipple/receipts` (or `$KIPPLE_STATE_DIR/receipts`).
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
Trash (by Finder, for example). Restores and saves of one receipt take a
lock on it, so two restores at once never lose each other's notes.

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

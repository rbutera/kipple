# The kipple plan (schema version 1)

`kipple scan --json` prints one plan document on stdout. It lists every
candidate kipple found, how big it is, whether it is selected, and why or
why not. Nothing moves during a scan. An agent can read the plan, decide,
and later pass item IDs to `kipple clean --only <id,...> --apply`.

The authoritative schema is generated from the Go types, so it cannot drift
from what kipple writes:

```sh
kipple schema plan      # JSON Schema (draft 2020-12) for this document
kipple schema rules     # for kipple rules --json
kipple schema receipt   # for apply receipts (wave 3)
kipple schema result    # for kipple clean --apply --json (wave 3)
```

Every document carries `schema_version`. Adding a field, a new enum value
(such as the receipt mover `freedesktop`) or a new error code (such as
`unsupported_platform` or `mounts_unavailable`) is compatible and keeps the
version; renaming or removing a field, or changing what a field, value or
code means, bumps it. A consumer should therefore tolerate enum values and
error codes it does not know. `kipple schema <document>` always describes
the running binary's output, new enum values included. Unknown fields are
rejected by the schema (`additionalProperties: false`), so a consumer can
rely on the field list of a given binary's schema being complete.

## Top level

| Field | Type | Meaning |
|---|---|---|
| `schema_version` | integer | Always `1` for this version. |
| `generated_at` | RFC 3339 time | When the scan finished, in UTC. |
| `kipple_version` | string | The kipple build that wrote the plan. |
| `home` | string | The effective home folder. Every path is below it. With `KIPPLE_HOME` set, this is that folder with symlinks resolved. |
| `level` | `safe` or `review` | The selection level used. |
| `totals` | object | Sums over items, see below. |
| `rules` | array | One entry per catalogue rule, in catalogue order. |
| `items` | array | Every listed candidate, grouped by rule, sorted by path. |
| `warnings` | array of strings | Problems that did not stop the scan, such as a rule root that is a symlink. |

### `totals`

Byte totals are allocated bytes (what the files occupy on disk), with every
hard-linked file counted once across the whole plan.

| Field | Meaning |
|---|---|
| `listed_count`, `listed_bytes` | All items. |
| `selected_count`, `selected_bytes` | Items with `selected: true`. |
| `reclaimable_bytes` | Bytes that moving the selected items would actually free. Files with other hard links (for example a pnpm store shared with `node_modules`) are excluded, since moving one link frees nothing. |

### `rules[]`

| Field | Meaning |
|---|---|
| `id`, `tier` | The rule and its tier (`safe`, `review`, `manual`, `info`). |
| `status` | `present` (at least one root exists), `absent` (no root exists; not an error, exit code stays 0), or `error` (a root exists but could not be scanned; see `warnings`). |
| `item_count`, `bytes` | The rule's listed items and their allocated bytes. |

## Items

| Field | Meaning |
|---|---|
| `id` | Stable 12-character ID: the first 12 characters of the lower-case, unpadded base32 encoding of `sha256(rule_id + "\x00" + home_relative_path)`. The same layout gives the same ID on every run and machine. |
| `rule`, `tier` | The rule that offers the item and its tier. |
| `path` | Absolute path. |
| `dev`, `ino` | The item's device and inode when it was scanned. Apply refuses a path whose identity has changed since. Both are `0` when the item could not be opened (for example a `refused:symlink_in_path` item). |
| `size_bytes` | Logical bytes (`st_size`), symlinks never followed. |
| `allocated_bytes` | Bytes on disk (`st_blocks * 512`). |
| `reclaimable_bytes` | Allocated bytes excluding files with other hard links. |
| `file_count` | Files, symlinks and other non-directory entries. |
| `newest_mtime` | Newest modification time inside the item; age conditions use it. |
| `scan_status` | `complete`, or `partial` when part of the item could not be read or sat on another volume. Sizes then cover only what was readable, `warnings` says what was skipped, and the item carries reason `scan_partial`. |
| `selected` | Whether `kipple clean --apply` would move it at this level. |
| `reasons` | Why it is not selected (empty when selected). See below. |
| `protected_paths` | Present only when something inside the item is protected: up to five item-relative paths. |
| `warnings` | Present only for partial or unmeasurable items. |
| `owner_command` | Present only on `info` items: the owning tool's own cleanup command. |
| `git` | Present only on items of rules that require a clean git state (project artifacts and Codex worktrees): `{"state": ..., "detail": ...}`. `detail` explains any state other than `clean_pushed`. |
| `at_risk` | Present (true) only when the git state is `dirty` or `unpushed`: removing the item's project files would lose work. |

## Item reasons

`reasons` uses a closed set. An item can carry several; it is selected only
when it carries none.

| Reason | Meaning |
|---|---|
| `tier_review` | A `review` item at level `safe`. Re-scan with `--level review` to select it. |
| `tier_manual` | A `manual` item. Never selected; moved only when its ID is passed to `--only`. |
| `tier_info` | An `info` item. Measured only; kipple never moves it. Use `owner_command`. |
| `too_recent` | Newer than the rule's minimum age (for example 7 days for agent debug logs, 14 for installers in Downloads, 90 for Xcode device support), or the age could not be proven because the scan of the item was partial. |
| `project_active` | A file in the project outside its artifact folders changed within the idle window (30 days), or the project could not be read completely, so it could not be proven idle. |
| `git_dirty` | The project has uncommitted changes. |
| `git_unpushed` | The project has commits its upstream does not. |
| `git_no_upstream` | The project's branch has no upstream. |
| `git_not_a_repo` | The project is not a git repository. |
| `git_unavailable` | `git` could not be run, timed out (5 seconds), or failed. |
| `owner_running` | The app or tool that owns the item is running. |
| `scan_partial` | Part of the item could not be read, or sat on another volume, so kipple could not inspect everything inside it. Such items are never pre-selected, since an unread folder could hide something protected. See the item's `warnings`. |
| `contains_mount` | Another volume is mounted somewhere inside the item. kipple never crosses into it and never selects the item, since moving it would take the mounted volume with it. The item also carries `scan_partial`. |
| `contains_protected` | Something inside the item cannot be regenerated or is protected by your config: a `.env` or `.env.*` file, a keychain, a Photos library, a KeePass `.kdbx` file, anything under a denylisted location, the same file as a protected one through a hard link, or a `.git` folder inside a project artifact such as `node_modules`. See `protected_paths`. Regenerable files that are only refused as item paths, such as `cacert.pem` in a virtualenv or a worktree's `.git` file, do not count. |
| `not_requested` | Only in the plan `kipple clean --only ...` prints as a dry run: another item was named, so this one would be left alone. |
| `refused:<reason>` | The safety policy refused the path itself. `<reason>` is a policy reason: `symlink_in_path`, `different_device`, `name_mismatch` and the rest of the list in the policy package. Such items are shown so you can see what kipple declined to touch; they have no sizes. |

Paths the policy refuses because they are denylisted, protected by config,
excluded by the rule, or not the shape of the rule's items are not listed
at all.

## Git states

`git.state` is one of `clean_pushed`, `dirty`, `unpushed`, `no_upstream`,
`not_a_repo` or `git_unavailable`. Only `clean_pushed` satisfies a rule's
git condition. kipple runs `git status --porcelain=v2 --branch` with the
repository's fsmonitor and hooks disabled, untracked files always listed,
a 5 second timeout, and `GIT_OPTIONAL_LOCKS=0`, so the check never runs
repository code and never writes to the repository. A project artifact is
checked in its project folder (the artifact's parent); a Codex worktree is
checked in the worktree itself.

## Rule roots

`rules[].roots` lists each of the rule's root folders that exists, with its
`dev` and `ino` at scan time. `kipple clean --plan` refuses an item whose
root is no longer that same folder (skip reason `root_changed`).

## Example

A plan for a small fixture home at level `safe`:

```json
{
  "schema_version": 1,
  "generated_at": "2026-11-17T12:00:00Z",
  "kipple_version": "0.1.0",
  "home": "/Users/ada",
  "level": "safe",
  "totals": {
    "listed_count": 4,
    "listed_bytes": 4321251328,
    "selected_count": 1,
    "selected_bytes": 1503232,
    "reclaimable_bytes": 1503232
  },
  "rules": [
    { "id": "caches.user", "tier": "safe", "status": "present", "item_count": 2, "bytes": 1503232,
      "roots": [{ "path": "Library/Caches", "dev": 16777232, "ino": 1180311 }] },
    { "id": "xcode.deriveddata", "tier": "safe", "status": "absent", "item_count": 0, "bytes": 0 },
    { "id": "project.node-modules", "tier": "review", "status": "present", "item_count": 1, "bytes": 119748096 },
    { "id": "ollama.models", "tier": "info", "status": "present", "item_count": 1, "bytes": 4200000000 }
  ],
  "items": [
    {
      "id": "2sqzl6oahyvx",
      "rule": "caches.user",
      "tier": "safe",
      "path": "/Users/ada/Library/Caches/com.example.app",
      "dev": 16777232,
      "ino": 48211907,
      "size_bytes": 1499820,
      "allocated_bytes": 1503232,
      "reclaimable_bytes": 1503232,
      "file_count": 12,
      "newest_mtime": "2026-09-30T08:15:00Z",
      "scan_status": "complete",
      "selected": true,
      "reasons": []
    },
    {
      "id": "e4eigatnt5jd",
      "rule": "caches.user",
      "tier": "safe",
      "path": "/Users/ada/Library/Caches/link",
      "dev": 0,
      "ino": 0,
      "size_bytes": 0,
      "allocated_bytes": 0,
      "reclaimable_bytes": 0,
      "file_count": 0,
      "newest_mtime": "0001-01-01T00:00:00Z",
      "scan_status": "complete",
      "selected": false,
      "reasons": ["refused:symlink_in_path"]
    },
    {
      "id": "er4r6xoqv32k",
      "rule": "project.node-modules",
      "tier": "review",
      "path": "/Users/ada/dev/site/node_modules",
      "dev": 16777232,
      "ino": 51870022,
      "size_bytes": 98304000,
      "allocated_bytes": 119748096,
      "reclaimable_bytes": 20480000,
      "file_count": 18211,
      "newest_mtime": "2026-03-02T10:00:00Z",
      "scan_status": "complete",
      "selected": false,
      "reasons": ["tier_review", "git_unpushed"],
      "git": { "state": "unpushed", "detail": "2 commits not pushed to origin/main" },
      "at_risk": true
    },
    {
      "id": "xpkwypbdgoao",
      "rule": "ollama.models",
      "tier": "info",
      "path": "/Users/ada/.ollama/models/blobs",
      "dev": 16777232,
      "ino": 39004417,
      "size_bytes": 4199991296,
      "allocated_bytes": 4200000000,
      "reclaimable_bytes": 4200000000,
      "file_count": 3,
      "newest_mtime": "2026-06-11T19:42:00Z",
      "scan_status": "complete",
      "selected": false,
      "reasons": ["tier_info"],
      "owner_command": "ollama rm <model>"
    }
  ],
  "warnings": []
}
```

## Receipts on Linux

On Linux, `kipple clean --apply` moves items into the freedesktop.org home
Trash and its receipt records `mover: "freedesktop"`, with `trash_dir` the
Trash's `files` folder. Each moved item also has an `info/<name>.trashinfo`
file, named after its `trash_path`, that file managers use to restore it;
`info_ino` records that file's inode, and `kipple restore` removes it only
if it is still that file and still describes the item's original path.
`info_ino` is an optional field, added within `schema_version` 1. A receipt
written on Linux validates against `kipple schema receipt` from the same
binary:

```json receipt
{
  "schema_version": 1,
  "id": "20261121T103000Z-k2p7qa",
  "kipple_version": "0.3.0",
  "created_at": "2026-11-21T10:30:00Z",
  "complete": true,
  "home": "/home/ada",
  "level": "safe",
  "permanent": false,
  "trash_dir": "/home/ada/.local/share/Trash/files",
  "mover": "freedesktop",
  "plan_generated_at": "2026-11-21T10:29:40Z",
  "moved": [
    {
      "id": "b2p5penllvv2",
      "rule": "pip.cache",
      "path": "/home/ada/.cache/pip/http",
      "trash_path": "/home/ada/.local/share/Trash/files/http",
      "bytes": 1048576,
      "reclaimable_bytes": 1048576,
      "dev": 66306,
      "ino": 1311522,
      "size": 4096,
      "mtime": "2026-10-02T08:14:00Z",
      "mover": "freedesktop",
      "moved_at": "2026-11-21T10:30:01Z",
      "info_ino": 1311700
    }
  ],
  "skipped": [],
  "displaced": [],
  "pending": []
}
```

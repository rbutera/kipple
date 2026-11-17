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

Every document carries `schema_version`. Adding a field is compatible;
renaming or removing a field, or changing what one means, bumps the version.
Unknown fields are rejected by the schema (`additionalProperties: false`), so
a consumer can rely on the field list below being complete.

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
| `git` | Present only on project artifacts: `{"state": ...}`. |
| `at_risk` | Present (true) only when a project's git state is `dirty` or `unpushed` (wave 3). |

## Item reasons

`reasons` uses a closed set. An item can carry several; it is selected only
when it carries none.

| Reason | Meaning |
|---|---|
| `tier_review` | A `review` item at level `safe`. Re-scan with `--level review` to select it. |
| `tier_manual` | A `manual` item. Never selected; moved only when its ID is passed to `--only`. |
| `tier_info` | An `info` item. Measured only; kipple never moves it. Use `owner_command`. |
| `too_recent` | Newer than the rule's minimum age (for example 7 days for agent debug logs, 14 for installers in Downloads, 90 for Xcode device support). |
| `project_active` | A file in the project outside its artifact folders changed within the idle window (30 days). |
| `git_dirty` | The project has uncommitted changes. |
| `git_unpushed` | The project has commits its upstream does not. |
| `git_no_upstream` | The project's branch has no upstream. |
| `git_not_a_repo` | The project is not a git repository. |
| `git_unavailable` | `git` could not be run. |
| `git_unchecked` | The git condition was not evaluated. Builds without git checking (the wave 2 read-only CLI) put it on every project artifact, which is therefore never selected. |
| `owner_running` | The app or tool that owns the item is running. |
| `scan_partial` | Part of the item could not be read, or sat on another volume, so kipple could not inspect everything inside it. Such items are never pre-selected, since an unread folder could hide something protected. See the item's `warnings`. |
| `contains_protected` | Something inside the item cannot be regenerated or is protected by your config: a `.env` or `.env.*` file, a keychain, a Photos library, a KeePass `.kdbx` file, anything under a denylisted location, the same file as a protected one through a hard link, or a `.git` folder inside a project artifact such as `node_modules`. See `protected_paths`. Regenerable files that are only refused as item paths, such as `cacert.pem` in a virtualenv or a worktree's `.git` file, do not count. |
| `refused:<reason>` | The safety policy refused the path itself. `<reason>` is a policy reason: `symlink_in_path`, `different_device`, `name_mismatch` and the rest of the list in the policy package. Such items are shown so you can see what kipple declined to touch; they have no sizes. |

Paths the policy refuses because they are denylisted, protected by config,
excluded by the rule, or not the shape of the rule's items are not listed
at all.

## Git states

`git.state` is one of `clean_pushed`, `dirty`, `unpushed`, `no_upstream`,
`not_a_repo`, `git_unavailable`, or `unchecked`. Only `clean_pushed`
satisfies a project rule's git condition.

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
    { "id": "caches.user", "tier": "safe", "status": "present", "item_count": 2, "bytes": 1503232 },
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
      "size_bytes": 98304000,
      "allocated_bytes": 119748096,
      "reclaimable_bytes": 20480000,
      "file_count": 18211,
      "newest_mtime": "2026-03-02T10:00:00Z",
      "scan_status": "complete",
      "selected": false,
      "reasons": ["tier_review", "git_unchecked"],
      "git": { "state": "unchecked" }
    },
    {
      "id": "xpkwypbdgoao",
      "rule": "ollama.models",
      "tier": "info",
      "path": "/Users/ada/.ollama/models/blobs",
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

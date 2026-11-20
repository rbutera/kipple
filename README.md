<p align="center"><img src="assets/brand/readme-header.svg" alt="kipple: clears the junk your AI tools and dev toolchains leave behind" width="100%"></p>

# kipple

kipple finds the caches, build folders and leftovers that AI coding tools,
local models and developer toolchains pile up in your Mac's home folder,
explains each one, and moves only what you approve to the Trash. It is a
single MIT-licensed Go binary with a JSON contract that agents can drive, a
terminal UI for people, and nothing it does happens without `--apply`.

- [What it cleans](#what-it-cleans)
- [The safety model](#the-safety-model)
- [Install](#install)
- [Usage](#usage)
- [For agents](#for-agents)
- [How it compares](#how-it-compares)
- [Roadmap](#roadmap)
- [Contributing](#contributing) and [licence](#licence)

## What it cleans

kipple knows a fixed catalogue of locations under your home folder
(`kipple rules` lists them all, with a one-sentence "what it is" and "what
happens if it goes" for each). Every rule has a tier:

| Tier | What it means | Examples |
|---|---|---|
| `safe` | Rebuilds itself. Selected by default. | `~/Library/Caches`, `~/Library/Logs`, Xcode DerivedData, simulator caches, Homebrew, npm, pnpm, Yarn, Bun, pip, uv, Poetry, Cargo, Go build, Gradle, CocoaPods and SwiftPM caches, Cursor, VS Code and Windsurf caches, Claude Code shell snapshots and debug logs, Codex logs |
| `review` | Rebuilds, but costs time or needs a condition. Selected with `--level review`. | Go module cache, Xcode device support unused for 90 days, `node_modules`, Rust `target`, Python virtualenvs and `.next` in projects untouched for 30 days whose git state is committed and pushed |
| `manual` | Never selected for you. Moved only when you name the item. | Xcode Archives, the Hugging Face hub cache, Codex worktrees (committed and pushed only), installers in Downloads older than two weeks |
| `info` | Measured and reported, never moved. kipple shows the owner's own command instead. | Ollama models (`ollama rm`), Docker's disk image, Claude Code and Codex session history, simulator runtimes |

## The safety model

kipple is built so that the failure mode is "it refused to move something
safe", never "it moved something it should not have". The details, and the
full denylist, are in [docs/safety.md](docs/safety.md); a test keeps that page
in step with the code.

- **Dry run is the only default.** `scan` never moves anything. `clean` and
  `restore` list what they would do and stop unless you add `--apply`.
- **The Trash is the only destination.** There is no permanent delete in
  v0.1.0: `kipple clean --permanent` is refused with a usage error and
  everything goes to the Trash. Permanent deletion comes back only once its
  staging design passes a red-team review (see the [roadmap](#roadmap)).
- **One gate.** A single policy function approves every path kipple lists,
  moves or restores, and apply asks it again immediately before each move,
  together with the rule's conditions (idle project, clean and pushed git,
  owning app not running) and the item's identity (device and inode).
  Anything that changed since the scan is skipped with a reason.
- **A hard-coded denylist** that config can extend but never shrink:
  Documents, Desktop, Pictures, Movies, Music, iCloud Drive and cloud
  storage, Mail, Messages, Photos libraries, keychains, Preferences,
  `~/.ssh`, `~/.gnupg`, `~/.aws`, `.env` files, Claude Code and Codex
  history, memory, plans and skills, and more. An item that merely
  contains something protected is refused too.
- **Receipts and restore.** Every `--apply` run writes a receipt before the
  first move and updates it item by item. `kipple restore <receipt>` puts
  things back, checking that each Trash entry is the very object kipple
  moved and that nothing now occupies its old path.
- **What kipple refuses outright:** anything outside your home folder or on
  another disk, following a symbolic link anywhere, running as root or with
  `sudo`, copying and deleting when a move is not possible, replacing an
  existing file, writing to a git repository, and the network. There is no
  telemetry.

### Full Disk Access and the Trash

macOS lets only apps with Full Disk Access open `~/.Trash`, and a terminal
does not have it by default. kipple handles both cases:

- **With Full Disk Access**, kipple moves each item straight into
  `~/.Trash` by rename, from a folder it has already verified. The receipt
  records `"mover": "rename"`. Finder's "Put Back" does not know about these
  items; `kipple restore` does.
- **Without it**, kipple falls back to Apple's own Trash call
  (`NSFileManager trashItemAtURL`), after checking that the item's name still
  holds the approved object. The receipt records `"mover": "nsfilemanager"`,
  and both Finder's "Put Back" and `kipple restore` work for these items.
  If another object is swapped into the item's place at that instant, kipple
  notices, records it in the receipt's `displaced` list so it can be
  restored, and skips the item.
- If neither works, `clean --apply` stops with error `trash_access` and
  moves nothing.

`kipple restore --apply` always needs to open the Trash folder itself, so it
needs Full Disk Access. To grant it, open System Settings > Privacy & Security >
Full Disk Access and add your terminal app.

## Install

kipple is for macOS, on Apple silicon and Intel.

### Release binary (recommended)

Each release has a universal archive (both architectures), per-architecture
archives, and a `checksums.txt` signed with [cosign](https://github.com/sigstore/cosign)
keyless signing by this repository's release workflow. The signature comes
as a sigstore bundle (`checksums.txt.sigstore.json`) and, for cosign 2, as a
detached signature and certificate (`checksums.txt.sig`, `checksums.txt.pem`).
Verify before you run:

```sh
VERSION=0.1.0
BASE=https://github.com/rbutera/kipple/releases/download/v$VERSION
curl -fsSLO "$BASE/kipple_${VERSION}_darwin_universal.tar.gz"
curl -fsSLO "$BASE/checksums.txt"
curl -fsSLO "$BASE/checksums.txt.sigstore.json"

# 1. The checksums were signed by the release workflow of rbutera/kipple
#    (cosign 3, or cosign 2.4 and later).
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/rbutera/kipple/.github/workflows/release.yml@refs/tags/v$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

# 2. The archive matches its signed checksum.
shasum -a 256 --check --ignore-missing checksums.txt

# 3. Install.
tar -xzf "kipple_${VERSION}_darwin_universal.tar.gz" kipple
sudo install -m 0755 kipple /usr/local/bin/kipple   # or any folder on your PATH
kipple version
```

With cosign 2 you can verify the detached files instead of the bundle:

```sh
curl -fsSLO "$BASE/checksums.txt.sig"
curl -fsSLO "$BASE/checksums.txt.pem"
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity "https://github.com/rbutera/kipple/.github/workflows/release.yml@refs/tags/v$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

The binary is ad-hoc signed, not notarised (that is on the roadmap). A file
downloaded with `curl` is not quarantined. If you downloaded it with a
browser, macOS may block the first run; remove the quarantine flag with
`xattr -d com.apple.quarantine kipple`.

`sudo` above is only for copying into `/usr/local/bin`. kipple itself refuses
to run as root.

### With Go

```sh
go install github.com/rbutera/kipple/cmd/kipple@latest
```

This needs Go 1.23 or later and the Xcode command line tools (the Trash
fallback uses cgo). A `go install` build reports its version as `dev`.

### Homebrew

A tap (`brew install rbutera/tap/kipple`) is planned after v0.1.0.

## Usage

Every example below is real output from a fixture home folder, with its path
shown as `/Users/you`.

### See what is there

```console
$ kipple scan
Home:  /Users/you
Level: safe

App caches (caches.user, safe)  3.8 MB in 2 items
  [x]   614.4 kB  czv45xjlw6m2  /Users/you/Library/Caches/com.example.editor
  [x]     3.1 MB  ppdu7ck4jyec  /Users/you/Library/Caches/com.spotify.client

App logs (logs.user, safe)  94.2 kB in 1 item
  [x]    94.2 kB  rjy4jw3xncq2  /Users/you/Library/Logs/DiagnosticReports

Xcode DerivedData (xcode.deriveddata, safe)  2.1 MB in 1 item
  [x]     2.1 MB  nwif5flmjnt3  /Users/you/Library/Developer/Xcode/DerivedData/MyApp-bxkq

npm cache (npm.cache, safe)  1.0 MB in 1 item
  [x]     1.0 MB  yurrlbk4dcox  /Users/you/.npm/_cacache/content-v2

Go module cache (go.mod-cache, review)  409.6 kB in 1 item
  [ ]   409.6 kB  xejhouin7ki7  /Users/you/go/pkg/mod/github.com  (tier_review)

Xcode Archives (xcode.archives, manual)  716.8 kB in 1 item
  [ ]   716.8 kB  yhslyxjbfi72  /Users/you/Library/Developer/Xcode/Archives/2026-03-01  (tier_manual)

Old installers in Downloads (downloads.installers, manual)  1.0 MB in 1 item
  [ ]     1.0 MB  fiup3oxwrtwv  /Users/you/Downloads/Installer.dmg  (tier_manual)

Ollama models (ollama.models, info)  2.1 MB in 1 item
  [ ]     2.1 MB  xpkwypbdgoao  /Users/you/.ollama/models/blobs  (tier_info)
      to clean, use: ollama rm <model>

Nothing found for 30 rules (not installed or empty).

Listed 9 items, 11.3 MB. Selected 5 items, 7.0 MB (7.0 MB reclaimable).
Selected total 7.0 MB. Nothing was moved (dry run). Next step: kipple clean --apply
```

Add `--level review` to also select review-tier items, and `--json` for the
machine-readable plan.

### Ask why

```console
$ kipple explain xcode.deriveddata
Home: /Users/you

xcode.deriveddata  Xcode DerivedData
Tier:       safe (regenerates by itself; selected at every level)
What:       Build products, indexes and intermediate files Xcode keeps for every project you have opened.
If removed: The next build of each project starts from scratch and Xcode re-indexes it, which can take a few minutes.
Roots:      ~/Library/Developer/Xcode/DerivedData
Conditions: owner not running
Owner:      Xcode
```

`kipple explain <item-id>` adds the item's path, size, scan status and why it
is or is not selected.

### Clean

`kipple clean` on its own is the same list as `scan`, ending with:

```console
Listed 9 items, 11.3 MB. Selected 5 items, 7.0 MB (7.0 MB reclaimable).
Selected total 7.0 MB. Nothing was moved (dry run). Next step: kipple clean --apply
This was a dry run: nothing was moved. Add --apply to move the selected items to the Trash.
```

Add `--apply` to move the selected items. `--only` names exactly which items
to act on, which is how you choose `review` and `manual` items (naming one is
your consent to its tier; `info` items are never moved):

```console
$ kipple clean --apply --only nwif5flmjnt3,yhslyxjbfi72
Moved 2 items to the Trash (2.8 MB; emptying the Trash frees 2.8 MB).
  moved    nwif5flmjnt3  /Users/you/Library/Developer/Xcode/DerivedData/MyApp-bxkq
      -> /Users/you/.Trash/MyApp-bxkq
  moved    yhslyxjbfi72  /Users/you/Library/Developer/Xcode/Archives/2026-03-01
      -> /Users/you/.Trash/2026-03-01

Receipt 20261120T075210Z-mgohsh (/Users/you/Library/Application Support/kipple/receipts/20261120T075210Z-mgohsh.json).
To put the items back: kipple restore 20261120T075210Z-mgohsh --apply
```

On a terminal, `clean --apply` shows the total and asks first; `--yes` skips
the question. With `--json`, or when output is not a terminal, it never asks:
`--apply` is the consent.

### History and restore

```console
$ kipple history
Home: /Users/you

20261120T075210Z-mgohsh  2026-11-20 07:52:10  moved 2 (2.8 MB), pending 0, displaced 0, skipped 0

$ kipple restore 20261120T075210Z-mgohsh
Receipt 20261120T075210Z-mgohsh
Would restore 2 items (moved 2, pending 0, displaced 0), skipped 0:
  nwif5flmjnt3  /Users/you/Library/Developer/Xcode/DerivedData/MyApp-bxkq
      <- /Users/you/.Trash/MyApp-bxkq
  yhslyxjbfi72  /Users/you/Library/Developer/Xcode/Archives/2026-03-01
      <- /Users/you/.Trash/2026-03-01

This was a dry run: nothing was moved. Add --apply to put the items back: kipple restore 20261120T075210Z-mgohsh --apply

$ kipple restore 20261120T075210Z-mgohsh --apply
Receipt 20261120T075210Z-mgohsh
Restored 2 items (moved 2, pending 0, displaced 0), skipped 0:
  nwif5flmjnt3  /Users/you/Library/Developer/Xcode/DerivedData/MyApp-bxkq
      <- /Users/you/.Trash/MyApp-bxkq
  yhslyxjbfi72  /Users/you/Library/Developer/Xcode/Archives/2026-03-01
      <- /Users/you/.Trash/2026-03-01
```

`kipple restore <item-id>` restores one item from the newest receipt that
moved it.

### Interactive review

`kipple tui` scans and shows everything grouped by rule, with the
highlighted item explained beside it. Space selects or unselects, `a` shows
the total and asks, `y` moves the selection to the Trash, any other key
cancels. It uses the same checks as `clean --apply` (info items cannot be
selected, and nothing blocked by more than its tier can be), and it has no
permanent delete. Colours follow the kipple palette; `NO_COLOR` turns them
off, and it works in 80 columns.

```text
 kipple  0.1.0  /Users/you                                                                          level safe
Found 9 items · 11.3 MB                                                                    Selected 5 · 7.0 MB
╭─────────────────────────────────────────────────────────────╮╭─────────────────────────────────────────────╮
│ App caches safe                                  2 · 3.8 MB ││ App caches safe                      3.1 MB │
│ ▌ ● ~/Library/Caches/com.spotify.client            3.1 MB   ││ ~/Library/Caches/com.spotify.client         │
│   ● ~/Library/Caches/com.example.editor          614.4 kB   ││ Status     Selected: goes to the Trash when │
│ Xcode DerivedData safe                           1 · 2.1 MB ││            you apply.                       │
│   ● …ary/Developer/Xcode/DerivedData/MyApp-bxkq    2.1 MB   ││ What       Files that apps keep in your     │
│ npm cache safe                                   1 · 1.0 MB ││            Caches folder to start and load  │
│   ● ~/.npm/_cacache/content-v2                     1.0 MB   ││            faster, which Apple requires     │
│ App logs safe                                   1 · 94.2 kB ││            apps to rebuild when missing.    │
│   ● ~/Library/Logs/DiagnosticReports              94.2 kB   ││ If removed Apps may start a little slower   │
│ Go module cache review                         1 · 409.6 kB ││            the first time while they        │
│   ○ ~/go/pkg/mod/github.com                      409.6 kB   ││            rebuild or re-download what they │
│ Old installers in Downloads manual               1 · 1.0 MB ││            need, and no documents or        │
│   ○ ~/Downloads/Installer.dmg                      1.0 MB   ││            settings are lost.               │
│ Xcode Archives manual                          1 · 716.8 kB ││ Contents   1 file, newest 20 Nov 2026       │
│   ○ …ibrary/Developer/Xcode/Archives/2026-03-01  716.8 kB   ││ ID         ppdu7ck4jyec                     │
│ Ollama models info                               1 · 2.1 MB ││                                             │
╰─────────────────────────────────────────────────────────────╯╰─────────────────────────────────────────────╯

↑↓ move   tab next rule   space select   a apply   q quit
```

### Commands and flags

| Command | What it does |
|---|---|
| `kipple scan` | List every item, its tier, size and why it is or is not selected. Never moves anything. |
| `kipple explain <item-id\|rule-id>` | Explain a rule, or an item from a fresh scan. |
| `kipple clean` | Dry run of what `--apply` would move. |
| `kipple history` | List receipts. |
| `kipple restore <receipt-id\|item-id>` | Dry run of putting items back; `--apply` does it. |
| `kipple tui` | Interactive review and apply. |
| `kipple rules` | The rule catalogue. |
| `kipple schema plan\|receipt\|rules\|result` | The JSON Schema of a document. |
| `kipple version` | Version, commit and `schema_version`. |

| Flag | Applies to | Meaning |
|---|---|---|
| `--json` | every command | Exactly one JSON document on stdout; everything else on stderr. |
| `--level safe\|review` | scan, clean, tui | Which tiers are selected by default (`safe` unless your config says otherwise). |
| `--apply` | clean, restore | Actually move. Without it nothing changes. |
| `--only <id,...>` | clean, restore | Act only on these item IDs. |
| `--plan <file\|->` | clean | Act on a saved plan from `scan --json`. It is untrusted: items are matched by ID against a fresh scan, and anything that changed is refused. |
| `--yes` | clean | Do not ask on a terminal. |
| `--permanent` | clean | **Refused in v0.1.0** with a usage error. Everything goes to the Trash. |

Configuration lives in `~/.config/kipple/config.toml`: `level` (the default
selection level), `protect` (extra folders kipple must never touch) and
`project_roots` (where to look for project build folders). It can add
protections, never remove one.

## For agents

kipple is designed to be driven by a coding agent: scan, inspect, approve a
subset by ID, apply, audit, undo. Every command takes `--json`, writes exactly
one JSON document to stdout and everything else to stderr, never prompts when
`--json` is set or stdout is not a terminal, and uses fixed exit codes. The
flow below is run, line for line, against a fixture in CI by
[`scripts/agent-flow-check.sh`](scripts/agent-flow-check.sh).

```sh
# 1. Scan. One JSON plan on stdout (schema: kipple schema plan).
kipple scan --json > plan.json

# 2. Inspect. Every item has a stable 12-character ID, its tier, size,
#    path, whether it is selected and the reasons if not.
jq -r '.items[] | [.id, .tier, .selected, .allocated_bytes, .path] | @tsv' plan.json
ID=$(jq -r '[.items[] | select(.selected)][0].id' plan.json)
kipple explain "$ID" --json

# 3. Approve a subset. Without --apply this is a dry run of the same IDs.
IDS=$(jq -r '[.items[] | select(.selected)][0:2] | map(.id) | join(",")' plan.json)
kipple clean --json --only "$IDS"
kipple clean --apply --json --only "$IDS" > result.json

# 4. Audit.
kipple history --json

# 5. Undo. Also a dry run unless --apply is given.
RECEIPT=$(jq -r .receipt_id result.json)
kipple restore "$RECEIPT" --json
kipple restore "$RECEIPT" --apply --json
```

Item IDs are derived from the rule and the home-relative path, so the same
item has the same ID on every run. A saved plan can also be handed back with
`kipple clean --plan plan.json --apply --json`; kipple treats it as a list of
candidates and re-checks everything against a fresh scan.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | Error. With `--json`, stdout is `{"error":{"code":"...","message":"..."}}`. |
| 2 | Usage error: unknown command, flag or argument (including `--permanent` in v0.1.0). |
| 3 | Nothing to do: nothing was selected. |
| 4 | Partial: some items were skipped or failed; each skip has a machine-readable `reason`. |

### Schemas

`kipple schema plan`, `kipple schema receipt`, `kipple schema rules` and
`kipple schema result` print JSON Schemas generated from the same Go types
that encode the documents, so they cannot drift. Every document carries
`schema_version`, currently `1`. It stays `1` for the whole of a kipple major
version: fields may be added, but a change that removes or redefines one
bumps `schema_version` and the major version together.
[docs/plan-schema.md](docs/plan-schema.md) documents every field and every
reason code, and [llms.txt](llms.txt) is a short machine-oriented summary.

### Testing against a fixture

`KIPPLE_HOME` makes kipple treat another folder as home, and
`KIPPLE_STATE_DIR` moves its receipts. Every output then names the effective
home. kipple refuses a `KIPPLE_HOME` that is, contains or sits inside your
real home folder, and never uses the system Trash fallback for it.

## How it compares

kipple is deliberately narrow. These tools are good at things kipple does not
attempt (uninstalling apps, finding duplicates, mapping storage, a native
app); the table compares only the overlap. Facts as of November 2026.

| | kipple | [ElfClean](https://elfclean.app) | [Mole](https://github.com/tw93/Mole) | [Dusty](https://github.com/yagcioglutoprak/dusty) |
|---|---|---|---|---|
| Licence | MIT | Closed source; free scan, one-time unlock for bulk cleaning | GPL-3.0 CLI; paid Mac app | MIT |
| Form | Go CLI and terminal UI | Sandboxed Mac App Store app | `mo` CLI (Bash and Go) | SwiftUI menu bar app with a `dusty` CLI inside the bundle |
| Command line | Yes | No | Yes | Yes |
| JSON | Every command, with published schemas | No | `analyze`, `status` and `history` | `scan --json` |
| Default action | Dry run; `--apply` moves to the Trash | Trash | `clean`, `purge` and `installer` delete permanently; `uninstall` uses the Trash | Permanent unless `--trash`; `clean` needs `--yes` |
| Restore | `kipple restore` from receipts | History screen | Not for permanent deletions | Deletion log |
| Git-aware project cleanup | Yes, re-checked before each move | Yes, re-checked before each move | `purge` checks git ownership | Judges activity by files and git history |

## Roadmap

- **Linux.** The platform-specific parts (the Trash, process checks, the
  no-replace rename) sit behind small interfaces, so a port is new files:
  XDG rule roots, the freedesktop.org Trash, `/proc` checks, and Linux
  release builds. Target v0.3.0.
- **GUI.** An optional native front end built on the same library, shipped
  as a separate download.
- **Homebrew tap** after v0.1.0.
- **Notarisation** with a Developer ID, so macOS never asks on first run.
- **Permanent deletion**, only once its staging design (each entry renamed
  into a private folder and confirmed by descriptor before it is unlinked)
  passes red-team review. Until then `--permanent` is refused.
- **Signed receipts**, keyed from the login Keychain, so another program
  running as you cannot forge one.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The one rule that matters most: tests
and development commands only ever touch fixture folders, never a real home.
Security issues, including any path that escapes the allowlist, go through
[SECURITY.md](SECURITY.md).

## Licence

MIT. See [LICENSE](LICENSE).

# ElfClean and the Mac-cleaner landscape (Nov 2026)

Research for an open-source, agent-first Mac cleaner. Sources: the r/macapps launch
thread (reddit.com/r/macapps/comments/1xyitil, 22 comments, scraped 17 Nov 2026),
elfclean.app (home, support, press, developer and Docker pages), tw93/Mole README,
docs and SECURITY_AUDIT.md, yagcioglutoprak/dusty README, dustly.app, discky.xyz.

## 1. What ElfClean is

ElfClean ("ElfClean: Dev Cache Cleaner", Mac App Store id 6819001057) is a closed-source,
sandboxed macOS 15+ app by solo developer Colin Sergay. Pitch: "Find the space AI and
code left on your Mac." Free to scan, free to clean one item at a time or remove one app;
a one-time US$14.99 unlock enables bulk cleaning. No accounts, analytics, telemetry or
network permission.

What it cleans ("more than 140 kinds of folders"), each tagged with a care level
(Rebuilds itself / Review first / Advanced / You choose):

- AI tools: Ollama models (shared layers counted once), Hugging Face hub cache,
  Claude Code and Codex session history (archived, never treated as a cache),
  agent-created git worktrees (merged ones flagged), old agent binaries
  (`~/.local/share/claude/versions`), Cursor/Devin/VS Code caches. Tools known:
  Claude Code, Codex, Cursor, Devin (Windsurf), VS Code, Roo Code, Aider, Continue.
- Projects: `node_modules`, `.next`, build dirs, Rust `target`, venvs, but only in
  projects untouched for 30+ days.
- Package caches: npm, pnpm, Yarn, Bun, pip, uv, Poetry, Cargo, Go, Gradle, CocoaPods,
  SwiftPM, Homebrew.
- Xcode: DerivedData (caches level), DeviceSupport (90-day rule), Archives (manual only,
  because they hold dSYMs), simulator caches/devices (runtimes never removed).
- Everyday: `~/Library/Caches`, `~/Library/Logs`, old installers in Downloads,
  leftovers of deleted apps, large files, duplicates, a home-folder storage map.
- Not cleaned, only measured with a copyable command: Docker.raw, Colima, OrbStack,
  Homebrew old versions, simulator runtimes. "ElfClean never runs these commands itself."

Three levels: Caches, Recommended (default, adds things that rebuild), Advanced
(adds models and recent items, resets on relaunch). Always manual regardless of level:
downloads, installers, language versions, conversations, worktrees, leftovers,
simulator devices, archives.

## 2. What ElfClean gets right (the trust features)

1. **Git check.** Every project is read; uncommitted or unpushed work is marked
   "At risk" and no level ticks it. Re-checked immediately before each move; if a
   worktree changed between scan and clean, the item is skipped with a reason. Also
   skipped if Claude Code or Codex is running in that project.
2. **Trash by default**, with a History screen that restores items (needed because
   Finder's Put Back does not work for App Store apps). Permanent delete is opt-in in
   Settings and only offered when everything ticked "rebuilds itself".
3. **Plain-language explanation** on every item: what it is, who made it, what
   happens if it goes.
4. **Honest numbers**: measured sizes, no health score, no "speeds up your Mac" claim.
5. **Never-touch list** and per-item "Keep".
6. **Protected by design**: running apps, credentials, cloud drives, Apple/system apps,
   "shared or ambiguous data".
7. Press kit claims 1,100+ package tests, 500 app tests, and audits that required a
   failing test per bug.

## 3. What the Reddit thread says

Thread is small (22 comments, mostly code requests) but the signal is clear:

- Praise: "Checking Git before ticking anything is what makes me trust a cleaner like
  this." Users describe being "in a constant battle with remaining storage" and having
  "lost hundreds of gigabytes" to AI leftovers. MacBook Air owners with tight disks.
- Questions that reveal expectations: does it cover `~/Library/Developer/Xcode/iOS
  DeviceSupport`, old Xcode Archives, the SwiftPM cache (`~/Library/Caches/org.swift.swiftpm`)?
  "Do you check Git status again right before cleaning, or only during the scan?"
  (TOCTOU concern; the dev re-checks.)
- Complaints: "Keeps quitting for me, M4 MacBook Pro Sequoia" (crash on macOS 15,
  hotfix submitted). "How is it better than Mole?" (users already know Mole).
- Limits the dev admits: sandboxed so it only reaches the home folder and added
  folders; gitignored `dist` holding handmade files can be ticked at Recommended;
  branch deleted on server reads as pushed until next fetch; closed source; macOS 15+;
  **no CLI at all** ("ElfClean is an app only").

## 4. Competitors

| Tool | Licence / price | Built in | CLI | JSON | Default action | Git-aware projects |
|---|---|---|---|---|---|---|
| ElfClean | closed, $14.99 once | native (MAS, sandboxed) | none | none | Trash | yes, rechecked pre-move |
| Mole (tw93/Mole, 75k stars) | GPL-3.0 CLI, $19 Mac app | Bash (5.9 MB) + Go (0.9 MB) | `mo` | `analyze --json`, `status --json`, `history --json` only | `clean`/`purge`/`installer` delete **permanently**; `uninstall`/`analyze` use Trash | `purge` has git ownership checks |
| Dusty (yagcioglutoprak/dusty) | MIT | Swift (SwiftUI menu bar) | `dusty` ships inside the .app | `scan --json` | permanent unless `--trash`; `clean` needs `--yes` | activity judged by files + git history |
| Dustly | paid, closed | Rust core, native UI | none | none | Trash | unknown |
| Discky | unknown | unknown | agent-oriented | unknown | removes "only through the tool that owns it" | unknown |
| CleanMyMac | subscription | native | new CLI | unknown | permanent | no |

Observations:

- Mole is the 800-lb gorilla and is excellent, but: GPL (unfriendly to embedding in
  other tools), mostly Bash (hard to consume as a library), `mo clean` deletes
  permanently and **empties your Trash by default**, dry-run is opt-in, and the scan
  itself has no JSON output (only analyze/status/history). Its own audit says
  "the underlying file operations remain path-based" with no atomic lease.
- Dusty is the closest in spirit (allowlist-only, SafetyValidator as the single
  authority, deletion log, `--json`), but it is a Swift GUI app first; the CLI is a
  helper binary inside the bundle, Trash is not the default, and there is no reusable
  library or plan/apply contract.
- Nobody ships: a plan file an agent can inspect and partially approve; a stable,
  versioned JSON schema; dry-run as the only default across every command; Trash
  as the only default; an MIT core library you can import.

## 5. Gaps to exploit (product thesis)

1. **Agent-first contract.** `scan --json` emits a versioned plan with stable item
   IDs, sizes, categories, risk tier, explanation, and the rule that matched. `apply`
   takes that plan (or a subset of IDs) and does nothing without `--apply`. Exit codes
   and schema are documented and tested so agents can build on them.
2. **Safety as the architecture, not a feature.** One allowlist of rules; a single
   path validator that is the only thing allowed to authorize a move; hard-coded
   denylist (Documents, Desktop, Pictures, Photos/Mail libraries, Mobile Documents,
   Keychains, `.ssh`, `.gnupg`, `.ark`, `.env`, agent memory/plans/skills);
   symlinks never followed; boot volume and home only; no sudo, ever.
3. **Dry-run default, Trash default, receipts.** Every run writes a receipt (what moved,
   from where, to where in Trash) so `restore` can put it back.
4. **Git-aware project artifacts**, re-checked immediately before move, plus a
   "process holds it open" check (lsof) for AI tool dirs.
5. **MIT, Go, single static binary**, `brew install`, no app bundle required. GUI is an
   optional separate target that calls the same library.
6. **Explain everything**: every rule carries a one-sentence "what this is" and
   "what happens if it goes", shown in CLI, TUI, JSON.

## 6. Rule inventory to start from (v1 allowlist candidates)

Rebuilds itself (tier: safe):
`~/Library/Caches/*` (excluding com.apple.* system-critical entries listed in design),
`~/Library/Logs/*`, `~/Library/Developer/Xcode/DerivedData`,
`~/Library/Developer/CoreSimulator/Caches`, `~/Library/Caches/org.swift.swiftpm`,
npm/pnpm/yarn/bun/pip/uv/poetry/cargo/go/gradle/cocoapods/homebrew caches,
Cursor/VS Code/Devin caches and `.obsolete` extension dirs, Claude Code/Codex caches
(never `~/.claude/projects`, memory, plans, skills).

Review first (tier: review): `node_modules`/`target`/`.venv`/`.next`/`build` in
projects untouched 30+ days with clean git state, Xcode iOS DeviceSupport unused 90+
days, old `.dmg`/`.pkg` in Downloads (14+ days).

Manual only (tier: manual, never pre-selected): Ollama / Hugging Face models,
agent session history, worktrees, Xcode Archives, simulator devices, leftovers of
uninstalled apps.

Never (denylist): everything in §5.2, plus Docker/OrbStack/Colima images (print the
owner's prune command instead), simulator runtimes, anything outside `$HOME`.

# kipple GUI design

Status: design for wave 5 (task 5.0). The orchestrator reviews this document before 5.1 starts. Nothing here is built yet.

Read with: `openspec/changes/kipple-v1/design.md` (D12, D13), `specs/gui/spec.md`, `docs/brand.md`, `docs/safety.md`, and `internal/tui`, whose information design this GUI follows.

## 1. Product framing

kipple's GUI is for the person who has a full disk and a hunch that Xcode, npm, Docker or their AI tools are to blame, but who does not live in a terminal. They open the app because something is wrong: a "disk almost full" alert, a build that failed for lack of space. They want two answers fast: where the space went, and what they can get back without breaking anything. The GUI has to give those answers in one screen, and it has to make the safe choice the easy one. It scans read-only, pre-selects only what rebuilds itself, explains every item in plain words, and moves nothing until the person has seen the total and the word Trash.

The product is the trust, not the scan. Every competitor can find caches. kipple's promise (docs/safety.md) is that it only ever moves things to the Trash, checks each item again just before it moves, writes a receipt, and can put it all back. The GUI is the most visible place that promise is kept or broken, so it is built as a calm, deliberate workbench, not as a cleaning gadget. There is no "Boost" button, no score, no animation of things being destroyed. It uses the brand's single idea: an orderly home folder, with the few pieces that do not belong shown in amber. The person looks, chooses, confirms, and gets a receipt.

## 2. Direction

**Mode: Operate.** This is a task tool. Scanability, consistency and native expectations outrank expression, and the brand lives in precise details. Context: there is no PRODUCT.md or DESIGN.md in the repo. This direction is inferred from the lead's brief, `docs/brand.md`, the shipped TUI and `docs/safety.md`. No interview was run.

**The world: a tidy workbench in daylight.** Paper ground, spruce ink, lichen for the row you are looking at, and amber for exactly one meaning: "this one does not belong, and is going to the Trash". Everything that is not a selected item, an at-risk marker or the selected total is ink or muted ink. A screen with nothing selected has no amber on it at all. That restraint is what makes the amber legible.

**The signature detail: the tile.** The mark is a k built from rounded tiles on a grid, with one stray tile in amber. The GUI's checkbox is that tile, at 16pt with the mark's corner ratio (radius about 27% of the side, so 4pt):

| State | Tile | Meaning |
|---|---|---|
| Selected | Filled amber tile with a check glyph | Goes to the Trash when you apply |
| Not selected | Outlined tile (1.5pt, `control`) | You may select it |
| Blocked | Sunken recess (`recess` fill, no outline) | Something stops it, and a chip says what |
| Info | A centred dot, no tile | Measured only, never movable |

The same vocabulary appears three more times, always as state, never as decoration. The scan screen shows one tile per place kipple looks, filling as each is visited. The header meter is an amber bar. The done screen shows the mark with the stray tile gone and an empty recess in its slot: the item has left, and the slot shows where it can go back. No tile ever flies, spins or fades away (brand.md, "Don't").

**What it is not.** It is not the default Fyne skin: no blue primary, no Material ripple, no grey card stack, no default checkbox. It is not a marketing surface: no hero gradient, no gigabyte counter that rolls up, no emoji, no confetti.

## 3. Window and layout

- Default size 960x640 points. Minimum 760x520, set by the content's minimum size. Resizable, no full-screen-specific layout.
- Window title `kipple`, always lowercase. Single window. Closing it quits, as with other single-window Mac utilities.
- Three bands on every screen: header (56pt), body (flexible), action bar (56pt). Outer gutter 16pt.
- On the review screen the body splits into the list pane (58% of the width, about 556pt at 960) and the detail pane (the rest, about 404pt), with a 1pt `separator` between them. This is the TUI's split at 104 columns and wider. The window's minimum width keeps both panes side by side, so there is no stacked layout.
- The wireframes below are drawn at 96x32 characters. One character is 10pt wide and one line is 20pt tall, so a frame is the 960x640 window.

Glyph key for the wireframes:

| Glyph | Means |
|---|---|
| `▚` | The brand mark (wordmark lockup in the header) |
| `■` | Selected tile, amber |
| `□` | Unselected tile, outline |
| `▫` | Blocked tile, recess |
| `·` | Info item, no tile |
| `☑` `☐` | Group tile: all or none selectable items selected (a third, partial state shows a bar) |
| `▌` | The cursor row (lichen fill, 3pt ink bar at the left edge) |
| `▲` | At risk, amber |
| `⟦safe⟧` `⟦review⟧` `⟨manual⟩` `info` | Tier pills |
| `░` | Scrim over the screen behind a sheet |

## 4. Screens

There are four screens, as the brief and D13 ask: scan (with progress), review, confirm, done. Two of them have more than one state. Scan has idle, looking and measuring. Confirm has asking and applying. A separate refusal layout covers the cases where kipple will not start.

### 4.1 Scan

#### Idle (first thing on launch)

kipple does not scan on launch. Scanning reads the real home folder, and the Downloads rule makes macOS show a privacy prompt the first time. Both should happen because the person pressed Scan, not by surprise. Return starts the scan, so the cost is one keystroke. This is decision 4 in section 15.

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ ▚ kipple  v0.2.0   ~                                                                         │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                              │
│                                                                                              │
│                                        ▆▆  ▆▆  ▆▆                                            │
│                                        ▆▆      ▆▆                                            │
│                                        ▆▆  ▆▆                                                │
│                                        ▆▆      ◆                                             │
│                                                                                              │
│                         Find the caches and developer junk in your home folder.              │
│                                                                                              │
│                    kipple looks first and shows you everything before anything moves.        │
│                    What you choose goes to the Trash, with a receipt to put it back.         │
│                                                                                              │
│                                        ┌──────────────┐                                      │
│                                        │     Scan     │  ⏎                                   │
│                                        └──────────────┘                                      │
│                                                                                              │
│                     macOS may ask to let kipple look in Downloads. It only reads             │
│                     there; installers move only if you pick them.                            │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│ Nothing has been scanned yet.                                                                │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

| Element | Content and states |
|---|---|
| Header | Wordmark lockup (20pt tall), version (`muted`), home label (`muted`): `~` for the real home, `KIPPLE_HOME …/a/b` with a `test home` pill when overridden. No totals before a scan. |
| Hero mark | `kipple-mark.svg` (light) or `kipple-mark-dark.svg` (dark) at 96pt, with one tile of clear space. |
| Headline | Heading size, `ink`: "Find the caches and developer junk in your home folder." |
| Promise | Body size, `muted`, two lines, as drawn. |
| Scan button | Primary. Default, hover, pressed, focused (offset ring). It has no disabled state here. Return activates it. |
| Downloads note | Caption size, `muted`. Shown only until the first scan of this launch finishes. |
| Action bar | Status text "Nothing has been scanned yet." No buttons. |

#### Looking and measuring

`scan.Run` reports `scanning <rule id>` once per rule while it finds candidates, then `scanning projects`, and then measures sizes with no further messages. The screen is honest about those two phases.

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ ▚ kipple  v0.2.0   ~                                                                         │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                              ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆                             │
│                              ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆                             │
│                              ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ▆▆ ░░ ░░ ░░ ░░ ░░                             │
│                                                                                              │
│                                  Looking in 31 of 36 places                                  │
│                                  Cursor caches                                               │
│                                                                                              │
│                                                                                              │
│                                        ┌──────────────┐                                      │
│                                        │  Stop scan   │  esc                                 │
│                                        └──────────────┘                                      │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│ Reading only. Nothing moves during a scan.                                                   │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

| Element | Content and states |
|---|---|
| Place grid | One 16pt tile per place kipple looks: every non-project rule in the effective catalogue, plus one tile for "Projects". Rows of 12, 8pt gaps. A tile is `recess` until its rule's progress message arrives, then `ink`. No tween: a tile changes when the work happens. Amber never appears here, because nothing is selected yet. |
| Count line | Subheading size, `ink`: "Looking in 31 of 36 places". In the measuring phase: "Measuring sizes". |
| Current place | Body size, `muted`: the rule's title from the catalogue (never the raw ID), or "Projects in ~/dev, ~/code, …" for the project pass. In the measuring phase, an indeterminate 4pt bar (`primary` on `header`) replaces it. |
| Stop scan | Secondary button. Esc or ⌘. activate it. It cancels the scan context and returns to idle with the status "Scan stopped. Nothing was read further, nothing moved." |
| Action bar | Status text "Reading only. Nothing moves during a scan." |

### 4.2 Review

The review screen is the product. It answers "where did the space go" in the header, "what can I safely get back" in the pre-selection, and "why" in the detail pane.

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ ▚ kipple  v0.2.0   ~                Found 41 · 38.6 GB    Selected 23 · 21.4 GB ━━━━━━━━──── │
├────────────────────────────────────────────────────────┬─────────────────────────────────────┤
│ ⚠ 2 places could not be read completely.     Details › │ npm cache              ⟦safe⟧ 6.1 GB│
│ SAFE  Rebuilds itself. Selected for you.               │ ~/.npm/_cacache                     │
│ App caches              ⟦safe⟧   ☑  14 items · 9.8 GB  │                                     │
│  ■ ~/Library/Caches/com.spotify.client         2.4 GB  │ Status   ■ Selected: goes to the    │
│  ■ ~/Library/Caches/Google                     1.9 GB  │          Trash when you apply.      │
│  ■ ~/Library/Caches/com.tinyspeck.slackmacg…   1.1 GB  │                                     │
│  ▫ …/Caches/com.todesktop.230313    app open   880 MB  │ What     npm's download cache. Every│
│ npm cache               ⟦safe⟧   ☑   1 item · 6.1 GB   │          package npm has fetched.   │
│▌■ ~/.npm/_cacache                              6.1 GB  │ If       npm downloads packages     │
│ Xcode DerivedData       ⟦safe⟧   ☑  3 items · 3.2 GB   │ removed  again the next time you    │
│  ■ …/DerivedData/Kipple-hdzvbqsxgmbn           2.0 GB  │          install them.              │
│ REVIEW  Rebuilds, but costs time. Your call.           │                                     │
│ Go module cache        ⟦review⟧  ☐   1 item · 4.7 GB   │ Contents 182,409 files, newest      │
│  □ ~/go/pkg/mod                                4.7 GB  │          12 Nov 2026                │
│ Project Rust target    ⟦review⟧  ☐  2 items · 3.9 GB   │                                     │
│  □ ~/dev/old-api/target                        2.6 GB  │ ID       k3v7q2mz4hda      [Copy]   │
│  ▫ ~/dev/kipple/target         uncommitted ▲   1.3 GB  │                                     │
│ MANUAL  Never selected for you. Only if sure.          │                                     │
│ Hugging Face hub cache ⟨manual⟩      2 items · 8.1 GB  │                                     │
│  □ …/hub/models--mistralai--Mistral-7B         7.6 GB  │                                     │
│ INFO  Measured only. kipple never moves these.         │                                     │
│ Docker Desktop disk image  info      1 item · 12 GB    │                                     │
│  · ~/Library/Containers/…/Docker.raw           12 GB   │                                     │
├────────────────────────────────────────────────────────┴─────────────────────────────────────┤
│ 23 items selected. Each is checked again before it moves.  [ Scan again ] [ Move to Trash… ] │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

**Header (56pt)**

| Element | Content and states |
|---|---|
| Lockup, version, home | As on the scan screen. |
| Found | `muted`: "Found 41 · 38.6 GB", every listed item including info, as the TUI shows it. |
| Selected | Label `muted`, numbers `amberInk`: "Selected 23 · 21.4 GB". With nothing selected it reads "Selected 0 · 0 B", all `muted`, and no amber appears anywhere. |
| Meter | 120x4pt. `amber` fill for the selected share of found bytes, on a `header` track. Rounded ends. It changes instantly, with no animation. The numbers beside it carry the same information. |

**List pane.** One virtualised `widget.List` with three row kinds. Section 11 explains why it is not a `widget.Tree`.

| Row | Height | Content and states |
|---|---|---|
| Warning banner | 32pt | Only when `plan.warnings` is not empty: a warning icon in `ink` and "2 places could not be read completely." with a "Details" toggle that expands the warnings inline (each through `output.SafeText`). It is the first row and is not focusable for selection. |
| Section | 40pt | Tier name in caption caps (`SAFE`, `REVIEW`, `MANUAL`, `INFO`, `muted`, bold) and a one-line explanation (`muted`). Static. Shown only for tiers that have items. |
| Group (rule) | 40pt | `header` fill. Rule title (subheading size, bold, `ink`), tier pill, group tile, then "14 items · 9.8 GB" (`muted`, right-aligned). The group tile exists for `safe` and `review` groups only. It is all, none or partial, and acts only on the group's selectable items. `manual` groups have no group tile, so every manual item is chosen one at a time. `info` groups have none. Clicking the title moves the cursor to the group's first item. |
| Item | 32pt | Tile, path, optional chip, optional `▲`, size. The path is home-relative (`~/…`), passed through `output.SafeText`, and truncated from the left so the distinctive end stays visible (`…/DerivedData/Kipple-hdz…`). The chip is a short reason for a blocked item (section 5.4). The size is right-aligned in a fixed 72pt column, `muted`. |

Item row states:

| State | Visual |
|---|---|
| Default | `surface` fill, path `ink` (`muted` for blocked and info items). |
| Hover | `hover` fill. |
| Cursor | `cursor` fill and a 3pt `ink` bar at the left edge. There is exactly one cursor row. It drives the detail pane. |
| Focused list | When the list has keyboard focus, the cursor row also gets the 2pt focus ring inset by 2pt. |
| Selected | Amber tile. The row is not tinted, so amber stays on the tile. |
| Blocked | Recess tile, `muted` path, chip. Clicking the tile does not select; the status text says why. |
| Info | Dot instead of tile, `muted` path, no chip. Clicking the tile area does nothing except move the cursor; the status text names the owner's command. |

Order matches the TUI: tier (safe, review, manual, info), then rule bytes descending, then item bytes descending within a rule.

**Detail pane.** A scrolling column for the cursor item. Labels sit in an 80pt column in caption size, `muted`. Values are body size. The field order is the TUI's: status first, then the explanation.

| Field | Content |
|---|---|
| Title row | Rule title (subheading, bold), tier pill, size (subheading, `ink`, right). |
| Path | Full home-relative path, wrapped, `muted`, with "Copy path" and "Show in Finder" text buttons. Show in Finder runs `/usr/bin/open -R <path>`, which only reveals the file. |
| Status | Selected: amber tile glyph and "Selected: goes to the Trash when you apply." in `amberInk`. Selectable: "Not selected. Click its box or press Space to select it." Manual: "Not selected: manual items are never chosen for you." Blocked: "Can't be selected:" and each reason as a sentence (section 5.4). Info: "Measured only; kipple never moves it." |
| Instead | Info items only: the owner's command in monospace with a Copy button. |
| At risk | Only when `at_risk`: `▲` and "Removing this may lose work; read If removed." in `amberInk`. |
| What, If removed | The rule's two sentences, `ink`. |
| Git | Project items: the state in words and the detail. For `unpushed` and `no_upstream` it adds, in `muted`: "kipple never fetches, so this compares with the last time the repository fetched." |
| Protected | When `protected_paths` is set: up to five item-relative paths, monospace, `muted`. |
| Contents | "182,409 files, newest 12 Nov 2026", `muted`. |
| Warnings | Item warnings, if any, `ink`. |
| ID | Monospace, `muted`, with Copy. This is the ID `kipple clean --only` takes. |

**Action bar**

| Element | Content and states |
|---|---|
| Status text | Left. The TUI's hint line, in sentences: "23 items selected. Each is checked again before it moves." and the transient hints from section 7. |
| Scan again | Secondary. It throws the selection away and rescans, after an inline confirmation in the status text if the selection differs from the pre-selection: "Scanning again resets your choices. Press ⌘R again to rescan." |
| Move to Trash… | Primary. Its label never changes width. Disabled when nothing is selected, with the status "Nothing is selected. Click an item's box to select it." ⌘⌫ activates it, as in Finder. |

### 4.3 Confirm

A sheet over the review screen, built as a modal pop-up (section 11). It is the only path to a move, and it is where the spec's "Confirmation required" scenario lives.

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ ▚ kipple  v0.2.0   ~                Found 41 · 38.6 GB    Selected 25 · 28.7 GB ━━━━━━━━━─── │
├────────────────────────────────────────────────────────┬─────────────────────────────────────┤
│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░╭────────────────────────────────────────────────────────────╮░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  Move 25 items to the Trash?                               │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  28.7 GB                                                   │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  from 6 rules: App caches, npm cache, Xcode DerivedData,   │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  Go module cache, and 2 more                               │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  Includes 2 review items you chose.                        │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  ▲ 1 item is at risk: ~/dev/scratch/node_modules           │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  Nothing is deleted. Each item is checked again just       │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  before it moves, goes to your Trash, and is listed in a   │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  receipt you can use to put it back. The space comes back  │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  when you empty the Trash.                                 │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  ┊ kipple doesn't have Full Disk Access, so it will use    │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  ┊ macOS's own Trash command. Finder's Put Back works for  │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  ┊ these items.                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                         [  Cancel  ]   [ Move to Trash ]   │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░╰────────────────────────────────────────────────────────────╯░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░│
├────────────────────────────────────────────────────────┴─────────────────────────────────────┤
│ 25 items selected. Each is checked again before it moves.  [ Scan again ] [ Move to Trash… ] │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

| Element | Content and states |
|---|---|
| Scrim | `scrim` over the whole window. Clicking it does nothing: dismissing takes a deliberate Cancel or Esc. |
| Sheet | 600pt wide, `surface`, 12pt radius, 24pt padding, shadow. |
| Title | Heading: "Move **25 items** to the Trash?" The count is `amberInk`; the rest is `ink`. The word "Trash" is always in the title. |
| Total | Display size, `ink`: "28.7 GB" (allocated bytes of the selection, as in the TUI and `kipple clean`). |
| Rules | `muted`: "from 6 rules: " and the first four rule titles, then "and N more". |
| Tier line | Only when review or manual items are selected: "Includes 2 review items you chose." or "Includes 2 review items and 1 manual item you chose." `ink`. |
| At-risk line | Only when any selected item is `at_risk`: `▲` (amber) and "1 item is at risk:" with up to three paths, then "and N more". |
| Promise | `muted`, fixed copy (section 7.1). |
| Trash note | Only when the Trash probe (section 12) reports the fallback: a 2pt `separator` rule at the left and the Full Disk Access note (section 7.1). When the probe reports that neither way works, the note becomes the trash-access message and Move to Trash is disabled, so the failure is shown before anything is attempted. |
| Cancel | Secondary, and it has focus when the sheet opens, so a stray Return cancels. Esc and ⌘. activate it. On cancel, the sheet closes and the status reads "Cancelled. Nothing was moved." |
| Move to Trash | Primary. Click it, or press ⌘⌫ a second time. Return activates it only after Tab has moved focus to it. |

#### Applying

The sheet stays up and changes content, so the person never loses sight of what they confirmed. Only the top of the window is drawn; below the sheet it matches the confirm frame.

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ ▚ kipple  v0.2.0   ~                Found 41 · 38.6 GB    Selected 25 · 28.7 GB ━━━━━━━━━─── │
├────────────────────────────────────────────────────────┬─────────────────────────────────────┤
│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░╭────────────────────────────────────────────────────────────╮░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  Moving to the Trash                                       │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━──────────────────────────  │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  12 of 25 items                                   11.0 GB  │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│  Checking ~/Library/Caches/com.spotify.client              │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                [ Stop after this item ]    │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░│                                                            │░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░╰────────────────────────────────────────────────────────────╯░░░░░░░░░░░░░░░░│
│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░│
```

| Element | Content and states |
|---|---|
| Title | "Moving to the Trash". |
| Progress | Determinate 6pt bar, `primary` on `header`, items done out of the targets. Below it: "12 of 25 items" left and bytes done right (from the plan's sizes). |
| Current | `muted`, one line, left-truncated: apply's progress message ("Checking …", "Moving …") with the path made home-relative. |
| Stop after this item | Secondary. It cancels apply's context. Apply finishes or skips the current item and records the rest as not attempted. The button then reads "Stopping after this item…" and is disabled. Esc and ⌘. do the same. |
| Window close, ⌘Q | Intercepted: they act as Stop, and the window closes once apply returns and the receipt is saved. Nothing is left half-recorded. |

### 4.4 Done

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ ▚ kipple  v0.2.0   ~                                                                         │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                              │
│    ▆▆  ▆▆                Moved 24 items to the Trash                                         │
│    ▆▆      ▆▆            28.1 GB                                                             │
│    ▆▆  ▆▆                is in your Trash now. Empty the Trash to get the space back.        │
│    ▆▆      ┌┐                                                                                │
│            └┘                                                                                │
│   ╭───────────────────────────────────────────╮  ╭──────────────────────────────────────╮    │
│   │ RECEIPT                                   │  │ MOVED                        28.1 GB │    │
│   │ Receipt   20261120T143205Z-a7k2qd  [Copy] │  │ ~/Library/Caches/com.spotify…  2.4 GB│    │
│   │ Moved     24 items · 28.1 GB              │  │ ~/Library/Caches/Google        1.9 GB│    │
│   │ Skipped   1                               │  │ ~/.npm/_cacache                6.1 GB│    │
│   │ How       Moved with macOS's Trash        │  │ ~/go/pkg/mod                   4.7 GB│    │
│   │           command (no Full Disk Access).  │  │ ~/dev/old-api/target           2.6 GB│    │
│   │                                           │  │ and 19 more                    Show ›│    │
│   │ [ Open Trash ]  [ Open receipt ]          │  │                                      │    │
│   ╰───────────────────────────────────────────╯  │ SKIPPED                              │    │
│                                                  │ ~/Library/Application Support/Cursor/│    │
│   Changed your mind?                             │ Cache                                │    │
│   Open the Trash, select an item and choose      │ Cursor is open. Quit it, scan again. │    │
│   Put Back. kipple restore 20261120T143205Z-     │                                      │    │
│   a7k2qd --apply also works.          [Copy]     │                                      │    │
│                                                  ╰──────────────────────────────────────╯    │
│                                                                                              │
│                                                                                              │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│ Finished 14:32. Nothing else will move.                        [ Scan again ]   [  Done  ]   │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

| Element | Content and states |
|---|---|
| Recess mark | 72pt, drawn in code: the six `ink` tiles of the mark, with an empty `recess` slot (1pt `control` outline) where the stray tile would sit straight. It is static. |
| Title | Heading: "Moved 24 items to the Trash". Variants in section 6. |
| Total | Display size, `ink`: bytes moved. |
| Space line | `muted`: "is in your Trash now. Empty the Trash to get the space back." When the moved items' reclaimable bytes are lower than their allocated bytes (hard links), it adds: "Emptying it frees about 22.8 GB; some files are shared with other folders." |
| Receipt card | `surface` card, 10pt radius. Receipt ID (monospace, Copy), Moved, Skipped (only if > 0), Displaced (only if > 0), Pending (only if > 0, read from the receipt on disk), How (the mover in words, section 7.3). Buttons: "Open Trash" and "Open receipt". |
| Open Trash | Runs `/usr/bin/open <trash dir>`, using the receipt's `trash_dir` (so a `KIPPLE_HOME` run opens the test folder's Trash, never the real one). |
| Open receipt | Runs `/usr/bin/open -R <receipt path>`, which reveals the receipt file in Finder. |
| Moved list | Card, up to six rows (path left-truncated, size), then "and 19 more" with a Show toggle that expands the list in place. |
| Skipped list | Under the moved list: each skipped path and its reason as a sentence with a next step (section 7.3). Shown only when something was skipped. |
| Displaced block | Only when `displaced` is not empty: a warning icon in `ink` and the copy in section 7.3. Never amber: a displaced object is not the person's choice. |
| Warnings | Result warnings, `ink`, at the bottom. |
| Changed your mind? | Restore guidance that depends on the mover (section 7.3), with the `kipple restore <id> --apply` command and Copy. A "Put back" button is decision 3. |
| Action bar | "Finished 14:32. Nothing else will move." Scan again (secondary), Quit (primary, default). |

## 5. Information hierarchy

### 5.1 What the eye meets first

1. The header totals: how much was found, how much is selected. This answers "where did the space go" before any scrolling.
2. The safe section, pre-selected, at the top. Most people's whole job is to read it and press ⌘⌫.
3. Sizes, right-aligned in one column, so the big items are found by scanning one edge.
4. Exceptions, which are the only coloured things: amber tiles, `▲`, and chips on blocked rows.
5. The detail pane, for the person who wants to know why.

### 5.2 Tiers

Tiers keep the CLI's words (`safe`, `review`, `manual`, `info`), so docs, the TUI, `--only` and the GUI all agree. Each tier gets one plain sentence on its section row:

| Tier | Section line | Pill |
|---|---|---|
| safe | Rebuilds itself. Selected for you. | `ink` text on `cursor` fill |
| review | Rebuilds, but costs time. Your call. | `onPrimary` text on `primary` fill (inverse) |
| manual | Never selected for you. Only if you're sure. | `ink` text, 1pt `control` outline, no fill |
| info | Measured only. kipple never moves these. | `muted` text, no fill, no outline |

These are the TUI's four pill treatments, so the two front ends read the same.

### 5.3 Pre-selection and opting in

- **Pre-selected:** exactly the items `kipple scan --level safe` marks `selected`. The GUI always scans at level `safe` and ignores the config file's `level` (decision 5), and selects `item.Selected && tier != info` as the TUI's `load` does. Test 5.2 holds it to that.
- **Review and manual items** are listed and unselected, with reasons `tier_review` or `tier_manual`. Clicking the tile is consent to that tier, exactly like naming the ID in `kipple clean --only`. A review group can be selected all at once with its group tile; a manual item only one at a time.
- **Blocked items:** any reason other than the tier reasons keeps an item unselectable, whatever its tier. The rule is `apply.Preview`'s, so what the GUI lets you select is what apply would accept.
- **Info items** are never selectable. There is no tile and no code path that adds an info ID to the selection; the view-model refuses it and a test proves it.
- **No permanent delete.** The GUI has no permanent action, no "Empty Trash" button, and never sets `apply.Options.Permanent`. The only destination is the Trash.

### 5.4 Reasons in words

Each blocking reason has a short chip for the row and a sentence for the detail pane. The sentences are the TUI's (`internal/tui/reasons.go`), moved to a shared package so both front ends use the same copy (section 12).

| Reason | Chip | Detail sentence |
|---|---|---|
| `too_recent` | recent | changed too recently for this rule |
| `project_active` | in use | the project was worked on in the last 30 days |
| `git_dirty` | uncommitted | the project has uncommitted changes |
| `git_unpushed` | unpushed | the project has commits that are not pushed |
| `git_no_upstream` | no upstream | the project's branch has no upstream to compare with |
| `git_not_a_repo` | not in git | the project is not a git repository, so kipple cannot tell what would be lost |
| `git_unavailable` | no git | git could not be run to check the project |
| `owner_running` | app open | the app that owns it is running. GUI adds: "Quit <owner> and scan again." |
| `scan_partial` | unreadable | part of it could not be read |
| `contains_mount` | mounted disk | another volume is mounted inside it |
| `contains_protected` | protected files | it holds protected files (keys, .env files, documents) |
| `refused:<policy reason>` | refused | refused by the safety policy (<reason>) |

Chips are caption size, `muted` text on `header`, 4pt radius. They are never amber.

### 5.5 At-risk items

`at_risk` gets the amber `▲` on the row, an "At risk" field in the detail pane, and a line in the confirm sheet. Amber is right here: the item does not belong, and it deserves a second look.

## 6. States

### 6.1 Empty

- **Nothing found at all:** the list pane shows "Nothing to clean." (heading) and "kipple looked in 36 places and found nothing it cleans." (`muted`). Move to Trash is disabled. Scan again is available.
- **Only info items:** the same headline, then "These are measured only:" and the info section, so the person still learns where the space went and which command frees it.

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ ▚ kipple  v0.2.0   ~                Found 2 · 14 GB       Selected 0 · 0 B   ──────────────  │
├────────────────────────────────────────────────────────┬─────────────────────────────────────┤
│ Nothing to clean.                                      │ Docker Desktop disk image    info   │
│ kipple looked in 36 places and found nothing it        │ ~/Library/Containers/…/Docker.raw   │
│ cleans. These are measured only:                       │                                     │
│                                                        │ Status   Measured only; kipple      │
│ INFO  Measured only. kipple never moves these.         │          never moves it.            │
│ Docker Desktop disk image  info      1 item · 12 GB    │ Instead  docker system prune [Copy] │
│▌· ~/Library/Containers/…/Docker.raw            12 GB   │                                     │
│ Ollama models              info      1 item · 2.0 GB   │ What     The disk image that holds  │
│  · ~/.ollama/models                           2.0 GB   │          every Docker container...  │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
│                                                        │                                     │
├────────────────────────────────────────────────────────┴─────────────────────────────────────┤
│ Nothing is selected.                                       [ Scan again ] [ Move to Trash… ] │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

- **Items, none selected** (nothing safe found, only review or manual): no empty message. The list shows the sections and the status reads "Nothing is pre-selected. Review and manual items are yours to choose."

### 6.2 Partial

| Case | Shown as |
|---|---|
| `plan.warnings` not empty (a rule root errored, or a scan warning) | The warning banner row at the top of the list, collapsed to a count, with Details. |
| Item `scan_partial` | Chip "unreadable", recess tile, the detail sentence, and the item stays unselectable, as in the CLI. |
| Item `contains_mount` | Chip "mounted disk", recess tile, detail sentence. |
| Apply partial (skipped > 0) | The done screen's Skipped list, the receipt card's Skipped row, and the title variant below. |

### 6.3 Errors and refusals

Refusals before a scan use a full-window layout: the header, a heading and body in the content column, and one button. No scan button is shown.

| Case | Source | Heading | Body | Buttons |
|---|---|---|---|---|
| Running as root | `policy.CheckNotRoot` | kipple doesn't run as root. | Open it as yourself. kipple never uses sudo. | Quit |
| Home refused (`KIPPLE_HOME` guard) | `resolveHome` / `guardRealHome`, code `home` | kipple won't scan this folder. | The error text (through `SafeText`), then: "KIPPLE_HOME is for test folders. It can't be your real home folder, inside it, or around it." | Quit |
| Home unreadable | code `home` | kipple can't find your home folder. | The error text. | Quit |
| Config error | code `config` | kipple can't read its settings. | The error text and the config path. | Quit |
| Scan failed | `scan.Run` error | kipple couldn't finish the scan. | The error text. Nothing was moved. | Scan again, Quit |
| Trash access | `apply.ErrTrashAccess`, code `trash_access` | kipple can't use your Trash folder, so nothing was moved. | macOS lets only apps with Full Disk Access open it. Allow kipple in System Settings > Privacy & Security > Full Disk Access, then try again. | Open Privacy Settings, Back to review |
| Receipt error | `apply.ErrReceipt`, code `receipt` | kipple stopped: it couldn't save the receipt. | Wireframe below. | Open Trash, Open receipt, Scan again, Quit |
| Other apply error | code `apply` | kipple stopped while moving items. | The error text and the receipt card if a receipt exists. | Open Trash, Open receipt, Quit |

"Open Privacy Settings" runs `/usr/bin/open "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"`, which opens a local settings pane. With the Trash probe in place, the trash-access case should only appear inside the confirm sheet. The full-window version is the backstop.

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ ▚ kipple  v0.2.0   ~                                                                         │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                              │
│    kipple stopped: it couldn't save the receipt.                                             │
│                                                                                              │
│    kipple never moves an item it can't record, so it stopped before the next one.            │
│    9 items moved before that and are in the Trash. 1 item was about to move; it may or       │
│    may not have moved. kipple restore looks for it by its tag.                               │
│                                                                                              │
│    The disk may be full. Free some space, then put things back or carry on.                  │
│                                                                                              │
│   ╭───────────────────────────────────────────╮                                              │
│   │ RECEIPT                                   │                                              │
│   │ Receipt   20261120T143205Z-a7k2qd  [Copy] │                                              │
│   │ Moved     9 items · 11.2 GB               │                                              │
│   │ Pending   1 · ~/Library/Caches/Google     │                                              │
│   │ Not tried 15                              │                                              │
│   │                                           │                                              │
│   │ [ Open Trash ]  [ Open receipt ]          │                                              │
│   ╰───────────────────────────────────────────╯                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
│                                                                                              │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│ The receipt on disk is the record of this run.                 [ Scan again ]   [  Done  ]   │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

The pending count and paths come from loading the receipt with `receipt.Store.Load`, because `plan.Result` does not carry `pending`. "Not tried" counts skipped entries whose detail is "not attempted: the receipt could not be saved".

### 6.4 Git: offline and unavailable

- **kipple never uses the network.** It does not fetch, so "pushed" means "pushed as of this repository's last fetch". The detail pane says so for `unpushed` and `no_upstream` (section 4.2).
- **No Command Line Tools.** An app launched from Finder has the minimal PATH, so `git` is `/usr/bin/git`. Without Apple's Command Line Tools that is a shim that pops up an install dialog every time it runs. Before scanning, the GUI runs `/usr/bin/xcode-select -p`. If that fails, it passes `scan.Options.Git` an inspector that reports `git_unavailable` without running git. Project items then show the chip "no git", and one banner row explains: "git isn't available, so kipple can't check project folders. They stay unselectable. Install Apple's Command Line Tools (xcode-select --install) and scan again."
- `git_unavailable` for any other reason (timeout, git missing from PATH) shows the same chip and sentence, without the banner.

### 6.5 Owner app running

A `safe` item whose owner is running is unselectable with `owner_running`: chip "app open", and the detail adds "Quit <owner name> and scan again." The owner name comes from the rule's `Owner.Name`.

### 6.6 Done-screen variants

| Result | Title | Notes |
|---|---|---|
| All moved | Moved 24 items to the Trash | |
| Some skipped | Moved 23 of 24 items to the Trash | Skipped list shown |
| None moved | Nothing was moved. | Total line hidden. Skipped list shown with reasons. |
| Stopped by the person | Stopped. Moved 12 of 25 items to the Trash. | Skipped entries "not attempted: interrupted" are grouped as "Not tried 13" instead of listed. |
| Displaced > 0 | As above | Displaced block shown |

## 7. Copy

All file-system names go through `output.SafeText`. Sentences end with full stops. "Trash" is capitalised. Never "delete", "erase", "clean up forever", "free up", "boost" or "optimise".

### 7.1 Confirmation

- Title: "Move {N items} to the Trash?" (`1 item` / `N items`).
- Total: "{bytes}", then "from {n rules}: {titles}".
- Promise: "Nothing is deleted. Each item is checked again just before it moves, goes to your Trash, and is listed in a receipt you can use to put it back. The space comes back when you empty the Trash."
- Full Disk Access note, shown when the probe says the fallback will be used: "kipple doesn't have Full Disk Access, so it will use macOS's own Trash command. Finder's Put Back works for these items."
- When neither way works (only under `KIPPLE_HOME`, or a build without cgo): "kipple can't use the Trash folder here, so it can't move anything. macOS lets only apps with Full Disk Access open it." Move to Trash is disabled.
- Buttons: "Cancel", "Move to Trash".

### 7.2 Status-line hints

| When | Text |
|---|---|
| Review, items selected | {N items} selected. Each is checked again before it moves. |
| Nothing selected, Move pressed | Nothing is selected. Click an item's box to select it. |
| Info item toggled | Info items can't be selected. Instead, run: {owner command} |
| Blocked item toggled | Can't select it: {first reason sentence}. |
| Manual item selected | Selected. Manual items are never chosen for you; this one is your call. |
| Confirm cancelled | Cancelled. Nothing was moved. |
| Copy | Copied. |

### 7.3 Done and restore

- How (mover), from the TUI's `moverNote`:
  - `rename`: "Moved straight into the Trash folder."
  - `nsfilemanager`: "Moved with macOS's Trash command (no Full Disk Access)."
- Changed your mind?, depending on the mover:
  - All `nsfilemanager`: "Open the Trash, select an item and choose Put Back. `kipple restore {id} --apply` also works."
  - Any `rename`: "Finder's Put Back doesn't know about items kipple moved straight into the Trash folder. To put them back, run `kipple restore {id} --apply` in Terminal, or drag them out of the Trash."
- Displaced: "Something else was at an item's name when macOS's Trash command ran, and it went to the Trash instead. kipple skipped the item and listed what moved in the receipt. Put it back with Finder's Put Back or `kipple restore {id} --apply`."
- Skipped reasons use `plan.Skip*` sentences. The doc for each is in docs/safety.md "Skip reasons"; the shared copy package gets one sentence and, where useful, a next step for each (for example `tool_running`: "An agent was running in this project. Stop it and scan again.").

## 8. Theme

A custom `fyne.Theme` in `cmd/kipple-gui/theme.go`, set with `app.Settings().SetTheme`. It follows the system appearance: Fyne passes the current variant to `Color`, and a settings listener refreshes the window when macOS switches.

### 8.1 Tokens

Brand tokens are from docs/brand.md. The others are derived from them for the GUI and get added to brand.md in 5.1: `amberInk` in light, which amber-deep cannot serve as text (3.5:1 on paper), plus the surfaces, interaction fills, `recess` and `disabled`.

| Token | Light | Dark | Use |
|---|---|---|---|
| `background` | `#F3F4EF` paper | `#10221E` spruce | Window ground, header and action bar |
| `surface` | `#FBFCF9` | `#152B26` | List and detail panes, cards, sheets |
| `header` | `#E9EDE8` | `#1A332D` | Group rows, chips, meter and progress tracks |
| `cursor` | `#E3EBE4` lichen | `#1F4038` | Cursor row, safe pill fill, secondary button fill |
| `hover` | `#EEF2EE` | `#19352F` | Hovered rows |
| `ink` | `#10221E` spruce | `#E3EBE4` lichen | Text, headings, icons |
| `muted` | `#4A5C57` slate | `#9DB2AA` fog | Secondary text, labels, sizes |
| `amber` | `#C26A0C` amber-deep | `#F2A43A` amber | Selected tile fill, meter fill, `▲`. Graphics only in light. |
| `amberInk` | `#995108` (new) | `#F2A43A` amber | Amber text: selected count and total, the selected status, at-risk text |
| `onAmber` | `#FBFCF9` | `#10221E` | The check glyph on a selected tile |
| `primary` | `#10221E` | `#E3EBE4` | Primary button fill, progress fill, focus ring |
| `onPrimary` | `#F3F4EF` | `#10221E` | Primary button text, review pill text |
| `control` | `#4A5C57` | `#9DB2AA` | Tile outline, manual pill outline, input borders |
| `separator` | `#D3DBD5` | `#2C4943` | Pane dividers, hairlines |
| `recess` | `#E2E7E2` | `#0B1815` spruce-lo | Blocked tile, empty grid slots, done-screen recess |
| `disabled` | `#8A9A94` | `#6B807A` | Disabled button text and glyphs only |
| `scrim` | `#10221E` at 32% | `#000000` at 55% | Behind the confirm sheet |
| `shadow` | `#10221E` at 16% | `#000000` at 45% | Sheet and card shadow (Fyne 2.8 `Rectangle.Shadow`) |

**Danger.** The brief asks for danger = amber. In kipple the dangerous thing is "this goes to the Trash", which is exactly amber's one meaning, so `amber` and `amberInk` are the danger tokens. Errors and warnings are not amber. The shipped TUI theme forbids amber for warnings and errors, and the brand says amber has one meaning. So Fyne's `error`, `warning` and `success` colours map to `ink`, and error states are carried by an icon and words. This is decision 1.

### 8.2 Fyne colour names

`Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color` answers Fyne's built-in names and kipple's own `kipple.*` names. Custom widgets read colours only through the theme, never as literals, so light and dark stay in one table.

| Fyne name | Token |
|---|---|
| `ColorNameBackground` | `background` |
| `ColorNameForeground` | `ink` |
| `ColorNamePlaceHolder` | `muted` |
| `ColorNameDisabled` | `disabled` |
| `ColorNamePrimary` | `primary` |
| `ColorNameForegroundOnPrimary` | `onPrimary` |
| `ColorNameButton` | `cursor` (secondary button fill) |
| `ColorNameDisabledButton` | `header` |
| `ColorNameHover` | `ink` at 7% (Fyne blends it over widgets) |
| `ColorNamePressed` | `ink` at 12% |
| `ColorNameFocus` | `ink` at 20% for built-in widgets; kipple's widgets draw the solid ring |
| `ColorNameSelection` | `cursor` |
| `ColorNameSeparator` | `separator` |
| `ColorNameInputBackground` | `surface` |
| `ColorNameInputBorder` | `control` |
| `ColorNameHeaderBackground` | `header` |
| `ColorNameMenuBackground`, `ColorNameOverlayBackground` | `surface` |
| `ColorNameShadow` | `shadow` |
| `ColorNameScrollBar` | `muted` at 55% |
| `ColorNameHyperlink` | `ink` (links are underlined) |
| `ColorNameError`, `ColorNameWarning`, `ColorNameSuccess` | `ink` (see Danger) |
| `ColorNameForegroundOnError`, `…OnWarning`, `…OnSuccess` | `background` (unused) |
| `kipple.surface`, `kipple.cursor`, `kipple.hover`, `kipple.muted`, `kipple.amber`, `kipple.amberInk`, `kipple.onAmber`, `kipple.control`, `kipple.recess`, `kipple.scrim` | the token of the same name |

Custom names work with `widget.RichText` segments (`RichTextStyle.ColorName`), which is how mixed `ink` and `amberInk` text is set in one line.

### 8.3 Type

`Font(style fyne.TextStyle) fyne.Resource`: brand.md says interface text is the platform system font. Fyne cannot ask macOS for it, so the theme loads `/System/Library/Fonts/SFNS.ttf` (regular and bold) and `/System/Library/Fonts/SFNSMono.ttf` (monospace) at runtime, reading the installed files and shipping none. If a file is missing or Fyne cannot render it (they are variable fonts, which is the risk), the theme falls back to Fyne's bundled fonts. 5.1 starts with a one-commit spike that settles this (decision 2). Bricolage Grotesque appears only inside the outlined brand SVGs, never as UI text.

`Size(name fyne.ThemeSizeName) float32`, type sizes:

| Step | Fyne name | Size (pt) | Weight | Use |
|---|---|---|---|---|
| Caption | `SizeNameCaptionText` | 11 | regular / bold caps | Section labels, chips, detail labels, notes |
| Body | `SizeNameText` | 13 | regular | Rows, values, buttons, status text |
| Subheading | `SizeNameSubHeadingText` | 15 | bold | Rule titles, detail title, count line |
| Heading | `SizeNameHeadingText` | 20 | bold | Screen and sheet titles |
| Display | none (custom `canvas.Text.TextSize`) | 28 | bold | The confirm total, the done total |

Steps are about 1.15 to 1.2 apart up to subheading; the two top steps are larger for the moments that carry the answer. Line spacing (`SizeNameLineSpacing`) is 4. Body text in the detail pane wraps at the pane width (about 50 characters), inside the readable range. Numbers in the size column are right-aligned so their right edges line up even with proportional figures.

### 8.4 Spacing, radii, strokes

Spacing is a 4pt scale. Fyne's two padding sizes take the two smallest steps. The rest are constants in `cmd/kipple-gui/layout.go`, used by custom layouts and fixed-size spacers.

| Name | pt | Use |
|---|---|---|
| `SizeNamePadding` | 4 | Fyne's padding between widgets |
| `SizeNameInnerPadding` | 8 | Inside buttons and inputs |
| `space3` | 12 | Row inner padding, chip padding |
| `space4` | 16 | Outer gutter, pane padding |
| `space5` | 24 | Sheet padding, gap between cards |
| `space6` | 32 | Above and below hero blocks |
| `space7` | 48 | Start-screen vertical rhythm |

| Fyne size name | Value | |
|---|---|---|
| `SizeNameInputBorder` | 1 | |
| `SizeNameInputRadius` | 4 | Same as the tile |
| `SizeNameSelectionRadius` | 6 | Cursor row |
| `SizeNameButtonRadius` | 6 | Fyne 2.8 |
| `SizeNameCardRadius` | 10 | Fyne 2.8 |
| `SizeNameDialogRadius` | 12 | Fyne 2.8, the sheet |
| `SizeNamePopupRadius`, `SizeNameMenuRadius` | 8, 6 | Fyne 2.8 |
| `SizeNameSeparatorThickness`, `SizeNameSplitThickness` | 1, 1 | |
| `SizeNameScrollBar`, `SizeNameScrollBarSmall`, `SizeNameScrollBarRadius` | 8, 3, 4 | |
| `SizeNameInlineIcon` | 16 | Same as the tile |
| `SizeNameModalBlurRadius` | 0 | The scrim does the work; no blur |
| Others | Fyne default | Window-button sizes are for inner windows, which kipple does not use |

Every size the theme returns is multiplied by a session zoom factor (section 9), so ⌘+ and ⌘- scale the whole interface.

### 8.5 Icons and brand assets

- `Icon(name fyne.ThemeIconName) fyne.Resource` returns Fyne's default icons (copy, folder, warning, info). Fyne recolours them with `ink`, so they match. kipple adds no icon set.
- The tile check, the group tile, the place grid and the recess mark are drawn in code with `canvas.Rectangle` (`CornerRadius`, `StrokeWidth`) and a small check-glyph SVG.
- The at-risk triangle is a one-path SVG recoloured with `amber`.
- **App icon:** `assets/brand/kipple-icon.icns` goes into the bundle as `Contents/Resources/kipple.icns`, named by `CFBundleIconFile` in the bundle's `Info.plist`. At runtime `app.SetIcon` uses `kipple-icon-1024.png`, for the About panel and for unbundled runs.
- **Header lockup:** `kipple-wordmark.svg` (light) or `kipple-wordmark-dark.svg` (dark), 20pt tall, above the brand's 18pt minimum, with one tile of clear space. **Start-screen mark:** `kipple-mark.svg` or `kipple-mark-dark.svg` at 96pt.
- The SVGs colour their tiles with `currentColor`. Fyne's SVG renderer is not guaranteed to resolve it, so the embed package replaces `currentColor` with the file's own declared colour before making the resource. The amber stray keeps its own fill.
- `go:embed` cannot reach outside a package's directory, so a small package `assets/brand` (`brand.go`, no build tag, standard library only) embeds the SVGs and PNG. Only `cmd/kipple-gui` imports it.

## 9. Keyboard and menus

Everything is reachable without a mouse. Fyne shortcuts are registered on the window canvas with `desktop.CustomShortcut` (⌘ is `fyne.KeyModifierSuper`). List keys are handled by the list widget, which is focusable.

| Keys | Where | Action |
|---|---|---|
| Return | Start | Scan |
| ⌘R | Start, review, done, scan error | Scan (again) |
| Esc, ⌘. | Scanning | Stop scan |
| ↑ ↓ | Review list | Move the cursor between items (headings skipped) |
| ⌥↑ ⌥↓ | Review list | Previous or next rule (the TUI's tab and shift-tab) |
| Page Up, Page Down, Home, End | Review list | Page, first, last item |
| Space | Review list | Toggle the cursor item (refusals explained in the status) |
| ⌥Space | Review list, on a safe or review group | Toggle the cursor item's whole group |
| ⌘C | Review | Copy the cursor item's path |
| ⇧⌘C | Review | Copy the cursor item's ID |
| ⌘⌫ | Review | Move to Trash… (opens the sheet) |
| ⌘⌫ again | Confirm | Move to Trash |
| Esc, ⌘. | Confirm | Cancel |
| Tab, ⇧Tab | Everywhere | Move focus: list, detail buttons, action bar |
| Esc, ⌘. | Applying | Stop after this item |
| ⇧⌘T | Done | Open Trash |
| ⌘O | Done | Open receipt |
| ⌘+ ⌘- ⌘0 | Everywhere | Zoom in, out, reset (session only) |
| ⌘W, ⌘Q | Everywhere | Close and quit (Stop first while applying) |

Menus, through `fyne.MainMenu`, which Fyne turns into the macOS menu bar:

- **kipple:** About kipple (version, licence, a line on safety), Quit. Fyne supplies the app menu.
- **File:** Scan ⌘R, Move to Trash… ⌘⌫, Open Trash ⇧⌘T, Open Receipt ⌘O, Close Window ⌘W. Items are disabled when they don't apply.
- **Edit:** Copy Path ⌘C, Copy ID ⇧⌘C.
- **View:** Zoom In, Zoom Out, Actual Size.
- **Help:** How kipple Stays Safe, a dialog with the five promises from docs/safety.md, embedded as text. No links out.

## 10. Accessibility

### 10.1 Contrast

Computed with the WCAG 2.x relative-luminance formula. Text needs 4.5:1. Graphics and control boundaries need 3:1.

| Pair | Light | Dark | Needs |
|---|---|---|---|
| `ink` text on `background` | 15.0:1 | 13.6:1 | 4.5 (text) |
| `ink` text on `surface` | 16.1:1 | 12.3:1 | 4.5 (text) |
| `ink` text on `header` | 14.0:1 | 11.1:1 | 4.5 (text) |
| `ink` text on `cursor` | 13.6:1 | 9.3:1 | 4.5 (text) |
| `ink` text on `hover` | 14.6:1 | 10.9:1 | 4.5 (text) |
| `muted` text on `background` | 6.4:1 | 7.4:1 | 4.5 (text) |
| `muted` text on `surface` | 6.9:1 | 6.7:1 | 4.5 (text) |
| `muted` text on `header` | 6.0:1 | 6.0:1 | 4.5 (text) |
| `muted` text on `cursor` | 5.8:1 | 5.1:1 | 4.5 (text) |
| `muted` text on `hover` | 6.3:1 | 5.9:1 | 4.5 (text) |
| `amberInk` text on `background` | 5.4:1 | 8.0:1 | 4.5 (text) |
| `amberInk` text on `surface` | 5.8:1 | 7.2:1 | 4.5 (text) |
| `amberInk` text on `header` | 5.0:1 | 6.5:1 | 4.5 (text) |
| `amberInk` text on `cursor` | 4.9:1 | 5.5:1 | 4.5 (text) |
| `amberInk` text on `hover` | 5.3:1 | 6.4:1 | 4.5 (text) |
| `onPrimary` text on `primary` | 15.0:1 | 13.6:1 | 4.5 (text) |
| `amber` on `surface` | 3.8:1 | 7.2:1 | 3.0 (graphic) |
| `amber` on `cursor` | 3.2:1 | 5.5:1 | 3.0 (graphic) |
| `amber` on `header` | 3.3:1 | 6.5:1 | 3.0 (graphic) |
| `onAmber` on `amber` | 3.8:1 | 8.0:1 | 3.0 (graphic) |
| `control` on `surface` | 6.9:1 | 6.7:1 | 3.0 (graphic) |
| `control` on `cursor` | 5.8:1 | 5.1:1 | 3.0 (graphic) |
| `control` on `header` | 6.0:1 | 6.0:1 | 3.0 (graphic) |
| `primary` (focus ring, progress) on `surface` | 16.1:1 | 12.3:1 | 3.0 (graphic) |
| `primary` (focus ring, progress) on `background` | 15.0:1 | 13.6:1 | 3.0 (graphic) |
| `primary` (focus ring, progress) on `cursor` | 13.6:1 | 9.3:1 | 3.0 (graphic) |
| `primary` focus ring on `primary` button | 1.0:1 | 1.0:1 | offset, see note |
| `disabled` on `surface` | 2.9:1 | 3.6:1 | exempt (inactive) |
| `recess` on `surface` | 1.2:1 | 1.2:1 | exempt (inactive) |

Notes on the pairs that would fail:

- The focus ring is the same colour as the primary button (1.0:1), so it is drawn 2pt outside the control with a 2pt gap of the ground colour. The ring is then measured against `surface` and `background`, where it is 12.3:1 or better.
- `disabled` text on `surface` is 2.9:1 (light) and 3.6:1 (dark). WCAG exempts inactive controls, and `disabled` is used only on disabled buttons and glyphs. Blocked and info rows use `muted` text (6.7:1 or better), because what they say still matters.
- The `recess` tile is 1.2:1 against `surface`. It marks an inactive control, which WCAG exempts, and the chip text says the same thing in words.
- Amber-deep text on paper is 3.5:1, which is why light text uses `amberInk` (`#995108`, 4.9:1 or better).

### 10.2 Not colour alone

| Meaning | Colour | Also carried by |
|---|---|---|
| Selected | Amber tile | Filled shape with a check glyph; "Selected" in the detail pane; the count in the header |
| At risk | Amber `▲` | The triangle's shape; the "At risk" field; the line in the confirm sheet |
| Blocked | Muted path | Recess tile shape; the chip's words; "Can't be selected" in the detail pane |
| Info | Muted path | No tile, a dot; the `info` pill; the section heading |
| Tier | Pill fill | The pill's word |
| Cursor | Lichen fill | The 3pt ink bar at the left edge |

### 10.3 Focus, targets, motion, screen readers

- Every custom widget (tile, list, text buttons) draws the 2pt offset ring when focused. Built-in buttons use Fyne's focus tint plus the same ring through a wrapper.
- Targets: item rows 32pt, group and section rows 40pt, buttons 32pt tall, tiles 16pt drawn inside a 32x32pt hit area.
- Motion: none that isn't state. Tiles change at once; the measuring bar is the only moving thing. There is no Fyne hook for macOS Reduce Motion, so there is no decorative motion to turn off.
- Zoom: ⌘+ and ⌘- scale the theme's sizes from 85% to 150%, so people can get larger text without system settings.
- Screen readers: Fyne 2.8 adds `fyne.Accessible` (`AccessibilityLabel`, `AccessibilityRole`). kipple's custom widgets implement it. A tile's label reads like "npm cache, ~/.npm/_cacache, 6.1 GB, safe, selected", and a blocked one adds the reason. 5.1 must check with VoiceOver whether Fyne's macOS driver passes these labels through. If it does not, the README says the GUI is not yet usable with VoiceOver and points to the CLI and TUI, which are.

## 11. What Fyne cannot do, and the workaround

| Limit | Workaround |
|---|---|
| No rich table, and `widget.Tree` rows are uniform, collapsible and carry no per-branch controls | One virtualised `widget.List` whose item template holds a section row, a group row and an item row, showing one per index. `SetItemHeight` gives headers 40pt and items 32pt. Groups never collapse, so a selected item can't be hidden, and the order matches the TUI. Thousands of `node_modules` rows stay cheap. |
| Native macOS menus only cover basics (no toolbar, no Services, no sheet-modal windows) | `fyne.MainMenu` for the menu bar. The header and action bar are drawn in the canvas. |
| Dialogs are drawn inside the canvas, not as native sheets | `widget.NewModalPopUp` holding kipple's own sheet layout (`surface`, 12pt radius, shadow), centred under the header, over a `scrim` rectangle. |
| Limited text styling: regular and bold only, no tracking, no inline runs in `widget.Label` | `widget.RichText` segments with kipple's colour names for mixed runs. Weight contrast comes from regular and bold plus size. Caption caps are uppercased strings. |
| `Label` truncates only at the end, and paths need their ends | Left truncation measured with `fyne.MeasureText` in the item row's layout. The full path is always in the detail pane. |
| Canvas objects can't be rotated, so the stray tile can't be drawn in code | The mark comes from the brand SVGs. Shapes drawn in code (tiles, recess) are always straight. |
| Fyne's SVG renderer may ignore `currentColor` | The embed package substitutes the declared colour before making the resource. |
| No API to reveal a file in Finder or open the Trash | `/usr/bin/open` by absolute path with arguments, never a shell: `open <trash dir>`, `open -R <receipt path>`, and the settings URL. Read-only. |
| Widgets must be updated on the main goroutine (Fyne 2.6 and later) | Scan and apply run in goroutines; progress and results reach the view through `fyne.Do`. |
| No tooltips in core Fyne (the tooltip add-on is a new dependency) | No tooltips. Everything a tooltip would say is in the detail pane or the status text. |
| Built-in focus styling is a tint | kipple's widgets draw the offset ring themselves. |
| No hook for Reduce Motion, Increase Contrast or the system text size | No decorative motion; contrast already high; in-app zoom. |
| Screen-reader support is new (2.8) and unverified on macOS | Implement `fyne.Accessible`; verify in 5.1; document honestly if it doesn't work. |

## 12. Architecture

The GUI adds no deletion logic (spec "Separate build target"). It scans with `scan.Run` and moves with `apply.Run`, passing the selection as `Only` and never setting `Permanent` or `DryRun`, exactly like the TUI.

| Package | Build tag | Imports Fyne | Role |
|---|---|---|---|
| `cmd/internal/launch` | none | no | Home resolution, the `KIPPLE_HOME` guard, the root refusal, config, receipt store, scan options and apply options, moved out of `cmd/kipple` so both front ends share them. It lives under `cmd/`, so `tools/checkhome` still allows it to read the real home. |
| `internal/selection` | none | no | Pre-selection, selectability and group toggling, the rules of section 5.3. Extracted from `internal/tui`, which then uses it. |
| `internal/words` | none | no | Reason, skip-reason and mover sentences and chips, moved from `internal/tui/reasons.go` and extended for skip reasons. |
| `internal/guimodel` | none | no | The view-model: screen state, rows, cursor, selection, totals, hints, confirm summary, done summary, error mapping. Scan and apply run through injected functions with callbacks, so tests drive it synchronously against fixtures. |
| `assets/brand` | none | no | `go:embed` of the brand SVGs and PNG. |
| `cmd/kipple-gui` | `gui` | yes | The Fyne app: theme, widgets, screens, menus, shortcuts, `fyne.Do` glue. |

Library additions, all small and read-only:

- `apply.ProbeTrash(env policy.Env) (TrashMode, error)` opens the home's `.Trash` through a policy session, closes it, and reports `rename` (it opened), `fallback` (EPERM or EACCES and a Finder trasher is available) or `unavailable`. It moves and writes nothing. The confirm sheet uses it for the Full Disk Access note.
- A Command Line Tools probe in `cmd/internal/launch` (`/usr/bin/xcode-select -p`) and an inspector that reports `git_unavailable` without running git (section 6.4).

Fyne version: `fyne.io/fyne/v2` v2.8.2, which needs Go 1.22 and so fits the module's `go 1.23`. It is added with `GOTOOLCHAIN=local go get`.

## 13. Build plan

Each commit leaves `make check` green, which runs lint, vet, race tests, the no-real-home check and the build. GUI code also passes `go vet -tags gui ./cmd/kipple-gui/...` and `go build -tags gui ./cmd/kipple-gui`.

### 5.1 The app

1. **Extract `cmd/internal/launch` from `cmd/kipple`.** A pure move of the resolve, guard, config, store and options code. Verify: the CLI's tests pass unchanged, and `make check-no-real-home` passes.
2. **Move reason copy to `internal/words`; add skip-reason and mover sentences.** The TUI switches to it. Verify: TUI golden tests unchanged; a test that every `plan.SkipReasons()` value has a sentence.
3. **Extract `internal/selection` from the TUI.** Verify: TUI tests unchanged; table tests for every tier and reason combination.
4. **Add `apply.ProbeTrash`.** Verify: fixture tests for an openable Trash, an unreadable one (chmod 000 inside the fixture), and no fallback under `KIPPLE_HOME`. Nothing is moved and the fixture's checksum is unchanged.
5. **Add `internal/guimodel` with tests** (section 14).
6. **Add Fyne and the `cmd/kipple-gui` skeleton**: window, theme (`theme.go`), the font spike's result, `assets/brand`. Verify: `go build -tags gui ./cmd/kipple-gui`; the `go list -deps` test.
7. **Header, start screen and scan screen**, wired to the view-model with `fyne.Do`.
8. **Review screen**: the grouped list, tile widget, detail pane, action bar, list keyboard handling.
9. **Confirm sheet and applying state**, with the close intercept.
10. **Done screen, refusal and error screens**, Open Trash, Open receipt, Open Privacy Settings.
11. **Menus, shortcuts, zoom, `fyne.Accessible`**, and the VoiceOver check written up in the commit message.
12. **Add the GUI tokens to docs/brand.md** (the derived tokens of section 8.1).

### 5.2 Selection parity

13. **Parity tests in `internal/guimodel`** (section 14), named after the spec scenarios.

### 5.3 CI and release

14. **`scripts/build-app.sh`**: builds `kipple-gui` for arm64 and amd64 with cgo, joins them with `lipo`, writes `kipple.app` (`Info.plist` with `CFBundleIdentifier com.rbutera.kipple`, `CFBundleIconFile kipple`, `LSMinimumSystemVersion`, `NSHighResolutionCapable`), copies `kipple-icon.icns`, ad-hoc signs with `codesign -s -`, and zips it. No `fyne` CLI tool is needed.
15. **CI**: the existing `gui` job also runs `go vet -tags gui` and `go test -tags gui ./cmd/kipple-gui/...`, then builds the bundle. Release: a separate job attaches `kipple-gui_<version>_darwin_universal.app.zip` and adds it to `checksums.txt` before signing. Verify: `actionlint` and `make release-snapshot`.

### 5.4 Docs

16. **Screenshot script and README section**: `scripts/gui-screenshots.sh` (section 14) writes `docs/img/gui-review-light.png`, `gui-review-dark.png`, `gui-confirm-light.png` and `gui-done-light.png`. The README gets a short GUI section with one screenshot, the install steps for the `.app`, and the Full Disk Access note. Verify: the images are committed and referenced, and the README test passes.

## 14. Test plan

**View-model tests, no window** (`internal/guimodel`, plain `go test`, every one on a `testutil.NewHome` fixture with `KIPPLE_HOME` and `KIPPLE_STATE_DIR` set):

| Test | Checks |
|---|---|
| `TestSameSelectionAsCLI` | Spec "Same selection as CLI": the view-model's pre-selected IDs equal the `selected` IDs of `kipple scan --level safe --json` on the same fixture, run through the CLI's own `run` entry point. |
| `TestInfoItemsNeverSelectable` | Toggling, group-toggling and any command path leave info IDs out of the selection, and the hint names the owner's command. |
| `TestConfirmationRequired` | Spec "Confirmation required": select, open confirm, cancel. Nothing under the fixture changed (checksum), no receipt was written, the status reads "Cancelled. Nothing was moved." |
| `TestReviewAndManualOptIn` | Review and manual items start unselected; one toggle selects them; manual groups have no group toggle. |
| `TestBlockedItemsRefused` | Every non-tier reason keeps an item unselectable, matching `apply.Preview`. |
| `TestGroupToggle` | All, none and partial states; blocked items are skipped. |
| `TestConfirmSummary` | Totals, rule list, tier line, at-risk lines; the Full Disk Access note when the probe says `fallback`; Move disabled when it says `unavailable`. |
| `TestApplyMovesSelection` | Apply through a fixture `DirTrasher`: the moved IDs equal the selection, the receipt exists, the done summary matches it. |
| `TestStopDuringApply` | Cancel after the first item: one moved, the rest "not attempted", done title "Stopped…". |
| `TestReceiptErrorState` | `SaveReceipt` fails on the Nth save: the receipt-error screen, with pending read from the store. |
| `TestTrashAccessState`, `TestHomeRefused`, `TestRootRefused`, `TestScanError` | Each refusal or error maps to its screen and copy. |
| `TestGitUnavailableBanner` | The Command Line Tools probe fails: no git process runs, project items show "no git", the banner appears. |
| `TestPartialAndWarnings` | `scan_partial`, `contains_mount` and plan warnings produce the chips and banner. |
| `TestEmptyStates` | Nothing found; only info items; nothing pre-selected. |
| `TestHostileNames` | Paths with control characters and escapes render through `SafeText` in rows, detail, confirm and done. |
| `TestOrdering` | Tier, then rule bytes, then item bytes, matching the TUI. |

**Build isolation** (in `cmd/kipple`, plain `go test`): `TestDefaultBuildHasNoFyne` runs `go list -deps ./...` without tags and fails on any `fyne.io/` package; it also runs `go list -deps -tags gui ./cmd/kipple-gui` and requires `fyne.io/fyne/v2`, so the check can't pass by accident.

**Widget tests** (`cmd/kipple-gui`, `-tags gui`, Fyne's `test` driver with its software renderer, no display): each screen renders at 960x640 in light and dark without panicking; the Move button is disabled with nothing selected; Space on an info row changes nothing; Esc on the sheet cancels. They assert widget state, not pixels, because font rendering differs between machines.

**Screenshots** (`scripts/gui-screenshots.sh`): runs a `gui`-tagged test that only runs when `KIPPLE_SCREENSHOTS=<dir>` is set. It builds a fixture home with `testutil.NewHome` in a temp dir (about 40 MB of realistic cache folders, a few project folders, one git repo with uncommitted work, so every row kind appears), sets `KIPPLE_HOME` and `KIPPLE_STATE_DIR` to it, asserts `GuardRealHome`, drives the view-model through scan, review, confirm and done (moving into the fixture's own `.Trash` with a `DirTrasher`), and saves `Canvas().Capture()` PNGs. It never touches the real home and never starts a real window. The sizes in the screenshots are the fixture's real megabytes, and the header shows the test-home label, because that is what is true.

## 15. Decisions needed before 5.1

1. **Amber as danger.** I read "danger = amber" as "selected for the Trash, or at risk", which is amber's brand meaning. Fyne's error, warning and success colours map to ink, as the TUI already does. Confirm, or say errors should be amber too, which would break the brand rule and the TUI's.
2. **Interface font.** Load SF Pro and SF Mono from `/System/Library/Fonts` at runtime, falling back to Fyne's bundled fonts if the spike shows Fyne can't render them. No font file ships either way. The alternative is to accept Fyne's bundled Noto Sans outright.
3. **Restore in the GUI.** People who use the `.app` may not have the CLI. Items moved by rename (when kipple has Full Disk Access) are unknown to Finder's Put Back, so their only way back is `kipple restore` or dragging them out by hand. I recommend a "Put back" button on the done screen for that receipt, built on `apply.Restore` with its own confirmation. It is outside D13's three views, so it needs a yes. Without it, the done screen gives the CLI command.
4. **A start screen instead of scanning on launch**, because the first scan triggers the macOS Downloads prompt and reads the real home. It costs one Return.
5. **The GUI always scans at level `safe`** and ignores the config file's `level`, to match the spec's parity requirement literally.
6. **Shared code moves**: `cmd/internal/launch`, `internal/selection` and `internal/words` are extracted from `cmd/kipple` and `internal/tui`; `apply.ProbeTrash` and the Command Line Tools probe are added. All are refactors or read-only additions, but they touch shipped, reviewed code.
7. **Fyne v2.8.2** is the version to pin.

# kipple brand

## Concept

In *Do Androids Dream of Electric Sheep?*, kipple is the useless stuff that
breeds when nobody is looking: junk mail, gum wrappers, yesterday's newspaper.
The kipple mark is a lowercase **k** laid out on a strict 3×4 grid of rounded
tiles, the way a careful person lays things out on a bench. Six tiles sit
exactly in their slots. The seventh, the foot of the k, has been knocked loose,
turned and nudged out of line, and it is the only thing in amber. That tile is
the kipple: one small thing out of place in an otherwise tidy system.

The mark says what the tool does without a broom or a bin. kipple looks at an
orderly home folder, finds the pieces that do not belong, and shows them to you
before anything moves. Nothing is torn up or dissolved, because kipple only
moves things to the Trash and can put them back. The amber tile is the exact
same shape as the others, so restoring it is just setting it back in its slot.
The app icon and the README banner extend the same idea: a field of slots with
a few tiles lifted out, each leaving an empty recess behind.

## Files

All files live in `assets/brand/`. Text in every SVG is converted to outlines, so
nothing depends on an installed font.

| File | Use |
|---|---|
| `kipple-mark.svg` / `kipple-mark-dark.svg` | The symbol alone, for light and dark grounds. 64×64 viewBox. |
| `kipple-wordmark.svg` / `kipple-wordmark-dark.svg` | Mark plus name. The default lockup. |
| `favicon.svg` | 16-unit pixel-snapped mark. Switches to dark colours with `prefers-color-scheme`. |
| `kipple-icon.svg`, `kipple-icon-1024.png`, `kipple-icon.icns` | macOS app icon, the source for Fyne. |
| `readme-header.svg` | 1200×300 banner for the top of README.md. |
| `src/build.py`, `src/outline.py` | The generator every SVG above comes from. |

## Palette

| Token | Hex | Light usage | Dark usage |
|---|---|---|---|
| `spruce` | `#10221E` | Ink: mark, wordmark, headings | Background of the icon and banner |
| `paper` | `#F3F4EF` | Background | Not used |
| `lichen` | `#E3EBE4` | Not used | Ink: mark, wordmark, headings |
| `amber` | `#F2A43A` | Not used, too faint on light grounds | The stray tile, the one flagged item |
| `amber-deep` | `#C26A0C` | The stray tile, the one flagged item | Not used |
| `slate` | `#4A5C57` | Secondary text | Not used |
| `fog` | `#9DB2AA` | Not used | Secondary text |
| `spruce-hi` / `spruce-lo` | `#1C3A33` / `#0B1815` | Icon field gradient only | Icon field gradient only |

Measured contrast: spruce on paper is 15:1. Lichen on spruce is 13.6:1, and
fog on spruce is 7.4:1. Amber on spruce is 8:1, and amber-deep on paper is
3.5:1, which is fine for a graphic but not for body text.

Amber means "this one is out of place". In the TUI and GUI, use it for items
selected for cleaning or needing review, and never for decoration or success
states.

## Type

**Bricolage Grotesque** by Mathieu Triay, SIL Open Font License 1.1.
[Google Fonts](https://fonts.google.com/specimen/Bricolage+Grotesque) ·
[source](https://github.com/ateliertriay/bricolage). The name fits: bricolage is
making something from whatever happens to be lying around.

- **Wordmark:** weight 640, width 88, optical size 96, tracking −0.005em. Always lowercase `kipple`.
- **Taglines and supporting lines:** weight 480 to 500, width 100, optical size 12 to 14.
- **Interface text:** the platform system font. Bricolage is for brand moments, not for UI.

To regenerate the SVGs, install `fonttools` and `uharfbuzz`. Download the
variable font into a folder, then run
`KIPPLE_BRAND_FONTS=<folder> python assets/brand/src/build.py assets/brand`.
Render PNGs from the SVGs with headless Chrome, and build the `.icns` with
`iconutil` from an iconset rendered at 16, 32, 128, 256 and 512 points at 1× and 2×.

## Clear space and size

- **Unit:** one tile, which is the width of a single square in the mark at the size you are using it.
- **Clear space:** keep one tile free on every side of the mark or wordmark. The amber tile counts as part of the mark.
- **Lockup:** in the wordmark, the top of the grid aligns with the top of the `k` ascender. The bottom row sits on the baseline, and the gap to the text is about a quarter of the ascender. Do not re-space it by hand; use the file.
- **Minimum size:** the mark down to 24px, and `favicon.svg` below that. The wordmark goes down to 18px tall.

## Recolouring

The tiles use `currentColor`, and each file sets a default `color` on its root
element. When you inline the SVG, set `color` on it to recolour the ink. The
stray tile carries the class `kipple-stray`, so a stylesheet can retint it with
`.kipple-stray { fill: … }`. For a one-colour version, such as a monochrome
terminal or an embossed print, make the stray the same colour as the ink. The
tilt still carries the idea.

## Do

- Keep the stray tile turned and out of line. It is the whole idea.
- Use the light files on paper or white and the dark files on spruce or any near-black.
- Keep amber to one meaning: the thing that does not belong.

## Don't

- Don't straighten the stray tile, add more stray tiles to the mark, or animate tiles flying away. kipple moves things to the Trash. It does not destroy them.
- Don't set the wordmark in another face, in capitals, or with the mark after the name.
- Don't add gradients, glows or outlines to the mark. The icon's depth belongs to the icon alone.
- Don't place the mark on busy imagery or on mid-tone grounds where neither variant reaches 3:1.

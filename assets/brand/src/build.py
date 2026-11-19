"""Generate the kipple brand SVGs from one geometry.

Usage: KIPPLE_BRAND_FONTS=<dir> python build.py <out-dir>
Needs fonttools and uharfbuzz. PNG and .icns outputs are rendered from the SVGs
(see docs/brand.md).

The mark is a lowercase k laid out on a 3x4 grid of rounded tiles, seven tiles
in place and the last one (the leg) knocked out of line: the bit of kipple.
"""
import math
import os
import sys

sys.path.insert(0, os.path.dirname(__file__))
from outline import outline  # noqa: E402

OUT = sys.argv[1] if len(sys.argv) > 1 else 'out'
# Directory holding BricolageGrotesque.ttf (the variable font from google/fonts, ofl/bricolagegrotesque).
FONTS = os.environ.get('KIPPLE_BRAND_FONTS', os.path.join(os.path.dirname(__file__), 'fonts'))
os.makedirs(OUT, exist_ok=True)

# Palette
SPRUCE = '#10221E'      # ink on light, icon field
SPRUCE_HI = '#1C3A33'   # icon field top
SPRUCE_LO = '#0B1815'   # icon field bottom
PAPER = '#F3F4EF'       # light ground
LICHEN = '#E3EBE4'      # ink on dark
FOG = '#9DB2AA'         # secondary text on dark
SLATE = '#4A5C57'       # secondary text on light
AMBER = '#F2A43A'       # the stray tile on dark
AMBER_DEEP = '#C26A0C'  # the stray tile on light

# Grid geometry (glyph units)
S = 12.0     # tile size
G = 2.6      # gutter
R = 3.2      # tile corner radius
STEP = S + G
# (col, row) of tiles that sit in place
TILES = [(0, 0), (0, 1), (0, 2), (0, 3), (2, 1), (1, 2)]
LEG = (2, 3)
STRAY_DX, STRAY_DY, STRAY_ROT = 3.0, 1.6, 17.0


def f(v):
    return ('%.2f' % v).rstrip('0').rstrip('.')


def rrect(x, y, w, h, r):
    """Rounded rect as a path (keeps the files path-only and predictable)."""
    return (f'M{f(x + r)} {f(y)}H{f(x + w - r)}A{f(r)} {f(r)} 0 0 1 {f(x + w)} {f(y + r)}'
            f'V{f(y + h - r)}A{f(r)} {f(r)} 0 0 1 {f(x + w - r)} {f(y + h)}'
            f'H{f(x + r)}A{f(r)} {f(r)} 0 0 1 {f(x)} {f(y + h - r)}'
            f'V{f(y + r)}A{f(r)} {f(r)} 0 0 1 {f(x + r)} {f(y)}Z')


def rot_rrect(cx, cy, s, r, deg):
    """Rotated rounded square as a path, sampled corners kept as arcs."""
    a = math.radians(deg)
    ca, sa = math.cos(a), math.sin(a)

    def p(x, y):
        return cx + x * ca - y * sa, cy + x * sa + y * ca
    h = s / 2
    seq = [((-h + r, -h), (h - r, -h)), ((h, -h + r), (h, h - r)),
           ((h - r, h), (-h + r, h)), ((-h, h - r), (-h, -h + r))]
    d = ''
    for i, (a0, a1) in enumerate(seq):
        x0, y0 = p(*a0)
        x1, y1 = p(*a1)
        d += (f'M{f(x0)} {f(y0)}' if i == 0 else f'A{f(r)} {f(r)} 0 0 1 {f(x0)} {f(y0)}') + f'L{f(x1)} {f(y1)}'
    x0, y0 = p(*seq[0][0])
    d += f'A{f(r)} {f(r)} 0 0 1 {f(x0)} {f(y0)}Z'
    return d


def stray_center(ox, oy, k):
    cx = ox + (LEG[0] * STEP + S / 2 + STRAY_DX) * k
    cy = oy + (LEG[1] * STEP + S / 2 + STRAY_DY) * k
    return cx, cy


def mark_paths(ox, oy, k):
    """Return (ink_path, stray_path) for the mark placed at origin, scale k."""
    ink = ''.join(rrect(ox + c * STEP * k, oy + r_ * STEP * k, S * k, S * k, R * k) for c, r_ in TILES)
    cx, cy = stray_center(ox, oy, k)
    return ink, rot_rrect(cx, cy, S * k, R * k, STRAY_ROT)


def glyph_extent():
    # rotated tile half extent
    h = S / 2
    a = math.radians(STRAY_ROT)
    ext = h * (abs(math.cos(a)) + abs(math.sin(a))) - (R) * (abs(math.cos(a)) + abs(math.sin(a)) - 1)
    cx = LEG[0] * STEP + S / 2 + STRAY_DX
    cy = LEG[1] * STEP + S / 2 + STRAY_DY
    return max(2 * STEP + S, cx + ext), max(3 * STEP + S, cy + ext)


GW, GH = glyph_extent()


def svg(w, h, body, title, extra=''):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {f(w)} {f(h)}" width="{f(w)}" height="{f(h)}"'
            f' role="img" aria-labelledby="t"{extra}>\n<title id="t">{title}</title>\n{body}\n</svg>\n')


def write(name, text):
    with open(os.path.join(OUT, name), 'w') as fh:
        fh.write(text)


# ---------- mark ----------
def build_mark(dark=False):
    box = 64
    k = 1.0
    ox, oy = (box - GW) / 2, (box - GH) / 2
    ink, stray = mark_paths(ox, oy, k)
    ink_c = LICHEN if dark else SPRUCE
    acc = AMBER if dark else AMBER_DEEP
    body = (f'<path fill="currentColor" d="{ink}"/>\n'
            f'<path class="kipple-stray" fill="{acc}" d="{stray}"/>')
    return svg(box, box, body, 'kipple', f' color="{ink_c}"')


# ---------- wordmark ----------
FONT = os.path.join(FONTS, 'BricolageGrotesque.ttf')
AXES = {'wght': 640, 'wdth': 88, 'opsz': 96}
GAP = 0.26   # mark-to-text gap, as a fraction of the ascender
TRACK = -0.005


def build_wordmark(dark=False):
    size = 100.0
    # measure text: baseline at 0
    d0, tw, (x0, y0, x1, y1) = outline(FONT, 'kipple', size, AXES, TRACK, 0, 0)
    asc = -y0             # ascender height above baseline (k / l top)
    # mark: tile grid top aligns to ascender, row-3 tile bottom sits on baseline
    k = asc / (3 * STEP + S)
    pad = 6.0
    mx, my = pad, pad + 0
    base = my + asc
    ink, stray = mark_paths(mx, my, k)
    tx = mx + GW * k + GAP * asc - x0
    d, tw, (bx0, by0, bx1, by1) = outline(FONT, 'kipple', size, AXES, TRACK, tx, base)
    w = bx1 + pad
    h = max(by1, my + GH * k) + pad
    ink_c = LICHEN if dark else SPRUCE
    acc = AMBER if dark else AMBER_DEEP
    body = (f'<g fill="currentColor"><path d="{ink}"/><path d="{d}"/></g>\n'
            f'<path class="kipple-stray" fill="{acc}" d="{stray}"/>')
    return svg(w, h, body, 'kipple', f' color="{ink_c}"')


# ---------- favicon: pixel-snapped on a 16 grid ----------
def build_favicon():
    # tiles 3px, gutter 1px, origin (2,1). leg tile nudged and turned.
    s, g = 3, 1
    ox, oy = 2, 1
    ink = ''.join(rrect(ox + c * (s + g), oy + r_ * (s + g), s, s, 0.7) for c, r_ in TILES)
    cx = ox + LEG[0] * (s + g) + s / 2 + 0.8
    cy = oy + LEG[1] * (s + g) + s / 2 + 0.4
    stray = rot_rrect(cx, cy, s, 0.7, STRAY_ROT)
    style = ('<style>.i{fill:%s}.s{fill:%s}@media (prefers-color-scheme:dark){.i{fill:%s}.s{fill:%s}}</style>'
             % (SPRUCE, AMBER_DEEP, LICHEN, AMBER))
    body = f'{style}\n<path class="i" d="{ink}"/>\n<path class="s" d="{stray}"/>'
    return svg(16, 16, body, 'kipple')


# ---------- app icon ----------
def squircle(cx, cy, size, n=5.0, steps=360):
    a = size / 2
    pts = []
    for i in range(steps):
        t = 2 * math.pi * i / steps
        c, s_ = math.cos(t), math.sin(t)
        x = a * math.copysign(abs(c) ** (2 / n), c)
        y = a * math.copysign(abs(s_) ** (2 / n), s_)
        pts.append((cx + x, cy + y))
    return 'M' + 'L'.join(f'{f(x)} {f(y)}' for x, y in pts) + 'Z'


def build_icon():
    W = 1024
    body_size = 824
    c = W / 2
    shape = squircle(c, c, body_size)
    # glyph inside the body, including the empty slots of the 3x4 tray
    k = 560 / (3 * STEP + S)
    gw = (2 * STEP + S) * k
    ox = c - gw / 2 - 10
    oy = c - (3 * STEP + S) * k / 2
    # only the slot the stray tile left behind is drawn, as a shallow recess
    slot_d = rrect(ox + LEG[0] * STEP * k, oy + LEG[1] * STEP * k, S * k, S * k, R * k)
    ink, stray = mark_paths(ox, oy, k)
    defs = f'''<defs>
<linearGradient id="field" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{SPRUCE_HI}"/><stop offset="1" stop-color="{SPRUCE_LO}"/></linearGradient>
<linearGradient id="tile" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#F7F9F5"/><stop offset="1" stop-color="#D9E3DC"/></linearGradient>
<linearGradient id="amber" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#F7B95A"/><stop offset="1" stop-color="#E48A1C"/></linearGradient>
<linearGradient id="rim" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#FFFFFF" stop-opacity=".16"/><stop offset=".5" stop-color="#FFFFFF" stop-opacity="0"/></linearGradient>
<filter id="drop" x="-10%" y="-10%" width="120%" height="125%"><feDropShadow dx="0" dy="12" stdDeviation="12" flood-color="#000" flood-opacity=".32"/></filter>
<filter id="tsh" x="-20%" y="-20%" width="140%" height="150%"><feDropShadow dx="0" dy="7" stdDeviation="6" flood-color="#020807" flood-opacity=".45"/></filter>
<filter id="ssh" x="-40%" y="-40%" width="180%" height="190%"><feDropShadow dx="6" dy="18" stdDeviation="16" flood-color="#020807" flood-opacity=".38"/></filter>
<clipPath id="body"><path d="{shape}"/></clipPath>
</defs>'''
    body = (f'{defs}\n<path d="{shape}" fill="url(#field)" filter="url(#drop)"/>\n'
            f'<g clip-path="url(#body)">\n'
            f'<path d="{slot_d}" fill="#000" fill-opacity=".2"/>\n'
            f'<path d="{slot_d}" fill="none" stroke="#FFFFFF" stroke-opacity=".08" stroke-width="3"/>\n'
            f'<path d="{ink}" fill="url(#tile)" filter="url(#tsh)"/>\n'
            f'<path d="{stray}" fill="url(#amber)" filter="url(#ssh)"/>\n'
            f'<path d="{shape}" fill="none" stroke="url(#rim)" stroke-width="4"/>\n'
            f'</g>')
    return svg(W, W, body, 'kipple')


# ---------- README header ----------
TAGLINE = 'Clears the junk your AI tools and dev toolchains leave behind.'
SUB = 'macOS  ·  dry run first  ·  Trash by default  ·  MIT'


def build_header():
    W, H = 1200, 300
    left = 80
    size = 124.0
    _, _, (x0, y0, x1, y1) = outline(FONT, 'kipple', size, AXES, TRACK, 0, 0)
    asc = -y0
    tag_axes = {'wght': 480, 'wdth': 100, 'opsz': 14}
    sub_axes = {'wght': 500, 'wdth': 100, 'opsz': 12}
    # vertical rhythm relative to the wordmark baseline
    tag_base = 0.30 * size + 34      # clears the p descenders
    sub_base = tag_base + 36
    top_rel, bottom_rel = -asc, sub_base + 4
    base = (H - (bottom_rel - top_rel)) / 2 - top_rel
    k = asc / (3 * STEP + S)
    ink, stray = mark_paths(left, base - asc, k)
    tx = left + GW * k + GAP * asc - x0
    d, _, _ = outline(FONT, 'kipple', size, AXES, TRACK, tx, base)
    td, _, _ = outline(FONT, TAGLINE, 28, tag_axes, 0, left + 2, base + tag_base)
    sd, _, _ = outline(FONT, SUB, 17, sub_axes, 0.03, left + 2, base + sub_base)
    # right side: a tray of slots, three tiles knocked loose, fading out leftwards
    ts = 38.0
    tg = ts * G / S
    tr = ts * R / S
    cols, rows = 6, 5
    gx0 = W - 44 - cols * (ts + tg) + tg
    gy0 = (H - (rows * (ts + tg) - tg)) / 2
    loose = {(4, 1): (16, 4, 3), (3, 3): (-11, -2, 5), (5, 4): (24, 3, -5)}
    filled, recess, sp = '', '', ''
    for rr in range(rows):
        for cc in range(cols):
            x, y = gx0 + cc * (ts + tg), gy0 + rr * (ts + tg)
            if (cc, rr) in loose:
                recess += rrect(x, y, ts, ts, tr)
                rot, dx, dy = loose[(cc, rr)]
                sp += rot_rrect(x + ts / 2 + dx, y + ts / 2 + dy, ts, tr, rot)
            else:
                filled += rrect(x, y, ts, ts, tr)
    defs = f'''<defs>
<linearGradient id="fade" x1="{f(gx0 + 10)}" y1="0" x2="{f(W - 60)}" y2="0" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="#fff" stop-opacity="0"/><stop offset=".6" stop-color="#fff"/></linearGradient>
<mask id="fm"><rect width="{W}" height="{H}" fill="url(#fade)"/></mask>
<clipPath id="c"><rect width="{W}" height="{H}" rx="20"/></clipPath>
<filter id="sh" x="-30%" y="-30%" width="160%" height="170%"><feDropShadow dx="0" dy="6" stdDeviation="5" flood-color="#000" flood-opacity=".45"/></filter>
</defs>'''
    body = (f'{defs}\n<g clip-path="url(#c)">\n<rect width="{W}" height="{H}" fill="{SPRUCE}"/>\n'
            f'<g mask="url(#fm)">\n<path d="{filled}" fill="{LICHEN}" fill-opacity=".075"/>\n'
            f'<path d="{recess}" fill="#000" fill-opacity=".35"/>\n'
            f'</g>\n<path d="{sp}" fill="{AMBER}" filter="url(#sh)"/>\n'
            f'<path d="{ink}" fill="{LICHEN}"/>\n<path d="{stray}" fill="{AMBER}"/>\n'
            f'<path d="{d}" fill="{LICHEN}"/>\n'
            f'<path d="{td}" fill="{LICHEN}"/>\n'
            f'<path d="{sd}" fill="{FOG}"/>\n</g>')
    return svg(W, H, body, 'kipple: ' + TAGLINE)


if __name__ == '__main__':
    write('kipple-mark.svg', build_mark())
    write('kipple-mark-dark.svg', build_mark(dark=True))
    write('kipple-wordmark.svg', build_wordmark())
    write('kipple-wordmark-dark.svg', build_wordmark(dark=True))
    write('favicon.svg', build_favicon())
    write('kipple-icon.svg', build_icon())
    write('readme-header.svg', build_header())
    print('built', sorted(os.listdir(OUT)))

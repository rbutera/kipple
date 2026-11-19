"""Outline text to an SVG path string. Usage as a module: outline(font, text, size, axes, tracking)."""
import sys, io, json
import uharfbuzz as hb
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.pens.boundsPen import BoundsPen

def outline(font_path, text, size, axes=None, tracking=0.0, x=0.0, y=0.0):
    """Return (d, width, bounds) for text set at `size` px, baseline at y, origin x.
    tracking in em units added after each glyph except the last."""
    tt = TTFont(font_path)
    if axes and 'fvar' in tt:
        tt = instantiateVariableFont(tt, axes)
    buf = io.BytesIO(); tt.save(buf); data = buf.getvalue()
    face = hb.Face(data); font = hb.Font(face)
    upem = face.upem
    b = hb.Buffer(); b.add_str(text); b.guess_segment_properties()
    hb.shape(font, b, {"kern": True, "liga": False})
    gs = tt.getGlyphSet(); order = tt.getGlyphOrder()
    s = size / upem
    pen = SVGPathPen(gs, ntos=lambda v: ('%.2f' % v).rstrip('0').rstrip('.'))
    bp = BoundsPen(gs)
    cx = 0
    n = len(b.glyph_infos)
    for i, (info, pos) in enumerate(zip(b.glyph_infos, b.glyph_positions)):
        name = order[info.codepoint]
        t = (s, 0, 0, -s, x + (cx + pos.x_offset) * s, y - pos.y_offset * s)
        gs[name].draw(TransformPen(pen, t))
        gs[name].draw(TransformPen(bp, t))
        cx += pos.x_advance + (tracking * upem if i < n - 1 else 0)
    return pen.getCommands(), cx * s, bp.bounds

if __name__ == '__main__':
    a = json.loads(sys.argv[1])
    d, w, bb = outline(a['font'], a['text'], a['size'], a.get('axes'), a.get('tracking', 0), a.get('x', 0), a.get('y', 0))
    print(json.dumps({'d': d, 'width': w, 'bounds': bb}))

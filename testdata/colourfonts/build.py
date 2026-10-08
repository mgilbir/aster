# Builds the colour fonts aster's colour-glyph tests draw with, into this
# directory:
#
#   python3 testdata/colourfonts/build.py
#
# aster bundles no colour font: a caller gives one with WithFont, or a system
# font answers. These two are small stand-ins for one, each glyph mapped to an
# emoji's code point and painted with one thing a colour glyph can be:
#
# ColourTest.ttf, COLR and CPAL:
#   U+1F534  COLRv0 layers: a square in palette colour 0 under a triangle in 1
#   U+1F7E2  COLRv1 solid fill at an alpha
#   U+1F308  linear gradient, pad, its stops out of order and past 0..1
#   U+1F31E  radial gradient between two circles, repeat
#   U+1F300  sweep gradient, reflect
#   U+1F600  layers: a rotated triangle in the foreground over a square
#   U+1F3A8  composite SRC_IN: a solid triangle kept only inside a square
#   U+1F4A0  composite MULTIPLY: a solid triangle multiplied onto a gradient
#   U+1F4A1  composite HSL_LUMINOSITY
#   U+1F52E  linear gradient, reflect, a stop at 30% alpha
#   U+1F4A7  radial gradient, pad, a stop at 50% alpha
#   U+0041   'A', an outline with no colour
#
# SbixTest.ttf, sbix: U+1F600 as a PNG in two strikes, 20 and 100 pixels per
# em, each a square of its own colour, so that a test can tell which strike
# was drawn; and 'A', an outline with no image.
#
# ColourPair.ttc is the two in one TrueType collection, for the system font
# scanner, which indexes each face of a collection.
#
# fontTools takes COLR's angles in degrees, and forme hands them out in
# radians. They are built with fontTools and their timestamps fixed, so that building
# them again produces the same bytes.
import os
import struct
import sys
import zlib

from fontTools.colorLib.builder import buildCOLR, buildCPAL
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib.tables import otTables as ot
from fontTools.ttLib.tables import _s_b_i_x as sbixTable
from fontTools.ttLib.tables import sbixGlyph, sbixStrike

F = ot.PaintFormat
FOREGROUND = 0xFFFF
UPEM, ASCENT, DESCENT, ADVANCE = 1000, 800, -200, 1000
TIMESTAMP = 3660681600


def poly(*pts):
    pen = TTGlyphPen(None)
    pen.moveTo(pts[0])
    for p in pts[1:]:
        pen.lineTo(p)
    pen.closePath()
    return pen.glyph()


SQ, TRI = "sq", "tri"
OUTLINES = {
    ".notdef": poly((100, 0), (100, 700), (600, 700), (600, 0)),
    SQ: poly((100, 0), (100, 800), (900, 800), (900, 0)),
    TRI: poly((100, 0), (500, 800), (900, 0)),
    "A": poly((100, 0), (500, 700), (900, 0)),
}


def fill(outline, paint):
    return {"Format": F.PaintGlyph, "Glyph": outline, "Paint": paint}


def solid(index, alpha=1.0):
    return {"Format": F.PaintSolid, "PaletteIndex": index, "Alpha": alpha}


def stop(offset, index, alpha=1.0):
    return {"StopOffset": offset, "PaletteIndex": index, "Alpha": alpha}


def line(extend, *stops):
    return {"Extend": extend, "ColorStop": list(stops)}


COLOR = {
    "u1F7E2": fill(SQ, solid(1, 0.5)),
    "u1F308": fill(SQ, {
        "Format": F.PaintLinearGradient,
        "ColorLine": line("pad", stop(1.25, 1), stop(-0.25, 0), stop(0.5, 2)),
        "x0": 100, "y0": 0, "x1": 900, "y1": 0, "x2": 100, "y2": 800}),
    "u1F31E": fill(SQ, {
        "Format": F.PaintRadialGradient,
        "ColorLine": line("repeat", stop(0.0, 0), stop(1.0, 1)),
        "x0": 400, "y0": 400, "r0": 50, "x1": 500, "y1": 400, "r1": 200}),
    "u1F300": fill(SQ, {
        "Format": F.PaintSweepGradient,
        "ColorLine": line("reflect", stop(0.0, 0), stop(0.5, 2), stop(1.0, 1)),
        "centerX": 500, "centerY": 400, "startAngle": 0.0, "endAngle": 90.0}),
    "u1F600": {"Format": F.PaintColrLayers, "Layers": [
        fill(SQ, solid(2)),
        {"Format": F.PaintRotateAroundCenter, "angle": 45.0, "centerX": 500, "centerY": 400,
         "Paint": fill(TRI, solid(FOREGROUND, 0.8))}]},
    "u1F3A8": {"Format": F.PaintComposite, "CompositeMode": "SRC_IN",
               "SourcePaint": fill(TRI, solid(0)),
               "BackdropPaint": {"Format": F.PaintTranslate, "dx": 400, "dy": 0,
                                 "Paint": fill(SQ, solid(1))}},
    "u1F4A0": {"Format": F.PaintComposite, "CompositeMode": "MULTIPLY",
               "SourcePaint": fill(TRI, solid(1)),
               "BackdropPaint": fill(SQ, {
                   "Format": F.PaintLinearGradient,
                   "ColorLine": line("pad", stop(0.0, 0), stop(1.0, 2)),
                   "x0": 100, "y0": 0, "x1": 900, "y1": 0, "x2": 100, "y2": 800})},
    "u1F52E": fill(SQ, {
        "Format": F.PaintLinearGradient,
        "ColorLine": line("reflect", stop(0.0, 0), stop(1.0, 1, 0.3)),
        "x0": 300, "y0": 0, "x1": 500, "y1": 0, "x2": 300, "y2": 800}),
    "u1F4A7": fill(SQ, {
        "Format": F.PaintRadialGradient,
        "ColorLine": line("pad", stop(0.0, 2), stop(1.0, 0, 0.5)),
        "x0": 500, "y0": 400, "r0": 0, "x1": 500, "y1": 400, "r1": 400}),
    "u1F4A1": {"Format": F.PaintComposite, "CompositeMode": "HSL_LUMINOSITY",
               "SourcePaint": fill(TRI, solid(2)),
               "BackdropPaint": fill(SQ, solid(0))},
}

V0 = {"u1F534": [(SQ, 0), (TRI, 1)]}

# Red, blue, and green.
PALETTES = [[(0.9, 0.1, 0.1, 1.0), (0.1, 0.2, 0.9, 1.0), (0.1, 0.7, 0.2, 1.0)]]


def base(family, order, cmap, outlines):
    fb = FontBuilder(UPEM, isTTF=True)
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap(cmap)
    fb.setupGlyf(outlines)
    # Each left side bearing is its outline's xMin, as renderers that move an
    # outline to the bearing hmtx states expect.
    glyf = fb.font["glyf"]
    fb.setupHorizontalMetrics({name: (ADVANCE, getattr(glyf[name], "xMin", 0)) for name in order})
    fb.setupHorizontalHeader(ascent=ASCENT, descent=DESCENT)
    fb.setupOS2(sTypoAscender=ASCENT, sTypoDescender=DESCENT, usWinAscent=ASCENT,
                usWinDescent=-DESCENT)
    fb.setupNameTable({"familyName": family, "styleName": "Regular"})
    fb.setupPost()
    return fb


def save(fb, path):
    fb.font["head"].created = fb.font["head"].modified = TIMESTAMP
    fb.font.recalcTimestamp = False
    fb.save(path)


def colr(path):
    names = sorted(COLOR) + sorted(V0)
    order = [".notdef", SQ, TRI, "A"] + names
    outlines = dict(OUTLINES)
    for name in names:
        # The outline a renderer with no colour draws in its place.
        outlines[name] = poly((100, 0), (100, 800), (900, 800), (900, 0))
    cmap = {ord("A"): "A"}
    cmap.update({int(name[1:], 16): name for name in names})
    fb = base("ColourTest", order, cmap, outlines)
    glyphs = dict(COLOR)
    glyphs.update(V0)
    fb.font["COLR"] = buildCOLR(glyphs, glyphMap=fb.font.getReverseGlyphMap())
    fb.font["CPAL"] = buildCPAL(PALETTES)
    save(fb, path)


def png(width, height, rgba):
    """A PNG of one colour, written by hand so that no imaging library is
    needed and the bytes never move."""
    raw = b"".join(b"\x00" + bytes(rgba) * width for _ in range(height))

    def chunk(kind, data):
        return (struct.pack(">I", len(data)) + kind + data
                + struct.pack(">I", zlib.crc32(kind + data)))

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))


# Each strike's colour: orange at 20 ppem, purple at 100.
STRIKES = {20: (255, 140, 0, 255), 100: (120, 40, 200, 255)}


def sbix(path):
    order = [".notdef", "A", "u1F600"]
    outlines = {".notdef": OUTLINES[".notdef"], "A": OUTLINES["A"],
                "u1F600": poly((100, 0), (100, 800), (900, 800), (900, 0))}
    fb = base("SbixTest", order, {ord("A"): "A", 0x1F600: "u1F600"}, outlines)
    table = sbixTable.table__s_b_i_x()
    table.version, table.flags = 1, 1
    for ppem, rgba in sorted(STRIKES.items()):
        strike = sbixStrike.Strike(ppem=ppem, resolution=72)
        for name in order:
            strike.glyphs[name] = sbixGlyph.Glyph(glyphName=name)
        side = ppem * 8 // 10
        strike.glyphs["u1F600"] = sbixGlyph.Glyph(
            glyphName="u1F600", graphicType="png ", originOffsetX=ppem // 10,
            originOffsetY=0, imageData=png(side, side, rgba))
        table.strikes[ppem] = strike
    fb.font["sbix"] = table
    save(fb, path)


def collection(path, *fonts):
    from fontTools.ttLib import TTFont
    from fontTools.ttLib.ttCollection import TTCollection
    ttc = TTCollection()
    ttc.fonts = [TTFont(f, recalcTimestamp=False) for f in fonts]
    ttc.save(path)


if __name__ == "__main__":
    here = os.path.dirname(os.path.abspath(__file__))
    out = sys.argv[1] if len(sys.argv) > 1 else here
    colr(os.path.join(out, "ColourTest.ttf"))
    sbix(os.path.join(out, "SbixTest.ttf"))
    collection(os.path.join(out, "ColourPair.ttc"),
               os.path.join(out, "ColourTest.ttf"), os.path.join(out, "SbixTest.ttf"))

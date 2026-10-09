# Builds NotoColorEmojiSubset.ttf, a subset of Noto Color Emoji (COLRv1) for
# the colour glyph tests to check aster against a real colour font:
#
#   python3 testdata/colourfonts/noto.py
#
# Noto Color Emoji is (c) 2013 Google LLC under the SIL Open Font License
# 1.1, which NotoColorEmojiSubset-OFL.txt is; it names no Reserved Font
# Name, and the subset is renamed all the same, as a modified version. The
# source is pinned by commit and by digest, so that building it again gives
# the same bytes.
#
# It keeps EMOJI, below: faces, gradients and composites from across the
# font, a flag (a ligature of two regional indicators), a ZWJ sequence and a
# skin tone (ligatures too), with the layout tables that make them. Their
# drawing by HarfBuzz is noto-hb-view.png:
#
#   hb-view --font-size=60 --margin=10 --background=EEEEEE -O png \
#     -o noto-hb-view.png NotoColorEmojiSubset.ttf "$(python3 testdata/colourfonts/noto.py --text)"
import hashlib
import os
import sys
import urllib.request

COMMIT = "e20cbc2bbec1926686be9f9bee7d1d2cfa1fea0e"
URL = f"https://raw.githubusercontent.com/googlefonts/noto-emoji/{COMMIT}/2D/fonts/Noto-COLRv1.ttf"
SHA256 = "b8e25ea68db82f9e4d0aee921f4420be2be39887bd5c893a2ad98710531f9d0c"
FAMILY = "Noto Color Emoji Subset"

# The text drawn: each a glyph after shaping.
EMOJI = [
    "\U0001F600", "\U0001F602", "\U0001F970", "\U0001F308", "\U0001F525", "\U0001F389",
    "\U0001F984", "\U0001F355", "\U0001F3A8", "\U0001F30D", "\U0001F680", "\U0001F49C",
    "\U0001F1F3\U0001F1F1",                # flag of the Netherlands
    "\U0001F469‍\U0001F4BB",          # woman technologist
    "\U0001F44D\U0001F3FD",                # thumbs up, medium skin tone
]
TEXT = "".join(EMOJI)
TIMESTAMP = 3660681600


def build(path):
    from fontTools import subset
    from fontTools.ttLib import TTFont
    data = urllib.request.urlopen(URL).read()
    if hashlib.sha256(data).hexdigest() != SHA256:
        sys.exit(f"{URL}: not the font pinned")
    src = path + ".src"
    with open(src, "wb") as f:
        f.write(data)
    try:
        font = TTFont(src)
        opts = subset.Options()
        opts.layout_features = ["*"]
        opts.name_IDs = ["*"]
        opts.notdef_outline = True
        sub = subset.Subsetter(opts)
        sub.populate(text=TEXT)
        sub.subset(font)
        name = font["name"]
        for rec in name.names:
            if rec.nameID in (1, 4, 16):
                rec.string = FAMILY
            elif rec.nameID == 6:
                rec.string = FAMILY.replace(" ", "")
            elif rec.nameID == 3:
                rec.string = f"{FAMILY.replace(' ', '')}-{COMMIT[:12]}"
        font["head"].created = font["head"].modified = TIMESTAMP
        font.recalcTimestamp = False
        font.save(path)
    finally:
        os.remove(src)


if __name__ == "__main__":
    if sys.argv[1:] == ["--text"]:
        print(TEXT, end="")
        sys.exit()
    here = os.path.dirname(os.path.abspath(__file__))
    build(os.path.join(here, "NotoColorEmojiSubset.ttf"))

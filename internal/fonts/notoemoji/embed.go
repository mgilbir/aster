// Package notoemoji embeds the monochrome Noto Emoji font, used as an
// always-present fallback so emoji codepoints have correct advance widths
// (text layout) and rasterize as monochrome glyphs in PNG output.
//
// This is the colour-free Noto Emoji (glyf outlines, about 2 MB), not Noto
// Color Emoji, whose CBDT bitmaps are about 11 MB: emoji in SVG output are
// drawn by the viewer's own font regardless, and a caller who wants them in
// colour in PNG and PDF registers a colour font, which the rasterizer and
// the PDF writer paint (COLR, sbix, CBDT). See LICENSE (SIL OFL 1.1).
package notoemoji

import _ "embed"

// Family is the font family name reported by the embedded TTF.
const Family = "Noto Emoji"

//go:embed NotoEmoji.ttf
var Regular []byte

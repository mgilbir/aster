// Package notoemoji embeds the monochrome Noto Emoji font, used as an
// always-present fallback so emoji codepoints have correct advance widths
// (text layout) and rasterize as monochrome glyphs in PNG output.
//
// This is the color-free Noto Emoji (glyf outlines), not Noto Color Emoji:
// resvg cannot rasterize color-bitmap (CBDT) fonts, and emoji in SVG output
// are drawn by the viewer's own font regardless. See LICENSE (SIL OFL 1.1).
package notoemoji

import _ "embed"

// Family is the font family name reported by the embedded TTF.
const Family = "Noto Emoji"

//go:embed NotoEmoji.ttf
var Regular []byte

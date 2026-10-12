# Fonts

Text is measured for layout and drawn in PNG and PDF from the same fonts, so a label never overflows the space the layout gave it. Out of the box, aster has Liberation Sans, Serif and Mono (metrically compatible with Arial, Times New Roman and Courier New, as vl-convert uses) and monochrome Noto Emoji. Every font option below applies to layout, PNG and PDF alike. SVG output names the fonts and leaves drawing to the viewer.

```go
regular, _ := os.ReadFile("Inter-Regular.ttf")
bold, _ := os.ReadFile("Inter-Bold.ttf")
c, err := aster.New(
    aster.WithFont("Inter", regular),
    aster.WithFont("Inter", bold),          // weight and style are read from the font
    aster.WithDefaultFontFamily("Inter"),   // what "sans-serif" (Vega's default) means
)
```

## Options

| Option | Default | What it does |
|---|---|---|
| `WithFont(family, data)` | none | register a TrueType, OpenType or WOFF font under `family`; a collection (`.ttc`/`.otc`) registers every face |
| `WithFontFace(family, ttc, index)` | none | register one face of a collection |
| `WithFontInstance(family, data, axes)` | none | register a variable font at one point of its design space (`{"wght": 650}`); register several under one family to let `font-weight` choose |
| `WithFontPalette(family, index)` | palette 0 | draw the family's colour glyphs with another CPAL palette (a dark-mode one, say) |
| `WithDefaultFontFamily(name)` | `Liberation Sans` | the family generic `sans-serif` resolves to |
| `WithDefaultSerifFamily(name)` | `Liberation Serif` | the family `serif` resolves to |
| `WithDefaultMonospaceFamily(name)` | `Liberation Mono` | the family `monospace` resolves to |
| `WithFontFallback(families...)` | registered fonts newest first, then embedded | the families that draw, in order, characters the requested family lacks; none: no fallback |
| `WithSystemFonts()` | off | also use fonts installed on the machine, when a spec names their family (or `WithFontFallback` does) |
| `WithTextMeasurement(false)` | on | don't measure text: use Vega's estimate (0.8 × length × size); the wordcloud transform then fails |
| `WithHarfBuzzTextMetrics()` | off | measure with HarfBuzz's rounding (size rounded up to whole pixels, advances to 1/64 px) instead of browser-exact advances; for output byte-identical to earlier versions of aster |

`New` loads the fonts you give it and fails if one does not load (or a `WithFontInstance` axis does not exist, a `WithFontFace` index is negative, or a `WithFontFallback` or `WithDefault*Family` family has no font), so a mistake is caught at startup, not on every render. It copies the bytes you pass, so the buffer is yours again once the option is made. The fonts load once, into one set that layout, PNG and PDF all draw from: no output parses a font, or scans the system's, a second time. With the embedded fonts alone, `New` loads nothing and stays cheap; they load on first use.

## Which font draws a character

1. **The family the spec names** (`"font": "Inter"`, or a generic family mapped above). A family you register faces for **is made of your faces alone**, as a CSS `@font-face` family is: an embedded or system font of the same name is no longer that family. So `WithFont("Liberation Sans", data)` replaces the embedded Liberation Sans, and `WithFont("Arial", data)` replaces the embedded Liberation Sans that stands in for Arial. Among the family's faces, CSS `font-weight` and `font-style` pick the best fit; of faces that fit equally, **the one registered last wins**. A family you don't register is the embedded faces of that name, then system fonts (with `WithSystemFonts`).
2. **The default family** (`WithDefaultFontFamily`, Liberation Sans unless you change it), as the generic sans-serif.
3. **The fallback chain, for characters those lack** (emoji, other scripts). By default this is your registered fonts, the most recently registered first, then the embedded ones, with Noto Emoji last. `WithFontFallback` replaces the chain with the families you list, in order, each one's best fit for the weight and style first:

   ```go
   aster.WithFontFallback("Noto Color Emoji", "Noto Sans CJK", "Noto Emoji")  // only these, in this order
   aster.WithFontFallback()                                                  // no fallback at all
   ```

   A listed family can be registered, embedded, or a system family (with `WithSystemFonts`). Naming one here is the only way a system font becomes a fallback. A listed family with no font makes `New` fail.
4. Otherwise the character is drawn as `.notdef` (an empty box) from the first embedded font.

## Emoji and colour fonts

Monochrome Noto Emoji is the fallback for emoji, so they always measure and draw, in the text's colour. No colour font is bundled. Register one (such as Noto Color Emoji) with `WithFont`, under any family name, and it is tried before the bundled fonts for characters the spec's fonts lack, and draws in colour:

- **Formats:** COLRv0 and COLRv1 (gradients, transforms and every composite mode), sbix and CBDT bitmaps (the strike for the size drawn), EBDT masks, and OpenType SVG glyphs (drawn by the rasterizer's own SVG renderer, `context-fill` being the text's colour).
- **Measurement** then uses that font's advances, as a browser using it would.
- **Variable colour fonts** are drawn at their default instance, or at the one `WithFontInstance` names.
- **In PDF**, a colour glyph is drawn as vectors where PDF can express it: composites as transparency groups, gradients as shadings. Only the Xor and Plus composite operators fall back to an image of the glyph, at 8 pixels per point of its size (256 to 1024 per em). Either way the text is kept, so it can be searched and copied.
- **SVG output** leaves emoji as text, for the viewer to draw.

## System fonts

`WithSystemFonts` indexes the machine's fonts (each face of a collection, too) and maps files into memory rather than reading them, so Apple Color Emoji (190 MB) costs only what is drawn from it. In PDF, emoji from such a font are also written as invisible text, in a subset cut from its outline tables alone, so they stay searchable and add about 100 KB, not the font's bitmaps.

A system font is used **only when a spec names its family** (`"font": "Apple Color Emoji"`), or when you list it in `WithFontFallback`; never as a fallback by default. This is for licences: some system fonts may not be drawn into documents you distribute (Apple Color Emoji's licence forbids it). It also keeps what a spec can make aster read to what the host enabled. Check a font's licence before distributing what it draws. A converter keeps at most 64 system faces loaded (about a megabyte of parsed tables each); past that the one loaded longest ago is dropped, and loaded again if a later spec names it. In PDF, a system font is embedded from its outline tables, cut out of the file, never from a copy of the whole file.

## PDF text

How text is written into a PDF (embedded subset fonts, fonts by name, or outlines) is chosen with `WithPDFText`; see [pdf.md](pdf.md).

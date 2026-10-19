# PDF output

`VegaLiteToPDF`, `VegaToPDF` and `SVGToPDF` produce a single-page, fully vector PDF, made for LaTeX `\includegraphics` and document pipelines. By default the fonts a chart uses are subset and embedded, so the file is self-contained and its text is selectable and searchable.

```go
pdf, err := c.VegaLiteToPDF(spec)                                         // embedded subset fonts
pdf, err := c.VegaLiteToPDF(spec, aster.WithPDFText(aster.PDFTextNamed))  // fonts by name only
```

CLI: `aster pdf -i chart.vl.json -o chart.pdf -text embed|named|outlines`.

## Text modes

| Mode | Text is | Size (bar chart) | Use when |
|---|---|---|---|
| `PDFTextEmbed` (default) | real PDF text in a TrueType subset holding only the glyphs used, each occurrence two bytes | smallest self-contained | almost always |
| `PDFTextNamed` | the same text, with fonts referenced by name only | smallest | many charts go into one document that embeds each font once (below) |
| `PDFTextOutlines` | filled paths, one per glyph occurrence | largest; text not selectable | nothing downstream may handle fonts |

In `PDFTextEmbed` a CFF (OpenType `OTTO`) font is embedded as a subset of its CFF program (`FontFile3`, `CIDFontType0C`; a CID-keyed one addressed by its CIDs), a TrueType one as a subset of its outlines. Text whose font program cannot be subset (glyph data that is malformed, for one glyph or for the font) is drawn as outlines, and written over them as invisible text in the font by name, so that it is still selected, searched and copied; the rest of the font is embedded. Text in a face no font program can be had of (a system font whose outlines are over 64 MiB) is drawn as outlines, its text written as `ActualText`, which some readers (poppler, PDFium) do not read around paths.

The file says PDF 1.4, or 1.5 when it holds `ActualText` (a glyph that stands for two texts, such as a precomposed and a combining accent, or the case above).

**`PDFTextNamed` is not standalone.** Glyphs are addressed by the glyph IDs of the exact font file used when rendering, so whatever assembles the final document must embed that same file. A viewer that substitutes another font may draw the wrong glyphs.

A CFF (OpenType `OTTO`) font is declared a `CIDFontType0`, a TrueType one a `CIDFontType2`; both use `Identity-H`, and the content's codes are always the font's **glyph IDs**. For a CID-keyed CFF font (most CJK OpenType fonts), whose CIDs are not its glyph IDs, the assembler must therefore embed it so that CID = GID: as the glyph-indexed program, not as the CID-keyed original with its own charset.

## Sharing fonts across many charts

The payoff of `PDFTextNamed`: render each chart with `VegaLiteToPDFUsage` (or `VegaToPDFUsage`, `SVGToPDFUsage`), which also returns a `[]FontUsage` per chart. Each one has `PostScriptName`, `Source` (the font file's bytes) and `GIDs` (the glyphs used).

1. Union the glyph IDs per font across all charts.
2. Build one subset per font with `aster.SubsetFont(source, gids)`. It keeps the source's glyph numbering, so the charts' Identity-encoded references resolve against it unchanged, and returns the PostScript name the charts reference.
3. Embed that one subset in the composed document.

Each font's glyphs are then stored once, however many charts use them. `SubsetFont` needs TrueType outlines (CFF fonts return an error) and ignores glyph IDs outside the font.

## What is supported

Only the SVG subset that Vega's SVG renderer writes: paths, text, images (fetched through the [Loader](loading-data.md), as PNG output fetches them), gradients (translucent stops included, under a soft mask of their alpha, and the `<pattern>` Vega wraps a radial gradient in), clipping, `vector-effect="non-scaling-stroke"`, `mix-blend-mode` (drawn as a group under PDF's blend mode), text stroked as well as filled (stroke, width, joins, caps and dashes, after the fill, as Vega's text marks are drawn), opacity composited as SVG composites it (a translucent group, and a translucent element that both fills and strokes, are each a transparency group, so that nothing in it shows through the rest of it), `clip-rule` on a clip path's one shape, a clip path's own `transform`, and links: a mark Vega wraps in `<a>` for its `href` is drawn, and the page gets a Link annotation over it that opens the URL. Only an absolute `http:`, `https:` or `mailto:` URL becomes a link (the SVG may be anybody's: a `javascript:`, `data:`, `file:` or relative one is drawn without a link), its bytes outside printable ASCII percent-encoded. `SVGToPDF` of other SVG fails with a descriptive error for anything outside that (an element, attribute or style property it does not draw, other patterns, a document type declaration's entities, `<use>`, filters: a `<filter>` element or a `filter` attribute, a `vector-effect` other than `non-scaling-stroke`, a gradient on text or on a non-scaling stroke, `clipPathUnits="objectBoundingBox"`, a clip path of several shapes with `clip-rule` evenodd, a `transform` on the `<svg>` element, …) rather than producing an incomplete page. Fall back to `SVGToPNG` for such input.

A presentation value that does not parse (a colour `notacolor`, an opacity `half`, a `NaN` stroke width, a transform that is not one) is ignored, as SVG says: the element is drawn as it is without it. Path data with an error is drawn up to the error. `font-variant` is drawn as normal, as in the PNG. Text is drawn after SVG's whitespace processing (newlines dropped, a tab a space, spaces collapsed), and text at a font size of zero is not drawn. A font is read from the element's `font-family`, `font-size`, `font-weight` and `font-style`; a numeric weight such as 450 is a weight.

The PDFs are checked by reading them back (with pdf0, and with PDFium, Chrome's PDF engine, against the PNG output); see [testing.md](testing.md).

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

Text whose font cannot be embedded (CFF outlines, system-font instances that cannot be recovered) falls back to outlines automatically in `PDFTextEmbed`.

**`PDFTextNamed` is not standalone.** Glyphs are addressed by the glyph IDs of the exact font file used when rendering, so whatever assembles the final document must embed that same file. A viewer that substitutes another font may draw the wrong glyphs.

## Sharing fonts across many charts

The payoff of `PDFTextNamed`: render each chart with `VegaLiteToPDFUsage` (or `VegaToPDFUsage`, `SVGToPDFUsage`), which also returns a `[]FontUsage` per chart. Each one has `PostScriptName`, `Source` (the font file's bytes) and `GIDs` (the glyphs used).

1. Union the glyph IDs per font across all charts.
2. Build one subset per font with `aster.SubsetFont(source, gids)`. It keeps the source's glyph numbering, so the charts' Identity-encoded references resolve against it unchanged, and returns the PostScript name the charts reference.
3. Embed that one subset in the composed document.

Each font's glyphs are then stored once, however many charts use them. `SubsetFont` needs TrueType outlines (CFF fonts return an error) and ignores glyph IDs outside the font.

## What is supported

Only the SVG subset that Vega's SVG renderer writes: paths, text, images (fetched through the [Loader](loading-data.md), as PNG output fetches them), gradients and clipping. `SVGToPDF` of other SVG fails with a descriptive error for anything outside that (embedded CSS, patterns, …) rather than producing an incomplete page. Fall back to `SVGToPNG` for such input.

The PDFs are checked by reading them back (with pdf0, and with PDFium, Chrome's PDF engine, against the PNG output); see [testing.md](testing.md).

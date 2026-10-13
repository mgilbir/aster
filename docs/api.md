# API reference

Every exported name of `github.com/mgilbir/aster`, grouped by task. The godoc ([pkg.go.dev](https://pkg.go.dev/github.com/mgilbir/aster)) has each in full; this page is the map.

## Converter

`aster.New(opts ...Option) (*Converter, error)` creates a converter. It is safe for concurrent use by any number of goroutines, and sharing one is cheaper than making one per goroutine (fonts load once, into one set every output uses). `Close()` cancels calls in flight (they fail with `context.Canceled`), waits for them, closes the Loader if it implements `io.Closer`, and makes every later call fail with `ErrClosed`. Closing twice is harmless, and so is closing from inside a call: from an error handler or a Loader method, `Close` cancels and returns at once, and the Loader is closed when the last call ends. A call started from a handler or a Loader (a nested render) is allowed too; after `Close` it fails with `ErrClosed`.

To cancel one call rather than the Converter, pass it `WithContext(ctx)`: every method takes it. The call ends when `ctx` ends, the Converter's `WithTimeout` runs out, or `Close` is called, whichever is first, and fails with `context.Canceled` or `context.DeadlineExceeded`; the Loader's methods get a context derived from `ctx`, with its values. A call whose context ends never returns output.

| Method | Input → output | Per-call options |
|---|---|---|
| `VegaLiteToSVG(spec)` | Vega-Lite JSON → SVG `string` | `RenderOption` |
| `VegaLiteToPNG(spec, …)` | Vega-Lite JSON → PNG `[]byte` | `PNGOption` |
| `VegaLiteToPDF(spec, …)` | Vega-Lite JSON → PDF `[]byte` | `PDFOption` |
| `VegaLiteToPDFUsage(spec, …)` | Vega-Lite JSON → PDF + `[]FontUsage` | `PDFOption` |
| `VegaLiteToVega(spec, …)` | Vega-Lite JSON → Vega JSON `[]byte` | `RenderOption`: `WithContext`, and `WithErrorHandler`, told of the compiler's warnings; `WithSignal` is ignored, nothing being rendered |
| `VegaToSVG` / `VegaToPNG` / `VegaToPDF` / `VegaToPDFUsage` | the same, from Vega JSON | as above |
| `SVGToPNG(svg, …)` / `SVGToPDF(svg, …)` / `SVGToPDFUsage(svg, …)` | an SVG string (best: one Vega wrote) → PNG / PDF | `PNGOption` / `PDFOption` |

The language of a spec is the method's, whatever its `$schema` says, and the `$schema`'s version does not choose the Vega-Lite compiler (`WithVegaLiteVersion` does).

### SVG input

`SVGToPNG` draws general SVG as [resvg](https://github.com/linebender/resvg) does, the reference its output is tested against. `SVGToPDF` draws only what Vega's SVG renderer writes ([pdf.md](pdf.md#what-is-supported)) and fails for anything else.

Filters (`<filter>` and the `filter` attribute) are drawn by `SVGToPNG` only. Vega never writes them, so they reach the engine only in SVG given to `SVGToPNG`; `SVGToPDF` fails for a `<filter>` element or a `filter` attribute ("svgpdf: unsupported SVG element <filter>").

- **Drawn:** every primitive of SVG 1.1 and `feDropShadow`: `feBlend`, `feColorMatrix`, `feComponentTransfer`, `feComposite`, `feConvolveMatrix` (`kernelUnitLength` is ignored, as by resvg), `feDiffuseLighting` and `feSpecularLighting` (distant, point and spot lights), `feDisplacementMap`, `feDropShadow`, `feFlood`, `feGaussianBlur`, `feImage`, `feMerge`, `feMorphology`, `feOffset`, `feTile` and `feTurbulence`.
- **`feImage`** draws an image or an element of the document. An image is a `data:` URI or an href fetched through the [Loader](loading-data.md#what-goes-through-the-loader), as an `<image>` is, with the same limits; one that cannot be read gives a transparent result and is reported as an `<image>` would be. An element (`href="#id"`) is drawn with the render's own bounds (elements, pixel work, layer depth); an element whose filter is the feImage's own is drawn with no filter, there and elsewhere (as resvg does), and an element whose filter is being applied further up (a longer cycle) is not drawn inside it. A chain of feImages drawing filtered elements is bounded by the layer depth ([limits.md](limits.md)).
- **Invalid parameters** are handled as resvg does: a primitive resvg replaces by a transparent result (an `feConvolveMatrix` whose `kernelMatrix` is not `orderX` × `orderY` numbers, whose target lies outside the kernel or whose divisor is zero; a lighting with no light source, or a `specularExponent` outside 1–128; an `feImage` with no href, or whose `#id` is no element) gives one here too.
- **resvg's own choices are kept**, since its output is what the PNG is tested against: `feDisplacementMap` moves a pixel by the scale squared (resvg multiplies by `scale` twice) and reads the map premultiplied; `feTile` gives an sRGB result whatever its input's space; `feTurbulence` samples a pixel at its corner, not its centre; a lighting's `lighting-color` is used as it is in the primitive's colour space (not converted to linearRGB), and its light's position ignores `primitiveUnits`; `feImage` draws an element at its own user coordinates measured from the corner of the primitive's sub-region, with the device scale but not the rest of the filtered element's transform, and an image fitted to the sub-region's size.
- **Ignored:** an element in a filter that is not a primitive (an unknown name), and CSS filter functions in the `filter` attribute (`blur(2px)`, `drop-shadow(…)`). An element whose `filter` has only functions is drawn unfiltered, where resvg applies them.

A filter's work and memory are bounded ([limits.md](limits.md)): its region's pixels, the pixel memory its results hold, and the pixel operations its passes make.

## Options for New

| Option | Default | |
|---|---|---|
| `WithLoader(l Loader)` | `DenyLoader{}` (also for `nil`) | what data and images may be loaded; [loading-data.md](loading-data.md). A nil pointer (`(*FileLoader)(nil)`) is an error |
| `WithTimeout(d)` | 30 s; `<= 0` none | the whole call; [limits.md](limits.md) |
| `WithMemoryLimit(bytes)` | 0: the fixed default budgets | replaces the memory budgets; [limits.md](limits.md) |
| `WithPixelWorkLimit(ops)` | 128 Mi | the rasterizer's work budget at scale 1, times the scale squared above it and at least 16 per output pixel; raise it for SVG with large filters; 0 is refused; [limits.md](limits.md) |
| `WithVegaLiteVersion(v)` | `"6.4"` | `"6.4"` or `"6.4.3"` (Vega-Lite 6.4.3), `"5.8"` or `"5.8.0"`; a leading `v` is accepted |
| `WithTheme(json string)` | none | a Vega config applied to every render, to the Vega-Lite compiler as well (so `VegaLiteToVega` reflects it); must be a JSON object |
| `WithTimezone(tz)` | `"UTC"` | local time for time scales, `timeFormat`, dates without a zone; any IANA name Go knows. **`"Local"` is refused**: it would make the output depend on the host |
| `WithFont`, `WithFontFace`, `WithFontInstance`, `WithFontPalette`, `WithFontFallback`, `WithDefaultFontFamily`, `WithDefaultSerifFamily`, `WithDefaultMonospaceFamily`, `WithSystemFonts`, `WithTextMeasurement`, `WithHarfBuzzTextMetrics` | see [fonts.md](fonts.md) | |

### Errors from New

`New` fails for an option that is not valid, rather than silently replacing it by a default: an unknown Vega-Lite version (the message lists the available ones), an unknown time zone or `"Local"`, a theme that is not a JSON object, a nil pointer as the Loader, a negative `WithFontFace` index, a font given with `WithFont`, `WithFontFace` or `WithFontInstance` that does not load, and a `WithFontFallback`, `WithDefaultFontFamily`, `WithDefaultSerifFamily` or `WithDefaultMonospaceFamily` family with no font. It loads the fonts when any font option is given (a copy of the bytes: the caller may reuse its buffer); with the embedded fonts alone it loads nothing, and they load on first use.

`AvailableVersions() ([]VersionInfo, error)` lists the bundled compilers (`Key` such as `"vl6_4"`, `VegaVersion`, `VegaLiteVersion` such as `"6.4.3"`); `WithVegaLiteVersion` takes a `VegaLiteVersion` or its `major.minor`. Its error is always nil, and is kept so that existing callers compile.

## Per-call options

| Option | Type | Default | |
|---|---|---|---|
| `WithContext(ctx)` | `ContextOption` (render, PNG, PDF) | none | run the call under `ctx` as well: cancel it, give it a deadline, pass values to the Loader; see above |
| `WithSignal(name, value)` | `SignalOption` (render, PNG, PDF) | none | set a top-level signal (a Vega-Lite parameter) before drawing, as `view.signal(name, value)`; `value` is anything `encoding/json` encodes; an undefined name fails; ignored by `SVGTo…` and `VegaLiteToVega` |
| `WithErrorHandler(h)` | `ErrorHandlerOption` (render, PNG, PDF) | none: errors fail the call | see [errors.md](errors.md) |
| `WithScale(f)` | `PNGOption` | 1 | PNG size factor; must be positive and finite |
| `WithRecodePNG()` | `PNGOption` | off | losslessly re-encode into the cheapest equivalent PNG (indexed or truecolor); same pixels, typically several times smaller |
| `WithQuantizePNG(maxColors)` | `PNGOption` | off | lossily quantize to at most `maxColors` colours (clamped to 2–256), 8-bit indexed with dithering and a quality guard; falls back to `WithRecodePNG` when the guard trips, and without trying for an image of more than 65,536 distinct colours; wins over `WithRecodePNG` |
| `WithPDFText(mode PDFTextMode)` | `PDFOption` | `PDFTextEmbed` | `PDFTextEmbed`, `PDFTextNamed`, `PDFTextOutlines`; [pdf.md](pdf.md) |

## Errors and diagnostics

| Name | |
|---|---|
| `ErrLimit` | wrapped by every error for an exceeded resource bound |
| `ErrClosed` | returned by every call after `Close` |
| `*LoadError` | `Resource` (`"data"` or `"image"`), `URL`, `Err`: a resource that could not be loaded or read |
| `Warning` | a Vega warning or a dropped link, seen only through `WithErrorHandler` |
| `context.DeadlineExceeded`, `context.Canceled` | wrapped by the error of a call that ran out of time (`WithTimeout`, or the deadline of a `WithContext` context) or was canceled (its context, or `Close`) |

`HTTPLoader`'s refusal of a private address has no exported sentinel: it is the `Err` of a `*LoadError`.

See [errors.md](errors.md).

## Loaders

`Loader` (interface: `Sanitize`, `Load`), `DenyLoader`, `HTTPLoader` / `NewHTTPLoader`, `FileLoader` / `NewFileLoader`, `StaticLoader` (its methods take a pointer: `&aster.StaticLoader{Value: v}`), `FallbackLoader` / `NewFallbackLoader`. See [loading-data.md](loading-data.md) for their fields and for writing your own.

## PDF fonts

`FontUsage` (`PostScriptName`, `Source`, `GIDs`) and `SubsetFont(source, gids) (subset, postScriptName, error)`; see [pdf.md](pdf.md#sharing-fonts-across-many-charts).

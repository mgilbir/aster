# API reference

Every exported name of `github.com/mgilbir/aster`, grouped by task. The godoc ([pkg.go.dev](https://pkg.go.dev/github.com/mgilbir/aster)) has each in full; this page is the map.

## Converter

`aster.New(opts ...Option) (*Converter, error)` creates a converter. It is safe for concurrent use by any number of goroutines, and sharing one is cheaper than making one per goroutine (fonts load once). `Close()` cancels calls in flight (they fail with `context.Canceled`), waits for them, closes the Loader if it implements `io.Closer`, and makes every later call fail with `ErrClosed`. Closing twice is harmless.

| Method | Input → output | Per-call options |
|---|---|---|
| `VegaLiteToSVG(spec)` | Vega-Lite JSON → SVG `string` | `RenderOption` |
| `VegaLiteToPNG(spec, …)` | Vega-Lite JSON → PNG `[]byte` | `PNGOption` |
| `VegaLiteToPDF(spec, …)` | Vega-Lite JSON → PDF `[]byte` | `PDFOption` |
| `VegaLiteToPDFUsage(spec, …)` | Vega-Lite JSON → PDF + `[]FontUsage` | `PDFOption` |
| `VegaLiteToVega(spec)` | Vega-Lite JSON → Vega JSON `[]byte` | none |
| `VegaToSVG` / `VegaToPNG` / `VegaToPDF` / `VegaToPDFUsage` | the same, from Vega JSON | as above |
| `SVGToPNG(svg, …)` / `SVGToPDF(svg, …)` / `SVGToPDFUsage(svg, …)` | an SVG string (best: one Vega wrote) → PNG / PDF | `PNGOption` / `PDFOption` |

The language of a spec is the method's, whatever its `$schema` says, and the `$schema`'s version does not choose the Vega-Lite compiler (`WithVegaLiteVersion` does).

## Options for New

| Option | Default | |
|---|---|---|
| `WithLoader(l Loader)` | `DenyLoader{}` (also for `nil`) | what data and images may be loaded; [loading-data.md](loading-data.md) |
| `WithTimeout(d)` | 30 s; `<= 0` none | the whole call; [limits.md](limits.md) |
| `WithMemoryLimit(bytes)` | 0: the fixed default budgets | replaces the memory budgets; [limits.md](limits.md) |
| `WithVegaLiteVersion(v)` | `"6.4"` | `"6.4"` (Vega-Lite 6.4.3) or `"5.8"` (5.8.0); a leading `v` is accepted |
| `WithTheme(json string)` | none | a Vega config applied to every render, to the Vega-Lite compiler as well (so `VegaLiteToVega` reflects it); must be a JSON object |
| `WithTimezone(tz)` | `"UTC"` | local time for time scales, `timeFormat`, dates without a zone; any IANA name Go knows |
| `WithFont`, `WithFontFace`, `WithFontInstance`, `WithFontPalette`, `WithFontFallback`, `WithDefaultFontFamily`, `WithDefaultSerifFamily`, `WithDefaultMonospaceFamily`, `WithSystemFonts`, `WithTextMeasurement`, `WithHarfBuzzTextMetrics` | see [fonts.md](fonts.md) | |

### Errors from New

`New` fails for an unknown Vega-Lite version (the message lists the available ones), an unknown time zone, a theme that is not a JSON object, a font given with `WithFont`, `WithFontFace` or `WithFontInstance` that does not load, and a `WithFontFallback` family with no font. It loads the fonts you give it; with the embedded fonts alone it loads nothing, and they load on first use.

`AvailableVersions() ([]VersionInfo, error)` lists the bundled compilers (`Key` such as `"vl6_4"`, `VegaVersion`, `VegaLiteVersion` such as `"6.4.3"`); pass the `major.minor` of `VegaLiteVersion` to `WithVegaLiteVersion`. Its error is always nil.

## Per-call options

| Option | Type | Default | |
|---|---|---|---|
| `WithSignal(name, value)` | `SignalOption` (render, PNG, PDF) | none | set a top-level signal (a Vega-Lite parameter) before drawing, as `view.signal(name, value)`; `value` is anything `encoding/json` encodes; an undefined name fails; ignored by `SVGTo…` |
| `WithErrorHandler(h)` | `ErrorHandlerOption` (render, PNG, PDF) | none: errors fail the call | see [errors.md](errors.md) |
| `WithScale(f)` | `PNGOption` | 1 | PNG size factor; must be positive and finite |
| `WithRecodePNG()` | `PNGOption` | off | losslessly re-encode into the cheapest equivalent PNG (indexed or truecolor); same pixels, typically several times smaller |
| `WithQuantizePNG(maxColors)` | `PNGOption` | off | lossily quantize to at most `maxColors` (2–256) colours, 8-bit indexed with dithering and a quality guard; falls back to `WithRecodePNG` when the guard trips; wins over `WithRecodePNG` |
| `WithPDFText(mode)` | `PDFOption` | `PDFTextEmbed` | `PDFTextEmbed`, `PDFTextNamed`, `PDFTextOutlines`; [pdf.md](pdf.md) |

## Errors and diagnostics

| Name | |
|---|---|
| `ErrLimit` | wrapped by every error for an exceeded resource bound |
| `ErrClosed` | returned by every call after `Close` |
| `*LoadError` | `Resource` (`"data"` or `"image"`), `URL`, `Err`: a resource that could not be loaded or read |
| `Warning` | a Vega warning or a dropped link, seen only through `WithErrorHandler` |

See [errors.md](errors.md).

## Loaders

`Loader` (interface: `Sanitize`, `Load`), `DenyLoader`, `HTTPLoader` / `NewHTTPLoader`, `FileLoader` / `NewFileLoader`, `StaticLoader`, `FallbackLoader` / `NewFallbackLoader`. See [loading-data.md](loading-data.md) for their fields and for writing your own.

## PDF fonts

`FontUsage` (`PostScriptName`, `Source`, `GIDs`) and `SubsetFont(source, gids) (subset, postScriptName, error)`; see [pdf.md](pdf.md#sharing-fonts-across-many-charts).

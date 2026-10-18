# Troubleshooting

Find the symptom, then the likely cause and fix.

| Symptom | Likely cause | Fix |
|---|---|---|
| `data "…" could not be loaded: … resource loading denied … (no loader configured)` | the default Loader refuses every URL | `WithLoader(aster.NewHTTPLoader(nil))`, `&aster.FileLoader{BaseDir: …}`; CLI `-allow-http` / `-allow-domain host`, `-allow-file DIR`. See [loading-data.md](loading-data.md) |
| `… is a private/loopback address (blocked)`, or `… the host is a private or loopback address (blocked …)` | `HTTPLoader` refuses non-public addresses (SSRF protection): the first for an address literal or a `localhost` name in the URL, the second for a name that resolved to one | for a data service you run: `AllowPrivateNetworks: true` (CLI `-allow-private-networks`) |
| `relative URI "data/cars.json" not allowed (no BaseURL configured)` | the spec uses a relative URL (as the Vega-Lite examples do) | `HTTPLoader{BaseURL: "https://cdn.jsdelivr.net/npm/vega-datasets@3.2.1/"}` (the base the spec's `data/…` resolves against), or a `FileLoader` over a local copy; CLI `-base-url URL`, or `-allow-file DIR` |
| `image "…" could not be loaded` | as for data; or the bytes are not a PNG, JPEG or GIF | allow the URL, or convert the image |
| a chart renders but some data is missing, and you used `WithErrorHandler` returning nil | the handler let a failed load go | log what the handler receives; see [errors.md](errors.md) |
| `TypeError: …`, `ReferenceError: …`, `RangeError: …` | an expression or transform in the spec throws (upstream would log it and draw the rest) | fix the spec; or render past it with `WithErrorHandler` |
| CLI: `cannot tell whether the input is Vega or Vega-Lite` | no `$schema`, and the top-level properties fit neither or both | add `$schema`, or pass `-from vega` / `-from vega-lite`. See [cli.md](cli.md) |
| CLI: `unexpected argument "chart.vl.json"` | the input was given as an argument | `-i chart.vl.json` (and `-o` for the output). See [cli.md](cli.md) |
| CLI: `no input: give -i FILE, or -i - …` | no `-i`, and standard input is a terminal | `-i FILE`, or pipe the spec in |
| CLI: empty 0×0 SVG from an aster that predates the CLI's language detection ([ADR 0008](adr/0008-cli-language-detection.md)) | a Vega-Lite spec without `$schema` was rendered as Vega | upgrade, or add `$schema` |
| error wraps `aster.ErrLimit` (`… exceeds the limit of …`) | the spec asks for more rows, items, bytes… than allowed | raise `WithMemoryLimit` for trusted specs; see [limits.md](limits.md) |
| `… timed out after 30s` (wraps `context.DeadlineExceeded`) | a heavy chart, usually a detailed or faceted map | `WithTimeout(2*time.Minute)`; CLI `-timeout 2m` (`0` disables) |
| `… ran past the deadline of the call's context` | the deadline of the context given with `WithContext` passed | give the request more time, or render ahead of it |
| `aster: converter is closed` (`ErrClosed`), or `context.Canceled` | `Close` was called before or during the call, or the call's `WithContext` context was canceled | keep one Converter for the process's lifetime; cancel one call with `WithContext` |
| `New` fails: `… loading font "X": … not an sfnt font program` | a font given to `WithFont` does not parse | check the file (TrueType, OpenType or WOFF) |
| `New` fails: `fallback family "X" has no font`, or `WithDefaultFontFamily: family "X" has no font` | `WithFontFallback` or `WithDefault*Family` names a family nothing provides (a typo, or a font not registered) | register it, or `WithSystemFonts()` for an installed one |
| my font is not used | the spec asks for another family (Vega's default is `sans-serif`), or another face of the family fits the weight or style better | set `WithDefaultFontFamily("My Font")` (CLI `-default-font "My Font"`; it fails if no font has that family), or register the font under the name the spec uses, which replaces any embedded font of that name. See [fonts.md](fonts.md#which-font-draws-a-character) |
| the wrong font draws emoji or other scripts | the default fallback chain tries your registered fonts first, newest first | `WithFontFallback("Noto Color Emoji", …)` to choose it |
| a system font is not used | system fonts are used only when `WithSystemFonts()` is set **and** the spec names the family (or `WithFontFallback` lists it) | both; they are never a fallback by default |
| emoji are black and white | no colour font is bundled | `WithFont("Noto Color Emoji", data)` |
| text width or layout differs slightly from a browser | text is shaped by forme; browsers use other engines | differences are sub-pixel; to match earlier aster output byte for byte, `WithHarfBuzzTextMetrics()` |
| dates are off by hours | renders are in UTC by default | `WithTimezone("Europe/Madrid")`; CLI `-timezone Europe/Madrid` |
| `unsupported timezone "Local"` | the host's zone would make output depend on the machine | name the zone: `WithTimezone("Europe/Madrid")` |
| `unknown Vega-Lite version "6.4.0"` | only the bundled versions compile | `"6.4"` (or `"6.4.3"`) or `"5.8"` (or `"5.8.0"`); `AvailableVersions` lists them |
| `svgpdf: unsupported …` from `SVGToPDF` | the SVG uses constructs Vega does not write | render that SVG with `SVGToPNG`, or render the spec with `VegaToPDF` |
| an interactive chart shows its initial state | there is no event loop | set the state with `WithSignal(name, value)` (CLI `-signal`) |

Still stuck: report an issue at <https://github.com/mgilbir/aster/issues> with the spec, the exact error, and the aster version: `aster version` for the command, `go list -m github.com/mgilbir/aster` in the module of a program that uses the library.

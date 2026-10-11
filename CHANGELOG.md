# Changelog

What changed between versions, for users of the library and the CLI. Breaking changes are marked **breaking**.

## Unreleased

### Security

- SVG output escapes a mark's `x`, `y` (and with them the `transform` attribute) when they are strings rather than numbers. Before, such a string, from the spec or from **data** (a Vega `{"field": …}`, a Vega-Lite channel with `"scale": null`), was written into `transform="translate(…)"` unescaped, so a data value could add markup, such as a `<script>` element, to SVG that a page inlines; PNG and PDF output, made from that SVG, could carry it too. The output is now upstream Vega's, escaped. Charts whose positions are numbers are unchanged. The SVG writer now accepts raw markup only as constants, and tests check that its output parses back to exactly the elements and attributes it wrote.

### Fixed

- `defined`, `aspect`, `smooth` and `aria` (of an item, and `aria` of a mark) treat only `false` as false, as upstream does: a `defined` of `0`, `""` or `NaN` no longer breaks a line or area, an `aspect` or `smooth` of `0` no longer changes an image, and an `aria` of `0` or `""` no longer hides a mark. Each reads back in expressions as the value given.
- **breaking:** a `monotone` interpolation whose `orient` is neither `horizontal` nor `vertical` fails the render, as upstream's ("curve is not a function"), instead of drawing as `vertical`.
- A text whose `x` or `y` and `angle` are all strings had the angle escaped twice.
- An axis whose `orient` is a signal failed to parse its encoding when a config value was an array holding `null` (`tickDash: [4, null, 2]`) or a string holding U+2028 or U+2029; both are now spliced as upstream's `stringValue` does.
- JavaScript's 32-bit integer conversion (`~~x`, `x | 0`) of a huge number, in text width estimates and a crossfilter's `ignore` mask, wrapped as on no JavaScript engine and differed between amd64 and arm64.
- TopoJSON positions keep their own length, as topojson-client keeps them: a position shorter or longer than its arc's first is no longer padded with `NaN` or cut to the first's length, and a transformed position of fewer than two numbers is padded to two. An arc whose second position has a `NaN` coordinate counts as empty when a mesh is stitched, as upstream.
- A graticule's point count is `d3.range`'s, a 32-bit conversion: a step so small that the count passes 2^31 is the `RangeError` upstream throws, or wraps as upstream does, instead of an `ErrLimit`.

### Bounds are charged where the work happens ([ADR 0010](docs/adr/0010-bounds-charged-where-work-happens.md))

- Each call has a ledger that every stage charges, before allocating, for the memory the input makes it allocate, and that the loops the input controls charge for their work, polling the call's timeout. `WithMemoryLimit(n)` bounds it at `n`; without it, at 2 GiB ([docs/limits.md](docs/limits.md)). A render past it fails with `ErrLimit`.
- Caches shared between calls are bounded by the bytes they hold, not by their number of entries: compiled expressions and regular expressions, custom symbol paths, and per converter the measured widths, CSS fonts, font fallback lists and glyph outlines. A 419-byte specification could keep 900 MB of compiled regular expressions alive after its render; a hostile CJK label under many font families, hundreds of MB.
- The rasterizer's cache of pattern tiles and of decoded colour glyph bitmaps is charged to the call's ledger, so `WithMemoryLimit` bounds it (it was a fixed 256 MiB).
- Parsed path data (custom symbol shapes and `path` marks) is charged to the call's ledger; a path string parses to sixteen times its size.
- `SVGToPDF` and the PDF methods report a limit reached inside a stage that cannot return an error as `ErrLimit`, as the other outputs do, rather than as an internal error.
- New: [ADR 0011](docs/adr/0011-faithful-output-free-algorithm.md), "faithful in output, free in algorithm".
- Vega-Lite compilation honours the timeout in every phase, from copying the input through normalization, parsing, the dataflow optimizer and assembly: each loop over views, selections, params, transforms, encodings, dataflow nodes and the values they hold charges the call's ledger. A spec with 100,000 point selections, which compiled for eight and a half minutes and returned up to six seconds past a 1 s timeout, now compiles in about 2.5 s and stops within milliseconds of the deadline.
- Vega-Lite compilation is no longer quadratic in a spec's views and selections where upstream's algorithm is (output unchanged, ADR 0011): the dataflow optimizer's child removals and insertions, its deepest-first node order, the ordering of root data sets, and the brush test every point selection repeats.
- Geographic work is bounded where it happens. The path-point budget counts the points a projection produces, after resampling and before its clip extent, so a tiny `precision` with a clip extent that drops everything fails with `ErrLimit` instead of running for seconds and allocating gigabytes; the measuring walk of a projection's `fit`, and `geoArea`, `geoBounds` and `geoCentroid` in expressions (with or without a projection), are bounded and stop at the timeout like a mark's paths. Decoding TopoJSON charges what it makes to the ledger first: one long arc referenced thousands of times no longer allocates gigabytes. Graticules charge their points. The clip circle's walk is bounded by construction, and polygon clipping rejoins its pieces in linear time (it was quadratic in the pieces). Charts render as before.
- **breaking:** a projection `clipAngle` whose magnitude reaches 2^50 radians (about 6.4e16 degrees), infinity included, fails the render with `ErrLimit`. Upstream never finishes drawing a sphere or a clipped polygon with it; with 1e18 the render looped for ever and `Close` blocked behind it.

### Errors reach the caller ([ADR 0006](docs/adr/0006-errors-reach-the-caller.md))

- **breaking:** a render fails when upstream Vega would log a problem and draw without it. This covers a data URL the Loader refuses or fails to load (including every URL under the default `DenyLoader`), loaded data that does not parse, an image that cannot be loaded or decoded, and an exception an expression or operator raises during evaluation. Before, these rendered a chart missing the data or the image, with a nil error. Data and image failures are an `*aster.LoadError` (`Resource`, `URL`, `Err`).
- New per-call option `WithErrorHandler(func(error) error)`: told of each such error and of each `aster.Warning`, it decides whether the call goes on. To keep the previous output, pass `aster.WithErrorHandler(func(error) error { return nil })`. See [docs/errors.md](docs/errors.md).
- New `aster.ErrClosed`, returned by every call after `Close` (the error was not matchable before).
- Fixed: embedded `data:` images drew nothing with the default loader, because they were sent through it and refused. Embedded PNG, JPEG and GIF images (`data:image/png`, `…/jpeg`, `…/gif`) are part of the specification and now draw with any loader; other `data:` URLs are still the Loader's to allow.

### Fonts are the caller's to choose ([ADR 0009](docs/adr/0009-caller-chooses-fonts.md))

- **breaking:** a family you register faces for (`WithFont`, `WithFontFace`, `WithFontInstance`) is made of your faces alone, as a CSS `@font-face` family is. Registering "Liberation Sans" or "Arial" now replaces the embedded font of that name; before, the embedded face won. Of faces of equal fit, the last registered now wins; before, the first did. Both are what the documentation said.
- **breaking:** `New` loads the fonts given with `WithFont`, `WithFontFace` and `WithFontInstance` and fails for one that does not load. Before, the first render that needed text failed, and every render after it.
- New `WithFontFallback(families ...string)`: the families that draw, in order, the characters a spec's fonts lack; with none, no fallback. It is also the way to make a system font a fallback.

### CLI

- **breaking:** a Vega-Lite spec without `$schema` is rendered as Vega-Lite. It used to be rendered as Vega, which drew an empty 0×0 chart with exit status 0. Input is now detected by `$schema`, else by its top-level properties, and an ambiguous input is an error. New `-from vega|vega-lite|auto` ([ADR 0008](docs/adr/0008-cli-language-detection.md)).
- **breaking:** failed loads and evaluation errors fail the command (exit status 1), with a hint when the cause is that no `-allow-http`/`-allow-domain` was given. New `-lenient` renders past them, printing each. Warnings are printed to stderr.
- `compile` refuses a spec it detects as Vega.

### Documentation

The README was split into task-oriented guides under [docs/](docs/README.md), and architecture decision records were added. Tests now keep the documentation true: the limits table, the package layers, every Mermaid diagram (rendered with Mermaid's renderer) and every relative link. The documented memory-limit costs, font priority and Loader contract, which did not match the code, were corrected.

## v0.1.1 (2026-10-10)

- Depends on pdf0 v0.5.0, golang.org/x/text v0.43.0 and golang.org/x/image v0.47.0.

## v0.1.0 (2026-10-10)

The first tagged release, with the pure-Go engine ([ADR 0001](docs/adr/0001-pure-go-engine.md)): Vega 6.4.0 runtime, Vega-Lite 6.4.3 and 5.8.0 compilers, SVG, PNG and vector PDF output, and the CLI. Untagged versions from before 2026-10-01 ran Vega in QuickJS on WebAssembly. Users of untagged versions may notice:

- `HTTPLoader` blocks private networks by default; `BlockPrivateNetworks` is ignored (use `AllowPrivateNetworks` to opt out).
- Text is measured with unrounded advances, as browsers do; `WithHarfBuzzTextMetrics()` restores the previous rounding ([ADR 0003](docs/adr/0003-unrounded-advances.md)).
- Renders are bounded by `WithTimeout` and `WithMemoryLimit` and fail with `ErrLimit` past a bound ([docs/limits.md](docs/limits.md)).

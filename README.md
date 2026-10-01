# aster

Go library and CLI for rendering [Vega](https://vega.github.io/vega/) and [Vega-Lite](https://vega.github.io/vega-lite/) visualization specs to SVG, PNG and vector PDF. Pure Go: no JavaScript runtime, no WebAssembly, no CGO.

Aster is a Go implementation of the Vega runtime and the Vega-Lite compiler, following Vega 6.4.0 and Vega-Lite 6.4.3 (and compiling Vega-Lite 5.8.0), with text measured by [forme](https://github.com/mgilbir/forme), its own SVG rasterizer for PNG and its own PDF writer. Its output is checked against upstream Vega running in node (see [Testing](#testing)). Everything runs in-process with no external dependencies.

## Features

- Vega-Lite to SVG, PNG, vector PDF, or compiled Vega JSON
- Vega to SVG, PNG, or vector PDF
- Arbitrary SVG to PNG or vector PDF conversion
- PDF output is fully vector with subset-embedded fonts (selectable text) — ideal for LaTeX `\includegraphics`
- Accurate text shaping with [forme](https://github.com/mgilbir/forme), measured the way browsers measure (unrounded advances), with embedded Liberation fonts and monochrome Noto Emoji
- Configurable scale factor for high-DPI PNG output
- Multiple Vega-Lite versions (5.8, 6.4)
- Custom fonts, themes, data loaders, memory limits, timeouts, and any IANA timezone
- A `Converter` is safe for concurrent use; renders take milliseconds (see [Performance](#performance))
- Works on any platform Go supports — no native libraries needed

## Install

```
go get github.com/mgilbir/aster
```

Requires Go 1.26+.

## Quick start

### Library

```go
package main

import (
    "log"
    "os"

    "github.com/mgilbir/aster"
)

func main() {
    spec := []byte(`{
        "$schema": "https://vega.github.io/schema/vega-lite/v5.json",
        "data": {"values": [{"a": "A", "b": 28}, {"a": "B", "b": 55}]},
        "mark": "bar",
        "encoding": {
            "x": {"field": "a", "type": "nominal"},
            "y": {"field": "b", "type": "quantitative"}
        }
    }`)

    c, err := aster.New()
    if err != nil {
        log.Fatal(err)
    }
    defer c.Close()

    // Render to SVG.
    svg, err := c.VegaLiteToSVG(spec)
    if err != nil {
        log.Fatal(err)
    }
    os.WriteFile("chart.svg", []byte(svg), 0644)

    // Render to PNG at 2x scale.
    png, err := c.VegaLiteToPNG(spec, aster.WithScale(2.0))
    if err != nil {
        log.Fatal(err)
    }
    os.WriteFile("chart.png", png, 0644)
}
```

### CLI

```bash
# Install the CLI
go install github.com/mgilbir/aster/cmd/aster@latest

# Render a spec to SVG
aster svg -i chart.vl.json -o chart.svg

# Render a spec to PNG at 2x scale
aster png -i chart.vl.json -o chart.png -scale 2

# Render a spec to vector PDF (subset-embedded fonts, selectable text)
aster pdf -i chart.vl.json -o chart.pdf

# ...with fonts referenced by name only, for later assembly-time embedding
aster pdf -i chart.vl.json -o chart.pdf -text named

# Pipe from stdin to stdout
cat chart.vl.json | aster svg > chart.svg

# Compile Vega-Lite to Vega JSON
aster compile -i chart.vl.json -o chart.vg.json

# Pick a Vega-Lite version and a render timeout
aster svg -i chart.vl.json -version 5.8 -timeout 60s

# Allow specs that load data over HTTP
aster svg -i chart.vl.json -o chart.svg -allow-http

# ...restricted to specific hosts
aster svg -i chart.vl.json -allow-domain cdn.jsdelivr.net
```

The CLI auto-detects Vega vs Vega-Lite from the `$schema` field. If absent, Vega-Lite is assumed.

Shared flags: `-i`/`-o` (input/output, stdin/stdout when omitted), `-version`, `-timeout`, `-allow-http`, and `-allow-domain` (repeatable; implies `-allow-http`). `png` also accepts `-scale` and `-recode`; these don't apply to `pdf` since it's vector output. `pdf` accepts `-text embed|named|outlines` to pick the [PDF text mode](#options) (default `embed`).

## API

### Converter

All rendering goes through a `Converter`, created with `aster.New()`. A converter is not safe for concurrent use — create one per goroutine if needed.

```go
c, err := aster.New(
    aster.WithVegaLiteVersion("5.8"),
    aster.WithTimeout(60 * time.Second),
    aster.WithLoader(aster.NewHTTPLoader(nil)),
)
if err != nil {
    log.Fatal(err)
}
defer c.Close()
```

**Rendering methods:**

| Method | Input | Output |
|--------|-------|--------|
| `VegaLiteToSVG(spec)` | Vega-Lite JSON | SVG string |
| `VegaLiteToPNG(spec, ...PNGOption)` | Vega-Lite JSON | PNG bytes |
| `VegaLiteToPDF(spec, ...PDFOption)` | Vega-Lite JSON | PDF bytes |
| `VegaLiteToVega(spec)` | Vega-Lite JSON | Vega JSON |
| `VegaToSVG(spec)` | Vega JSON | SVG string |
| `VegaToPNG(spec, ...PNGOption)` | Vega JSON | PNG bytes |
| `VegaToPDF(spec, ...PDFOption)` | Vega JSON | PDF bytes |
| `SVGToPNG(svg, ...PNGOption)` | SVG string | PNG bytes |
| `SVGToPDF(svg, ...PDFOption)` | SVG string | PDF bytes |
| `VegaLiteToPDFUsage(spec, ...PDFOption)` | Vega-Lite JSON | PDF bytes + per-font glyph usage |
| `VegaToPDFUsage(spec, ...PDFOption)` | Vega JSON | PDF bytes + per-font glyph usage |
| `SVGToPDFUsage(svg, ...PDFOption)` | SVG string | PDF bytes + per-font glyph usage |

### Options

Options passed to `aster.New()`:

| Option | Default | Description |
|--------|---------|-------------|
| `WithVegaLiteVersion(v)` | `"6.4"` | Vega-Lite version (`"5.8"` or `"6.4"`) |
| `WithLoader(l)` | `DenyLoader{}` | Data loading strategy (see [Loaders](#loaders)) |
| `WithTimeout(d)` | 30s | Max duration per render |
| `WithMemoryLimit(bytes)` | 0 (defaults) | Budget for what one render may hold (loaded data, rows, scene items, SVG size, raster canvas); see below |
| `WithTextMeasurement(bool)` | `true` | forme text shaping for accurate layout |
| `WithHarfBuzzTextMetrics()` | disabled | Measure with HarfBuzz's rounding (whole-pixel size, 1/64 px advances) instead of exact advances, for output byte-stable with earlier versions |
| `WithFont(family, ttf)` | — | Register a custom TTF font (used by both measurement and PNG) |
| `WithDefaultFontFamily(name)` | `"Liberation Sans"` | Family that generic `sans-serif` resolves to (both pipelines) |
| `WithDefaultSerifFamily(name)` | `"Liberation Serif"` | Family that generic `serif` resolves to (both pipelines) |
| `WithDefaultMonospaceFamily(name)` | `"Liberation Mono"` | Family that generic `monospace` resolves to (both pipelines) |
| `WithSystemFonts()` | disabled | Also use system-installed fonts (both pipelines) |
| `WithTheme(json)` | — | Vega theme config applied to all renders |
| `WithTimezone(tz)` | `"UTC"` | Timezone for local-time operations (time scales, `timeFormat`, parsing dates without a zone); any IANA zone Go knows, others make `New` return an error |

`WithMemoryLimit` is not a heap cap: it scales the render's budgets (rows at 256 bytes each, scene items at 512, loaded bytes, the SVG at a quarter of the limit) and the rasterizer's canvas memory, and each budget is charged before the allocation it pays for. Without it the defaults apply: 1M rows, 500k scene items, 64 MiB of loaded data, 128 MiB of SVG. `WithTimeout` bounds one call across all of its stages.

**PNG options** passed per render:

| Option | Default | Description |
|--------|---------|-------------|
| `WithScale(f)` | `1.0` | Scale factor; 2.0 produces 2x dimensions |
| `WithRecodePNG()` | disabled | Losslessly re-encode into the cheapest equivalent PNG format (indexed/truecolor); same pixels, typically several-fold smaller |

**PDF options** passed per render:

| Option | Default | Description |
|--------|---------|-------------|
| `WithPDFText(mode)` | `PDFTextEmbed` | How text is represented; see below |

PDF text modes:

- **`PDFTextEmbed`** (default) — real PDF text with subset TrueType fonts embedded: only the glyphs a chart uses ship, once, and each occurrence costs two bytes. Self-contained, selectable, searchable. Text whose font cannot be embedded (CFF outlines, unrecoverable system-font instances) falls back to glyph outlines automatically.
- **`PDFTextNamed`** — the same text structure with fonts referenced by name only, for pipelines that generate many charts and embed the shared font once when assembling the final document. Glyphs are addressed by the IDs of the exact font file used at generation time, so the assembler must embed that same file; standalone viewers will substitute another font and may draw wrong glyphs.
- **`PDFTextOutlines`** — every glyph occurrence becomes filled path outlines. Largest output and text is not selectable, but no font machinery is involved at all.

**Shared font embedding** — the payoff of `PDFTextNamed` when composing many charts into one document: render each chart with `...PDFUsage` collecting the reported `FontUsage` (PostScript name, source font bytes, glyph IDs), union the glyph IDs per font across all charts, build one shared subset per font with `SubsetFont`, and embed that single subset in the composed document. `SubsetFont` preserves the source's glyph numbering, so the named output's Identity-encoded glyph references resolve against it without remapping. Each font's glyphs are then stored once, no matter how many charts use them.

### Loaders

Loaders control how Vega fetches external data. The default denies all loading for security. Loaders that hold resources (like `FileLoader` and `FallbackLoader`) are automatically closed when `Converter.Close()` is called.

```go
// Deny all external data (default).
aster.New()

// Allow HTTP/HTTPS requests.
aster.New(aster.WithLoader(aster.NewHTTPLoader(nil)))

// Allow HTTP with a custom client (timeouts, proxies, etc).
aster.New(aster.WithLoader(aster.NewHTTPLoader(customClient)))

// HTTP with domain whitelisting — only these hosts are permitted.
aster.New(aster.WithLoader(&aster.HTTPLoader{
    Client:         http.DefaultClient,
    AllowedDomains: []string{"cdn.jsdelivr.net"},
}))

// HTTP with base URL — relative URIs in specs are resolved against it.
aster.New(aster.WithLoader(&aster.HTTPLoader{
    Client:  http.DefaultClient,
    BaseURL: "https://cdn.jsdelivr.net/npm/vega-datasets@v1.29.0/",
}))

// Serve files from a local directory (uses os.Root for path containment).
aster.New(aster.WithLoader(&aster.FileLoader{BaseDir: "./data"}))

// Static test data — returns a JSON payload for any URI, no server needed.
aster.New(aster.WithLoader(&aster.StaticLoader{
    Value: []map[string]any{{"a": "A", "b": 28}, {"a": "B", "b": 55}},
}))

// Composite: try local files first, fall back to HTTP.
aster.New(aster.WithLoader(aster.NewFallbackLoader(
    &aster.FileLoader{BaseDir: "./data"},
    aster.NewHTTPLoader(nil),
)))
```

**Available loaders:**

| Loader | Description |
|--------|-------------|
| `DenyLoader` | Rejects all loading (default) |
| `HTTPLoader` | HTTP/HTTPS with optional `AllowedDomains` and `BaseURL` |
| `FileLoader` | Local files from a base directory, secured with `os.Root` |
| `StaticLoader` | Returns a fixed JSON value for any URI (test stub) |
| `FallbackLoader` | Tries child loaders in order until one succeeds |

`HTTPLoader` rejects non-HTTP schemes (`ftp:`, `javascript:`, `data:`, `file:`), URIs with userinfo (`user:pass@host`), and domains not in the allowlist. Domain matching is case-insensitive. The same policy is re-checked on every HTTP redirect hop, so an allowed host cannot redirect a request to a disallowed one; a `CheckRedirect` on your own `http.Client` still applies on top. Response bodies are capped at 64 MiB by default (`MaxResponseBytes` raises or disables the cap).

When rendering specs from untrusted sources, set `BlockPrivateNetworks: true` to additionally reject hosts that resolve to loopback, link-local, or private addresses (including cloud metadata endpoints like `169.254.169.254`), and pair it with `AllowedDomains` — name resolution happens at policy-check time, so the flag alone does not defend against DNS rebinding.

`FileLoader` rejects absolute paths, path traversal (`..`), and URIs with schemes. It uses Go's `os.Root` for OS-level path containment, which also blocks symlink escapes, refuses anything that is not a regular file, and caps file size at 64 MiB by default (`MaxBytes` raises or disables the cap).

`FallbackLoader` naturally routes by URI shape — `FileLoader` accepts relative paths while `HTTPLoader` accepts absolute URLs — so combining them covers specs that reference both local and remote data.

### Custom fonts

The embedded Liberation Sans covers most Latin text. For other scripts or specific fonts:

```go
ttf, _ := os.ReadFile("MyFont-Regular.ttf")
bold, _ := os.ReadFile("MyFont-Bold.ttf")

c, err := aster.New(
    aster.WithFont("My Font", ttf),
    aster.WithFont("My Font", bold),
    aster.WithDefaultFontFamily("My Font"),
)
```

Custom fonts are used for both text measurement (SVG layout) and PNG rendering.

## Performance

A render takes milliseconds: on an Apple M1 Pro the 627 Vega-Lite examples render to SVG in 2.4 ms each (geometric mean) and to PNG in 7.9 ms, 2.5× and 1.7× faster than upstream Vega running in node 24 on the same specs, and a `Converter` is ready for its first chart in under 10 ms. [internal/cmd/enginebench](internal/cmd/enginebench) runs the comparison; [RESULTS.md](internal/cmd/enginebench/RESULTS.md) has the full numbers, including the QuickJS/WASM engine earlier versions of this package used, which was 13–62× slower.

**Concurrency:** a `Converter` is safe for concurrent use by any number of goroutines, so one can serve a whole process. Each call renders on state of its own; what calls share (fonts and their shaping caches, locales, colour schemes, compiled expressions) is immutable or synchronized. A `Loader` passed to `WithLoader` is called from several goroutines at once and must be safe for that (the loaders in this package are). `Close` may be called at any time: it cancels the calls in flight, waits for them, closes the loader, and every later call returns an error. Throughput of a mixed workload (`BenchmarkParallel`, renders per second, 10 cores):

| Goroutines | 1 | 4 | 10 |
|---|---|---|---|
| one shared `Converter` | ~760 | ~1,900 | ~2,300 |
| one `Converter` each | ~750 | ~2,150 | ~3,000 |

Past about four goroutines the garbage collector appears to be the limit.

## Developer notes

### Architecture

The engine is organized along Vega's own module boundaries, under `internal/`:

| Package | Role |
|---|---|
| `vegalite` | the Vega-Lite compiler (6.4.3 and 5.8.0) |
| `vega` | the Vega parser, dataflow, runtime, guides and layout |
| `expr` | Vega expressions, compiled to Go closures |
| `transforms`, `geo`, `scale`, `format` | data transforms (with hierarchy, force, voronoi, contour, label, wordcloud), projections and geo paths, scales and color schemes, d3-format / d3-time-format |
| `scene`, `svg` | the scenegraph and bounds, and SVG output byte-compatible with Vega's SVG renderer |
| `raster`, `svgpdf`, `text` | SVG to PNG, SVG to vector PDF, and text shaping with forme |
| `jsval`, `jsmath`, `jssort` | JavaScript values and JSON, V8-exact `Math`, and V8's sort, so results match upstream bit for bit |

[docs/DESIGN.md](docs/DESIGN.md) describes the conventions that keep it bit-compatible with upstream, and [docs/SECURITY_REVIEW.md](docs/SECURITY_REVIEW.md) the security review and the bounds on what a spec can make the engine do.

### Testing

Upstream is the oracle. `testdata/oracle-node` pins Vega 6.4.0 / Vega-Lite 6.4.3 (and resvg), and `testdata/oracle-node-vl5` Vega-Lite 5.8.0, running in node; `internal/oracle` drives them and caches their answers under `testdata/oracle-cache`, which is recreated on demand and never committed. The engine's SVG, compiled Vega and PNG are compared with what upstream produces for the same spec.

On the 1,347 specs of the corpus (260 Vega fixtures, 332 Vega-Lite fixtures, Vega's 92 example specs, the 627 Vega-Lite examples, 23 vl-convert specs and 13 fuzz-found regressions), 1,344 render identically to upstream or within half a pixel (text is shaped by forme on one side and node-canvas on the other; the clock is pinned on both sides, and timer events do not fire in a static render); the remaining few are those whose oracle answer depends on the platform node runs on, listed with the reason in `testdata/oracle-expect.txt`; the compiled Vega is identical to upstream's for every Vega-Lite spec.

```bash
(cd testdata/oracle-node && npm ci)        # node version pinned in package.json (volta)
(cd testdata/oracle-node-vl5 && npm ci)
go test ./...                               # tests that need the oracle skip without it
ASTER_ORACLE=require go test ./...          # as in CI: fail instead of skipping
scripts/fetch-corpora.sh                    # external corpora (TestCorpusWild, TestCorpusDeneb)
scripts/record-upstream-vectors.sh          # upstream's own test suites, for the TestUpstream* replays
go test -run TestCompareWithNode -v . -args -compare.report=/tmp/report.md
scripts/fmacheck.sh                         # fused multiply-add audit
ASTER_FUZZ=3000 go test -run TestFuzzDifferential -v -timeout 3h .
```

`go test -short ./...` needs no node: the unit tests replay vectors recorded from upstream (by the generators under each package's `testdata/`).

Upstream's own test suites are replayed too. `scripts/record-upstream-vectors.sh` runs the test files of Vega's and d3's packages (from the git tag of the installed version) against the installed packages and records every call they make with upstream's answer, in `testdata/upstream-vectors-cache` (git-ignored, derived). The `TestUpstream*` tests in `internal/` replay those calls against the engine and compare exactly; they skip without the vectors, and fail with `ASTER_ORACLE=require`. Where the engine and upstream differ, the difference is listed, with its reason, in `testdata/upstream-vectors/known-divergences.txt`, and asserted both ways: an unlisted difference fails, and so does a listed one that has gone away.

### Building from source

Everything needed is committed, so a plain `go build ./...` works offline. `make vendor-datasets` re-vendors the vega-datasets test data (requires network).

### Known limitations

- **Emoji:** Monochrome [Noto Emoji](https://fonts.google.com/noto/specimen/Noto+Emoji) is bundled as a fallback, so emoji have text metrics and rasterize (in black-and-white) in PNG output. Color emoji are not supported.
- **Interactive features:** Selection and signal interactivity are evaluated at initial state only; there is no event loop.
- **Remote images in PNG:** Image marks referencing external URLs render in SVG output (the URL is embedded as an `href`), but the rasterizer does not fetch them, so they are blank in PNG output. Embedded `data:` URLs render fine.

## Acknowledgments

Aster stands on the shoulders of giants. Special thanks to the [vl-convert](https://github.com/vega/vl-convert) project, whose architecture, test suite, and font choices were invaluable references throughout this project's development.

Thanks also to the [Vega](https://vega.github.io/vega/) and [Vega-Lite](https://vega.github.io/vega-lite/) teams for building such excellent visualization grammars, whose implementations this one follows closely, and to the authors of the dependencies that make this possible: [forme](https://github.com/mgilbir/forme), [goecma262](https://github.com/mgilbir/goecma262) and [pdf0](https://github.com/mgilbir/pdf0). Earlier versions of aster ran Vega in [QuickJS](https://bellard.org/quickjs/) (via [QuickJS-NG](https://github.com/quickjs-ng/quickjs) and [fastschema/qjs](https://github.com/fastschema/qjs)) on [wazero](https://github.com/tetratelabs/wazero) (via [andsifr](https://github.com/mgilbir/andsifr)), with [resvg](https://github.com/linebender/resvg) for PNG; resvg remains the reference the rasterizer is tested against.

## License

[BSD 3-Clause](LICENSE). Third-party notices for the projects the engine derives from are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

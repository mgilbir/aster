# aster

Go library and CLI for rendering [Vega](https://vega.github.io/vega/) and [Vega-Lite](https://vega.github.io/vega-lite/) visualization specs to SVG, PNG and vector PDF. Pure Go: no JavaScript runtime, no WebAssembly, no CGO.

Aster is a Go implementation of the Vega runtime and the Vega-Lite compiler, following Vega 6.4.0 and Vega-Lite 6.4.3 (and compiling Vega-Lite 5.8.0), with text measured by [forme](https://github.com/mgilbir/forme), its own SVG rasterizer for PNG and its own PDF writer. Its output is checked against upstream Vega running in node (see [Testing](#testing)). Everything runs in-process with no external dependencies.

## Features

- Vega-Lite to SVG, PNG, vector PDF, or compiled Vega JSON
- Vega to SVG, PNG, or vector PDF
- Arbitrary SVG to PNG or vector PDF conversion
- PDF output is fully vector with subset-embedded fonts (selectable text) — ideal for LaTeX `\includegraphics`
- Accurate text shaping with [forme](https://github.com/mgilbir/forme), measured the way browsers measure (unrounded advances), with embedded Liberation fonts and monochrome Noto Emoji
- Colour emoji in PNG and PDF from a colour font you register (COLRv0, COLRv1, sbix, CBDT)
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

# Set signals (Vega-Lite parameters) before rendering: JSON values, or strings
aster svg -i chart.vl.json -signal cutoff=10 -signal 'label=Q3 sales'
```

The CLI auto-detects Vega vs Vega-Lite from the `$schema` field. If absent, Vega-Lite is assumed.

Shared flags: `-i`/`-o` (input/output, stdin/stdout when omitted), `-version`, `-timeout`, `-allow-http`, `-allow-domain` (repeatable; implies `-allow-http`), and `-allow-private-networks` (let HTTP loading reach loopback, link-local and private addresses, which are denied by default). `png` also accepts `-scale` and `-recode`; these don't apply to `pdf` since it's vector output. `pdf` accepts `-text embed|named|outlines` to pick the [PDF text mode](#options) (default `embed`). `svg`, `png` and `pdf` accept `-signal name=value` (repeatable) to set a signal before rendering; a value that parses as JSON is that value, anything else a string.

## API

### Converter

All rendering goes through a `Converter`, created with `aster.New()`. A converter is safe for concurrent use, so one can serve a whole process (see [Performance](#performance)).

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
| `WithFont(family, ttf)` | — | Register a custom TTF font (used by both measurement and PNG); a colour font draws in colour |
| `WithFontFace(family, ttc, index)` | — | Register one face of a font collection (`WithFont` registers every face of one, which font-weight and font-style choose among) |
| `WithFontInstance(family, ttf, axes)` | — | Register a variable font at a point of its design space (`{"wght": 650}`): its outlines, metrics and variable colour glyphs; register several under one family to have font-weight choose |
| `WithDefaultFontFamily(name)` | `"Liberation Sans"` | Family that generic `sans-serif` resolves to (both pipelines) |
| `WithDefaultSerifFamily(name)` | `"Liberation Serif"` | Family that generic `serif` resolves to (both pipelines) |
| `WithDefaultMonospaceFamily(name)` | `"Liberation Mono"` | Family that generic `monospace` resolves to (both pipelines) |
| `WithSystemFonts()` | disabled | Also use system-installed fonts, when a spec names them (both pipelines) |
| `WithTheme(json)` | — | Vega theme config applied to all renders |
| `WithTimezone(tz)` | `"UTC"` | Timezone for local-time operations (time scales, `timeFormat`, parsing dates without a zone); any IANA zone Go knows, others make `New` return an error |

`WithMemoryLimit` is not a heap cap: it scales the render's budgets (rows at 256 bytes each, scene items at 512, loaded bytes, the SVG at a quarter of the limit) and the rasterizer's canvas memory, and each budget is charged before the allocation it pays for. Without it the defaults apply: 1M rows, 500k scene items, 64 MiB of loaded data, 128 MiB of SVG. `WithTimeout` bounds one call across all of its stages, text shaping included: a run of text stops partway when the call's time is up. Shaping is also bounded on its own, by an allowance of lookup work per call about sixty times what the heaviest example chart needs, and with `WithMemoryLimit` by the glyphs of one run (a quarter of the limit); a run past either is refused with an error wrapping `ErrLimit`, as is one in a font whose layout tables exceed what the shaper reads (a malformed or hostile font).

A specification that exceeds any resource limit (these budgets, and the fixed bounds on nesting depth, expression size, tick and legend counts, and PNG and PDF output) fails with an error wrapping `aster.ErrLimit`; a call that runs out of time fails with one wrapping `context.DeadlineExceeded`:

```go
svg, err := c.VegaLiteToSVG(spec)
switch {
case errors.Is(err, aster.ErrLimit):
	// the specification asks for more than this converter allows
case errors.Is(err, context.DeadlineExceeded):
	// the render took longer than WithTimeout
}
```

**Signals** — every render method that takes a specification also takes `WithSignal(name, value)`, which sets a top-level signal before the chart is drawn, as Vega's `view.signal(name, value)` does. A Vega-Lite parameter (a variable, or the value a bound input sets) is a signal of the same name, so a chart can be rendered as it looks after the input changes:

```go
svg, err := c.VegaLiteToSVG(spec, aster.WithSignal("cutoff", 10))
png, err := c.VegaToPNG(spec, aster.WithScale(2), aster.WithSignal("year", 2000), aster.WithSignal("region", "EMEA"))
```

The value is anything `encoding/json` encodes, held as that JSON value. Signals are set in the order given, each propagated before the next. A name the specification does not define fails the render; the SVG-input methods ignore the option.

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

Loaders control how Vega fetches external data, and the images PNG and PDF output draw. The default denies all loading for security. Loaders that hold resources (like `FileLoader` and `FallbackLoader`) are automatically closed when `Converter.Close()` is called.

```go
// Deny all external data (default).
aster.New()

// Allow HTTP/HTTPS requests.
aster.New(aster.WithLoader(aster.NewHTTPLoader(nil)))

// Allow HTTP with a custom client (transport, TLS, redirect policy, etc).
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

`HTTPLoader` rejects non-HTTP schemes (`ftp:`, `javascript:`, `data:`, `file:`), URIs with userinfo (`user:pass@host`), and domains not in the allowlist. Domain matching is case-insensitive, ignores one trailing dot, and takes ASCII names only (write an internationalized name in its punycode form). The same policy is re-checked on every HTTP redirect hop (at most 10), so an allowed host cannot redirect a request to a disallowed one; a `CheckRedirect` on your own `http.Client` still applies on top. Response bodies are capped at 64 MiB after decompression (`MaxResponseBytes` raises or disables the cap), and one request, redirects and body included, takes at most 60 seconds (`Timeout`); a body over the cap, or over what is left of the render's load budget (`WithMemoryLimit`), ends the render with `ErrLimit`. So does a file over `FileLoader`'s `MaxBytes`, also behind a `FallbackLoader`.

**Private networks are denied by default.** A specification names its URLs, so a hostile one could otherwise make the server fetch from its own network: `localhost`, loopback, link-local (cloud metadata at `169.254.169.254`), RFC 1918 and unique-local, carrier-grade NAT, multicast and unspecified addresses, in every spelling (`2130706433`, `0x7f.1`, `::ffff:127.0.0.1`, NAT64 and 6to4 forms). The check is made on the address a connection is actually made to, so a name that resolves to one, DNS rebinding and redirects do not get around it. Set `AllowPrivateNetworks: true` (CLI: `-allow-private-networks`) to reach a data service on your own network. This replaces `BlockPrivateNetworks`, which is now ignored. The check lives in the connections of the default client and of a `Client` whose `Transport` is an `*http.Transport`; a `Transport` with a deprecated `Dial`, or any other `http.RoundTripper`, chooses where it connects itself, and for those only address literals and `localhost` names are refused, so combine them with `AllowedDomains`.

**Proxies are honoured.** The default client (a nil `Client`, or `http.DefaultClient`) follows `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` as `http.DefaultTransport` does, and a `Transport` of your own keeps its `Proxy`. The connection to the proxy is allowed whatever its address, since whoever runs the process chose it. The proxy resolves the target's name itself, though, so a request it carries gets only the checks on the URL (address literals, `localhost` names, `AllowedDomains`), not the check on the address finally reached: when a proxy is set, deny internal destinations at the proxy, or use `AllowedDomains`. A request the proxy settings send directly (`NO_PROXY`) keeps the full check.


`FileLoader` rejects absolute paths, path traversal (`..`), control characters and URIs with a scheme (`file:`, `http:`); the URI is a file path, so nothing is percent-decoded and a name with a colon, such as `22:48`, is an ordinary file name. It uses Go's `os.Root` for OS-level path containment, which also blocks symlink escapes, refuses anything that is not a regular file, and caps file size at 64 MiB by default (`MaxBytes` raises or disables the cap).

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

A render takes milliseconds: on an Apple M1 Pro the 627 Vega-Lite examples render to SVG in 1.9 ms each (geometric mean) and to PNG in 7.2 ms, 3.5× and 2.1× faster than upstream Vega running in node 24 on the same specs, and a `Converter` is ready for its first chart in under 10 ms. [internal/cmd/enginebench](internal/cmd/enginebench) runs the comparison; [RESULTS.md](internal/cmd/enginebench/RESULTS.md) has the full numbers, including the QuickJS/WASM engine earlier versions of this package used, which was 13–62× slower.

**Concurrency:** a `Converter` is safe for concurrent use by any number of goroutines, so one can serve a whole process. Each call renders on state of its own; what calls share (fonts and their shaping caches, locales, colour schemes, compiled expressions) is immutable or synchronized. A `Loader` passed to `WithLoader` is called from several goroutines at once and must be safe for that (the loaders in this package are). `Close` may be called at any time: it cancels the calls in flight, waits for them, closes the loader, and every later call returns an error. Throughput of a mixed SVG workload (`BenchmarkParallel`, renders per second, median of five runs, 10 cores):

| Goroutines | 1 | 4 | 10 |
|---|---|---|---|
| one shared `Converter` | ~950 | ~2,500 | ~3,300 |
| one `Converter` each | ~950 | ~2,700 | ~4,100 |
| one shared `Converter`, `GOGC=400` | ~1,080 | ~3,700 | ~4,900 |

The gap between a shared `Converter` and one per goroutine is the garbage collector's pacing, not contention: mutex and block profiles show calls never wait on each other, but ten converters hold a larger live heap, so at the default `GOGC=100` the collector runs about a third as often (458 cycles in 20,000 renders against 1,542). A process that renders in parallel gets the throughput back by letting the heap grow further between collections, with a higher `GOGC` (or `debug.SetGCPercent`), or `GOGC=off` with a `GOMEMLIMIT` sized for the machine; at `GOGC=400` one shared `Converter` matches one per goroutine.

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

On the 1,418 specs of the corpus (260 Vega fixtures, 332 Vega-Lite fixtures, Vega's 92 example specs, the 627 Vega-Lite examples, 23 vl-convert specs and 84 fuzz-found regressions), 1,415 render identically to upstream or within half a pixel, or fail where upstream fails (text is shaped by forme on one side and node-canvas on the other; the clock is pinned on both sides, and timer events do not fire in a static render); the remaining few are those whose oracle answer depends on the platform node runs on, listed with the reason in `testdata/oracle-expect.txt`; the compiled Vega is identical to upstream's for every Vega-Lite spec.

Beyond the corpus, the same comparison runs over generated and collected inputs. Each has a floor on its case count, so it cannot silently shrink, and an expectation file under `testdata/sweeps` that lists, with a reason, every case that is not `ok`; a case that regresses fails, and so does an entry that no longer applies.

| Sweep | Cases | What it probes |
|---|---:|---|
| `TestSweepVegaSchema` | 7,549 | one chart per (property, value) Vega's schema declares |
| `TestSweepVegaLiteSchema` | 27,513 | the same over Vega-Lite's schema, comparing the compiled Vega |
| `TestSweepValues` | 500 | untidy data (NaN, ±Infinity, -0, null, empty strings, words where numbers go, non-ASCII text) through stacks, scales, aggregates and axes |
| `TestSweepSignals` | 166 | signal writes after the first render, re-run as `View.signal` + `runAsync` would |
| `TestSweepTimezones` | 801 | local time in nine zones around 2024's daylight-saving transitions, node running with that `TZ` |
| `TestSweepLocales` | 372 | every d3 number and time locale, through axes, legends, formats and parses |
| `TestCorpusWild` | 1,981 | Vega-Lite specs collected from the wild (chart-llm, pinned) |
| `TestCorpusDeneb` | 62 | Deneb's templates and examples (pinned) |

All of them agree with upstream except a handful of CJK and emoji cases, where neither side has the glyphs and what node measures depends on the platform's font fallback.

Other parts of the harness:

- **Upstream's own test suites.** `scripts/record-upstream-vectors.sh` runs the test files of 29 Vega and d3 packages, at the installed versions, and records every call they make with upstream's answer; 34 `TestUpstream*` tests replay 26,395 of those calls against the engine and require exact equality. The few known divergences are listed with their cause in `testdata/upstream-vectors/known-divergences.txt`, some only for the architecture node recorded them on.
- **Differential fuzzing.** `ASTER_FUZZ=<n> go test -run TestFuzzDifferential .` mutates corpus specs and renders each mutant on both sides, checking the engine for panics and a time budget; each disagreement is triaged into a fix with a regression spec under `testdata/corpus/regress`, or into a stated reason (a deliberate limit, a loader policy).
- **Coverage-guided fuzzing.** Each parser has a `go test -fuzz` target, seeded from the corpus (the JSON reader, the expression parser, event selectors, TopoJSON, serialized scenegraphs, the scene-to-SVG renderer, and the SVG input of the rasterizer and of the PDF writer, among others); `scripts/fuzz-targets.txt` lists them, `make fuzz` runs them (`FUZZTIME=30s` for a short pass), and the nightly `fuzz.yml` workflow runs each for minutes, keeping its corpus between nights. Besides "no panic, bounded time" they check properties: the JSON reader accepts what `encoding/json` accepts and re-reads its own output, the SVG renderer writes well-formed XML, and every PDF reads back with pdf0. A crasher is fixed, and its input kept under the package's `testdata/fuzz/`.
- **The comparator is tested too.** `internal/svgdiff` is mutation-tested: 336 kinds of change over 718 documents must each be reported, and the equivalences it tolerates are listed.
- **Untrusted input.** `TestDeepInput` drives 69 deeply nested shapes (JSON, expressions, specs, SVG for PNG) to depths up to 100,000 in child processes with a capped stack, each within a time limit; `go run ./internal/cmd/recursionaudit` finds every recursion in the call graph and checks each against `scripts/recursion.allow`, which says how it is bounded. Resource limits (elements, render work, pixels, time) have their own tests.
- **Floating point.** `scripts/fmacheck.sh` rejects fused multiply-adds the Go compiler could introduce on arm64 where V8 does not have them.
- **Performance.** `BenchmarkScenes` times representative charts; `ASTER_PERF=1 go test -run TestPerfReport .` breaks a render down by stage (parse, compile, dataflow, text, SVG, PNG, PDF). CI gates pull requests on it: `scripts/benchgate.sh` (`make benchgate`) benchmarks the base and the head alternately on one machine and fails on a significant increase in allocations (over 2%), bytes (5%) or time (15%) in a fixed set of benchmarks; a PR label `perf-regression-ok` allows an intended trade-off.

`scripts/check.sh` (or `make check`) runs the gates CI runs.

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

Colour glyphs are checked against HarfBuzz's drawing of the same fonts (`hb-view`, images in `testdata/colourfonts`, among them a subset of Noto Color Emoji), and aster's PDFs are read back with PDFium, Chrome's PDF engine, and compared with its PNGs (`TestPDFium*`). Those need a Python with pypdfium2, pinned in `scripts/pdfium/requirements.txt`, and skip without one:

```sh
python3 -m venv .pdfium && .pdfium/bin/pip install --require-hashes -r scripts/pdfium/requirements.txt
ASTER_PDFIUM=.pdfium/bin/python go test ./internal/svgpdf -run TestPDFium
```

Upstream's own test suites are replayed too. `scripts/record-upstream-vectors.sh` runs the test files of Vega's and d3's packages (from the git tag of the installed version) against the installed packages and records every call they make with upstream's answer, in `testdata/upstream-vectors-cache` (git-ignored, derived). The `TestUpstream*` tests in `internal/` replay those calls against the engine and compare exactly; they skip without the vectors, and fail with `ASTER_ORACLE=require`. Where the engine and upstream differ, the difference is listed, with its reason, in `testdata/upstream-vectors/known-divergences.txt`, and asserted both ways: an unlisted difference fails, and so does a listed one that has gone away.

### Building from source

Everything needed is committed, so a plain `go build ./...` works offline. `make vendor-datasets` re-vendors the vega-datasets test data (requires network).

### Known limitations

- **Emoji:** Monochrome [Noto Emoji](https://fonts.google.com/noto/specimen/Noto+Emoji) is bundled as a fallback, so emoji always have text metrics and draw (in the text's colour). No colour font is bundled. A colour font registered with `WithFont` (such as [Noto Color Emoji](https://github.com/googlefonts/noto-emoji)) is tried before the bundled fonts for characters the requested families lack, and draws in colour: COLRv0 and COLRv1 (gradients, transforms and every composite mode), sbix and CBDT bitmaps (the strike for the size drawn), and EBDT masks. Measurement then uses that font's advances, as a browser using it would. A variable colour font is drawn at its default instance, or at the one `WithFontInstance` names. In PDF a colour glyph is drawn as vectors where PDF can say what it paints (its composites as transparency groups: blend modes as PDF's own, Porter-Duff operators with soft masks), its gradients as shadings (a sweep as a mesh, a repeated one with its colour line once a period, translucent stops under a soft mask), and otherwise, for the Xor and Plus operators, as an image of it at 8 pixels a point of its size, 256 to 1024 per em; either way its text is kept, so it can be searched and copied. SVG output leaves emoji as text, for the viewer to draw. OpenType SVG glyphs are drawn too: in PNG by the rasterizer's own SVG renderer, their `context-fill` the text's colour, and in PDF as that drawing's image. `WithSystemFonts` indexes each face of a font collection (`.ttc`, `.otc`) and maps system font files into memory rather than reading them, so Apple Color Emoji (190 MB) costs only what is drawn from it. A system font is used only when a spec names its family (for example `"font": "Apple Color Emoji"`), never as a fallback: check a system font's licence before distributing what it draws. In PDF, emoji are also written as invisible text in a subset of the font cut from its outline tables alone, so Apple Color Emoji adds about 100 KB to a PDF, not its 190 MB of bitmaps, and its emoji can be searched and copied.
- **Interactive features:** There is no event loop: a chart is rendered at its initial state, or at the state `WithSignal` sets (a parameter, a bound input's value); events themselves (pointer, timer, input) are not simulated.
- **Images:** PNG and PDF output fetch the images they draw through the Loader, as data is fetched (so the default loader draws only embedded `data:` images), each distinct URL once. PNG, JPEG and GIF are drawn; a PDF embeds an RGB or grey JPEG as it is and the rest as compressed RGB with a soft mask for transparency. An image that cannot be fetched or decoded is left out, as a broken image is, and one over the size limits fails the render with `ErrLimit`. An image mark without a width or a height takes it from the image, as upstream does once the image has loaded; a call fetches each image once for its SVG, PNG and PDF.

## Acknowledgments

Aster stands on the shoulders of giants. Special thanks to the [vl-convert](https://github.com/vega/vl-convert) project, whose architecture, test suite, and font choices were invaluable references throughout this project's development.

Thanks also to the [Vega](https://vega.github.io/vega/) and [Vega-Lite](https://vega.github.io/vega-lite/) teams for building such excellent visualization grammars, whose implementations this one follows closely, and to the authors of the dependencies that make this possible: [forme](https://github.com/mgilbir/forme), [goecma262](https://github.com/mgilbir/goecma262) and [pdf0](https://github.com/mgilbir/pdf0). Earlier versions of aster ran Vega in [QuickJS](https://bellard.org/quickjs/) (via [QuickJS-NG](https://github.com/quickjs-ng/quickjs) and [fastschema/qjs](https://github.com/fastschema/qjs)) on [wazero](https://github.com/tetratelabs/wazero) (via [andsifr](https://github.com/mgilbir/andsifr)), with [resvg](https://github.com/linebender/resvg) for PNG; resvg remains the reference the rasterizer is tested against.

## License

[BSD 3-Clause](LICENSE). Third-party notices for the projects the engine derives from are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

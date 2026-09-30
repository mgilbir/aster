# purego

An experimental pure-Go Vega / Vega-Lite engine: no JavaScript runtime and no
WebAssembly. It exposes the same API as the root `aster` package
(`purego.New`, `Converter.VegaLiteToSVG`, `…ToPNG`, `…ToPDF`, `SVGToPNG`, the
same options and loaders), so the two engines can be compared call for call.
It follows Vega 6.4.0 / Vega-Lite 6.4.3 — the versions the root engine
vendors. See [DESIGN.md](DESIGN.md) for the architecture and conventions.

```go
c, err := purego.New(purego.WithLoader(purego.NewHTTPLoader(nil)))
if err != nil {
	log.Fatal(err)
}
defer c.Close()
svg, err := c.VegaLiteToSVG(spec)
```

## Concurrency

A `Converter` is safe for concurrent use by any number of goroutines: share one
for the whole process instead of creating one per request. Each call renders
on state of its own and returns what it would return alone; what the calls
share (fonts and their shaping caches, number and time locales, colour schemes,
compiled expressions) is immutable or synchronized. One Converter loads fonts and fills caches once, so it costs less memory and start-up time; for peak throughput on many cores, one per goroutine is faster (see Performance). A `Loader`
given to `WithLoader` is called from several goroutines at once and must be
safe for that (the loaders in this package are). `Close` may be called at any
time: it cancels the calls in flight, waits for them, closes the loader, and
every later call returns an error. The root `aster` Converter, which wraps a
single JavaScript runtime, is not safe for concurrent use.
`TestConcurrentConverterSharesNothing` and `TestConcurrentCloseDuringRender`
check this under `-race`.

## Status

Measured on 1,347 specs: the corpus in `testdata/corpus` (260 Vega fixtures,
332 Vega-Lite fixtures, upstream Vega's 92 example specs, 13 fuzz-found
regression cases) plus the root package's 627 Vega-Lite examples and 23
vl-convert specs:

| Comparison | Result |
|---|---|
| SVG vs the root (QuickJS) engine | 1,320 identical or equal within 0.01 px, 0 different, 0 purego errors; 15 are rejected by the reference engine too (mostly canvas-only transforms), and 12 are cases where the reference itself departs from V8 (see below) or that draw the current time |
| Vega-Lite → Vega vs the root engine | 979 of 979 identical |
| Vega-Lite → Vega vs upstream | vega-lite 6.4.3 and 5.8.0 each: 1,924 corpus specs and 959 themed compilations byte-identical, key order included |
| SVG vs upstream Vega 6.4.0 in node | 624 of 627 byte-identical, 1 equal within 0.5 px; the 2 others measure emoji with a colour emoji font |
| PNG vs resvg | mean absolute error 0.073 / 255 per channel over 627 renders |

Specs where the reference engine, not purego, departs from V8 (confirmed
against node): `Infinity` formatted without Intl, QuickJS's last-bit
trigonometry, its sort order for inconsistent comparators, its
`Date.prototype.toString` zone name and its lenient `Date.parse`.

## Performance

Apple M1 Pro, `go test -bench . ./purego/` (warm converter, each engine with
its own loader):

| Spec | SVG QuickJS | SVG purego | PNG QuickJS | PNG purego |
|---|---|---|---|---|
| `bar` | 48 ms | 0.9 ms | 158 ms | 4.5 ms |
| `trellis_bar` | 96 ms | 2.4 ms | 236 ms | 14.7 ms |
| `repeat_splom` | 1,783 ms | 29 ms | 2,444 ms | 105 ms |
| `geo_choropleth` | 6,505 ms | 85 ms | 9,201 ms | 131 ms |
| `New` + first render | 238 ms | 3.9 ms | | |

SVG allocations per render (`geo_choropleth`: 1.09 M objects and 116 MB before,
0.18 M and 46 MB now). Throughput of the `BenchmarkParallel` mix (bar,
trellis_bar, scatter plot, stacked area, treemap; renders per second, 10 cores):

| Goroutines | 1 | 4 | 10 |
|---|---|---|---|
| one shared `Converter` | ~760 | ~1,900 | ~2,300 |
| one `Converter` each | ~750 | ~2,150 | ~3,000 |

A render is allocation-heavy, and past about four goroutines the garbage
collector appears to be the limit; `GOGC` / `GOMEMLIMIT` tuning has not been
measured. A shared `Converter` gives about 25% less throughput than one per
goroutine at ten goroutines; the remaining contention has not been found.

## What differs from the root package

- **Text** is shaped with [forme](https://github.com/mgilbir/forme) in both
  engines (the shared `internal/text` package); go-text/typesetting, which the
  root package used before, is gone from the module. Widths match go-text's
  recorded output exactly on 3,846 test cases; the exceptions are bugs in the
  latter (bold/italic emoji fall back to `.notdef`, Hebrew is shaped as
  Latin), which the root engine no longer has either. See
  [internal/text/FORME_EVALUATION.md](../internal/text/FORME_EVALUATION.md).
- **PNG** is rasterized by `internal/raster`, not resvg; it covers the SVG
  Vega emits plus common general SVG (no CSS `<style>`, masks, filters,
  patterns or markers).
- **Time zones**: any IANA zone (`WithTimezone`), not only UTC.
- **Vega-Lite 5.8** compiles alongside 6.4 (`WithVegaLiteVersion("5.8")`); both render with the Vega 6.4 runtime.
- **`WithMemoryLimit`** is not a heap cap. It scales the render's budgets (rows
  at 256 bytes each, scene items at 512, loaded bytes, the SVG size at a
  quarter of the limit) and the rasterizer's canvas memory; the budgets are
  charged before the allocation they pay for. Memory the engine does not count
  (the text shaper's caches, the Vega-Lite compiler, JSON parsing of the spec,
  PDF building) is outside it. Without the option the defaults apply: 1M rows,
  500k scene items, 64 MiB of loaded data, 128 MiB of SVG.
- **`WithTimeout`** (default 30 s) bounds one public call across all its stages,
  except that the Vega-Lite compiler is checked only before and after.
- **Randomness** (`random()`, `sample`, jitter, bootstrap intervals) is seeded
  per render, as in the root engine, so output is reproducible.
- PDF output uses the same `internal/svgpdf` converter as the root package.

## Checking it

```sh
export GOPRIVATE='github.com/mgilbir/*'
go test ./purego/...                                   # unit tests, golden vectors
go test ./purego/ -run TestCompareWithReference -v \
    -args -compare.report=/tmp/report.md               # vs the root engine
go test ./purego/ -run TestAgainstUpstreamRenderings -v # vs upstream in node
purego/scripts/fmacheck.sh                             # fused multiply-add audit
```

Golden vectors were recorded from upstream with the generators under each
package's `testdata/`, using the pinned node setup in
`testdata/oracle-node` (`npm ci` there; `node sync.mjs` after re-vendoring).

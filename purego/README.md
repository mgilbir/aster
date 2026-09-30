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

## Status

Measured on the corpus in `testdata/corpus` (260 Vega fixtures, 332 Vega-Lite
fixtures) plus the root package's 627 Vega-Lite examples and 23 vl-convert
specs:

| Comparison | Result |
|---|---|
| SVG vs the root (QuickJS) engine | 1,226 of 1,233 identical or equal within 0.01 px; the other 7 are cases where the reference itself departs from V8 (see below) or that draw the current time |
| Vega-Lite → Vega vs the root engine | 975 of 975 identical |
| Vega-Lite → Vega vs upstream vega-lite 6.4.3 | 1,924 corpus specs + 27,513 property-sweep cases byte-identical, key order included |
| SVG vs upstream Vega 6.4.0 in node | 612 of 627 within 0.5 px; the rest are 1 px width differences from text-advance rounding (both engines round advances like go-text/HarfBuzz; node-canvas does not) and emoji fonts |
| PNG vs resvg | mean absolute error 0.074 / 255 per channel over 624 renders |

Specs where the reference engine, not purego, departs from V8 (confirmed
against node): `Infinity` formatted without Intl, QuickJS's last-bit
trigonometry, and QuickJS's sort order for inconsistent comparators.

## Performance

Apple M1 Pro, `go test -bench . ./purego/` (warm converter, each engine with
its own loader):

| Spec | SVG QuickJS | SVG purego | PNG QuickJS | PNG purego |
|---|---|---|---|---|
| `bar` | 48 ms | 1.4 ms | 158 ms | 4.5 ms |
| `trellis_bar` | 96 ms | 2.6 ms | 236 ms | 14.7 ms |
| `repeat_splom` | 1,783 ms | 34 ms | 2,444 ms | 105 ms |
| `geo_choropleth` | 6,505 ms | 115 ms | 9,201 ms | 131 ms |
| `New` + first render | 238 ms | 3.9 ms | | |

## What differs from the root package

- **Text** is shaped with [forme](https://github.com/mgilbir/forme) rather than
  go-text/typesetting. Widths match go-text exactly on 3,846 test cases; the
  exceptions are bugs in the latter (bold/italic emoji fall back to `.notdef`,
  Hebrew is shaped as Latin). See
  [internal/text/FORME_EVALUATION.md](internal/text/FORME_EVALUATION.md).
- **PNG** is rasterized by `internal/raster`, not resvg; it covers the SVG
  Vega emits plus common general SVG (no CSS `<style>`, masks, filters,
  patterns or markers).
- **Time zones**: any IANA zone (`WithTimezone`), not only UTC.
- **Vega-Lite 5.8** is not available (`WithVegaLiteVersion("5.8")` errors).
- **`WithMemoryLimit`** bounds the rows and scene items a render may create
  rather than a heap size.
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

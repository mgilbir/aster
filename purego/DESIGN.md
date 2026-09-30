# purego — a pure-Go Vega / Vega-Lite engine

`purego` renders Vega and Vega-Lite specifications to SVG, PNG and PDF with no
JavaScript and no WebAssembly: no QuickJS, no resvg, no andsifr. It exposes the
same API as the root `aster` package (`purego.New`, `Converter.VegaLiteToSVG`,
…) so the two engines can be compared call for call.

Priorities, in order: **correctness** (output matches upstream Vega), **security**
(specifications are untrusted input), **performance**.

## Sources of truth

- **Behaviour:** upstream Vega 6.4.0 / Vega-Lite 6.4.3 and the exact d3 and
  vega-* module versions vendored for the root engine
  (`internal/js/modules/vl6_4/manifest.json`). Their sources are the
  reference to read before implementing anything; a local copy lives in any
  `node_modules` with `vega` and `vega-lite` installed (see *Oracles*).
- **Differential oracle:** the root `aster` package (the same Vega 6.4.0 /
  Vega-Lite 6.4.3
  inside QuickJS). Given the same spec, `purego` should produce the same SVG.
- The Go code is an idiomatic Go design organised along Vega's own module
  boundaries. It is **not** a line-by-line transliteration of JavaScript: use Go
  types, Go error handling, Go naming. Keep upstream's *semantics* exactly,
  including its odd corners, and say in a comment when a rule is non-obvious
  ("vega-scale treats a `nice` of `true` on a time scale as …").

## Package map

All packages live under `purego/internal/` unless noted.

| Package | Upstream counterpart | Contents |
|---|---|---|
| `purego` (public) | `aster` root | `Converter`, options, loaders (aliases of the root types) |
| `jsval` | JS values, `JSON` | `Value` (tagged struct), ordered `Object`, strict JSON parse/stringify, JS number formatting (`toFixed`, `toPrecision`, …), `Number()` coercion, field paths |
| `jsmath` | V8's `Math` (src/base/ieee754.cc) | bit-exact ports of every transcendental `Math` function: trig, inverse trig, hyperbolic, exp/log family, pow, cbrt, hypot |
| `format` | d3-format, d3-time, d3-time-format, vega-format, vega-time | number formats, locales, time intervals, time format/parse, time units, `timeFloor`, `timeSequence`, `timeBin` |
| `expr` | vega-expression, vega-functions | expression parser → AST → compiled Go closures; the function library; hooks for signals/data/scales supplied by the runtime |
| `scale` | d3-scale, vega-scale, d3-interpolate, d3-color, d3-scale-chromatic | every scale type, ticks, tick formats, schemes, colour parsing and interpolation |
| `transforms` | vega-transforms, vega-statistics, vega-regression, vega-hierarchy, vega-force, vega-voronoi, vega-label, vega-wordcloud, vega-encode (non-mark) | data transforms as plain functions over tuples, with typed parameter structs |
| `geo` | d3-geo, d3-geo-projection, vega-projection, vega-geo | projections, geo streams/clipping, geo path, graticule, geojson/topojson |
| `scene` | vega-scenegraph | `Scenegraph`, `Mark`, `Item`; path generation for every mark type (d3-shape curves); bounds; text metrics interface |
| `svg` | vega-scenegraph `SVGStringRenderer` | scenegraph → SVG text, byte-compatible with upstream |
| `vega` | vega-parser, vega-dataflow, vega-runtime, vega-view, vega-view-transforms, vega-encode | spec parsing, the dataflow graph, signals, data loading, scales, mark encoding, guides (axes/legends/titles), layout and autosize; produces a `scene.Scenegraph` |
| `vegalite` | vega-lite | Vega-Lite → Vega compiler |
| `internal/text` (module level) | (host) | text measurement and shaping with `github.com/mgilbir/forme`, shared with the root package and `svgpdf` |
| `raster` | (resvg) | SVG → RGBA rasterizer for the SVG subset Vega emits, plus PNG encoding |

PDF output reuses `aster/internal/svgpdf` (already pure Go).

Dependency direction (no cycles): `jsval` ← `format` ← `expr` ← `scale` ←
`transforms`/`geo` ← `scene` ← `vega` ← `purego`; `svg` depends on `scene`;
`vegalite` depends only on `jsval` (it emits Vega JSON); `raster` and `internal/text`
depend on nothing engine-specific.

## Core conventions

**Values.** Everything dynamic is a `jsval.Value`. Its zero value is
`undefined`. Objects are `*jsval.Object` (insertion ordered). Numbers are
`float64`. A date is `KindTimestamp` (epoch ms), distinct from a number. A
regular expression is `KindPattern` compiled with `github.com/mgilbir/goecma262`
(never Go's `regexp`: its semantics differ from JavaScript's).

- `v.AsDouble()` is a *reading* (null/""/[] → NaN); `jsval.ToNumber(v)` is
  JavaScript's `Number(v)` coercion (null/""/[] → 0). Use the one upstream uses:
  `+x` in upstream is `ToNumber`.
- `v.IsTruthy()` is JavaScript truthiness. `v.AsString()` is `String(v)`.
- `jsval.JSNumberString` is `Number.prototype.toString`.

**Tuples.** A datum is a `jsval.Value` of kind object. Identity is the
`*jsval.Object` pointer. Transforms that derive new tuples create new objects;
transforms that annotate (formula, stack, window, …) write fields into the
object they were given, as upstream does, so do not share one object between two
unrelated datasets without copying.

**Errors, not panics.** Anything reachable from a specification returns an
error or records a diagnostic; it never panics. A top-level `recover` in the
public API converts an unexpected panic into an error, but relying on it is a
bug.

**Security.** Specifications and data are untrusted.

- Bound recursion (expression nesting, group nesting, JSON depth, hierarchy
  depth) and bound work that a spec can make arbitrarily large (`sequence`
  lengths, `bin` step counts, tick counts, `maxbins`, wordcloud/force
  iterations, string repeats in `pad`, …). Choose limits far above any real
  chart and return an error when exceeded.
- Honour `context.Context` cancellation in every loop that can run long.
- No reflection-based evaluation, no access to the host from expressions:
  the expression language can only call the whitelisted function table.
- Data loading only through the configured `Loader`.
- Regular expressions run through goecma262's bounded matcher; use its `Err`
  methods for untrusted patterns.

**Floating point parity.** The Go spec allows `x*y + z` to be fused into a
single FMA instruction — **even across statements** — and on arm64 the
compiler does so, which changes the last bit relative to JavaScript (and can
change far more: `i := (n-1)*p; i - floor(i)` fused yields a tiny negative
residual instead of 0, and `Inf * -1e-16` is `-Inf` where upstream gets
NaN). Where results must match upstream bit for bit (geometry, scales,
statistics — nearly everywhere), round every product that feeds an addition
or subtraction explicitly, **at the product**: `i := float64((n-1) * p)`,
`float64(x*y) + z`. `scripts/fmacheck.sh` lists the fused instructions the
compiler emits for the engine, against a reviewed allowlist.
Likewise, Go's transcendental functions (`math.Sin`, `Pow`, `Exp`, `Tan`, …)
disagree with V8 in the last bit for a large share of arguments, and a
one-ulp difference changes printed output. Always use `jsmath` for them;
`math` is fine for exact operations (`Floor`, `Sqrt`, `Abs`, `Max`, …).

**Constants.** Go evaluates constant expressions exactly and rounds only the
result; JavaScript rounds every intermediate to a double. So a multi-step
constant such as `30 * (math.Pi / 180)` can fold to a different double than
upstream computes. Where a constant expression has more than one rounding
step and feeds output, declare it as a `var` (evaluated at run time with
per-step rounding) or check it against node.

**Performance.** Avoid `interface{}` boxing on hot paths, avoid reflection,
avoid `fmt` in inner loops, preallocate, compile expressions once to closures.
Benchmarks live next to the code (`BenchmarkXxx`).

**Style.** gofmt, `go vet` clean. Package doc comments. Comments explain *why*
and name the non-obvious upstream rule; they do not narrate the code. No
references to other ports of Vega. Exported identifiers only where another
package needs them.

## Oracles and tests

Unit tests use golden vectors recorded from upstream with node. Each package
that needs them keeps a generator under `testdata/` (e.g.
`testdata/gen_ticks.js`) and the recorded output beside it, so tests need
neither node nor network. Generators resolve upstream modules through
`NODE_PATH`, which must point at `purego/testdata/oracle-node/node_modules`:
`purego/testdata/oracle-node/package.json` pins every upstream module to the
exact version the root engine vendors (`cd purego/testdata/oracle-node && npm
ci`). After re-vendoring (`go run ./cmd/vendor-js`), regenerate that
package.json from the manifest so the three stay identical.
Vega 6.x modules are ESM, which ignores `NODE_PATH`, so generators are `.mjs`
files that resolve modules explicitly:

```js
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const d3 = await import(require.resolve('d3-format'));
```

Run as `NODE_PATH=... node testdata/gen_x.mjs > testdata/x.json`.

End-to-end, `purego/compare_test.go` renders the corpus with both engines and
compares SVG structurally (element tree, attributes, numbers within a small
tolerance). Its report is the progress metric.

## Build environment

The module proxy configured on this machine refuses `github.com/mgilbir/*`;
always run Go with `GOPRIVATE='github.com/mgilbir/*'` (it fetches those
modules directly). `GONOSUMDB` is set explicitly in this environment and
therefore not derived from `GOPRIVATE`: when adding or upgrading one of those
modules, also pass `GONOSUMDB='github.com/mgilbir/*'`.

Do not add module dependencies beyond the standard library, `golang.org/x/image`,
`golang.org/x/text`, `github.com/mgilbir/goecma262`, `github.com/mgilbir/forme`,
and (tests/benchmarks only) the root `aster` module's own packages.

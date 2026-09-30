# Security review

The review was made when the engine was the `purego` package, before it
replaced the QuickJS engine as the root package: `purego.go` there is
`aster.go` now, `purego/*_security_test.go` the root package's
`*_security_test.go`, and `purego.New` is `aster.New`. The commands below are
updated; the findings are left as written.

Scope: `purego` (branch `purego-full`) and the shared packages it uses
(`internal/text`, `internal/svgpdf`, `internal/fontsubset`, `internal/loader`,
`internal/pngopt`). Threat model as in the task: the attacker controls the
Vega / Vega-Lite spec, the data the configured `Loader` returns, the SVG given
to `SVGToPNG` / `SVGToPDF`, and fonts given to `WithFont`. The host wants
bounded time and memory, no crashes, no access beyond the Loader's policy and
no injection into the SVG / PDF it gets back.

Method: code reading against `DESIGN.md` ("Security") and proof by concrete
input. Every finding below has a reproducer that was run; "not verified" is
said where it is not. The findings below were written against the unmodified engine (the reviewer added
only `purego/*_security_test.go` files and `testdata/security/`); each has a Status line for the fixes made afterwards.

Timings were taken on a machine with a load average of 100 to 300, so wall
time is unreliable; CPU seconds and peak heap (`runtime.MemStats.HeapAlloc`,
sampled every 20 ms) are the numbers to trust. They are from a single run on
an Apple M1 Pro class machine.

## How to run the reproducers

```sh
export GOPRIVATE='github.com/mgilbir/*' GONOSUMDB='github.com/mgilbir/*'
# cheap, deterministic (injection, loader policy, XML validity, PDF names)
go test . -run 'TestInjection|TestImageURL|TestHref|TestControlChar|TestPDFFontName|TestVegaLiteInvalid'
# heavy: each case runs in a child process and is killed at a 4 GiB heap or a wall-clock cap
ASTER_SECURITY=1 go test . -run 'TestResource|TestTimeout' -v
# sweeps (no failures found; left as regression nets)
go test . -run 'TestExtremeValues|TestTypeConfusion' -args -sec.n=8000 -sec.seed=7
go test . -run TestSVGMutations -args -sec.svgn=6000
go test . -run TestHostileFonts -args -sec.fontn=3000
go test -race . -run TestConvertersDoNotShareState
```

Ad-hoc input: `SEC_PROBE=file SEC_KIND=vega|vl|vl2vega|svgpng|svgpdf|vegapng|vegapdf [SEC_TIMEOUT=1s] [SEC_MEM=bytes] [SEC_LOADER=record|bigcsv:N|bigjson:N] [SEC_CAP=heapMB] go test . -run TestSecProbe -v`.

Files: `harness_security_test.go` (child-process harness, probe),
`injection_security_test.go`, `resource_security_test.go`,
`pdf_security_test.go`, `font_security_test.go`, `extreme_security_test.go`,
`svgmutate_security_test.go`, `race_security_test.go`, `panic_security_test.go`; specs and SVGs in
`testdata/security/`.

## Summary

| # | Severity | Component | Finding |
|---|---|---|---|
| 1 | High | `internal/svg` | Attribute injection through a string `angle` (text, symbol, ...) |
| 2 | High | `internal/svg` | Attribute injection through a gradient `id` (fill / stroke) |
| 3 | High | `internal/expr` | Unbounded string concatenation: 530 B spec needs > 32 GB |
| 4 | High | `internal/transforms` | `impute` (and `kde`, `quantile`, `flatten`) allocate unbounded output; `impute`: 250 B spec, > 12 GB |
| 5 | High | `vega`, `purego.go` | Default limits (5M rows / 5M items, no output cap) allow 7.5 GiB and a 370 MB SVG from a 174 B spec |
| 6 | High | many transforms, `vegalite`, `raster` | `WithTimeout` is not honoured: 18 to 46 CPU-s for a 1 s timeout; Vega-Lite compile, PNG and PDF have no context at all |
| 7 | High | `internal/raster` | PNG rasterizer has no time bound and a multi-GB worst case (497 B SVG: 4.4 GiB) |
| 8 | Medium | `vega`, data formats | `WithMemoryLimit` is checked after parsing / generating: 64 MiB limit still costs 0.7 to 6 GiB |
| 9 | Medium | `internal/geo` | Projection resampling has no point budget: 515 B spec yields a 700 MB SVG |
| 10 | Medium | `internal/svg` | Image URLs bypass `Loader.Sanitize` |
| 11 | Medium | `purego.go`, `internal/loader` | `href` scheme allow-list is dropped when the Loader is permissive (`StaticLoader`): `javascript:` links |
| 12 | Medium | `internal/svg` | CSS declaration injection through `blend` |
| 13 | Medium | `vegalite` | Compile time is unbounded, quadratic in `params`; a 5.8 compile holds a process-wide write lock |
| 14 | Low | `internal/svg` | C0 control characters / U+FFFE are written raw: output is not well-formed XML, `VegaToPDF` fails |
| 15 | Low | `purego.go`, `internal/fontsubset` | `SubsetFont` returns an unsanitized PostScript name; `SVGToPDF` and `SubsetFont` have no `recover` |
| 16 | Low | `internal/loader` | `FileLoader.Load` has no size cap; `HTTPLoader` 64 MiB default times parse amplification |
| 17 | Low | `internal/vegalite` | Compiler panics (nil dereference) on an unknown `mark` type and on a non-drag `translate`; recovered as "internal error" |

Verified safe (no finding): expression sandbox, expression / JSON / group
nesting, XML entities in `SVGToPNG` / `SVGToPDF`, `data:` image limits, PDF
name escaping, TopoJSON, font parsing, panics in the Vega runtime, rasterizer,
PDF writer and text stack, data races. Details at the end.

---

## 1. High: attribute injection via `angle`

**Status: Fixed. `appendAngle` escapes the raw value with the attribute escaper (valid numeric angles are byte-identical); `clip-path` references also go through `attr`.**

- Reproducer: `TestInjectionRotateAngleString/{text,symbol}`;
  `testdata/security/attribute-injection.vg.json`.
- Input: `{"type":"text","encode":{"update":{"angle":{"value":"1)\" onmouseover=\"alert(1)\" data-x=\"("}}}}`.
- Output: `<text ... transform="translate(10,10) rotate(1)" onmouseover="alert(1)" data-x="()" ...>`.
- Root cause: `internal/svg/style.go` `appendAngle` appends `v.AsString()` of the raw
  (non-number) property straight into the `transform` attribute
  (`w.buf = append(dst, ...)`), with no escaping. Used from `render.go`
  (two places) and `textimg.go`. Every other attribute goes through the escaping
  `attr` / `attrBytes`; this one writes bytes directly.
- Impact: XSS when the SVG is inlined in HTML; the attacker adds event
  handlers or any attribute to `<text>` / `<path>` elements.
- Fix: write through `appendEscaped(..., true)`, or better parse the value
  with `Number()` semantics and write a number (a non-numeric angle renders
  `rotate(NaN)` upstream anyway). Audit every `w.buf = append(w.buf, <string>...)`
  in `internal/svg` (the remaining ones are numeric or constant).

## 2. High: attribute injection via gradient id

**Status: Fixed. `paintAttr` writes `url(#id)` through the escaping `attr`; output for valid ids is unchanged.**

- Reproducer: `TestInjectionGradientID/{fill,stroke}`.
- Input: `fill: {"signal":"{gradient:'linear',id:'q\" onload=\"alert(5)',stops:[...]}"}`.
- Output: `<path d="..." fill="url(#q" onload="alert(5))"/>`. (The `<defs>` copies are escaped correctly; the reference is not.)
- Root cause: `internal/svg/style.go` `paintAttr` calls `attrRaw(name, r.gradientRef(g))`;
  `gradientRef` (`defs.go`) returns `"url(#" + g.ID + ")"` where `g.ID` is
  spec-controlled. `attrRaw` is documented as "cannot contain characters
  needing escapes", which is false here.
- Fix: use `attr` (escaping). Also consider restricting gradient ids to
  `[A-Za-z0-9_.:-]`, since an id containing `)` or whitespace produces a
  reference no consumer resolves.

## 3. High: unbounded string concatenation

**Status: Fixed. `Scope.checkLen` (per-string limit, `MaxStringLength`) runs before `+`, `join`, `pad`/repeat, `replace` (with a pre-check for regexp replacement), array stringification; strings over 4 KiB and arrays over 256 elements from `sequence()` are also charged to a render-wide `expr.StringBudget` (128 MiB default, `vega.Limits.MaxStringBytes`).**

- Reproducer: `TestResourceStringConcatenationBounded`;
  `testdata/security/string-doubling.vg.json`.
- Input: 12 signals, `s0 = pad('x', 16000000)`, `s_i = s_{i-1} + s_{i-1}`. A single
  expression can do the same with `join([a,a,a,...])` (64 copies of a 16 MB
  string cost 1.9 GB in 1 s).
- Result: heap passes 7.8 GiB within a few hundred ms (the final string would be 32 GB);
  the host is OOM-killed. `WithMemoryLimit` and the context do not apply.
- Root cause: `MaxStringLength` (16M, `expr/fn_string.go`) is enforced by `pad`/`truncate`
  only. `Scope.add` (`expr/js.go`) and `join` (both verified) do not check it; other
  string builders (`replace` with a long replacement over many matches, array / object
  stringification) were not tested and probably do not either.
- Fix: one `checkStringLen` applied wherever a string is built (concat, join,
  replace, array/object stringification); use V8's limit (2^29 - 24 UTF-16 units)
  or, better, `MaxStringLength`. Consider a per-render budget of total bytes of
  strings created by expressions.

## 4. High: transforms that allocate their whole output with no limit

**Status: Fixed. `internal/budget` travels in the context; `impute` (cells and output), `kde`/`density`, `quantile`, `regression`, `flatten`, `fold`, `cross`, `sequence`, `aggregate` cells x measures and `pivot` (plus a rows x columns cap) reserve their output before allocating. Unit tests: `transforms/limits_test.go`.**

- Reproducers: `TestResourceImputeCrossProduct`, `TestResourceKDEGroupsTimesSteps`,
  `TestResourceFlattenArrays`;
  `impute-cross-product.vg.json` (245 B), `kde-groups-steps.vg.json` (249 B),
  `flatten-sequence-arrays.vg.json` (187 B).
- `impute`: 20,000 rows each with a distinct group and key produce 4e8 imputed
  tuples. Heap reached 7.1 GiB in 5.5 s even with `WithTimeout(2s)`, and passed
  12 GiB with the default 30 s timeout (killed by the harness). `cross` and
  `aggregate` have `MaxGroupCells`; `impute` has nothing (`transforms/impute.go`).
- `kde` / `quantile` with `groupby`: `MaxSteps` (1e6) applies per group, groups are not limited;
  2000 rows / 1000 groups, `steps:1e6`: 3.1 GiB in 2.6 s.
- `flatten` of rows whose arrays come from `sequence(1000000)`: 1000 rows -> 1e9 tuples, 2.4 to 3.5 GiB in 1 s.
- Root cause: the only row accounting is `runView.checkRows(len(out)-len(in))`
  in `vega/ops_transform.go`, evaluated after the transform returned its full slice,
  and `MaxGroupCells` / `MaxSteps` are per call, not per render.
- Fix: a per-render row budget object (`Charge(n)` that returns `ErrLimit` and
  also polls the context) passed to every transform through `contextT`, charged
  before allocating (`impute`: `len(groups)*len(keys)`; `kde`/`quantile`/`density`/
  `regression`: `groups*steps`; `flatten`: sum of array lengths; `fold`: rows*fields;
  `cross`; `lookup` with multiple matches; `pivot`).

## 5. High: defaults allow multi-GiB renders from tiny specs

**Status: Fixed. Defaults lowered: 1M rows, 500k items, 20k facet cells, 64 MiB loaded, 2M path points, 128 MiB expression strings; SVG output capped at 128 MiB (`svg.Options.MaxBytes`; a quarter of `WithMemoryLimit` when set). The 4M-row reproducer now fails in milliseconds at 6 MiB. Remaining: there is still no per-item memory cost model (long strings and paths per item are bounded only by the string budget and the SVG cap).**

- Reproducer: `TestResourceDefaultLimitsBoundHeap`; `seq-4m-rows.vg.json` (174 B):
  `sequence 0..4,000,000` + a `symbol` mark. With the defaults it needs 7.5 GiB
  heap and 17 s and returns a 370 MB SVG (run with a 16 GB cap).
- Root cause: `Limits.withDefaults` (`vega/view.go`): 5M rows, 5M items; nothing caps
  scene memory per item (paths, strings), the SVG size, or the number of path
  points; the default `purego.New()` has `memoryLimit == 0` so `limits()` leaves
  the defaults.
- Fix: lower defaults to numbers a real chart needs (for example 1M rows / 500k
  items), add `Limits.MaxOutputBytes` (and a path-point budget, see 9), and make
  `WithMemoryLimit` default to something finite (for example 1 GiB) or document
  loudly that `0` is unsafe for untrusted specs.

## 6. High: `WithTimeout` is not honoured

**Status: Fixed for every reproducer (each stops within about 1 s wall / 1.3 s CPU at `WithTimeout(1s)`): bootstrap (`BootstrapCICtx`), polynomial fit (`FitRegressionCtx`), aggregate/pivot (work-based `budget.Ticker`), window frames, force (`sim.halt` in collide, many-body and link forces), geo paths (`Path.Bind`), scene bounds (`Bounder.Context`), SVG writing, raster, PDF, and Vega-Lite compile (context passed to `vegalite.Options`). Remaining: JSON parsing of the spec and of loaded data is not interruptible (bounded by size; loaded bytes are capped), and the legend with 90k entries costs about 3 CPU-s before the facet-cell limit stops it.**

`DESIGN.md` says "Honour `context.Context` cancellation in every loop that can
run long". Measured with `WithTimeout(1s)`, a single render, 160 to 250 byte
specs (`TestTimeoutIsHonoured`):

| Input | CPU seconds | Where |
|---|---|---|
| `aggregate` `ci0`/`ci1` on 200k rows (`ci-bootstrap-200k.vg.json`) | 46 to 48, returns with no error | `transforms.BootstrapCI` (`measure.go:412`) does 1000 resamples x n with no context |
| `regression` `poly` order 100, 200k rows | 45, no error | fit in `regression_methods.go` (not profiled); context only checked per group |
| `pivot` with 200k distinct values | 27 | `Pivot` (`pivot.go:99`) hands one measure per distinct value to `Aggregate`; apparently quadratic (not profiled) |
| `window` median, frame +/-1e6, 200k rows | 23 | per-row work over the frame (not profiled); the context is polled per row only |
| `legend` with 90k entries | 18 | legend layout, super-linear (not profiled), no polling |
| `force` collide, 100k nodes, radius 1e9 | > 90 s wall (killed) | context is checked between ticks only; one tick is O(n^2) (`transforms/force/force.go`) |
| formula `pad('x',16e6)` over 20k rows | 15 | one row is 16M characters; polled per row |
| 20k text items of 500 characters | 12 | text bounds/measure phase does not poll |
| projection scale 1e12 (finding 9) | 9 | geo path generation |

Phases without any context:

- Vega-Lite compile (`vegalite.Compile`, called from `compileVegaLite`): see 13.
- JSON parse of the spec (bounded by depth only).
- `SVGToPNG`, `VegaToPNG`'s raster phase: `raster.RenderPNG([]byte(svg), ...)` (see 7).
- `SVGToPDF`: `svgpdf.ConvertWithUsage(svg, m, opts)`, including text shaping.
- `WithMemoryLimit` also does not apply to these.

Fix: give every loop that can exceed ~10 ms per call a `poll` (the helper
already exists in `transforms/util.go`), thread a `ctx` (or a `*Budget`) into
`BootstrapCI`, regression/poly, `Pivot`'s aggregate, `Window` frame evaluation,
legend/guide layout, `collide`/`nbody`/link forces (poll per node batch, cap
`iterations`), the mark-encode and bounds phases, `vegalite.Compile`,
`raster.Render`, `svgpdf.Convert`. A regression test per item is already in
`TestTimeoutIsHonoured`.

## 7. High: rasterizer has no time bound and a large memory multiplier

**Status: Fixed before this round (rasterizer context, canvas memory and pixel limits); reproducers pass.**

- Reproducers: `TestResourceRasterizer/{layers,use-expansion-fullcanvas,rects-1000-fullcanvas}`;
  `layers-8k.svg` (497 B), `use-expansion-fullcanvas.svg` (899 B), `rects-1000-fullcanvas.svg` (59 KB).
- `layers-8k.svg`: 16 nested `<g opacity="0.5">` on an 8000x8000 canvas: peak heap
  4.4 GiB (each layer is a full 256 MiB canvas; `MaxLayerDepth = 16`,
  `MaxPixels = 64M`), 12 s. A plain 8000x8000 rect already costs 0.5 GiB.
- `use-expansion-fullcanvas.svg`: 2048 full-canvas 4000x4000 fills through `<use>` (under
  the 8M render-node budget): did not finish in 90 s.
- `rects-1000-fullcanvas.svg`: 1000 full-canvas translucent rects at 2000x2000: 21 CPU-seconds.
- Root cause: limits bound elements (`MaxRenderNodes` 8M) and pixels per canvas
  (`MaxPixels` 64M) but not their product, nor layer memory; the rasterizer has
  no deadline (`raster.Options` has no `Context`).
- Fix: add a pixel-work budget (sum over shapes of covered pixels, layers counted
  by area) and a deadline/context polled per shape and per scanline batch; cap the
  total layer memory (`MaxLayerDepth * canvas` is 4 GiB at the defaults) or composite
  layers at their dirty bounds; have `SVGToPNG` honour `WithTimeout` and
  `WithMemoryLimit` (for example by lowering `MaxPixels`).

## 8. Medium: `WithMemoryLimit` is enforced after the allocation

**Status: Fixed. `sequence` reserves rows before allocating; `parseDSVLimit` fails before creating the row that would exceed the remaining row budget (and 16 cells per row), polls the context, and loaded bytes are charged (`budget.Load`). `WithMemoryLimit` also scales loaded bytes, expression strings and SVG size. Remaining: JSON data is parsed before its rows are counted (size-capped by `MaxLoadBytes`).**

- Reproducers: `TestResourceMemoryLimitIsEnforcedBeforeAllocation/{sequence,csv-from-loader}`.
- `sequence 0..4M` with `WithMemoryLimit(64 MiB)` (262,144 rows allowed): fails with
  "data exceeds the limit of 262144 rows" after peaking at 677 MiB, because
  `transforms.Sequence` makes all `n <= MaxSequence (10M)` tuples first.
- A 32 MiB CSV of one-character rows (`SEC_LOADER=bigcsv:32`, served by a Loader the
  host trusts to be bounded at 64 MiB like `HTTPLoader`) needs 5.6 GiB and 29 to 41 s,
  ignoring both `WithTimeout(5s)` and `WithMemoryLimit`, and then fails with "data exceeds the limit".
  The parsers (`vega/parse_data.go` read path; CSV / JSON / TopoJSON) create every
  row before `checkRows`, and do not poll the context.
- Fix: pass the remaining row budget into the format readers and `Sequence`
  (fail as soon as it is exceeded), poll the context while parsing, and cap
  bytes per `Load` result independent of the Loader. (The 256 B per row used by
  `limits()` is about right: measured 170 to 175 B per row for `sequence` and
  one-column CSV rows; the problem is when it is applied, and that strings,
  paths and output are not counted.)

## 9. Medium: no point budget in projection resampling

**Status: Fixed. Every point a `geo.Path` emits is charged to the render's budget (`MaxPoints`, 2M) and the context is polled; the scale 1e12 reproducer fails in 0.2 s at 7 MiB.**

- Reproducer: `TestResourceProjectionScale`; `projection-scale-1e12.vl.json` (515 B,
  an orthographic globe with `scale: 1e12`). 3.3 GiB peak, 9 CPU-s, 695 MB SVG;
  scale 1e15 passes 7.7 GiB. The extreme-value sweep found 4 such hangs
  (`projection.scale` 1e15 / 1e21 in Vega-Lite and Vega examples).
- Root cause: `geo/resample.go` subdivides each segment to `resampleMaxDepth = 16`
  (2^16 points per segment) when the projected midpoint is far from the chord, which
  at huge scales is always; no context, no total budget. The path string builder
  (`scene.StringPath`) and `svg` writer then materialise it.
- Fix: count emitted points in the resampler / path context and fail past a budget
  (for example 5M per render); poll the context in `GeoPath`'s stream callbacks; consider
  clamping `scale`.

## 10. Medium: image URLs bypass the Loader

**Status: Fixed. `purego.go` sets `svg.Options.Image`: the URL goes through `Loader.Sanitize`, then vega-loader's allow-list; a rejected URL renders an empty source (what a failed load renders). The Loader is called without a distinct "image" context because `Loader.Sanitize` has no options parameter.**

- Reproducers: `TestImageURLGoesThroughLoader`, `TestImageURLIsOfferedToLoader`.
- With the default `DenyLoader`, `{"type":"image","encode":{"update":{"url":{"value":"http://169.254.169.254/..."}}}}`
  yields `<image xlink:href="http://169.254.169.254/...">`. `Loader.Sanitize` is never
  called (`svg/textimg.go` `image()` uses the static `SanitizeURL`; `purego.go` does not set `svg.Options.Image`).
  `file:` URLs are accepted too (and the `file://` prefix is stripped).
- The engine itself never fetches the URL (`raster` accepts only `data:` URIs), so this
  is a policy bypass for whoever renders the SVG next (browser, another converter),
  and it breaks the README / DESIGN promise that resources go through the Loader.
- Fix: in `renderSVG` set `so.Image` (or an `ImageHref` hook) to call `c.cfg.loader.Sanitize`
  and drop the URL on error, like `so.Href`. `data:image/png|jpeg|gif` could be allowed
  without the Loader.

## 11. Medium: href scheme allow-list lost with a permissive Loader

**Status: Fixed. The `Href` hook applies `svg.SanitizeURL` to the URI and to the Loader's result in addition to `Loader.Sanitize`. Not done: `StaticLoader.Sanitize` still accepts every URI and `FallbackLoader.Sanitize` still returns the original URI.**

- Reproducer: `TestHrefSchemeAllowListWithPermissiveLoader`.
- `purego.go` replaces upstream's `SanitizeURL` with `Loader.Sanitize`. `StaticLoader.Sanitize`
  (and a `FallbackLoader` that contains one) returns every URI, so
  `href: "javascript:alert(document.cookie)"` becomes `<a xlink:href="javascript:...">`.
  `HTTPLoader` and `FileLoader` reject non-http(s) / any scheme, so the default loaders are safe;
  the hole is in the documented public loader `StaticLoader`, and in any custom Loader written
  assuming "Sanitize is about fetching data".
- Fix: apply `svg.SanitizeURL` (the vega-loader allow-list) to the href in addition to
  `Loader.Sanitize`, in the `so.Href` closure. Also make `StaticLoader.Sanitize`
  reject or restrict schemes, and have `FallbackLoader.Sanitize` return the accepting child's sanitized value.
  (`TestHrefRejectedByLoaderIsDropped` guards the working path.)

## 12. Medium: CSS injection through `blend`

**Status: Fixed. `blend` is written only when it is one of the CSS mix-blend-mode keywords; otherwise the style is omitted. Paint strings containing `url(` are still passed through (not restricted).**

- Reproducer: `TestInjectionBlendCSS`.
- `blend: "normal;background:url(http://attacker.example/x);position:fixed"` gives
  `style="mix-blend-mode: normal;background:url(http://attacker.example/x);position:fixed;"`.
  The attribute cannot be broken out of (quotes are escaped), but the spec author controls the CSS of the
  element: network fetches from an embedding page, UI redressing with `position:fixed` in inline SVG.
  `internal/svg/style.go` writes `it.Blend` with `appendEscaped`, which is HTML-escaping, not CSS validation.
- Fix: allow-list the CSS blend-mode keywords (`normal multiply screen overlay darken lighten color-dodge color-burn hard-light soft-light difference exclusion hue saturation color luminosity`), otherwise skip the style. Consider the same for `cursor`
  and any other value that ends up in CSS (none found besides this one), and for `fill` / `stroke` strings
  containing `url(` to an external resource (paint values are passed through untouched).

## 13. Medium: Vega-Lite compilation is unbounded and can block other converters

**Status: Fixed. Selection signal/data assembly was quadratic (linear signal lookups, copy-on-append); 4,000 point params now compile in 0.3 s instead of 4.8 s. `vegalite.Options.Context` is checked throughout compilation and `purego.go` passes the call's context. The version flag and its process-wide lock are gone: compilations share no mutable state. Remaining: thousands of interval params still allocate quadratically, because the output itself is quadratic (each selection test lists every brush, as upstream does).**

- Reproducer: `TestTimeoutCoversVegaLiteCompile`; `params-4000.vl.json` (135 KB of `params` with `select: "point"`).
  `VegaLiteToVega` takes about 5 CPU-s for 4000 params and grows quadratically
  (2000: 1.1 CPU-s, 4000: 4.8 CPU-s); by extrapolation a 1 MB spec takes minutes. `WithTimeout` is not involved.
- Root cause: `assembleUnitSelectionSignals` -> `findSignal` / `findSignalIndex` linear scans per
  signal; there is no ctx in `vegalite.Compile`.
- `vegalite/version.go`: a `WithVegaLiteVersion("5.8")` compile takes `versionMu.Lock()` for its whole duration,
  so any long 5.8 compile stalls every 6.4 compile in the process (all Converters). No data race
  (`TestConvertersDoNotShareState` under `-race` is clean), but it is a cross-tenant stall.
  README says 5.8 errors; `supportedVersions` actually lists it.
- Fix: thread a context (or a work counter) through compile and check it at the `Model` level; index signals by name in a map;
  cap `len(params)`, `len(layer)`, `len(concat)`; replace the global `v5` with a compile-context field
  as the comment in `version.go` already considers.

## 14. Low: control characters make the SVG invalid XML

**Status: Fixed. `appendEscaped` (text and attributes) drops C0 controls other than tab/LF/CR and U+FFFE/U+FFFF and replaces invalid UTF-8 and lone surrogates with U+FFFD; deliberate divergence from upstream, documented in the function.**

- Reproducer: `TestControlCharactersKeepOutputWellFormed`; `control-characters.vg.json`.
- `text: "a\u0001b\u0008c￾d"` is written raw. XML 1.0 forbids these characters
  (U+FFFE is also written raw; lone surrogates are already replaced by U+FFFD). Browsers refuse the whole image,
  and `VegaToPDF` fails with `svgpdf: parsing SVG: XML syntax error on line 1: illegal character code U+0001`
  (`VegaToPNG` is tolerant). One control character in a data field therefore breaks PDF export of the chart,
  and differing parsers disagree about it.
- Fix: in `svg.writer.text/attr/attrBytes` drop or replace characters outside `#x9 | #xA | #xD | [#x20-#xD7FF] | [#xE000-#xFFFD] | [#x10000-#x10FFFF]` (upstream escapes nothing, so
  this is deliberate divergence; say so in the comment).

## 15. Low: `SubsetFont` name, missing `recover` in PDF entry points

**Status: Fixed. `fontsubset` sanitizes the PostScript name it returns (the same rule as `/BaseFont`: `!`–`~` minus PDF delimiters, 63 characters) and `Parse`/`Subset` recover panics; `svgpdf.ConvertWithUsage` recovers panics, and `purego`'s PDF path recovers too.**

- `TestPDFFontNameInjection`: the PDF itself is safe (`/BaseFont /A#29#3E#3E#2FEvil#3C#3C#2FB#28`), but
  `SubsetFont` returns the raw PostScript name from the font file (`A)>>/Evil<</B(`), which hosts that
  assemble their own PDFs (as the API encourages) would write unescaped. `fontsubset.postScriptName` should
  restrict to printable ASCII without PDF delimiters (the PostScript name rules: 1-63 characters, `[!-~]` minus `[](){}<>/%`).
- `Converter.SVGToPDFUsage` and `SubsetFont` do not recover, unlike `renderSVG`, `raster.Render`
  and `text`. 6000 mutated SVGs and 3400 mutated fonts found no panic, so this is defence in depth only.

## 16. Low: loader hardening

**Status: Fixed. `FileLoader.MaxBytes` caps file size (64 MiB by default, negative for unlimited, as `HTTPLoader.MaxResponseBytes`); `Load` refuses non-regular files and reads through a `LimitReader`. Loaded bytes are also counted against the per-render budget (`budget.Load`).**

- `FileLoader.Load` reads the whole file (`root.ReadFile`) with no size cap and no context;
  a FIFO or very large file inside the base directory blocks or exhausts memory (not verified; code reading).
- `HTTPLoader` caps at 64 MiB by default, but a 64 MiB CSV expands to about 5 GiB (finding 8). Consider a render-level
  byte budget across all `Load` calls, and caching repeated URLs.
- `DNS rebinding` is documented as not covered.

---

## Checked and found sound

- **Expression sandbox**: about 50 escape attempts (`constructor`, `__proto__`, method calls, `eval`, `Function`,
  `this`, `window`, `Math.*`, `JSON.*`, `new`, `import`, template literals, regex literals, `toString`, `delete`, `++`,
  arrow functions) are parse errors, unknown signals, or evaluate to undefined. Only the function table is callable.
- **Recursion**: `jsval.ParseJSON` rejects depth > 256; expression parser depth 512 (tested with 40,000-deep
  `+`, `!`, `(`, `abs(`, `[`, `{a:`, `?:`, `.a`, `[0]`, `=`: all clean errors); `svgpdf` depth 512;
  `raster` depth 256; hierarchy depth/size (`ErrTooLarge`) on 200k-deep chains; group nesting is bounded by JSON depth.
- **Regular expressions** go through goecma262 `*Err` methods with budgets: `(a+)+$`, `(a*)*b`, `(x+x+)+y` on 60 to 100,000
  character inputs, and `countpattern` over 200k rows, return promptly.
- **Other limits that work**: `sequence` (10M, expression `sequence()` 1M), `bin` maxbins and step search, tick counts
  (`tickCount:1e9`, `nice:1e9`), 100k-tick axes, `cross` (4M cells), `force` iterations (100k), `contour` raster (16M cells) and
  levels, `graticule` point budget, repeat (10,000 views), `pad` (16M), `regression` order (100), `quantile` steps (1M), label bitmap,
  wordcloud / label (canvas needed: unsupported).
- **XML entities**: `encoding/xml` and the `raster` parser do not expand entities or read external ones
  (`entities.svg`: 8-level billion-laughs DOCTYPE plus `SYSTEM "file:///etc/passwd"` renders in 30 ms / 7 MiB and no
  file is read). `<style>` `@import` and external `<image>` are not fetched; only `data:` images decode, with size and
  pixel limits (300 `<use>` of one 62 KB 8000x8000 PNG cost 0.3 s and 312 MiB).
- **Text escaping** in element content and in all `attr()` paths is correct (`<`, `>`, `&`, `"`, tab, LF, CR);
  tested with title, subtitle, description, aria labels, names, roles, styles, fonts, colours, strokes, `opacity`, `strokeDashOffset`,
  `strokeDash`, `href`, `x`, `dx`, `radius`. The only raw writers are findings 1, 2 and 12.
- **PDF**: names are hex-escaped by `pdf0.Name`; text is written as hex glyph strings.
- **Panics / internal errors**: 8,000 extreme-numeric mutants of the corpus: no panic; the only non-returning mutants are finding 9.
  About 21,000 type-confusion mutants: panics only in the Vega-Lite compiler (finding 17), none in the Vega runtime, no hang.
  6,000 mutated SVGs through `SVGToPNG` and `SVGToPDF`, and 3,400 structurally mutated fonts through `WithFont`, `VegaToSVG`, `SVGToPNG`,
  `SVGToPDF`, `SubsetFont`: no panic, no "internal error", no crash.
- **Concurrency**: no data race with 6 Converters (Vega, Vega-Lite 6.4 and 5.8, SVG / PNG / PDF) under `-race`, and none with 12 goroutines
  sharing one Converter over the whole corpus (`TestConcurrentConverterSharesNothing`, `ASTER_STRESS_FULL=1`; every SVG equals the serial
  render) or with `Close` racing renders (`TestConcurrentCloseDuringRender`). Package-level state is read-only tables, `sync.Once`
  initialisations and mutex-guarded caches (`expr` case, pattern and compiled-program caches, `format`, `scale` schemes, `raster` fonts,
  custom symbol paths, the SVG buffer pool); everything else (dataflow, scenegraph, projections and their cached geo pipelines, random
  generator, budgets, deadline) is per render. A Converter is safe for concurrent use; its `Loader` must be too.
- **Loader policy** for data: every data load goes through `Sanitize` then `Load`; `HTTPLoader` checks userinfo, scheme, domain
  and every redirect hop; `FileLoader` uses `os.Root` and rejects schemes, absolute paths and `..`.

## 17. Low: Vega-Lite compiler panics on invalid input

**Status: Fixed. Every panic reachable from spec input that a 7,000-mutant type-confusion sweep found is now a descriptive error (unknown or mistyped marks, non-drag translate, malformed `select.on`/`between`, lookups without a source, extents of selections without projected fields, a 5.8 legend without a scale, null scales). Upstream throws bare TypeErrors there.**

- Reproducer: `TestVegaLiteInvalidSpecsDoNotPanic` (`panic_security_test.go`); the type-confusion sweep
  (`go test . -run TestTypeConfusion -args -sec.n=7000 -sec.seed=20`) finds about 250 more per run,
  all in the compiler (`vegalite: internal error`), none in the Vega runtime.
- `"mark": "nope"` (also an array, or `{"type": {}}`, or inside `layer`) -> nil pointer dereference at
  `internal/vegalite/mark.go:408` (`getMarkGroup`, the mark compiler lookup returns nil).
- selection `translate: "__proto__"` (any string that is not a drag selector) -> nil dereference at
  `internal/vegalite/selection.go:1591` (`e.Get("between").Index(0).ObjValue()`).
- Also seen, not minimised: `encoding.x.field: null` in a `vconcat` with an interval parameter ("index out of range [0] with length 0")
  and `select.on: [1,2]`.
- Impact: none for the host (`vegalite.Compile` recovers), but the caller gets "internal error" instead of a
  message about the invalid spec, and every such panic is a path whose state (lock, lazy signals) relies on
  the deferred cleanup. Fix: validate `mark` against the known mark types in `normalize`, and check the
  selector shape before indexing.

## Documentation to fix with the code

`DESIGN.md` Security section claims: "Honour `context.Context` cancellation in every loop that can run long" (see 6), "bound ... string
repeats in `pad`" (concatenation is not bounded, 3), and "Data loading only through the configured `Loader`" (true for loads, not for
the image URLs written to the output, 10). `README.md` says `WithMemoryLimit` "bounds the rows and scene items a render may create";
it does so only after the fact (8) and not for strings, paths, SVG output, PNG or PDF.

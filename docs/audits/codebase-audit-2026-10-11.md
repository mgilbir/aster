# Codebase audit — 2026-10-11

Scope: the whole repository at `main` = `0424529` (after PR #104): the public package, `cmd/aster`, every package under `internal/`, the docs, scripts and CI. That is about 135k lines of non-test Go, all read. Eleven reviewers each read one slice in full. The lead read the public surface and the CLI, then re-ran the headline claims of every Critical finding and most High findings against a CLI built from `main` (`go build -o /tmp/aster-audit/aster ./cmd/aster`).

The threat model comes from `docs/security.md:7`: the attacker controls the spec, the data a Loader returns, the SVG given to `SVGToPNG`/`SVGToPDF`, and fonts given to `WithFont`. The bar for a resource finding is `docs/limits.md`: "a render that would exceed a bound fails … instead of exhausting the host", and `WithTimeout` "bounds one call from start to finish".

Verification labels:

- **CONFIRMED-R(lead)**: re-run by the lead, with the numbers given.
- **CONFIRMED-R**: reproduced by the slice reviewer with a scratch test or the CLI.
- **CONFIRMED-T**: traced through the code, not run.
- **PLAUSIBLE**: suspected, not proven.

Upstream-matching behaviour was not filed unless it is a safety issue. Where a finding still matches upstream, it says so.

Line numbers were re-checked by the lead. Where a reviewer's number was off, the verified one is used.

---

## 1. Summary

**Counts:** Critical 9 · High 24 · Medium 38 · Low 33 · Info 5 (109 findings).

| ID | Sev | Area | Issue | Location | Status |
|---|---|---|---|---|---|
| C1 | Critical | SVG output | A string `x`/`y`, from the spec or **data**, is written unescaped into `transform`, so `<script>` can be injected | internal/scene/props.go:539-544 | CONFIRMED-R(lead) |
| C2 | Critical | transforms/voronoi | 8 finite inline points make `cell()` loop forever, then the process dies of a fatal OOM that recover cannot catch | internal/transforms/voronoi/diagram.go:149-173 | CONFIRMED-R(lead) |
| C3 | Critical | geo | A huge `clipAngle` makes `circleStream` loop forever; the timeout and Close are ignored | internal/geo/circlestream.go:27 | CONFIRMED-R(lead) |
| C4 | Critical | vegalite | `jsSet` index-key ordering is cubic; an 8 KB spec runs for seconds and 70 KB for hours, ignoring the timeout | internal/vegalite/dyn.go:591 (via deepClone :311, spread :126) | CONFIRMED-R(lead) |
| C5 | Critical | geo/vega data | TopoJSON decode expands arcs × references with no budget and no ctx | internal/geo/topojson.go:86,157; internal/vega/dataload.go:176 | CONFIRMED-R(lead) |
| C6 | Critical | raster | Glyph and path flattening is unbudgeted and not culled; `MaxPathSegments` is dead; a 4 KB spec takes 917 MB | internal/raster/text.go:576; internal/raster/dom.go:327 | CONFIRMED-R(lead) |
| C7 | Critical | raster | Decoded images are cached per `<image>` element and never charged; 75 KB spec → 929 MB | internal/raster/image.go:236 | CONFIRMED-R(lead) |
| C8 | Critical | expr | The size of a regex program is not charged and global caches keep it; a 419-byte spec → 923 MB | internal/expr/parse.go:687; internal/expr/fn_string.go:248 | CONFIRMED-R(lead) |
| C9 | Critical | format | `config.locale` strings amplify label bytes without bound; a 2 KB spec → 67 MB SVG at 1.2 GB RSS | internal/format/numlocale.go:426; timeformat.go | CONFIRMED-R(lead) |
| C10 | High | vegalite/expr | An explicit `sort` array of 200+ items (and long `or`/`+` chains) fails to compile, though upstream accepts it | internal/vegalite/exprdeps.go:37; datanodes.go:265 | CONFIRMED-R(lead) |
| C11 | High | vega | One global rerun counter: 17+ independent data triggers fail with ErrLimit under default settings | internal/vega/env_data.go:177 | CONFIRMED-R(lead) |
| C12 | High | PDF/text | Font-string regex reads `font-weight` 450 or 1000 as the font size; PDF text is drawn at 450 pt | internal/text/css.go:33; internal/svgpdf/text.go:371 | CONFIRMED-R(lead) |
| C13 | High | vega budget | MaxRows is not enforced: most row-creating paths are never charged and filters credit negative rows | internal/vega/ops_transform.go:242; dataload.go:39,59 | CONFIRMED-R(lead) |
| C14 | High | guides | The `tickMinStep` loop is O(count) before the tick cap, with no ctx check (6.5 s at a 2 s timeout) | internal/vega/guides/scalehelpers.go:142 | CONFIRMED-R(lead) |
| C15 | High | vegalite | Nested repeat and layer repeat multiply past `maxRepeatChildren`; a 1 KB spec reaches 985 MB in 2 s | internal/vegalite/normalize.go:626, mapLayerRepeat | CONFIRMED-R(lead) |
| C16 | High | vegalite | Signal naming for projected selection fields is quadratic with no ctx (96 s at a 1 s timeout) | internal/vegalite/selection.go:712 | CONFIRMED-R |
| C17 | High | geo | Resampling multiplies points before the only point charge and ctx poll; returns success after the deadline | internal/geo/path.go:38; resample.go; cliprectangle.go | CONFIRMED-R |
| C18 | High | vega/geo | `geoArea`/`geoBounds`/`geoCentroid` run on an unbound Path, with no point budget and no ctx | internal/vega/env_geo.go:32 | CONFIRMED-R |
| C19 | High | vega data | The DSV cell guard is 16×maxRows; a 689 KB CSV with a wide header allocates 1.6 GB | internal/vega/dataload.go:372 | CONFIRMED-R |
| C20 | High | vega | Warnings embed spec text, repeat per facet cell and are kept without bound | internal/vega/report.go:50 | CONFIRMED-R |
| C21 | High | expr | `indexof`/`lastindexof` are O(n·m) with no tick and ignore the timeout | internal/expr/utf16.go:136 | CONFIRMED-R |
| C22 | High | expr | The `extent` skip loop has no tick, and `charAt` re-encodes the whole string on every element | internal/expr/fn_seq.go:256 | CONFIRMED-R |
| C23 | High | expr | `split` and `timeSequence` build large arrays without charging the string/array budget | internal/expr/fn_string.go:394; fn_time.go:165 | CONFIRMED-R |
| C24 | High | transforms | kde2d grids (about 512 MB per group) are never charged | internal/transforms/contour/transforms.go:67 | CONFIRMED-R |
| C25 | High | transforms | The window `values` op clones its frame per row: O(n·frame) memory with no budget | internal/transforms/measure.go:421 | CONFIRMED-R |
| C26 | High | transforms | Pivot charges a 128-byte state per cell as 1/16 of a row (1.5 GB under a 256 MiB limit) | internal/transforms/aggregate.go:84 | CONFIRMED-R |
| C27 | High | transforms | Contour hole assignment is O(holes×ring) with no ctx (success after the deadline); ring memory is not charged | internal/transforms/contour/marching.go:125, emit :266 | CONFIRMED-R |
| C28 | High | raster (SVG input) | feMerge cost scales with its merge nodes, which are not counted and never poll ctx (90 s at a 200 ms timeout) | internal/raster/filter.go:93 | CONFIRMED-R |
| C29 | High | PNG | `WithQuantizePNG` runs outside ctx and limits; 13 s at a 1 s timeout; docs say it is "always safe" | internal/pngopt/quantize.go:203; aster.go:816 | CONFIRMED-R |
| C30 | High | raster | Filter intermediates fall outside MaxCanvasBytes under a fixed 1 GiB cap | internal/raster/filter.go:36 | CONFIRMED-R |
| C31 | High | fonts | A hostile font's duplicate table records (FontData ×316) or overlapping `loca` (SubsetFont 140 KB → 216 MB) amplify memory | internal/text/sfnt.go:85; internal/fontsubset/fontsubset.go:235 | CONFIRMED-R |
| C32 | High | text | Face-list and CSS caches are bounded by count, not bytes, and live as long as the Converter | internal/text/fontset.go:270; text.go cssCache | CONFIRMED-R |
| C33 | High | PDF | Ordinary Vega output fails the whole PDF: `blend`, `fontVariant`, translucent gradient stops, one bad colour | internal/svgpdf/render.go:120; color.go | CONFIRMED-R |
| C34 | Medium | public API | An image over the per-image limit vanishes silently from SVG but fails PNG with ErrLimit | aster.go:538; report.go:60 | CONFIRMED-R(lead) |
| C35 | Medium | public API | PNG/PDF sanitize image URLs a second time; one failure is reported twice, once with a bogus error | images.go:59 after aster.go:524-531; internal/raster/image.go | CONFIRMED-R |
| C36 | Medium | images | The `data:` scheme check is case-sensitive in imageref and case-insensitive in aster | internal/imageref/imageref.go:43 vs aster.go:615 | CONFIRMED-R |
| C37 | Medium | public API | Calling `Close` from an error handler or a Loader deadlocks the call | aster.go:211, 226 | CONFIRMED-R(lead) |
| C38 | Medium | vegalite | Every Vega-Lite compile warning is dropped (transform typos, dropped channels, invalid aggregates) | internal/vegalite (no sink), e.g. scale.go:309 | CONFIRMED-T |
| C39 | Medium | vega | Expression `warn()`/`info()`/`debug()` are swallowed | internal/vega/context.go:389 | CONFIRMED-T |
| C40 | Medium | public API/docs | The spec JSON parse is unbounded by default; limits.md says 1 GiB; the doc test masks it | aster.go:410,735; export_test.go:148 | CONFIRMED-T |
| C41 | Medium | loader | The private-network check is skipped when a Transport sets only `DialTLSContext` | internal/loader/http.go:341 | CONFIRMED-R |
| C42 | Medium | loader | `FallbackLoader.Sanitize` discards the child's rewrite, so links lose their resolution | internal/loader/loader.go:249 | CONFIRMED-T |
| C43 | Medium | expr | Engine limits and ctx errors become non-fatal JS `RangeError`s (timeSequence, pad, repeat) | internal/expr/fn_time.go:167; fn_string.go | CONFIRMED-R |
| C44 | Medium | expr | `String(array)` charges the budget quadratically, causing a false ErrLimit (5,000×20 B strings) | internal/expr/js.go:118 | CONFIRMED-R |
| C45 | Medium | expr | `exp`/`log`/`tan` use Go's math, not the V8-exact jsmath (16%/0.08%/34% of results differ) | internal/expr/fn_math.go:90 | CONFIRMED-R |
| C46 | Medium | vega | The colour value reference evaluates with stale scope flags, giving a ReferenceError in nested groups | internal/vega/parse_encode.go:466 | CONFIRMED-R |
| C47 | Medium | scene | `defined` treats 0/""/NaN as false; upstream only treats `=== false` that way | internal/scene/props.go (triOf); shapes.go | CONFIRMED-R |
| C48 | Medium | scene | `ParsePath` turns 4 bytes into a 64-byte command with no budget; the `customSymbols` cache is bounded by count | internal/scene/svgpath.go:136; shapes.go:583 | CONFIRMED-R |
| C49 | Medium | vegalite | The legacy `in` predicate is accepted but compiles to `indexof([], …)`: every row is silently dropped | internal/vegalite/predicate.go:36 | CONFIRMED-R |
| C50 | Medium | vegalite | Schema-invalid structural values (`"layer":{}`, `"transform":{}`) compile to a blank chart with no error | normalize.go:575,612; layer.go:49 | CONFIRMED-R |
| C51 | Medium | transforms | A window frame offset of ±Infinity or 1e300 becomes MinInt64: inverted frame, wrong sums | internal/vega/ops_transform.go:638; transforms/window.go | CONFIRMED-R |
| C52 | Medium | transforms | Nest/kde2d/wordcloud group keys write dates as epoch milliseconds (upstream: `String(date)`) | internal/transforms/hierarchy/build.go:65 | CONFIRMED-R |
| C53 | Medium | scale | `Time.Ticks` swallows ErrRangeTooLarge under `context.Background()`, giving an empty axis | internal/scale/time.go:79 | CONFIRMED-R |
| C54 | Medium | format | The filtered-interval offset budget is shared across steps: `utcOffset('quarter',d,33000)` gives NaN | internal/format/interval.go:379 | CONFIRMED-R |
| C55 | Medium | format | `clampStep` caps `millisecond.every(k)` at 2^40, moving the tick boundaries | internal/format/interval.go:522 | CONFIRMED-R |
| C56 | Medium | scale | A descending piecewise domain is truncated before it is reversed (d3 reverses the full arrays) | internal/scale/pmap.go:83 | CONFIRMED-R |
| C57 | Medium | PDF | A NaN stroke width, dash or miter limit is written into the content stream; a NaN opacity fails the PDF | internal/svgpdf/render.go; content.go; color.go:67 | CONFIRMED-R |
| C58 | Medium | PDF | ToUnicode maps glyphs to whole clusters, first use wins, which corrupts extraction of RTL and combining text | internal/svgpdf/text.go:239-260 | CONFIRMED-R |
| C59 | Medium | PDF | `fontSize` 0 is drawn at 11 pt; tab and newline are drawn as .notdef boxes | internal/svgpdf/text.go; internal/text/css.go | CONFIRMED-R |
| C60 | Medium | PDF (SVG input) | Quadratic text concatenation on CDATA (6 MB → 62 s) | internal/svgpdf/parse.go:143 | CONFIRMED-R |
| C61 | Medium | raster | A NaN/Inf gradient parameter indexes the LUT and panics, failing the render | internal/raster/paint.go (spread → lut) | CONFIRMED-R |
| C62 | Medium | raster | An element whose filter region exceeds MaxFilterPixels is silently not drawn | internal/raster/effects.go:294 | CONFIRMED-R |
| C63 | Medium | raster | The pattern tile cache (256 MiB) is released from the budget and ignores WithMemoryLimit | internal/raster/pattern.go:173 | CONFIRMED-T |
| C64 | Medium | transforms | `density` reserves no output rows; label masks (about 192 MB) are not charged | internal/transforms/density.go:139; label/scaler.go:69 | CONFIRMED-T |
| C65 | Medium | geo | Projection `fit` streams the whole object through the resampler with no ctx | internal/geo/fit.go:28 | CONFIRMED-R |
| C66 | Medium | test infra | The svgdiff tolerance (0.5) also applies to text and opacity, so `"0.25"`=`"0.5"` and `1e+3`=`1000` | internal/svgdiff/svgdiff.go:200 | CONFIRMED-R |
| C67 | Medium | test infra | An oracle timeout is cached as "upstream errors" and counted as agreement | internal/oracle/oracle.go:282 | CONFIRMED-T |
| C68 | Medium | test infra | The SVG fuzz oracle checks only well-formedness, which is why C1 got through | internal/svg/fuzz_test.go | CONFIRMED-T |
| C69 | Medium | CI | fmacheck passes on stale entries and on zero sites found (`-trimpath`) | scripts/fmacheck.sh:84-93 | CONFIRMED-R |
| C70 | Medium | public API | No method takes a `context.Context`: a request cannot be cancelled without closing the Converter | aster.go:375-1057 | CONFIRMED-T |
| C71 | Medium | public API | Font bytes are aliased, not copied, and parsed up to three times (layout, PNG, PDF), lazily | options.go:159; aster.go:664,830,1066 | CONFIRMED-T |
| C72 | Low | CLI | The CLI cannot load relative or local data (no `-base-url`, no file loader), and no hint is given | cmd/aster/main.go (options) | CONFIRMED-R |
| C73 | Low | CLI/docs | "Exit 2 for invalid flags" holds only for flag-package errors; `-text x`, `-from x`, `-timeout -1s` exit 1 | docs/cli.md:54; cmd/aster/main.go | CONFIRMED-R |
| C74 | Low | public API | `WithDefault{Font,Serif,Monospace}Family("Typo")` is accepted silently; `WithFontFallback("Typo")` fails New | options.go:225-248; aster.go:188 | CONFIRMED-R |
| C75 | Low | public API | `WithFontFace` silently turns a negative index into 0 | options.go:189 | CONFIRMED-T |
| C76 | Low | public API | `WithLoader((*FileLoader)(nil))` is a non-nil interface, so it panics at first use | options.go:57; aster.go:159 | CONFIRMED-T |
| C77 | Low | public API | `lazyShaper.Shape` swallows a shaper init error and returns no glyphs | aster.go:845-851 | CONFIRMED-T |
| C78 | Low | public API | `WithVegaLiteVersion("5.8.0")` (what `AvailableVersions` reports) is rejected | aster.go:136-149 | CONFIRMED-T |
| C79 | Low | public API | `WithTimezone("Local")` is accepted and makes output depend on the host | aster.go:152-157 | CONFIRMED-T |
| C80 | Low | images | `size()` swaps in a 32 MiB load budget in place of the call's; data and images each get the full allowance | images.go:118, 40-45 | CONFIRMED-T |
| C81 | Low | docs | `design-rules.md:90-93` says VL compilation is not interruptible; it is | docs/design-rules.md:90 | CONFIRMED-T |
| C82 | Low | docs | The architecture dependency statement omits golittlecms, gopenjpeg and formalis (via pdf0) | docs/architecture.md:110 | CONFIRMED-T |
| C83 | Low | licensing | THIRD_PARTY_NOTICES lacks d3-ease, d3-regression, d3-dsv, and delaunator's Mapbox text | THIRD_PARTY_NOTICES.md:7-14 | CONFIRMED-T |
| C84 | Low | doc tests | The limits doc test checks only the budget block; its spec-size row is tautological | limits_doc_test.go; export_test.go:148-153 | CONFIRMED-R |
| C85 | Low | doc tests | The link test misses reference, titled and HTML links; the Mermaid test misses indented fences; the layers test accepts duplicates | docs_links_test.go; docs_mermaid_test.go; layers_doc_test.go | CONFIRMED-R |
| C86 | Low | CI | check.sh records missing tools as SKIPPED with exit 0; benchgate skips packages that fail to compile | scripts/check.sh:39-57; scripts/benchgate.sh:171 | CONFIRMED-T |
| C87 | Low | test infra | An upstream known-divergence entry accepts any divergence, and signatures are cut to 300 chars | internal/upstream/upstream.go:152,323 | CONFIRMED-T |
| C88 | Low | loader | The blocked-dial error leaks the resolved internal IP; the limit message names the wrong cap | internal/loader/http.go:179,199,408 | CONFIRMED-T |
| C89 | Low | tools | `enginebench` breaks `GOOS=windows go build ./...` | internal/cmd/enginebench/main.go:322 | CONFIRMED-R |
| C90 | Low | tools | vendor-datasets never checks downloaded hashes | cmd/vendor-datasets/main.go:89 | CONFIRMED-T |
| C91 | Low | vega | A non-ASCII DSV delimiter splits on its first byte; a null row in loaded JSON fails Render outright | internal/vega/dataload.go:146,47 | CONFIRMED-T |
| C92 | Low | vega | Negative `Limits` fields mean different things per field; the `checkRows` message ignores the real cause | internal/vega/view.go:92-127; dataload.go:59 | CONFIRMED-T |
| C93 | Low | vega | Load tests `Has("values")` rather than truthiness; the "Infinite extent" text differs from upstream | internal/vega/ops_core.go:677,483 | CONFIRMED-T |
| C94 | Low | vega | `format.parse` mutates the caller's inline `values` (internal callers of `vega.Render`) | internal/vega/dataload.go:191,457 | CONFIRMED-R |
| C95 | Low | scene | Unknown `orient` silently picks monotoneX; ToInt32 of a huge fontSize differs by platform; a `null` array element is lost in guide expressions | internal/scene/curves.go:81; text.go:212; guides/util.go:289 | CONFIRMED-T |
| C96 | Low | expr | Object-literal key checks lag 6.2.2 (`then`, quoted keys); `in` checks own properties only | internal/expr/compile.go:397-429, 741 | CONFIRMED-R |
| C97 | Low | expr | Concatenation doesn't re-pair surrogates; sticky `split`; `MaxStringLength` is checked in bytes; g/y `test` returns false when the step budget runs out | internal/expr/utf16.go:77; fn_string.go:343,408; scope.go:216 | CONFIRMED-R / PLAUSIBLE |
| C98 | Low | jsval | Stale cost constants (48 B vs 16 B Value); `Relate` and `CompareUTF16` disagree with the expr versions | internal/jsval/json.go:48; relate.go:418,459 | CONFIRMED-T |
| C99 | Low | transforms | Force `iterations` over 1,000 are silently truncated; voronoi's blanket recover hides TypeErrors; limit messages print MinInt64 | force/forces.go:131; voronoi/voronoi.go:49; sequence.go:34 | CONFIRMED-T |
| C100 | Low | scale | `count` above maxSamples silently gives an empty colour range; time parsing skips ASCII whitespace only | internal/scale/vegainterp.go:90; format/timeparse.go:233 | CONFIRMED-T/R |
| C101 | Low | scale | vega-scale helpers are duplicated in `scale` and `guides` and diverge; `Nice` loops unbounded (dead); the `LocalZone` global misleads | internal/scale/vegaticks.go; ticks.go:141; time.go:10 | CONFIRMED-T |
| C102 | Low | PDF | TextNamed declares CFF faces as CIDFontType2; usage copies whole font programs even when not asked; misleading errors | internal/svgpdf/fonts.go:112; usage.go:448; text.go | CONFIRMED-T |
| C103 | Low | raster | Clip, mask and final-copy allocations fall outside the canvas budget; marker-mid is O(n²); CSS past its budget is silently unstyled | internal/raster/render.go; marker.go:66; css.go:592 | CONFIRMED-T |
| C104 | Low | vegalite | Lone `'` date pattern, `null` datetime part, `autosize:5`, `titleCase` byte slicing, non-array config `signals` | datanodes.go:132; defs.go:432; compile.go:134; dyn.go:486; config.go:258 | CONFIRMED-R/T |
| C105 | Info | vega | Internal-error messages include up to 24 lines of goroutine stack | internal/vega/view.go:267; dataflow.go:669 | CONFIRMED-T |
| C106 | Info | public API | `AvailableVersions` returns an error that is always nil | aster.go:119 | CONFIRMED-T |
| C107 | Info | comments | Stale comments: pngopt "both engines", CLI "aster/runtime:", raster "not implemented", resvg mentions | internal/pngopt/doc.go:3; cmd/aster/main.go:36; internal/raster/raster.go:33; recode.go:16; fonts/notoemoji/embed.go:6 | CONFIRMED-T |
| C108 | Info | dead code | Dead code: geo GeoCircle/GeoLength/…, `vegalite.CompileJSON`, `_ = ops` and similar, `Face.Subset` | various | CONFIRMED-T |
| C109 | Info | repo | `docs/audits/codebase-audit-2026-07-07.md` is untracked and not ignored, and the local doc tests walk it | docs/audits/ | CONFIRMED-T |

---

## 2. System map

### Layers and real execution paths

The layer list in `docs/architecture.md` is test-checked and accurate. Every output is produced **from the SVG string**:

```
VegaLiteToX(spec) ─┬─ checkSpecSize (only with WithMemoryLimit) ─ jsval.ParseJSONLimit(spec, 0 by default!) ─ vegalite.Compile(ctx)
                   │       └─ deepClone (no ctx) → normalize (no ctx) → buildModel → parse → assemble
VegaToX(spec) ─────┴─ jsval.ParseJSONLimit ─ vega.Render(ctx, Options{Loader, Limits, Report, TextMeasurer, Shaper, Random LCG(123456789)})
                          parse → dataflow (rank heap, pulses) → encode → guides/layout → scenegraph
                   svg.Render(scene, {Href: Loader.Sanitize+allow-list, Image: Loader.Sanitize + size fetch})
   PNG:  raster.RenderPNG(svgString, {Images: callImages.load → Loader.Sanitize AGAIN → Load}) → pngopt (no ctx)
   PDF:  svgpdf.ConvertWithUsage(svgString, measurer, {Images: callImages.load}) → pdf0
SVGToPNG / SVGToPDF(svg)  — same back halves, caller SVG is attacker-controlled per security.md
```

`aster.go:375-1057` is the whole public flow. Each public method:

1. Takes a read lock (`enter`).
2. Builds a per-call context from the Converter's base context, with the timeout, a shaping budget, a `callReport` and a `callImages` cache (`opContext`, aster.go:238).
3. Runs the stages.

`Close` cancels the base context and takes the write lock (aster.go:206-221).

### Key invariants and where they are enforced

| Invariant (as documented) | Where enforced | Reality |
|---|---|---|
| Every call is bounded by `WithTimeout` | ctx polled in vega dataflow, transforms, text shaping, raster scanlines | Many loops have no poll: C3, C4, C14–C18, C21, C22, C27–C29, C65 |
| Memory is bounded by budgets charged *before* allocation | `internal/budget` rows/items/strings/canvas; raster `allocCanvas` | Charged at a few chokepoints only: C5–C9, C13, C19, C20, C23–C26, C30–C32, C48 |
| Errors upstream logs fail the call (ADR 0006) | `vega.Options.Report` → `callReport.decide` | Holds for the Vega runtime; VL compile warnings (C38), expr `warn()` (C39) and several swallowed limits (C34, C53, C62, C100) bypass it |
| SVG output is safe to inline | `appendEscaped` in internal/svg | One raw writer, `AppendPos`, bypasses it: C1 |
| Each resource goes through the Loader once per URL per call | `callImages` | Images are sanitized twice: C35 |
| The Converter is immutable after New | `config` struct | Font byte slices are aliased (C71) |
| Concurrency-safe | per-call state; mutex-guarded caches | Holds: reviewers found no races (theme sharing, Measurer, image fetch, raster caches) |

### What is sound

Said once, so it isn't re-litigated:

- **Parity with upstream.** The dataflow kernel matches vega-dataflow step for step. d3-geo clipping, d3-format and d3-time-format, d3-scale, vega-transforms, jssort (V8 TimSort) and JS number semantics are faithful ports. Go-vs-node differentials on 332 specs (with mutated field names) were identical.
- **Loader containment.** `os.Root` blocks symlinks, `..` and absolute paths. The dial-time private-address check covers DNS rebinding and redirects.
- **Sandboxing and robustness.** The expression sandbox allows no prototype pollution or host reach. Panics are recovered at every layer. PDF name and string escaping is correct. Concurrency was race-clean.

The project's weakness is **one class of problem**: resource and time bounds that exist on paper and in a few chokepoints but not where the work happens.

---

## 3. Findings by category

### 3.1 Boundary and safety: injection

**C1 — Critical — SVG markup injection via string positions.** `internal/scene/props.go:539-544`

```go
func (it *Item) AppendPos(dst []byte, prop string) []byte {
	if v, ok := it.Raw[prop]; ok {
		if !v.IsTruthy() { return append(dst, '0') }
		return append(dst, v.AsString()...)      // unescaped, into transform="translate(…)"
```

- **Scenario (spec):** `{"type":"symbol","encode":{"enter":{"x":{"value":"1)\"/><script>alert(2)</script><g x=\""}}}}` makes `aster svg` emit a live `<script>` element and exit 0.
- **Scenario (data, which matters more):**
  - Vega: `"x":{"field":"a"}` with a datum `a = "\"><script>alert(5)</script>"`.
  - Vega-Lite: `"x":{"field":"a","type":"nominal","scale":null}`.
  
  Both emit the script. A host that renders user CSV is exploitable without the spec author being hostile. Lead-reproduced with all three. Upstream vega escapes the same value (oracle output `translate(&quot;&gt;…`).
- **Status:** CONFIRMED-R(lead).
- **Direction:** escape in `AppendPos` (`appendEscaped(dst, s, true)`). Then make raw attribute writers impossible for spec-derived values (see Design tension 3). Strengthen the SVG fuzz oracle (C68) to assert the element and attribute sets, not just well-formedness. Check whether raster/svgpdf honour injected elements (e.g. `<image>`); they still fetch through the Loader, so this is markup injection, not a Loader bypass.

### 3.2 Boundary and safety: process-killing and timeout-ignoring loops

**C2 — Critical — Voronoi cell walk never terminates.** `internal/transforms/voronoi/diagram.go:149-173`

- **Scenario:** a voronoi transform over inline points (97.2,82.1), (5e-324,-1e308), (-1e308,41.2), (1,36.3), (95.7,0), (19.2,-1e308), (-1e308,-1e308), (64.9,70.8). The lead's run, under `ulimit -v 4 GB`: `fatal error: out of memory` in `voronoi.(*diagram).cell` after 0.9 s, exit 2.
- **Why it matters:** a fatal OOM cannot be recovered, so **one inline-data spec kills the host process**. The delaunator hull walks are bounded; this one has no step bound.
- **Status:** CONFIRMED-R(lead).
- **Direction:** bound the walk at `len(halfedges)` steps and treat the cell as degenerate past that; poll ctx; reject or clamp |coord| > 1e150 before triangulating. Add the 8-point regression test. `TestHostileInputs` (voronoi_test.go:200) only logs and never generates this class of input.

**C3 — Critical — `circleStream` infinite loop on a huge `clipAngle`.** `internal/geo/circlestream.go:27`

- **Mechanism:** `for t := t0; …; t -= step`. At a radius of about 1e16 rad, `t -= step` no longer changes `t`. `clipExtent` drops every point, so `limitSink`, the only point charge and ctx poll, is never reached, while `rectStream` buffers points.
- **Scenario:** projection `{type:"orthographic",clipAngle:1e18,clipExtent:[[-1e12,-1e12],[-1e12,-1e12]]}` with a shape mark over `{"type":"Sphere"}`. With `-timeout 1s`, the lead killed it at 15 s.
- **Side effect:** `Converter.Close` blocks behind it (it waits on `mu.Lock`).
- **Status:** CONFIRMED-R(lead).
- **Direction:** validate projection parameters at Set (`clipAngle` in (0,180], finite `scale`/`precision`; also C17), bound circleStream at `ceil(τ/δ)+1` iterations, and charge and poll *before* post-clip.

**C4 — Critical — Cubic index-key ordering in the VL input clone.** `internal/vegalite/dyn.go:591` (`jsSet`), called from `deepClone` :311 and `spread` :126

- **Mechanism:** for every key, `jsSet` scans every existing key and re-adds the tail key by key with `Object.Delete` (O(n)). `deepClone` runs before any `cc.check()`.
- **Scenario:** one inline row with 1,001 numeric keys in descending order (8 KB). The lead measured 3.9 s at `-timeout 1s`; the reviewer measured 12 KB → 20 s at 200 ms. The cost is cubic, so 70 KB runs for hours. A second route is `spread` of a long string (`"mark":{"type":"boxplot","box":"aaa…"}`), which is O(n²).
- **Status:** CONFIRMED-R(lead).
- **Direction:** make `jsSet` O(1) when the new index exceeds the last index key; in `deepClone`, sort index keys once; or move JS index-first ordering into the JSON writer (Design tension 4). Poll ctx in `deepClone`.

**C14 — High — The `tickMinStep` reduction is O(count) before the tick cap.** `internal/vega/guides/scalehelpers.go:142` (and a parallel copy in `internal/scale/vegaticks.go`, C101)

- **Scenario:** domain [0,1], `tickCount:1e9`, `tickMinStep:1.3e-9` gives 6.5 s at `-timeout 2s`. The error then reads "timed out … : limit exceeded: tick count 7.07e+08 …". With 1e12 it runs for minutes.
- **Upstream:** has the same loop.
- **Status:** CONFIRMED-R(lead).
- **Direction:** clamp `n` to `maxTicks+1` before the loop, or binary-search (the step is monotone).

**C16 — High — Quadratic selection signal naming.** `internal/vegalite/selection.go:712`

- **Mechanism:** `for counter := 1; signals.has(sg); counter++` restarts at 1 for every field. Twenty thousand CJK one-character fields all map to `_`.
- **Scenario:** a 120 KB spec ran 96 s against a 1 s context.
- **Status:** CONFIRMED-R.
- **Direction:** keep a per-base next-suffix map (same output, O(n)); call `cc.check()` per field.

**C17 — High — Geo resampling bypasses MaxPoints and the timeout.** `internal/geo/path.go:38` (comment) vs pipeline order post-clip → resample → pre-clip (projection.go:270-277); `cliprectangle.go` buffers `polyPts`

- **Scenario:** a 200-vertex polygon with `{type:"equirectangular",scale:1e9,precision:1e-9,clipExtent:[[-1e12,-1e12],[-1e12,-1e12]]}` takes 3.6 s and allocates 3.1 GB at a 100 ms timeout, and returns **err=nil**. At 2,000 vertices it takes 19 s and allocates 18 GB.
- **Status:** CONFIRMED-R.
- **Direction:** add a counting and polling stage directly after resample; bound `precision` from below. The comment at path.go:38 claims this bound exists.

**C18 — High — Geo expression functions are unbounded.** `internal/vega/env_geo.go:32`

- **Mechanism:** `p.Path().Area(geojson)` uses a Path that is only bound (budget and ctx) when a geopath or geoshape ran.
- **Scenario:** `sequence(0,200)` plus a formula `geoArea('p', …)` with `precision:1e-9` takes 1.9 s at a 100 ms timeout and returns err=nil; 2,000 rows take about 20 s.
- **Status:** CONFIRMED-R.
- **Direction:** bind the projection's Path to the view's ctx and budget in `projectionOf`.

**C21 — High — Naive string search.** `internal/expr/utf16.go:136`

- **Mechanism:** `for i := from; i+len(sub) <= len(s); i++ { if s[i:i+len(sub)] == sub`, with no `tick()`.
- **Scenario:** `indexof(pad('',400000,'a'), pad('',200000,'a')+'b')` takes 0.9 s with an already-expired ctx. At the sizes the string budget allows it would run for tens of minutes.
- **Status:** CONFIRMED-R.
- **Direction:** use `strings.Index` and map byte offsets to code units; use a linear search on `[]uint16` otherwise.

**C22 — High — `extent` skip loop.** `internal/expr/fn_seq.go:256`

- **Scenario:** `extent({length:3e9})` iterates 16.7M times with no tick (1.5 s with an expired ctx). Per row of a formula over 4,096 rows that is about 100 minutes. On strings, `charAt` re-encodes UTF-16 on every element: `extent(pad('é',40000,'é'))` takes 10.7 s.
- **Status:** CONFIRMED-R.
- **Direction:** tick in the skip loop; convert to UTF-16 once; charge or lower the 1<<24 array-like cap.

**C27 — High — Contour hole assignment.** `internal/transforms/contour/marching.go:125` (`for _, hole := range holes`, O(holes×ring) with no ctx); `emit` :266 charges nothing

- **Scenario:** a 1024² grid of 1s with isolated 0s takes 8.4 s at a 1 s deadline and returns `err=nil` with 261k holes; 2048² takes 83 s. Separately, a 512² checkerboard allocates about 56 MB per threshold, and up to 4,096 thresholds are allowed.
- **Status:** CONFIRMED-R.
- **Direction:** poll ctx in the hole loop (with a bbox pre-check); charge ring vertices to `budget.Points`; cap thresholds × cells.

**C28 — High — feMerge (SVGToPNG input).** `internal/raster/filter.go:93`

- **Mechanism:** each merge node allocates a full w×h×4 buffer (`toSpace(run.input(in))`), but the primitive count charged is `len(sp.prims)`, and ctx is not polled inside the loop.
- **Scenario:** a 1000² canvas with 3,000 `<feMergeNode in="SourceAlpha"/>` ran 90 s at a 200 ms timeout and allocated 22.9 GB in total.
- **Status:** CONFIRMED-R.
- **Direction:** count merge nodes as primitives, poll per node, memoize SourceAlpha, cap the node count.

**C29 — High — PNG quantization outside every bound.** `internal/pngopt/quantize.go:203`; called at aster.go:816 with no ctx

- **Mechanism:** median cut rescans every box per split (O(colours × distinct)), with maps sized by the number of distinct colours.
- **Scenario:** `SVGToPNG` of a 1500² noise image with `WithQuantizePNG(256)` takes 12.96 s at `WithTimeout(1s)` and succeeds. At 2048² it takes 21 s and 364 MB, then falls back anyway.
- **Docs:** options.go:397-412 says "tens of milliseconds … enabling it is always safe".
- **Status:** CONFIRMED-R.
- **Direction:** pass ctx; bail out early when the distinct-colour count exceeds about 64k (charts don't have that many); quantize from the in-memory NRGBA; fix the doc.

### 3.3 Boundary and safety: memory that escapes the budgets

**C5 — Critical — TopoJSON expansion.** `internal/geo/topojson.go:86` (`arc.dim = pts[0].Len()`), `:157` (`NewArrSlab(a.n, a.dim)` per reference); `internal/vega/dataload.go:176` calls `TopologyFeatures` with no ctx and no budget

- **Scenario A (references):** one 10,000-point arc referenced by 2,000 LineStrings, inline, with no Loader. The 212 KB spec reached 1.64 GB RSS, took 1.99 s at `-timeout 1s`, and **exited 0**. The cost is R×K, so a few hundred KB more runs the host out of memory.
- **Scenario B (dimension):** a 20,000-coordinate first position followed by 2,000 empty positions (45 KB) allocates 916 MB.
- **Status:** CONFIRMED-R(lead), and also reproduced independently by two reviewers.
- **Direction:** pass ctx and the points budget into `TopologyFeatures`; charge positions × dim before materialising; cap `dim` (GeoJSON uses at most 4).

**C6 — Critical — Rasterizer geometry is unbudgeted.** `internal/raster/text.go:576` (`r.fl.appendFlatten(p, ctm.mul(g.matrix()), shapeTol)` for every glyph of a run before filling); `dom.go:327`

- **Mechanism:** every curve flattens to up to 400 points wherever it lies; nothing is culled against the clip, and edge work isn't charged. `MaxPathSegments` is declared at dom.go:327 and defaulted, but never read.
- **Scenario:** `{"data":{"values":[{}]},"autosize":{"type":"none"},"width":10,"height":10,"mark":{"type":"text","fontSize":100000,"text":"W@"×2000}}` (4 KB) gives `aster png` at 917 MB RSS (lead). The reviewer measured 3.48 GB under `WithMemoryLimit(64 MiB)` for 20 KB. The growth is linear, so about 100 KB kills the host. Raw paths (`c9e8 0 0 9e8 1 1`×40,000) and round-join strokes behave the same way.
- **Status:** CONFIRMED-R(lead).
- **Direction:**
  - Cull glyphs and paths whose device bounding box misses the clip.
  - Flatten and fill per glyph.
  - Enforce a cumulative segment budget derived from WithMemoryLimit.
  - Charge edges × scanlines to `MaxPixelOps`.

**C7 — Critical — Decoded images per element.** `internal/raster/image.go:236` (`if img, cached := r.imgs[n]` keyed by `*node`)

- **Mechanism:** each `<image>` decodes again: png.Decode, a premultiplied copy and mips. Nothing goes through `allocCanvas`. Image limits (64 Mpx, 32 MiB) are fixed and ignore WithMemoryLimit.
- **Scenario:** a VL image mark with one 4096² PNG data URI over 8 rows (75 KB spec) peaks at 929 MB RSS (lead). Under `WithMemoryLimit(64 MiB)` the reviewer measured 878 MB.
- **Status:** CONFIRMED-R(lead).
- **Direction:** key the cache by trimmed href or content hash; charge decoded bytes (×4 plus mips) to a per-render image budget; scale the image limits with WithMemoryLimit.

**C8 — Critical — Regex program size.** `internal/expr/parse.go:687` (`jsval.NewPattern` at parse); `fn_string.go:248` (the `patternCache`, cleared only when it holds 512 entries); `programCache` (1,024 entries, sources of 2,048 chars or less)

- **Mechanism:** goecma262's 200k-instruction cap is the only bound, at about 30 MB retained per pattern. Nothing charges it, and both caches are process-wide and bounded by entry count.
- **Scenario:** a 419-byte spec, signal `length([/(a{1000}){190}/ ×20])`, peaks at 923 MB (lead). With about 120 literals in a cached program (~3.6 GB) the memory persists **after the render**. At run time, `test('(a{1000}){150}'+datum.i,'x')` compiles about 24 MB per row into the global cache. Patterns just over the cap cost 120 MB and are not cached as errors, so they are rebuilt per row.
- **Status:** CONFIRMED-R(lead).
- **Direction:** a much lower program cap (in goecma262); charge pattern size to the render's budget; make both caches byte-bounded or per render, and never cache large programs.

**C9 — Critical — Locale amplification.** `internal/format/numlocale.go:426` (`out = append(out, l.numerals[d]...)` over the whole output, separators included); `timeformat.go` `compileFormat` copies literals up to 4,096 times

- **Scenario:** `config.locale.number = {thousands:"0"×1024, grouping:[1], numerals:["9"×64]×10}` with an axis `format:",b"` on [0,1e308]. A 2,019-byte spec gave a 67 MB SVG, 1.17 GB RSS and 4.4 s (lead), and **exit 0**. An 8 KB spec reaches tens of GB. Time locales (`dateTime:"%x"×4095` with a 16 KiB `date`) behave the same way.
- **Status:** CONFIRMED-R(lead).
- **Direction:** cap locale member lengths in `NewLocale` (separators and numerals to a few dozen bytes, patterns to about 1 KB), or compute the output length up front and fail with ErrLimit.

**C13 — High — MaxRows is not a bound.** `internal/vega/ops_transform.go:242`; `dataload.go:39`, `:59`

- **Mechanism:** `checkRows(len(out)-len(in), …)` charges a negative delta for filters (`budget.AddRows` has no floor). Rows from Load-with-signal, `modify()`/trigger inserts, `setdata()`, PreFacet, nest/treelinks and aggregate outputs are never recorded. Reruns charge again, and that double-charge gives false positives.
- **Scenario:** under `WithMemoryLimit(512000)` (MaxRows 1,000), 900 rows, ten `filter:false` derivatives and nine `sequence 0..999` (9,891 rows alive) render **without error** (lead). `values:{signal:"sequence(0,50000)"}` gives no error (reviewer). In the other direction, `sequence stop:{signal:"n"}`, rerun via WithSignal from n=600 to n=601, fails with "1201 rows".
- **Docs:** `WithMemoryLimit` is documented as turning into MaxRows, so the memory promise does not hold.
- **Status:** CONFIRMED-R(lead).
- **Direction:** per-operator replace accounting in the kernel: whenever an operator's tuple list is replaced, charge new minus previous (Design tension 1).

**C15 — High — Multiplicative repeat expansion.** `internal/vegalite/normalize.go:626` (`if len(repeatValues)*len(rows)*len(cols) > maxRepeatChildren` per repeat); `mapLayerRepeat` has no bound; normalize has no `cc.check()`

- **Scenario:** four nested repeats of 30 values (1,008 bytes) reached 985 MB RSS before the 2 s timeout (lead). Four levels of 100 is about 1e8 views, an OOM before any check. `repeat:{row:[30],column:[30],layer:[1000]}` gives 900k units.
- **Status:** CONFIRMED-R(lead).
- **Direction:** a cumulative "views created" counter across all repeat, layer-repeat and concat expansion, scaled from WithMemoryLimit; poll ctx in the loops.

**C19 — High — DSV wide header.** `internal/vega/dataload.go:372`

- **Mechanism:** the guard is `len(rows)*len(columns) > 16*maxRows` (16M cells), and the row weight is charged after parsing.
- **Scenario:** a 689 KB CSV with a 100,000-column header and 400 blank lines allocates 1.6 GB and runs 7.5 s, then fails with "data exceeds the limit of 1000000 rows" with about 160 rows actually present.
- **Status:** CONFIRMED-R.
- **Direction:** charge cells per row before creating them; cap the column count; name the real bound.

**C20 — High — Warning retention.** `internal/vega/report.go:50` (`g.warnings = append(g.warnings, err.Error())`); `ops_scale.go` `"Unsupported scale property: "+name`

- **Scenario:** a scale in a faceted group with a 100,000-character unknown property key, over 2,000 cells, keeps 200 MB of warnings. MaxSubflows (20,000) allows about 20 GB.
- **Status:** CONFIRMED-R.
- **Direction:** dedupe, cap by count and bytes, truncate embedded spec text.

**C23 — High — Uncharged expression arrays.** `internal/expr/fn_string.go:394` (`split`); `fn_time.go:165` (`timeSequence`, up to 4.2M dates)

- **Scenario:** `length(split(pad('',16000000,'a'),''))` allocates 503 MB while only 15 MB is charged. limits.md says "expression strings and arrays 128 MiB".
- **Status:** CONFIRMED-R.
- **Direction:** `chargeItems` before building; audit the other array producers (`merge`, `pluck`, `slice`, `reverse`, `sort` copies).

**C24 — High — kde2d grids.** `internal/transforms/contour/transforms.go:67` (`MaxGridCells` per grid, fixed)

- **Mechanism:** each group allocates about 512 MB (two float32 rasters, one float64 raster and a 16M-element `jsval` array).
- **Scenario:** `size:[4096,4096]`, `cellSize:1`, `groupby` over 3 distinct values gives 1.5 GB in 1.1 s; 30 groups is about 13 GB within the default timeout.
- **Status:** CONFIRMED-R.
- **Direction:** charge `budget.Canvas` per grid before `make`; scale `MaxGridCells` with the memory limit.

**C25 — High — Window `values`.** `internal/transforms/measure.go:421` (`jsval.Arr(slices.Clone(data))` per row)

- **Scenario:** `frame:[null,null]` over 4,000 rows takes 250 MB; 40,000 rows is about 25 GB.
- **Upstream:** shares one growing array per frame, so aster's output also diverges.
- **Status:** CONFIRMED-R.
- **Direction:** reserve frame cells per row via `ReserveWeight`, or alias as upstream does.

**C26 — High — Pivot undercount.** `internal/transforms/aggregate.go:84`

- **Mechanism:** the reservation is `(len(order)+1)*(16+nm)/16` rows, but each measure state is 128 bytes.
- **Scenario:** under `WithMemoryLimit(256 MiB)`, a pivot of 2,500 unique keys × unique values allocates 1.5 GB.
- **Status:** CONFIRMED-R.
- **Direction:** `ReserveWeight` in bytes (`cells × nm × sizeof(mstate)`).

**C30 — High — Filter intermediates (SVG input).** `internal/raster/filter.go:36` (`maxFilterLiveBytes = 1 << 30`, fixed)

- **Mechanism:** filter results and the IIR blur scratch (8 B/px) are not charged to MaxCanvasBytes, although its doc says it covers "filter in flight".
- **Scenario:** 12 named blurs on 1500² under a 20 MiB canvas limit use 223 MB with no error.
- **Status:** CONFIRMED-R.
- **Direction:** charge them to `r.canvasBytes`.

**C31 — High — Hostile font amplification** (fonts are attacker-controlled, security.md:7).

- `internal/text/sfnt.go:85` adds up every directory record, and `outlineProgram` copies duplicate tags. A 418 KB TTF with 490 records pointing at `glyf` returns a 132 MB `FontData`; the ceiling is about 32 GB. It runs on every embed/named PDF.
- `internal/fontsubset/fontsubset.go:235` checks only `start>end`, so overlapping `loca` ranges copy L bytes per kept glyph. Through public `aster.SubsetFont`, a 140 KB font gives a 216 MB subset.
- **Status:** CONFIRMED-R.
- **Direction:** one hardened sfnt reader (dedupe tags, first record wins, output ≤ input size); reject non-monotonic `loca` or cap the glyf output.

**C32 — High — Text caches.** `internal/text/fontset.go:270` (rune memo up to 16,384 entries per list, 16,384 lists); `text.go` `cssCache` keyed by the full font string

- **Scenario:** one 16,000-rune CJK string under 300 data-driven family names leaves 172 MB live; the ceiling is about 9 GB. 500 font strings with 100 KB family names leave 51 MB.
- **Why it matters:** this outlives the render and is outside every budget. The byte-bounded `widthCache` beside it documents why that matters (text.go:55-59).
- **Status:** CONFIRMED-R.
- **Direction:** one byte budget across the caches; skip long keys; a smaller rune memo.

**C48 — Medium — Path parsing.** `internal/scene/svgpath.go:136`; `shapes.go:583`

- **Scenario:** a 32 MiB path string gives 8.4M `PathCmd`s, 512 MiB live and 3.5 GiB allocated. The CLI peaked at 2.29 GB with no ErrLimit. The process-global `customSymbols` cache (256 entries) can pin about 16 MiB per 1 MiB path across renders.
- **Status:** CONFIRMED-R.
- **Direction:** charge parsed commands; don't cache large paths.

**C63 — Medium — Pattern tile cache.** `internal/raster/pattern.go:173` (`r.patternBytes+len(sub.pix) > 256<<20`)

- **Mechanism:** tiles are released from the canvas budget and kept up to a fixed 256 MiB.
- **Scenario:** `WithMemoryLimit(16 MiB)` still allows 256 MiB of tiles.
- **Status:** CONFIRMED-T.
- **Direction:** derive the cap from MaxCanvasBytes.

**C64 — Medium — `density` and label.**

- `internal/transforms/density.go:139` reserves no output rows (KDE does, kde.go:307), so up to 1,000,001 tuples exist before `checkRows`.
- label/scaler.go:69 allocates up to 3 × 64 M bools without charging.
- **Status:** CONFIRMED-T.
- **Direction:** `reserveOut` before sampling; charge masks and use bitsets.

**C65 — Medium — Projection fit.** `internal/geo/fit.go:28` streams through the resampler with no ctx; `ConfigureProjection` has no ctx.

- **Scenario:** `precision:1e-12` with `fit` over 2,000 vertices takes 1.66 s.
- **Status:** CONFIRMED-R.
- **Direction:** thread ctx through.

### 3.4 Correctness

**C10 — High — Long generated expression chains fail to compile.** `internal/vegalite/exprdeps.go:37` (`maxExprDepth = 200`); generator `datanodes.go:265` (`parseAllCalculateForSortIndex` emits `a ? 0 : b ? 1 : …`)

- **Scenario:** a nominal x with `"sort":[250 category names]` (countries, say) fails with `Invalid expression: expression too deeply nested` (lead). With 150 items the output is identical to upstream. `filter:{or:[1000 preds]}` and `calculate:"1+1+…"` also fail.
- **Docs:** limits.md says "expression nesting 512 levels".
- **Status:** CONFIRMED-R(lead).
- **Direction:** parse right-associative ternary chains and left-associative binary chains iteratively in the VL-side parser; use one limit constant shared with `internal/expr`.

**C11 — High — Global rerun counter.** `internal/vega/env_data.go:177` (`v.reruns++`, never reset, shared by data triggers, layout reruns and every SignalWrite)

- **Scenario:** 20 data sets, each `on:[{trigger:"true",insert:"{a:2}"}]`, each settling after one application. **Default settings**: `limit exceeded: data triggers did not settle after 16 re-evaluations` (lead).
- **Why it matters:** a legitimate spec fails, labelled ErrLimit.
- **Status:** CONFIRMED-R(lead).
- **Direction:** count per propagation or per data set.

**C12 — High — CSS font regex picks the weight as the size.** `internal/text/css.go:33`; `internal/svgpdf/text.go:371` (`cssFontString` re-serialises SVG attributes)

- **Scenario:** Vega `fontWeight:450, fontSize:12` produces `/F0 450 Tf` in the PDF (lead); weight 1000 gives 83×. Layout uses the same parser: `MeasureText("Hello","450 12px Arial")` is 1,025 px. Further:
  - fontSize 1e6 is written as `1e+06px` and drawn at 6 pt;
  - fontSize -20 is drawn at 20 pt;
  - `bold 12px/1.5 Arial` is measured at 1.5 px.
- **PNG:** unaffected (raster formats its own string).
- **Status:** CONFIRMED-R(lead).
- **Direction:** pass a structured font (family, size, weight, style) from scene to the writers; use the strict canvasfont tokenizer only to validate canvas font strings (Design tension 5).

**C33 — High — svgpdf rejects ordinary Vega output.** `internal/svgpdf/render.go:120` (`unsupported attribute`); color.go

- **Scenario:** each of these fails the *whole* PDF:
  - `fontVariant:"small-caps"`;
  - `blend:"multiply"` (style `mix-blend-mode`);
  - a data-driven invalid colour (`"notacolor"`, `currentColor`);
  - `strokeCap:"Round"`, `opacity:"half"`;
  - **translucent gradient stops** (an area chart fading to transparent).
- **Inconsistency:** a NaN stroke slips through as a corrupt stream (C57).
- **Status:** CONFIRMED-R.
- **Direction:** follow SVG's "invalid presentation value → ignore"; support blend modes (the ExtGState BM machinery exists) and translucent stops (the soft-mask machinery exists); ignore font-variant.

**C46 — Medium — Stale scope in the colour value reference.** `internal/vega/parse_encode.go:466` (`s := ev.ctx.scope(); s.Datum = jsval.Obj(d)` without resetting `NoDatum`/`NoItem`/`NoEvent`)

- **Scenario:** a group with `signals:[{name:"q",update:"2"}]` holding a rect with `fill:{color:{r:{value:255},…}}` gives the warning `ReferenceError: datum is not defined` and no fill. The same rect at top level renders red.
- **Status:** CONFIRMED-R.
- **Direction:** reset the flags, or evaluate through `exprFn.eval`.

**C47 — Medium — `defined` semantics.** `internal/scene/props.go` (`triOf`), shapes.go:326/365/432

- **Scenario:** `"defined":{"value":0}` (or `{field:"valid"}` with 0/1 data) draws no line; node draws `M0,0L50,50L100,0`. Upstream treats only `=== false` as undefined.
- **Status:** CONFIRMED-R.
- **Direction:** record "explicit false" only.

**C49 — Medium — Legacy `in` predicate.** `internal/vegalite/predicate.go:36` accepts `in`, but the expression reads only `oneOf`.

- **Scenario:** `{"filter":{"field":"b","in":["x"]}}` gives `indexof([], datum["b"]) !== -1`, an empty chart with no error. Upstream throws.
- **Status:** CONFIRMED-R.
- **Direction:** use `oneOf ?? in`, or throw.

**C50 — Medium — Schema-invalid structure is silently accepted.**

- **Mechanism:** `Value.Items()`/`keysOf` on non-arrays return empty (normalize.go:575,612,617; layer.go:49; predicate.go:197).
- **Scenario:** about 70 of 1,500 type-confusion mutants compiled OK where node errors: `"layer":{}`, `"vconcat":"x"`, `"transform":{}`, `"params":1`, `"type":0`. The result is a blank chart and nil error, against ADR 0006. `typeconfusion_test.go` asserts only "no panic".
- **Status:** CONFIRMED-R.
- **Direction:** throw a compileError in the structural slots.

**C51 — Medium — Window frame with Inf or huge offsets.** `internal/vega/ops_transform.go:638` → transforms/window.go

- **Mechanism:** `int(f)` of ±Inf or 1e300 is MinInt64.
- **Scenario:** `frame:[0,Infinity]` with `sum` over [0,1,2] gives `undefined,0,-1`; upstream gives `3,3,2`.
- **Status:** CONFIRMED-R.
- **Direction:** clamp to [-n,n] or treat ±Inf as unbounded.

**C52 — Medium — Date group keys.** `internal/transforms/hierarchy/build.go:65` (also contour/transforms.go kde2d groupby, wordcloud)

- **Mechanism:** `AsString()` writes epoch milliseconds; upstream's `String(date)` is second-precision local time.
- **Scenario:** nest by a date field splits 500 ms-apart values into two groups, and shows numeric keys where upstream shows date strings.
- **Status:** CONFIRMED-R.
- **Direction:** use the zone-aware `keyString`, which the root transforms package already uses.

**C53 — Medium — Swallowed time-tick limit.** `internal/scale/time.go:79` (`format.TicksWith(context.Background(), …)`; `if err != nil { return nil }`)

- **Scenario:** utc [0,3e12] with `tickCount:{interval:"minutes",step:2}` renders an axis with no ticks after 1.5 s of uninterruptible work, with no ErrLimit.
- **Status:** CONFIRMED-R.
- **Direction:** give `Ticker` a ctx and an error.

**C54 / C55 / C56 — Medium — Time interval and scale fidelity.**

- **C54:** `internal/format/interval.go:379`. `budget := maxFilterIterations` is shared across steps, so `utcOffset('quarter', d, 33000)` gives NaN where d3 gives `+011970-01-01`. Fix: reset the budget per move.
- **C55:** `interval.go:522`. The 2^40 clamp changes `millisecond.every(1e13)` ticks: 91 ticks at 1.0995e12 versus d3's 10 at 1e13. Fix: keep k as a float for millisecond and year.
- **C56:** `internal/scale/pmap.go:83`. `domain[:n], rng[:n]` is applied before reversing. Domain [2,1,0] with range [0,10,20,30] gives 15 at 0.5 where d3 gives 25. Fix: reverse the full slices first.
- **Status:** all CONFIRMED-R.

**C57 / C58 / C59 — Medium — PDF text and stroke fidelity.**

- **C57:** `parseLength` accepts NaN (`strconv.ParseFloat`). `strokeWidth:{signal:"0/0"}` writes `NaN w`: an invalid PDF with **no error**. A NaN opacity fails the PDF (`clamp01(NaN)` = NaN, color.go:67). Fix: drop non-finite values as absent attributes, and make `appendNum` refuse non-finite values.
- **C58:** `recordToUnicode` (text.go:239-260) maps every glyph to its whole cluster, first use wins. "ab שלום cd" makes every space in the document extract as `" שלו"`. "q́ q" makes every later q extract as "q́". Fix: logical cluster ends, map only the first glyph, use ActualText on conflict.
- **C59:** `fontSize:0` → `font-size="0px"` → `ParseCSSFont` falls back to 11 px → `/F0 11 Tf`. Text "a\tb" draws `.notdef`. Fix: skip size ≤ 0, and normalise \t\n\r to space per SVG.
- **Status:** all CONFIRMED-R.

**C61 — Medium — NaN in a gradient LUT index.** `internal/raster/paint.go` (spread → `g.lut[int(t…)]`)

- **Scenario:** `x2="1e-160"` with `spreadMethod="repeat"` gives `invLen2=+Inf`, then `t=NaN`, then `raster: internal error: index out of range [-9223372036854775808]`.
- **Status:** CONFIRMED-R.
- **Direction:** treat a non-finite `invLen2` as degenerate; check for NaN after spread.

**C45 — Medium — Non-V8 math in expressions.** `internal/expr/fn_math.go:90` (`"exp": math.Exp`, and `log`/`tan`)

- **Scenario:** `exp(1.5)` gives …065 where V8 gives …0645; across 100k random arguments 16%/0.08%/34% of results differ.
- **Why it matters:** `jsmath.Exp/Log/Tan` exist and are used elsewhere; the jsmath doc says one ulp changes output text.
- **Status:** CONFIRMED-R.
- **Direction:** map to jsmath.

### 3.5 Errors that never reach the caller (ADR 0006)

**C34 — Medium — An over-limit image is silent in SVG, fatal in PNG.**

- **Mechanism:** `aster.go:538` (`info.Width, info.Height, _ = imagesFrom(ctx).size(ctx, u)`), then `report.go:60` (`image()` drops anything wrapping ErrLimit "as they end the call by themselves"). They don't on this path.
- **Scenario:** a Loader returns a 40 MiB PNG for an image mark without width or height. `VegaToSVG` succeeds with `<image … width="0" height="0">`, and the handler is never called. `VegaToPNG` fails with `ErrLimit: image … is over 33554432 bytes` (lead).
- **Status:** CONFIRMED-R(lead).
- **Direction:** carry ErrLimit out of `size()` and fail the SVG stage, as PNG and PDF do.

**C38 — Medium — Vega-Lite warnings vanish.**

- **Mechanism:** there is no warning sink in `internal/vegalite`. Upstream `log.warn` sites are silent no-ops: a typo'd transform (`{"calcualte":…}` → `default: continue`), a channel the mark can't use (`size` on `area`), `"aggregate":"avg"`, and incompatible scale properties (scale.go:309).
- **Docs:** `WithErrorHandler` says h is told "of every warning"; cli.md says warnings print to stderr.
- **Scenario:** a typo'd transform renders the chart without that transform, with a nil error and no Warning.
- **Status:** CONFIRMED-T.
- **Direction:** add a `compileCtx.warn` sink → `callReport.warn`; give `VegaLiteToVega` a way to receive warnings.

**C39 — Medium — Expression `warn()`/`info()`.** `internal/vega/context.go:389` (`func (c *rtContext) Log(...) {}`)

- **Upstream:** routes these to `view.warn`.
- **Status:** CONFIRMED-T.
- **Direction:** route `LogWarn` to Report (bounded, see C20), or document the exclusion.

**C43 — Medium — Limits disguised as JS exceptions.** `internal/expr/fn_time.go:167` (`throw("RangeError", "%v", err)` for ErrRangeTooLarge and ctx errors); `pad`/`repeat`/`replace` over `MaxStringLength`

- **Mechanism:** these are non-fatal `*expr.Error`s.
- **Scenario:** `timeSequence('milliseconds',0,1e10)` gives a RangeError with `errors.Is(err, ErrLimit)` false. A cancelled ctx gives "RangeError: context canceled". Either way the result is a partial render or a handler decision, not the documented ErrLimit or DeadlineExceeded.
- **Status:** CONFIRMED-R.
- **Direction:** a bound with no upstream counterpart is always `limitPanic`/`cancelled`.

**C44 — Medium — Quadratic String(array) charge.** `internal/expr/js.go:118` (`s.checkLen(len(b) + len(part))` charges the prefix per element)

- **Scenario:** `'' + arr` with 5,000×20 B strings fails with ErrLimit, while `join(arr, ',')` on the same input succeeds.
- **Status:** CONFIRMED-R.
- **Direction:** check the length while building and charge once.

**C62 — Medium — Filter region too large → element vanishes.** `internal/raster/effects.go:294`

- **Mechanism:** no error, no report. At the default 32 Mpx the default filter region of a full-canvas element exceeds the limit above a 22 Mpx canvas (e.g. scale 4 on 1200²).
- **Status:** CONFIRMED-R.
- **Direction:** ErrLimit or a Warning, or draw unfiltered.

### 3.6 Public API and affordances

**C35 — Medium — Double sanitize.**

- **Mechanism:** aster.go:524-531 writes `src = SanitizeURL(Loader.Sanitize(url))`. PNG/PDF read the SVG back and call `callImages.load` (images.go:59), which calls `Loader.Sanitize` again. When the fetch fails, the rasterizer then reports `ImageError(href, "not a data: URI")` (internal/raster/image.go).
- **Scenario:** a Loader mapping `x.png` → `https://h/x.png` fetches `https://h/https://h/x.png`. The handler receives two LoadErrors, one of them bogus. A Loader that only accepts logical names draws the image in SVG and drops it from PNG/PDF.
- **Docs:** contradicts loader.go:14-15 and loading-data.md.
- **Status:** CONFIRMED-R (two reviewers).
- **Direction:** carry the resolved image table from the SVG stage (Design tension 2), or require and document an idempotent Sanitize.

**C36 — Medium — Inconsistent `data:` detection.**

- **Mechanism:** `imageref.IsData` (imageref.go:43) is case-sensitive; `inlineImage` (aster.go:615) is not.
- **Scenario:** `url:"DATA:image/png;base64,…"` makes SVG succeed while PNG sends the URL to DenyLoader and **fails the call**.
- **Status:** CONFIRMED-R.
- **Direction:** use one case-insensitive helper.

**C37 — Medium — Re-entrancy deadlock.** aster.go:226 (`enter` holds `RLock` for the whole call) and :211 (`Close` takes `Lock`)

- **Scenario:** `VegaToSVG(spec, WithErrorHandler(func(error) error { c.Close(); return nil }))` with a failing data URL **hangs forever** (lead, 5 s probe). A handler or Loader that starts another render on `c` while a Close is pending also deadlocks (writer-preferring RWMutex).
- **Docs:** contradicts aster.go:59 "Close may be called at any time from any goroutine".
- **Status:** CONFIRMED-R(lead).
- **Direction:** decide the handler outside the lock, or have Close only cancel and mark when called from inside a call; at minimum document the restriction.

**C40 — Medium — Spec parse unbounded by default.**

- **Mechanism:** aster.go:410 and :735 pass `c.limits().MaxParseBytes`, which is 0 without WithMemoryLimit (aster.go:634-647), and `ParseJSONLimit(…, 0)` means MaxInt64 (internal/jsval/json.go:71). `checkSpecSize` is also a no-op without a limit.
- **Docs:** limits.md says "parsed JSON (spec and data) | 1 GiB" and calls the spec parse "bounded by size". Only loaded data gets the 1 GiB (internal/vega/view.go:123 `WithDefaults`).
- **Test gap:** `TestLimitsDocMatchesCode` passes because `LimitsForTest` calls `.WithDefaults()` (export_test.go:148), which the call sites don't.
- **Status:** CONFIRMED-T.
- **Direction:** use `c.limits().WithDefaults()` at the call sites; make the test read what the call sites use.

**C41 — Medium — HTTPLoader private-network check bypass.** `internal/loader/http.go:341` (`rt.DialContext = checkedDial(t.DialContext, px)` returns nil when `t.DialContext == nil`)

- **Mechanism:** net/http then uses its zero Dialer, without the check.
- **Scenario:** `Transport{DialTLSContext: f}` with `http://<name resolving to 127.0.1.1>/` returned the internal body. This contradicts loader.go:58-60.
- **Status:** CONFIRMED-R.
- **Direction:** install the guarded default dialer when `DialContext` is nil.

**C42 — Medium — FallbackLoader drops rewrites.** `internal/loader/loader.go:249` (`if _, err := child.Sanitize(ctx, uri); err == nil { return uri, nil }`)

- **Scenario:** `NewFallbackLoader(&HTTPLoader{BaseURL:"https://d/"})` with href `page.html` writes `page.html` into the SVG instead of `https://d/page.html`. A child that rewrites to a safe proxy has the raw URL emitted.
- **Status:** CONFIRMED-T.
- **Direction:** return the accepting child's sanitized URI.

**C70 — Medium — No per-call cancellation.**

- **Mechanism:** every method (aster.go:375-1057) builds its context from `c.base`. The only levers are the global `WithTimeout` and `Close`, which kills the whole Converter.
- **Scenario:** an HTTP server rendering per request cannot stop a render when the client disconnects or a request deadline passes. It either waits up to the timeout or closes the shared Converter, which cancels every other user's render.
- **Status:** CONFIRMED-T.
- **Direction:** add `…Context(ctx, spec, opts...)` variants, or a `WithContext(ctx)` RenderOption that is merged with `c.base`.

**C71 — Medium — Font bytes aliased and loaded repeatedly.**

- **Mechanism:**
  - `WithFont` keeps the caller's slice (options.go:159); `text.WithFont` doesn't copy either.
  - The layout measurer (aster.go:700), the PNG shaper (:830) and, with measurement off, the PDF measurer (:1066) are each built **separately and lazily** from `fontOptions()`.
  - `loadFonts` (:187) builds a throwaway measurer when measurement is off.
- **Scenario:** a caller that reuses or pools the font buffer after `New` silently changes the PNG font (built at the first PNG call) but not layout. A 20 MB CJK font costs two or three parses. With `WithSystemFonts`, the system is scanned twice, so a font installed between the first SVG and the first PNG makes layout and pixels disagree.
- **Docs:** contradicts "configuration is immutable after New" (aster.go:50).
- **Status:** CONFIRMED-T.
- **Direction:** copy font bytes in the Option; build one font set in New and share it between the measurer and the shaper.

**C72 – C80 — Low.**

- **C72 CLI data.** No `-base-url` and no file loader. `aster svg -i ex.vl.json -allow-http` on a Vega-Lite example with `data/cars.json` fails with `relative URI … not allowed (no BaseURL configured)`, and `withLoadHint` gives no hint. cli.md lists FileLoader as library-only, but not BaseURL.
- **C73 CLI exit codes.** See the table.
- **C74 generic families.** A typo in `WithDefaultFontFamily` silently falls back. troubleshooting.md recommends this option for "my font is not used".
- **C75** `max(0, index)` hides a caller bug.
- **C76** a typed-nil Loader panics.
- **C77** `lazyShaper.Shape` returns nil on shaper init failure, so the label transform treats text as empty.
- **C78** `WithVegaLiteVersion` accepts only major.minor, while `AvailableVersions` advertises "6.4.3".
- **C79** `"Local"` → `time.LoadLocation("Local")` = the host zone.
- **C80** `size()` passes a fresh 32 MiB budget instead of the call's. Images and data each have their own allowance equal to `MaxLoadBytes`, so a call can load 2× the "loaded bytes" budget limits.md documents as one.

### 3.7 Test infrastructure that can pass silently

- **C66 — Medium:** `internal/svgdiff/svgdiff.go:200` applies `Abs 0.5` to text content and every attribute. opacity 0.2 equals 0.6, the text "0.25" equals "0.5", and `1e+3`, `+5%` and `1,000` collapse into 1000, 5% and "1 000". So d3-format sign, exponent and separator regressions pass the corpus comparison. **Direction:** compare text exactly and keep the tolerance for geometry only.
- **C67 — Medium:** `internal/oracle/oracle.go:282` turns a timeout into `Result{Err:…}`, which is cached (CI restores the cache by prefix). The sweeps classify engine-error plus node-error as "ok", so one slow CI run permanently masks a later engine failure. **Direction:** don't cache timeouts and don't count them as agreement.
- **C68 — Medium:** `internal/svg/fuzz_test.go` checks only XML well-formedness, so C1 passes. **Direction:** assert the element and attribute sets, and seed raw-string positions.
- **C69 — Medium:** `scripts/fmacheck.sh` exits 0 on stale entries, and also when it finds zero sites (reproduced with `GOFLAGS=-trimpath`). **Direction:** fail on stale entries, a zero or collapsed site count, or a build failure; unset GOFLAGS.
- **C84–C87 — Low:**
  - **C84 limits doc test:** the fixed-bounds table is unchecked, and the spec-size row re-derives the formula instead of reading the code.
  - **C85 doc tests:** blind spots in the link, Mermaid and layers tests (see the table).
  - **C86 CI gates:** check.sh "SKIPPED" exits 0 despite its own header; its `go mod tidy` gate rewrites go.mod in place; benchgate's `2>/dev/null || continue` drops packages that don't compile.
  - **C87 upstream known divergences:** an entry accepts any divergence; the 300-character signature truncation can collide; the macOS expect entries are never verified in CI.

### 3.8 Documentation drift

These are not covered elsewhere.

- **C81:** `docs/design-rules.md:90-93` says VL compilation is not interruptible. It is (`vegalite.Options.Context`, compile.go:37). But see C4 and C15: the clone and normalize steps do ignore it.
- **C82:** architecture.md:110 omits the transitive mgilbir modules (golittlecms, gopenjpeg, formalis via pdf0), which matter to security readers.
- **C83:** licence notices are incomplete (see the table). Whether they are required is the maintainer's call.
- **Unbacked guarantees in limits.md.** "Ticks and legend entries" are bounded, but only after C14's loop. "Geographic path points 2,000,000 per render" only counts points that survive clipping (C17). "Expression nesting 512" is 200 on the VL side (C10). These are covered by the findings named.
- **Over-strong claims in options.go doc comments.**
  - options.go:397-412 (`WithQuantizePNG`) says "always safe" (C29).
  - options.go:327-358 (`WithErrorHandler`) says "every warning" (C38, C39) and doesn't forbid calling the Converter (C37).
  - README.md:48 lists time among ErrLimit cases, although a timeout gives DeadlineExceeded.
- **Stale comments (C107):** pngopt "both rendering engines"; the CLI's `"aster/runtime:"` prefix; raster.go "external resources not implemented"; recode.go "resvg always emits RGBA"; notoemoji/embed.go mentions resvg; `Recode` says "every pixel preserved", but 16-bit input is truncated.

### 3.9 Lower-severity correctness and hygiene

C88–C109 are in the summary table. Each table row is self-contained: a fixing agent can work from the location and the one-line issue. The reviewers' detailed scenarios for these are not repeated here.

---

## 4. Design tensions

1. **Budgets live at chokepoints; work happens everywhere.** About 30 of the findings here are one bug repeated. A bound exists in `internal/budget` or as a package constant, but it is charged at the operator boundary, at the sink, or after the transform returns, while the allocation or loop that matters happens upstream of it. Examples: geo charges at `limitSink` (C3, C17, C18), rows at two call sites as deltas (C13), raster at `allocCanvas` (C6, C7, C30), and expressions per function, opt-in (C21–C23). Every new transform or function must remember to charge and to poll, and many don't.
   - **Alternative to weigh:** a per-call *ledger* passed in ctx with two meters, bytes and work steps. Every amplifying primitive constructor charges it by construction: budgeted slice and array builders, a flattening sink, an image decoder, `ArrSlab`, regex compile. Add a lint rule (or a `recursionaudit`-style tool) that flags data-dependent loops in packages that don't take the ledger. Row accounting moves into the kernel as "replace" semantics.
   - **Cost:** plumbing ctx into scale, geo and text APIs that are currently pure. Doing nothing means this audit's findings recur at the same rate as new code.

2. **SVG is the only interface between stages.** PNG and PDF re-parse the SVG string, so everything the SVG stage knew is lost or must be re-derived:
   - which URLs were sanitized (C35);
   - which image failed and why (the duplicate reports in C35);
   - the structured font (C12, regex round trip);
   - finite-number guarantees (C57);
   - whether a string was escaped (C1 becomes PNG/PDF input as well).
   
   It is elegant for `SVGToPNG`/`SVGToPDF` and for "anything visible in a PNG is in the SVG". But the Vega→PNG path pays three serialisations and inherits every SVG-level ambiguity. **Alternative:** have Vega→PNG/PDF consume the scenegraph (or a typed display list) directly, with the SVG parser kept only for SVG input. A cheaper middle road: a per-call side table (URL → resolved URL → bytes or error; text item → structured font) that the back ends consult before re-deriving.

3. **Escaping is a per-writer decision.** `internal/svg` centralises escaping in `appendEscaped`, but raw writers (`AppendPos`, `appendAngle`, `itemNumAttr`, `appendWordTransform`) each choose for themselves, and one chose wrong (C1). Expression strings in the VL compiler are likewise built by concatenation, faithfully copying upstream's unescaped splices. **Alternative:** a writer API where spec-derived values can only go through escaping methods: a typed `attrRaw(constant)` vs `attr(value)`, and transform builders whose operands are numbers or escaped strings. Back it with a fuzz oracle that asserts structure (C68). For expressions, document that field, data and selection names are expression-capable input (security.md), since hosts building specs from CSV headers are exposed.

4. **Upstream fidelity is enforced eagerly, including where it costs safety.** JS index-key ordering is maintained on every insert (C4), and upstream's O(count) tick loop is ported verbatim (C14), as are d3's unbounded circle stream (C3) and the 200-deep recursive descent over generated chains (C10). Fidelity is checked at the output, so most of this could move to the edges: order keys once in the JSON writer, binary-search the tick count, parse chains iteratively. Output stays byte-identical and the loops become bounded. **Rule to weigh:** "faithful in output, free in algorithm", recorded as an ADR, so a reviewer can tell a deliberate verbatim port from an incidental one.

5. **Diagnostics have two channels, and only one is wired.** ADR 0006 made errors reach the caller for the Vega runtime. But the VL compiler, expression `warn()` and several leaf packages (scale ticks, raster filters, image sizing) still swallow problems or return empty results. Engine limits sometimes arrive as JS exceptions, which the handler is then allowed to ignore (C43). The project has no stated policy on schema-invalid input either (lenient in C50, upstream-throwing elsewhere). **Alternative:** one `diag` interface threaded like ctx (`Warn`, `Error`, `Limit`), with every leaf package required to use it, and an ADR stating "invalid input is an error; upstream leniency is reproduced only where it is documented upstream".

---

## 5. Expectation gaps

- Expected `WithMemoryLimit(64 MiB)` to bound a render's memory. Found 0.9–3.5 GB from sub-100 KB specs (C5–C9, C13, C24–C26).
- Expected `WithTimeout` to bound every call ("from start to finish"). Found loops that ignore it (C3, C4, C14, C16, C21, C22, C27–C29), and some that **return success after the deadline** (C5, C17, C18, C27).
- Expected SVG output of an untrusted spec or untrusted **data** to be safe to inline in HTML. Found `<script>` injection (C1).
- Expected `Loader.Sanitize` to be called once per URL, as documented. Found it applied twice for PNG/PDF (C35).
- Expected an image too large to fail every output format the same way. Found SVG silent, PNG ErrLimit (C34).
- Expected `WithErrorHandler` to see "every warning". Found that VL compile warnings and expression `warn()` never arrive (C38, C39).
- Expected `Close` to be callable "at any time from any goroutine". Found a deadlock from inside a handler (C37).
- Expected to cancel one render (an HTTP client gone away). Found no ctx parameter anywhere (C70).
- Expected a 250-category `sort` array to compile, as upstream does. Found "expression too deeply nested" (C10).
- Expected 20 independent data triggers to work. Found ErrLimit by default (C11).
- Expected PDF text to match the SVG for valid CSS weights. Found 450 pt text (C12).
- Expected `blend`, `fontVariant` and fade-out gradients to export to PDF. Found the whole PDF fails (C33).
- Expected `WithQuantizePNG` to be "always safe", "tens of ms". Found seconds to minutes (C29).
- Expected the documented 1 GiB spec-parse bound. Found none by default (C40).
- Expected the CLI to render a local Vega-Lite example with relative data. Found no way to (C72).
- Expected `WithDefaultFontFamily("Typo")` to fail like `WithFontFallback("Typo")`. Found a silent fallback (C74).
- Expected `$schema: …/vega-lite/v5.json` to choose, or at least warn about, the 5.8 compiler. Found that only `WithVegaLiteVersion` decides, silently. This is documented (options.go:116-119), but nothing warns on a major-version mismatch.
- Expected two aster SVGs inlined in one HTML page to keep their clip paths apart. Found ids restart (`clip1`, `gradient_0`) per document and there is no id-prefix option. The second chart's `url(#clip1)` resolves to the first chart's clip.

---

## 6. Open questions

1. **Threat model for `config.locale` and theme-like spec keys.** Should untrusted specs be allowed to set locales at all (C9), or only an API option? This also decides whether C9 is fixed by caps or by policy.
2. **Is a non-idempotent `Loader.Sanitize` supported?** The fix for C35 depends on it.
3. **Fidelity vs safety divergence.** Is diverging from upstream acceptable for bounded algorithms with identical output (C3, C4, C14), and for clamping absurd parameters (`clipAngle`, `precision`, `frame`)? An ADR would settle it once.
4. **Schema-invalid input policy.** Error, upstream-lenient, or "whatever upstream does"? C50 and the 5.8-mode leniency (about 300 of 1,500 mutants accepted where vega-lite@5.8.0 rejects) need one answer.
5. **Is SVG given to `SVGToPNG`/`SVGToPDF` fully untrusted?** security.md:7 says yes. If so, C28, C30, C60 and the raster findings are in scope at the stated severities; if not, they drop a level.
6. **Should `Result.Warnings` exist at all**, given that `Report` is the public channel? It is the unbounded store in C20.
7. **CI:** is perf.yml a required check, as performance.md implies? Who verifies the macOS half of `testdata/oracle-expect.txt` (the oracle job is Ubuntu-only)?
8. **Does upstream d3-delaunay hang on the C2 input?** If it does, C2 matches upstream behaviour. It still needs the bound, because here the outcome is a dead process rather than a hung tab.
9. **Licence notices (C83):** which ported projects require reproduction, and in what form? This is the maintainer's or a lawyer's call.
10. **The untracked `docs/audits/codebase-audit-2026-07-07.md`:** commit it as history, delete it, or ignore it? It currently skews local doc-test runs (C109).

---

*Reproduction inputs used by the lead are in `/tmp/aster-audit/` (not part of the repository): `bigtext.vl.json`, `img.vl.json`, `re.vg.json`, `loc.vg.json`, `topo.vg.json`, `clip.vg.json`, `vor.vg.json`, `rep.vl.json`, `keys.vl.json`, `sort.vl.json`, `tick.vg.json`, `rows.vg.json`, `trig.vg.json`, `fw.vg.json`. The reviewers' inputs are under `/tmp/aster-audit/{scene,docs,vlA}/`.*

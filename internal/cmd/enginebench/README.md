# enginebench

Times the three ways this repository can render Vega / Vega-Lite on the same
specs:

- **node**: upstream Vega 6.4.0 / Vega-Lite 6.4.3 in node
  (`purego/testdata/oracle-node`), text measured by node-canvas.
- **aster**: the root package, Vega in QuickJS and resvg, both WASM on andsifr.
- **purego**: the pure-Go engine.

```sh
(cd purego/testdata/oracle-node && npm ci)   # once
internal/cmd/enginebench/run.sh out           # all specs, ~1 hour; writes out/report.md
internal/cmd/enginebench/run.sh out -filter 'geo|map' -budget 1s -stages svg
```

`run.sh` runs the engines one after another, each in its own process, so
startup and peak memory belong to one engine and they never compete for the
CPU. Each process writes a JSON file; `enginebench -report a.json b.json …`
merges them.

## Method

- **Specs:** the 627 Vega-Lite examples (`testdata/vega-lite/v6.4.3/specs`)
  and the 92 Vega gallery specs (`purego/testdata/corpus/vg-gallery`).
- **Stages:** `vl2vg` (Vega-Lite → Vega JSON), `svg` and `png` (spec to
  output, end to end; for Vega-Lite this includes compilation). Each engine
  starts from the spec text on every run.
- **Warm engine:** one engine per process renders every (spec, stage) once
  before timing, so lazy initialisation, JIT warm-up and caches are out of the
  timed runs. Cases that fail there are listed, not timed, and the report
  compares only cases every engine completed.
- **Timing:** repeated runs until the budget (default 300 ms) is spent, at
  least 3, at most 200; a run over 2 s is not repeated. The median is reported.
- **Same inputs:** datasets are served from memory with CDN URLs mapped to the
  local vega-datasets copy, random numbers are seeded identically, and TZ is UTC.
- **Startup:** `init` is creating the engine (node: its own startup plus
  importing the modules; purego initialises lazily, so its cost shows in the
  first render), `first VL→SVG` is a cold bar chart right after.

## Caveats

- PNG is not the same pipeline everywhere: node draws the scenegraph on a
  cairo canvas and encodes it; aster and purego render SVG and rasterize it
  (resvg in WASM, purego's own rasterizer).
- Text is measured by node-canvas (DejaVu) in node and by forme with the
  embedded fonts in the Go engines; the root engine calls from QuickJS into Go
  for each measurement.
- Each render is single-threaded in all three, but the Go and V8 garbage
  collectors use other cores. purego's `Converter` is also safe for
  concurrent use, which this does not measure; see `BenchmarkParallel` in
  `purego/perf_bench_test.go`.
- `alloc_mb` in the Go JSON files is Go heap allocation per run; the root
  engine's WASM memory is not in it.
- Numbers from a laptop vary by a few percent between runs; compare ratios
  rather than absolute times across machines.

The results of the first run, with node 20 and 24, the QuickJS engine and
purego, are in [RESULTS.md](RESULTS.md) (raw JSON in `results-2026-09-30/`).

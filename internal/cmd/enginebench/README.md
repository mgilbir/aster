# enginebench

Times this package's engine against upstream Vega 6.4.0 / Vega-Lite 6.4.3
running in node (`testdata/oracle-node`, text measured by node-canvas) on the
same specs.

```sh
(cd testdata/oracle-node && npm ci)          # once
internal/cmd/enginebench/run.sh out           # all specs, ~30 minutes; writes out/report.md
internal/cmd/enginebench/run.sh out -filter 'geo|map' -budget 1s -stages svg
NODE="volta run --node 20 node" internal/cmd/enginebench/run.sh out20
```

`run.sh` runs node and the engine one after another, each in its own process,
so startup and peak memory belong to one engine and they never compete for the
CPU. Each process writes a JSON file; `enginebench -report a.json b.json …`
merges any number of them (the first node file is the baseline).

## Method

- **Specs:** the 627 Vega-Lite examples (`testdata/vega-lite/v6.4.3/specs`)
  and the 92 Vega gallery specs (`testdata/corpus/vg-gallery`).
- **Stages:** `vl2vg` (Vega-Lite → Vega JSON), `svg` and `png` (spec to
  output, end to end; for Vega-Lite this includes compilation). Each engine
  starts from the spec text on every run.
- **Warm engine:** each process renders every (spec, stage) once before
  timing, so lazy initialisation, JIT warm-up and caches are out of the timed
  runs. Cases that fail there are listed, not timed, and the report compares
  only cases every engine completed.
- **Timing:** repeated runs until the budget (default 300 ms) is spent, at
  least 3, at most 200; a run over 2 s is not repeated. The median is reported.
- **Same inputs:** datasets are served from memory with CDN URLs mapped to the
  local vega-datasets copy, random numbers are seeded identically, and TZ is UTC.
- **Startup:** `init` is creating the engine (node: its own startup plus
  importing the modules; the engine initialises lazily, so its cost shows in
  the first render), `first VL→SVG` is a cold bar chart right after.

## Caveats

- PNG is not the same pipeline: node draws the scenegraph on a cairo canvas
  and encodes it; the engine renders SVG and rasterizes it.
- Text is measured by node-canvas (DejaVu) in node and by forme with the
  embedded fonts in the engine.
- Each render is single-threaded in both, but the Go and V8 garbage
  collectors use other cores. A `Converter` is also safe for concurrent use,
  which this does not measure; see `BenchmarkParallel` in `perf_bench_test.go`.
- `alloc_mb` in the engine's JSON is Go heap allocation per run.
- Numbers from a laptop vary by a few percent between runs; compare ratios
  rather than absolute times across machines.

## Results

[RESULTS.md](RESULTS.md) has two runs. The latest (2026-10-03, raw JSON in
`results-2026-10-03/`) times the pure-Go engine against node 24 after the
performance work. The first (2026-09-30, raw JSON in `results-2026-09-30/`) is
the run that decided to replace the previous engine, Vega in QuickJS with resvg
for PNG (both WebAssembly), with the pure-Go one: node 20 and 24, the QuickJS
engine ("aster" there) and the pure-Go engine ("purego" there, now the package).

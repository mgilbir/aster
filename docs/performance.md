# Performance

A render takes milliseconds, and one `Converter` serves a whole process. The figures below are from [RESULTS.md](../internal/cmd/enginebench/RESULTS.md), measured on 2026-10-03 (commit 819a348) on an Apple M1 Pro. Times are medians of repeated runs on a warm engine, compared with upstream Vega 6.4.0 / Vega-Lite 6.4.3 in node 24 on the same specs.

| Workload (627 Vega-Lite examples) | aster, geometric mean | node 24 | speedup |
|---|---:|---:|---:|
| Vega-Lite → Vega | 0.30 ms | 0.52 ms | 1.7× |
| spec → SVG | 1.92 ms | 6.62 ms | 3.5× |
| spec → PNG | 7.17 ms | 14.8 ms | 2.1× |

A new `Converter` with the embedded fonts costs almost nothing: they load on first use, and the first bar chart after `New` took 9.4 ms. Fonts you register are loaded by `New` instead. Peak RSS over the whole benchmark was 270 MB for aster and 1,025 MB for node. The QuickJS-in-WebAssembly engine earlier versions used was 13–62× slower than this one; see [ADR 0001](adr/0001-pure-go-engine.md).

`internal/cmd/enginebench` reproduces these numbers ([how](../internal/cmd/enginebench/README.md)). On your own charts, `ASTER_PERF=1 go test -run TestPerfReport -v .` breaks a render down by stage (json, compile, parse, dataflow, text, svg, png, pdf).

## Concurrency

A `Converter` is safe for concurrent use by any number of goroutines. Each call renders on state of its own, and what calls share (fonts and their shaping caches, locales, colour schemes, compiled expressions) is immutable or synchronised. A `Loader` passed to `WithLoader` is called from several goroutines at once and must be safe for that; the package's own loaders are.

Throughput of a mixed SVG workload (`BenchmarkParallel`, renders per second, median of five runs, 10 cores):

| Goroutines | 1 | 4 | 10 |
|---|---|---|---|
| one shared `Converter` | ~950 | ~2,500 | ~3,300 |
| one `Converter` each | ~950 | ~2,700 | ~4,100 |
| one shared `Converter`, `GOGC=400` | ~1,080 | ~3,700 | ~4,900 |

The gap between a shared `Converter` and one per goroutine is the garbage collector's pacing, not contention. Mutex and block profiles show calls never wait on each other, but ten converters hold a larger live heap, so at the default `GOGC=100` the collector runs about a third as often: 458 cycles in 20,000 renders, against 1,542. A process that renders in parallel gets the throughput back by letting the heap grow further between collections: a higher `GOGC` (or `debug.SetGCPercent`), or `GOGC=off` with a `GOMEMLIMIT` sized for the machine. At `GOGC=400` one shared `Converter` matches one per goroutine.

## Regressions

CI gates pull requests on performance. `scripts/benchgate.sh` (`make benchgate`) benchmarks the base and the head alternately on one machine, and fails on a significant increase in allocations (over 2%), bytes (5%) or time (15%) in a fixed set of benchmarks. A PR label `perf-regression-ok` allows an intended trade-off.

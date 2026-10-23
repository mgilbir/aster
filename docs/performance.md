# Performance

A render takes milliseconds, and one `Converter` serves a whole process. The figures below are from [RESULTS.md](../internal/cmd/enginebench/RESULTS.md), measured on 2026-10-23 (commit ee3d5769) on a Linux x86-64 machine with an AMD Ryzen 9 6900HX. Times are medians of repeated runs on a warm engine, compared with upstream Vega 6.4.0 / Vega-Lite 6.4.3 in node 24.21.0 on the same specs, and the lowest of three alternating rounds per engine. The run before it, on an Apple M1 Pro at 819a348 (2026-10-03), measured a 3.5× speedup to SVG and 2.1× to PNG; between the two, the audit fixes made the parse stage slower and the follow-ups took part of that back.

| Workload (627 Vega-Lite examples) | aster, geometric mean | node 24 | speedup |
|---|---:|---:|---:|
| Vega-Lite → Vega | 0.39 ms | 0.67 ms | 1.7× |
| spec → SVG | 2.35 ms | 6.20 ms | 2.6× |
| spec → PNG | 7.07 ms | 12.4 ms | 1.8× |

A new `Converter` with the embedded fonts costs almost nothing: they load on first use, and the first bar chart after `New` took 7.9 ms. Fonts you register are loaded by `New` instead. Peak RSS over the whole benchmark was 320 MB for aster and 6,432 MB for node (1,025 MB for node on the M1 Pro). The QuickJS-in-WebAssembly engine earlier versions used was 13–62× slower than this one; see [ADR 0001](adr/0001-pure-go-engine.md).

`internal/cmd/enginebench` reproduces these numbers ([how](../internal/cmd/enginebench/README.md)). On your own charts, `ASTER_PERF=1 go test -run TestPerfReport -v .` breaks a render down by stage (json, compile, parse, dataflow, text, svg, png, pdf).

## Concurrency

A `Converter` is safe for concurrent use by any number of goroutines. Each call renders on state of its own, and what calls share (fonts and their shaping caches, locales, colour schemes, compiled expressions) is immutable or synchronised. A `Loader` passed to `WithLoader` is called from several goroutines at once and must be safe for that; the package's own loaders are.

Throughput of a mixed SVG workload (`BenchmarkParallel`, renders per second, median of five runs, 10 cores of the Apple M1 Pro, 2026-10-03):

| Goroutines | 1 | 4 | 10 |
|---|---|---|---|
| one shared `Converter` | ~950 | ~2,500 | ~3,300 |
| one `Converter` each | ~950 | ~2,700 | ~4,100 |
| one shared `Converter`, `GOGC=400` | ~1,080 | ~3,700 | ~4,900 |

The gap between a shared `Converter` and one per goroutine is the garbage collector's pacing, not contention. Mutex and block profiles show calls never wait on each other, but ten converters hold a larger live heap, so at the default `GOGC=100` the collector runs about a third as often: 458 cycles in 20,000 renders, against 1,542. A process that renders in parallel gets the throughput back by letting the heap grow further between collections: a higher `GOGC` (or `debug.SetGCPercent`), or `GOGC=off` with a `GOMEMLIMIT` sized for the machine. At `GOGC=400` one shared `Converter` matches one per goroutine.

## Regressions

`scripts/benchgate.sh` (`make benchgate BASE=<ref>`) benchmarks a base and a head alternately on one machine, and fails on a significant increase in allocations (over 2%), bytes (5%) or time (15%) in a fixed set of benchmarks. Times come from timed runs at GOMAXPROCS 1; allocations and bytes come from separate runs of a fixed number of iterations with the collector off (`GOGC=off`), where they repeat exactly, because in a timed run the number of collections, and so what `sync.Pool` still holds, varies from run to run. Run it before asking for a merge. CI runs it on every push to main, against the commit before the push, and on a release candidate, against the last release (the `Heavy gates` workflow, `.github/workflows/heavy.yml`); its `allow_regression` input reports an intended trade-off without failing.

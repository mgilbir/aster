# Limits and timeouts

Specifications are untrusted input: a 200-byte spec can ask for a billion rows. Every render is therefore bounded in time and in the work and memory it may use, and a render that would exceed a bound fails, with an error wrapping `aster.ErrLimit` (or `context.DeadlineExceeded` for time), instead of exhausting the host. The defaults are far above what real charts need. Tune them with two options on `New`:

```go
c, err := aster.New(
    aster.WithTimeout(10*time.Second),  // whole call, every stage; default 30 s; <= 0 disables
    aster.WithMemoryLimit(256<<20),     // replaces the memory budgets below; default: the fixed defaults
)
```

`errors.Is(err, aster.ErrLimit)` tells a spec that asked for too much from one that is broken. The message names the bound ("data rows 4000000 exceeds the limit of 1000000").

## WithTimeout

It bounds one call from start to finish: compiling, evaluation, text shaping, writing SVG, PNG and PDF. A run of text stops partway when the time is up. The error wraps `context.DeadlineExceeded` and names the stage. Default 30 s; zero or negative means no timeout (the CLI's `-timeout 0`). Slow but legitimate charts are mostly geographic: projections of detailed maps, and faceted maps.

Two steps are bounded by size rather than time, because they cannot be interrupted: parsing the specification's JSON, and parsing loaded JSON data.

## WithMemoryLimit

It is not a heap cap: Go has none per call. It is converted into the budgets below, each charged *before* the allocation it pays for. With `WithMemoryLimit(n)`, each budget becomes the formula in the third column, which **replaces** the default: a large `n` raises the bounds as well as a small one lowers them. `WithMemoryLimit(0)`, the default, keeps the defaults.

<!-- limits:begin (checked by TestLimitsDocMatchesCode; edit with the code) -->
| Budget | Default | With `WithMemoryLimit(n)` |
|---|---|---|
| data rows | 1,000,000 | n/512, at least 1,000 |
| scene items | 500,000 | n/1536, at least 1,000 |
| facet cells (subflows) | 20,000 | n/16384, between 1,000 and 20,000 |
| dataflow operators | 500,000 | n/1536, between 10,000 and 500,000 |
| loaded bytes (data and images of one call) | 64 MiB | n/2, at least 1 MiB |
| parsed JSON (spec and data) | 1 GiB | n/2, at least 1 MiB |
| expression strings and arrays | 128 MiB | n/4, at least 1 MiB |
| bitmaps the heatmap transform paints | 512 MiB | n/2, at least 1 MiB |
| SVG output | 128 MiB | n/4, at least 1 MiB |
| specification size | none | n/16, at least 1 MiB |
| glyphs of one shaped run of text | none | n/4, at least 1 MiB |
| rasterizer canvas memory (PNG) | 1 GiB | n, between 4 MiB and 1 GiB |
| rasterizer pixels per canvas (PNG) | 64 Mi | n/8, between 512 Ki and 128 Mi |
| memory charged to the call's ledger | 2 GiB | n, at least 1 MiB |
<!-- limits:end -->

The last row is the ledger's byte meter ([ADR 0010](adr/0010-bounds-charged-where-work-happens.md)): one per call, spanning every stage, charged by the code that allocates, before it allocates, for memory the rows above do not count. The rows above each bound their own kind of memory and keep their own semantics (rows are replaced as data flows, canvases come and go); an allocation is charged to one meter, its own if it has one and the ledger's otherwise, so together they stay within a small multiple of `n`. Today the ledger is charged for the programs of regular expressions an expression compiles (literals as the expression is parsed, patterns made from strings as they are made), parsed path data (custom symbols and `path` marks), the pattern tiles and colour glyph bitmaps the rasterizer keeps for reuse, the GeoJSON a TopoJSON document decodes to (every reference to an arc makes its positions anew) and the points of a graticule, and whatever a stage adds to it as it is bounded. Its default is about fifteen times what the heaviest chart of the corpus holds at its peak (130 MiB, rendered to SVG, PNG and PDF).

The ledger also meters the call's *work*: a loop whose trip count the input controls charges each step, and charging polls the call's context, so `WithTimeout` and `Close` stop that loop, wherever it is. Work is counted, not capped: time is bounded by `WithTimeout`.

On 32-bit platforms a count or size past what an `int` holds saturates at 2 GiB − 1 rather than wrapping; that only matters for limits above 8 GiB.

Fixed bounds, the same whatever `WithMemoryLimit` says:

| Bound | Value |
|---|---|
| geographic path points | 2,000,000 per render, counted where the projection produces them (after resampling, before the clip extent; each of albersUsa's three projections counts its own; a projection's `fit` counts too) |
| projection `clipAngle` | magnitude below 2^50 radians, about 6.4e16 degrees: past it upstream's clip circle never finishes, and aster refuses the angle with `ErrLimit` ([ADR 0011](adr/0011-faithful-output-free-algorithm.md)) |
| dataflow evaluations | 50,000,000 operator runs per render |
| one image | 32 MiB, 64 Mi pixels |
| HTTP response body | `HTTPLoader.MaxResponseBytes`, 64 MiB |
| file | `FileLoader.MaxBytes`, 64 MiB |
| JSON nesting | 256 levels; values nested at run time 1,024 |
| expression nesting | 512 levels |
| one string an expression builds | 16,777,216 UTF-16 code units (V8's own limit, 536,870,888, is a `RangeError` as upstream) |
| a regular expression compiled from a string | 1 MiB of source; its program, up to goecma262's 200,000 instructions, is charged to the call's ledger unless the shared cache keeps it, from a bound computed from the parsed pattern before it is compiled (a program past what the ledger has left, or past the 200,000, is refused unbuilt), as are literals and `countpattern`'s patterns |
| SVG input to `SVGToPNG` | 256 MiB, 4,000,000 elements, depth 256 |
| SVG input to `SVGToPDF` | 64 MiB, 1,000,000 elements |
| text shaping | about sixty times the lookup work of the heaviest example chart, per call |
| caches shared between calls | by the bytes they hold, entries not used lately dropped first: compiled expressions 32 MiB and regular expressions 32 MiB per process (no entry over 1 MiB), custom symbol paths 16 MiB per process (no entry over 1 MiB); per converter, measured widths 3 MiB, CSS fonts 1 MiB, font fallback lists 8 MiB, and glyph outlines 4 MiB per font face |
| transforms that multiply rows | `sequence` 10,000,000 (`sequence()` in expressions 1,000,000), `bin` steps, `kde`/`quantile` steps 1,000,000, `regression` order 100, `force` iterations 100,000, `cross` 4,000,000 cells, `contour` 16,000,000 cells, ticks and legend entries |

## Choosing a limit

- **Trusted specs** (your own charts): keep the defaults, or raise the timeout for heavy maps.
- **Untrusted specs** (a service that renders what users send): set `WithMemoryLimit` to what one render may hold, for example `256 << 20`, and a timeout that fits your request budget. Don't use a broad `HTTPLoader`; see [security.md](security.md). Run renders under a concurrency limit of your own: the bounds are per call, and ten parallel calls can each use their budget.
- A render that fails with `ErrLimit` on a chart you trust: raise `WithMemoryLimit`. For example `2 << 30` allows about 4M rows. Or raise the Loader's cap if the error names a response or a file.

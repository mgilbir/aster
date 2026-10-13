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

Parsing JSON, the specification's and loaded data's, checks the deadline as it reads: about every 16 KiB of text, inside a long string or run of white space too (a single number literal is read whole). Both are also bounded by size, by the parsed-JSON budget below (1 GiB, or `n/2` with `WithMemoryLimit(n)`), the specification also by its size row and loaded data by the loaded-bytes allowance. Decoding an image (bounded by its bytes, 32 MiB, and its pixels) cannot be interrupted either, but it is fed its bytes a few at a time with the deadline checked between them: a PNG makes at most about a quarter of a million pixels between two checks, however well it compresses. A JPEG or GIF can decode longer stretches, bounded only by its bytes and pixels (a progressive JPEG is put together after its last byte is read). What is done with a decoded image (converting its pixels, halving it for a small drawing, writing it into a PDF) checks the deadline every 65,536 pixels. So does every pass the rasterizer makes over a filter region (each primitive's, every pass of a blur, a conversion between colour spaces, the copy of the result) and over a layer it composites, a mask it applies or two clips it intersects; the buffers a filter allocates are zeroed by the runtime between two checks.

`WithTimeout` bounds every call; `WithContext(ctx)` bounds one call by `ctx` as well (its cancellation and its deadline), which a server uses to stop a render whose client has gone away. A call whose context ends fails with the context's error and returns no output.

## WithMemoryLimit

It is not a heap cap: Go has none per call. It is converted into the budgets below, each charged *before* the allocation it pays for. With `WithMemoryLimit(n)`, each budget becomes the formula in the third column, which **replaces** the default: a large `n` raises the bounds that have no ceiling as a small one lowers them (the table gives each one's floor and ceiling), and the bounds that are none by default become bounded. `WithMemoryLimit(0)`, the default, keeps the defaults.

<!-- limits:begin (checked by TestLimitsDocMatchesCode; edit with the code) -->
| Budget | Default | With `WithMemoryLimit(n)` |
|---|---|---|
| data rows | 1,000,000 | n/512, at least 1,000 |
| scene items | 500,000 | n/1536, at least 1,000 |
| facet cells (subflows) | 20,000 | n/16384, between 1,000 and 20,000 |
| dataflow operators | 500,000 | n/1536, between 10,000 and 500,000 |
| loaded bytes (data and images of one call, together) | 64 MiB | n/2, at least 1 MiB |
| parsed JSON (spec and data) | 1 GiB | n/2, at least 1 MiB |
| expression strings and arrays | 128 MiB | n/4, at least 1 MiB |
| bitmaps the heatmap transform paints | 512 MiB | n/2, at least 1 MiB |
| SVG output | 128 MiB | n/4, at least 1 MiB |
| specification size | none | n/16, at least 1 MiB |
| glyphs of one shaped run of text | none | n/4, at least 1 MiB |
| rasterizer canvas memory (PNG) | 1 GiB | n, between 4 MiB and 1 GiB |
| rasterizer pixels per canvas (PNG) | 64 Mi | n/8, between 512 Ki and 128 Mi |
| rasterizer path points held at once (PNG) | 128 Mi | n/16, at least 64 Ki |
| pixels of one image (SVG, PNG and PDF output) | 64 Mi | n/16, between 64 Ki and 64 Mi |
| memory charged to the call's ledger | 2 GiB | n, at least 1 MiB |
| Vega-Lite views | 65,536 | n/32768, at least 64 |
<!-- limits:end -->

The rasterizer's canvas memory is every pixel buffer alive at once: the canvas, offscreen layers (opacity groups, masks, filters), clip coverage maps (one per clipped element, held while it draws) and every result and scratch buffer of a filter chain. Its path points are the geometry the elements being drawn hold: parsed path data, flattened polylines, stroke outlines and scan-converter edges (an edge counts as five points); geometry wholly outside the canvas or clip is not built.

Data rows are the tuples alive at once, each held by the operator that made it: a data set's loaded or inline rows, the rows a transform makes (an `aggregate`'s groups, a `sequence`, a `fold`), the copies a derived data set hands on, the rows `modify()` inserts, a prefacet's cells. A transform that runs again replaces what it held, so a signal write is not charged twice, and an operator that only passes rows on (a `filter`, a sort) holds none ([ADR 0012](adr/0012-rows-held-by-their-maker.md)). With `WithMemoryLimit`, a row is also weighed: one that outweighs 512 bytes (many fields, long strings) counts for more than one, so the rows bound their memory, not just their number. Delimited text (CSV, TSV, DSV) is charged a row at a time, before the row is made.

The last row is the ledger's byte meter ([ADR 0010](adr/0010-bounds-charged-where-work-happens.md)): one per call, spanning every stage, charged by the code that allocates, before it allocates, for memory the rows above do not count. The rows above each bound their own kind of memory and keep their own semantics (rows are replaced as data flows, canvases come and go); an allocation is charged to one meter, its own if it has one and the ledger's otherwise, so together they stay within a small multiple of `n`. Today the ledger is charged for the regular expressions an expression or `countpattern` compiles (literals as the expression is parsed, patterns made from strings as they are made; the parse and then the compiled form, each before it allocates), parsed path data (custom symbols and `path` marks), the images the rasterizer decodes (before it decodes them, once per distinct image), the pattern tiles, colour glyph bitmaps, gradients, glyph outlines and SVG glyph documents it keeps for reuse (each cache also bounded by its own bytes), the maps and buffers of PNG quantization and recoding, the pixels of a PDF's images and the streams made of them, a PDF's content stream, its ExtGStates, forms and cached glyphs, the font programs cut out for it, the GeoJSON a TopoJSON document decodes to (every reference to an arc makes its positions anew) and the points of a graticule, the text of axis and legend labels (whose length a format's width and a locale choose), the call's table of the image and link URLs it resolved through the Loader, the tuple list each dataflow operator outputs (a filter's or a sort's list of the rows it passes on, which the data rows do not count; given back when the Vega run is over), and whatever a stage adds to it as it is bounded. A regular expression search holds for backtracking at most what the ledger has left: goecma262 allocates that memory as it searches, so the ledger bounds it rather than counts it ([ADR 0014](adr/0014-library-memory-bounded-by-ledger-room.md)). Its default is about fifteen times what the heaviest chart of the corpus holds at its peak (130 MiB, rendered to SVG, PNG and PDF).

The transforms charge the ledger for what they hold that is not rows: the rasters of `kde2d` and density `contour` (two float32 and a float64 raster a group, and the grid each group outputs), contour rings and their output geometry, the triangulation of `voronoi`, the running state of an `aggregate` or `pivot` cell for each measure, the `values` arrays of a `window`, the bitmaps and masks of `label`, and the board of `wordcloud`. A `density` reserves its samples as rows before drawing them, and `countpattern` its distinct tokens. Where upstream's algorithm would not end, aster refuses the input with `ErrLimit` ([ADR 0011](adr/0011-faithful-output-free-algorithm.md)): a `voronoi` whose coordinates leave the triangulation's links cycling, and a `pack` whose front chain does not close.

The ledger also meters the call's *work*: a loop whose trip count the input controls charges each step, and charging polls the call's context, so `WithTimeout` and `Close` stop that loop, wherever it is. Work is counted, not capped: time is bounded by `WithTimeout`.

Vega-Lite views are the views a Vega-Lite specification describes, counted before the compiler builds any: every unit, layer, concatenation and facet, and a repeat's spec once per repeated value (per combination of its `row`, `column` and `layer` values, nested repeats multiplying). A 1 KB specification of four nested repeats describes 810,000 views; past the bound it fails with `ErrLimit` naming the count.

On 32-bit platforms a count or size past what an `int` holds saturates at 2 GiB − 1 rather than wrapping; that only matters for limits above 8 GiB.

Fixed bounds, the same whatever `WithMemoryLimit` says:

<!-- fixed:begin (checked by TestFixedLimitsDocMatchesCode; edit with the code) -->
| Bound | Value |
|---|---|
| geographic path points | 2,000,000 per render, counted where the projection produces them (after resampling, before the clip extent; each of albersUsa's three projections counts its own; a projection's `fit` counts too) |
| projection `clipAngle` | magnitude below 2^50 radians, about 6.4e16 degrees: past it upstream's clip circle never finishes, and aster refuses the angle with `ErrLimit` ([ADR 0011](adr/0011-faithful-output-free-algorithm.md)) |
| dataflow evaluations | 50,000,000 operator runs per render |
| re-evaluations | in each run (the first, and each signal write's): 16 changes by the triggers of one data set, and 16 view size changes; a data set's change nests a run in the one that made it, 256 deep at most |
| delimited data (CSV, TSV, DSV) | 65,536 columns; without `WithMemoryLimit`, 16 cells per data row the budget allows |
| warnings | every warning reaches the handler with its text cut to 1 KiB, at a character boundary, with an ellipsis: Vega's, the Vega-Lite compiler's and a refused link's. Vega's are reported once per distinct text and call, and past 256 distinct ones, one more says the rest are not reported; the Vega-Lite compiler's are reported as often as upstream logs them (each comes from compile work the call's budget meters, and none is kept) |
| one image | 32 MiB, 64 Mi pixels (with `WithMemoryLimit`, the derived pixel bound above, the same in every output); an image past either fails the call in every output, `VegaToSVG` included when it reads the size of a mark that has none |
| HTTP response body | `HTTPLoader.MaxResponseBytes`, 64 MiB |
| file | `FileLoader.MaxBytes`, 64 MiB |
| JSON nesting | 256 levels; values nested at run time 1,024 |
| expression nesting | 512 levels, in the Vega-Lite compiler and in Vega. Vega counts a chain of binary operators (`a \|\| b \|\| …`) one level per operator; the Vega-Lite compiler, which only reads the fields an expression uses, counts such a chain, and a chain of conditionals (`a ? 0 : b ? 1 : …`, what an explicit `sort` array compiles to), as one level |
| one string an expression builds | 16,777,216 UTF-16 code units (V8's own limit, 536,870,888, is a `RangeError` as upstream) |
| a regular expression compiled from a string | 1 MiB of source; paid for to the call's ledger before it allocates, as are literals and `countpattern`'s patterns: its parse from goecma262's bound by the source's length, then its compiled form, up to goecma262's 200,000 instructions, from goecma262's bound on it (a pattern past what the ledger has left, or past that cap, is refused unbuilt), trued to what it holds once built and kept charged unless the shared cache keeps it; the deadline stops the parse and the compile |
| a regular expression search | goecma262's budget for each search: 1,000,000 steps plus 100 a byte of input, and for backtracking at most what the call's ledger has left, and at most 256 MiB plus 32 bytes a byte of input; past either the render fails with `ErrLimit`, and the deadline stops it |
| SVG input to `SVGToPNG` | 256 MiB, 4,000,000 elements, depth 256 |
| style sheets of SVG input to `SVGToPNG` | 4 MiB, 100,000 rules, 100,000,000 selector-matching steps; past any of them the call fails rather than draw the document partly styled |
| caches of one PNG render | gradients 8 MiB (4,096 entries), glyph outlines 32 MiB (16,384 entries), SVG glyph documents 64 MiB (64 documents), each charged to the call's ledger as it fills; one that would pass its bound is emptied first, and what it cannot keep is built again where it is used |
| one filter region (PNG) | 32 Mi pixels; past it the call fails rather than leave the element undrawn |
| pixel work of one PNG render | 128 Mi pixel operations, times the PNG scale squared when the scale is above one, and at least 16 per output pixel: pixels filled and stroked, edge crossings, points flattened, layers composited and masks applied, pattern tiles, and every pass a filter makes over its region, summed over the document; past it the call fails with `ErrLimit`. The heaviest chart of the corpora spends less than a fifth of it at every scale (TestPixelOpsMarginOverCorpus holds every chart to half). The bitmaps the `label` transform draws and a PDF's images of colour glyphs, drawn at their own pixels, have the budget of a PNG of their size at scale one |
| SVG input to `SVGToPDF` | 64 MiB, 1,000,000 elements, 10,000,000 path segments, 16 MiB of text; per render, glyph outlines kept 8 MiB and painted colour glyphs 16 MiB (more is drawn, not kept), a dash pattern 256 values |
| fonts | a font program given in memory 64 MiB, a system font file 1 GiB (mapped); per converter, 64 system font faces loaded at once (the one loaded longest ago is dropped past it) and 64 MiB of font programs cut out of collections and system fonts for PDF embedding, none over 32 MiB kept (each charged to the call's ledger when made) |
| text shaping | about sixty times the lookup work of the heaviest example chart, per call |
| caches shared between calls | by the bytes they hold, entries not used lately dropped first: compiled expressions 32 MiB and regular expressions 32 MiB per process (no entry of either over 1 MiB), custom symbol paths 16 MiB per process (no entry over 1 MiB); per converter, measured widths 3 MiB (no entry over 256 KiB), CSS fonts 1 MiB (no entry over 64 KiB), font fallback lists 8 MiB (no entry over 1 MiB), and glyph outlines 4 MiB per font face (no entry over 256 KiB) |
| transforms that multiply rows | `sequence` 10,000,000 (`sequence()` in expressions 1,048,576), `bin` steps 1,000,000, `kde`/`quantile` steps 1,000,000, `regression` order 100, `force` iterations 100,000 (a `collide` or `link` force's own iterations 1,000), `cross` 4,000,000 cells, `contour` 16,777,216 cells |
| ticks, labels and legend entries of one guide | 100,000, refused before they are listed (a `tickCount` with `tickMinStep` included) |
| values a scale's `ticks` lists | 1,048,576 (log ticks: 4,194,304 steps); calendar ticks and `timeSequence` 4,194,304 dates |
| colours sampled from a scheme (`schemeCount`) | 1,048,576 |
| `config.locale.number` | decimal, thousands separator, minus sign and each numeral 16 bytes; each currency part, percent sign and NaN text 64 bytes |
| `config.locale.time` | `dateTime`, `date`, `time` 256 bytes, expanding into each other to 4 KiB at most; each name 128 bytes, 64 names per list |
| format memos of one call (`format`, `timeFormat`, `timeParse` and labels) | 1 MiB each, by the bytes they hold, entries not used lately dropped first; no entry over 64 KiB |
<!-- fixed:end -->

## Choosing a limit

- **Trusted specs** (your own charts): keep the defaults, or raise the timeout for heavy maps.
- **Untrusted specs** (a service that renders what users send): set `WithMemoryLimit` to what one render may hold, for example `256 << 20`, and a timeout that fits your request budget. Don't use a broad `HTTPLoader`; see [security.md](security.md). Run renders under a concurrency limit of your own: the bounds are per call, and ten parallel calls can each use their budget.
- A render that fails with `ErrLimit` on a chart you trust: raise `WithMemoryLimit`. For example `2 << 30` allows about 4M rows. Or raise the Loader's cap if the error names a response or a file.

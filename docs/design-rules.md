# Design rules

The rules the engine's code follows, for contributors and for coding agents
working on it: what to treat as the source of truth, and the conventions that
keep the engine bit-compatible with upstream, safe on untrusted input, and
safe for concurrent use. Where it is in the code is [architecture.md](architecture.md);
how it is tested, [testing.md](testing.md); why it is built this way, [adr/](adr/).

Priorities, in order: **correctness** (output matches upstream Vega), **security**
(specifications are untrusted input), **performance**.

## Sources of truth

- **Behaviour:** upstream Vega 6.4.0 / Vega-Lite 6.4.3 and the exact d3 and
  vega-* module versions pinned in `testdata/oracle-node/package.json`
  (Vega-Lite 5.8.0 in `testdata/oracle-node-vl5`). Their sources are the
  reference to read before implementing anything; `npm ci` there installs
  them (see *Oracles*).
- **Differential oracle:** the same upstream modules running in node
  (`internal/oracle`). Given the same spec, the engine should produce the same
  SVG, the same compiled Vega and (against resvg) the same pixels.
- The Go code is an idiomatic Go design organised along Vega's own module
  boundaries. It is **not** a line-by-line transliteration of JavaScript: use Go
  types, Go error handling, Go naming. Keep upstream's *semantics* exactly,
  including its odd corners, and say in a comment when a rule is non-obvious
  ("vega-scale treats a `nice` of `true` on a time scale as …").

## Package map

[architecture.md](architecture.md) maps each package to its upstream counterpart and lists the package layers. Each package imports only packages of lower layers; `TestPackageLayers` checks that list against the code, so a new package goes into it and an import that goes up a layer fails.

## Core conventions

### Values

Everything dynamic is a `jsval.Value`. Its zero value is
`undefined`. Objects are `*jsval.Object` (insertion ordered). Numbers are
`float64`. A date is `KindTimestamp` (epoch ms), distinct from a number. A
regular expression is `KindPattern` compiled with `github.com/mgilbir/goecma262`
(never Go's `regexp`: its semantics differ from JavaScript's).

- `v.AsDouble()` is a *reading* (null/""/[] → NaN); `jsval.ToNumber(v)` is
  JavaScript's `Number(v)` coercion (null/""/[] → 0). Use the one upstream uses:
  `+x` in upstream is `ToNumber`.
- `v.IsTruthy()` is JavaScript truthiness. `v.AsString()` is `String(v)`.
- `jsval.JSNumberString` is `Number.prototype.toString`.

### Tuples

A datum is a `jsval.Value` of kind object. Identity is the
`*jsval.Object` pointer. Transforms that derive new tuples create new objects;
transforms that annotate (formula, stack, window, …) write fields into the
object they were given, as upstream does, so do not share one object between two
unrelated datasets without copying.

### Errors, not panics

Anything reachable from a specification returns an
error or is reported; it never panics. A top-level `recover` in the
public API converts an unexpected panic into an error, but relying on it is a
bug.

What upstream logs and carries on past (a failed load, an operator's
exception, a warning) is reported, never dropped: through the dataflow's
`report` (`internal/vega/report.go`), or, for images, the call's report in the
root package. The caller's `WithErrorHandler`, or by default the failure of the
call, decides what happens next ([errors.md](errors.md)). A new place where
upstream logs must report the same way.

### Security

Specifications and data are untrusted.

- Bound recursion (expression nesting, group nesting, JSON depth, hierarchy
  depth) and bound work that a spec can make arbitrarily large (`sequence`
  lengths, `bin` step counts, tick counts, `maxbins`, wordcloud/force
  iterations, …). Choose limits far above any real chart and return an error
  when exceeded.
- **Bounds are charged where the work happens** ([ADR 0010](adr/0010-bounds-charged-where-work-happens.md)).
  Each public call has one ledger (`budget.Ledger`, in the call's
  `context.Context`; `budget.LedgerFrom(ctx)`), spanning every stage. Every
  loop whose trip count the input controls meters its steps: `Ledger.Work(n)`
  per coarse step (a row, a feature), or a `budget.Meter` in a tight loop.
  Charging polls the call's context, so `WithTimeout` and `Close` stop the
  loop that is running; a bare `ctx.Err()` at a stage boundary is not enough.
  Every allocation the input sizes is charged **before** it is made, to its
  own meter if it has one, otherwise to the ledger (`Ledger.Alloc`, `Free`
  when released). Code that cannot return an error uses `MustWork` and
  `MustAlloc`, which panic with a `*budget.Stop` that the public API recovers.
- The older meters stay where they are and keep their semantics: data rows,
  loaded bytes and path points (`budget.Budget`, one per Vega render). Rows
  are held by the operator that made them ([ADR 0012](adr/0012-rows-held-by-their-maker.md)):
  every path that makes tuples (ingestion, a transform's new tuples, the
  copies of a derived relay, a prefacet's cells, `modify()` and `setdata()`)
  goes through the kernel's one charge point (`internal/vega/rows.go`), which
  replaces what the operator held; an operator that passes tuples on holds
  none. The tuple list an operator outputs, unless it is its input's, is
  charged to the call's ledger the same way. Every transform that multiplies tuples (`impute`, `kde`, `quantile`,
  `flatten`, `fold`, `cross`, `sequence`, `pivot`, `aggregate` cells,
  `regression`) reserves the rows it makes with `budget.Reserve` before it
  allocates, counting what its previous run held as free. The tests check that
  no transform passes on a tuple no operator holds. The expression
  evaluator's budget for large strings and arrays (`expr.StringBudget`) and
  its per-string length limit: an expression function builds its strings and
  arrays only through the evaluator's builders (`strBuilder`, `makeItems`,
  `itemBuilder`, `concat`), which check the limit and charge the budget before
  they allocate; `TestEveryBuiltinIsMetered` holds every function of the table
  to it. The rasterizer's
  canvas and pixel budgets, the shaping budget, the SVG size.
- Every bound is derived from `WithMemoryLimit` in one function,
  `deriveLimits` (limits.go), and documented in [limits.md](limits.md), which
  `TestLimitsDocMatchesCode` checks.
- Where upstream's algorithm is unbounded or super-linear in the input, use a
  bounded one with identical output, and refuse parameters past the point
  where upstream's output is degenerate with an error wrapping `ErrLimit`
  ([ADR 0011](adr/0011-faithful-output-free-algorithm.md)).
- Every stage honours the call's context, Vega-Lite compilation included
  (`vegalite.Options.Context`). In `internal/vegalite` a loop meters its
  steps with `cc.work(n)` when its trip count comes from the spec or its
  data: views and children, selections and params, transforms and their
  field lists, encoding arrays and conditions, values, domains, expression
  tokens, dataflow nodes, the items of a value walked whole. A loop over a
  compile-time table (channels, property lists, fixed config property names)
  does not; a spec-sized loop around one charges a heavier step instead
  (`modelStep` per child model). A function takes the compilation once,
  `cc := ...`, and a helper with such a loop takes the `*compileCtx`.
  `TestDeadlineIsHonouredThroughoutCompile` is the guard: it compiles a
  family of specs, each large in one dimension, and fails if any stretch of
  the compilation goes 200 ms without a poll; a new spec-sized dimension
  gets a spec there. The Vega parser and the dataflow's build charge
  `Scope.work` and `flowGraph.work` the same way, guarded by
  `TestParsePollsThroughout`. JSON an input supplies (a specification, a
  `values` string, a loaded file) is parsed with `jsval.ParseJSONContext`,
  which polls as it reads.
- An input that once exhausted a bound goes into the bounded-input corpus
  (`testdata/security/bounded`, run by `TestBoundedInputs` in every test run;
  see [testing.md](testing.md#robustness)).
- Nothing attacker-controlled is written raw into the SVG: every attribute and
  text goes through the escaping writer (which also drops characters XML 1.0
  forbids), CSS-bound values (`blend`) are allow-listed, and image and link URLs
  pass both the `Loader` and vega-loader's scheme allow-list. In `internal/svg`
  this holds by construction: only markup.go touches the output buffer, and
  raw markup (names, constant values) is of type `lit`, which a string variable
  cannot become without a conversion `TestNoRawMarkup` rejects. A value that is
  a word where a number is expected (`x: "wide"`) is spec text too: upstream
  prints it, and it is escaped like any other.
- No reflection-based evaluation, no access to the host from expressions:
  the expression language can only call the whitelisted function table.
- Data loading and image/link URLs only through the configured `Loader`.
- Regular expressions run through goecma262's bounded matcher; use its `Err`
  methods for untrusted patterns. Compile with `jsval.NewPattern`, which pays
  for the parse and the compiled form before either allocates and stops at
  the context's deadline, and search with a copy made by `WithContext` for
  the call, its backtracking memory bounded by the ledger's room
  (`jsval.MatchMemory`); never change a Regexp the pattern cache shares.

### Floating point parity

The Go spec allows `x*y + z` to be fused into a
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

### Constants

Go evaluates constant expressions exactly and rounds only the
result; JavaScript rounds every intermediate to a double. So a multi-step
constant such as `30 * (math.Pi / 180)` can fold to a different double than
upstream computes. Where a constant expression has more than one rounding
step and feeds output, declare it as a `var` (evaluated at run time with
per-step rounding) or check it against node.

### Concurrency

A `Converter` is safe for concurrent use; the engine keeps that
true by construction. Package-level variables are one of: immutable after
`init` (tables, registries filled by `init` functions), a `sync.Once`
initialisation, a `sync.Pool`, or a cache. A cache whose keys or values the
input chooses is an `internal/bcache.Cache`, bounded by the bytes its entries
hold as the caller estimates them (what the value retains, not its header),
because an entry count is no bound when one entry can be a megabyte.
Everything a render mutates hangs off values the render creates: the
dataflow, the scenegraph, projections (and the pipelines they cache for a
`geo.Path`), the `Random`, the budget, the ledger. A compiled
`expr.Program` is immutable and shared between renders through
`expr.CompileCached`. New package-level mutable state needs a lock and an
entry in this list; `TestConcurrentConverterSharesNothing` (run it with
`-race`, and with `ASTER_STRESS_FULL=1` for the whole corpus) is the guard.

### Performance

Avoid `interface{}` boxing on hot paths, avoid reflection,
avoid `fmt` in inner loops, preallocate, compile expressions once to closures.
Benchmarks live next to the code (`BenchmarkXxx`).

### Style

gofmt, `go vet` clean. Package doc comments. Comments explain *why*
and name the non-obvious upstream rule; they do not narrate the code. No
references to other ports of Vega. Exported identifiers only where another
package needs them.

## Oracles and tests

Unit tests use golden vectors recorded from upstream with node: each package that needs them keeps a generator beside its tests (`<pkg>/testdata/gen_*.mjs`) and the recorded output, so tests need neither node nor network. End to end, the engine's SVG and compiled Vega are compared with upstream's on the corpus and the sweeps, and every difference is listed with its reason or the test fails. How to run all of it, and how to write a generator, is in [testing.md](testing.md).

## Dependencies

Do not add module dependencies beyond the standard library, `golang.org/x/image`,
`golang.org/x/text`, `github.com/mgilbir/goecma262`, `github.com/mgilbir/forme`,
`github.com/mgilbir/pdf0` (PDF output) and the module's own packages.

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
- `jsval.JSNumberString` is `Number.prototype.toString`; `jsval.ParseDecimal`
  is the one conversion of decimal text to a number (JSON.parse's, exact for
  literals of any length): never `strconv.ParseFloat` on a value's text.
- Read a value as upstream does, with jsval's views, not Go's nearest
  convenience: `JSLower`, `JSUpper`, `JSTrim` and `IsJSSpace` (never
  `strings.ToLower`, `TrimSpace` or `unicode.IsSpace`, which differ on İ, ß,
  U+0085 and U+FEFF; `TestNoGoCaseOrSpaceInTheEngine` checks); `UpperFirst`
  for `s[0].toUpperCase() + s.slice(1)`; `RegExpFoldEqual` for a
  case-insensitive regular expression; `v.Strict()` where upstream compares
  with `===`, a `switch` or `Set.has`, `v.PropertyKey(n)` where it reads
  `obj[v]` or `v in obj`, `v.StringIs(s)` for `String(v) === s`;
  `ToArrayLike` where an `Array.prototype` method takes any array-like;
  `Walker.Join` for `Array.prototype.join`; `PlainMap` for upstream's `{}`
  used as a map; `v.InternKey()` (d3's InternMap) or `v.MapKey()` (a `Map`)
  to key values by identity, never their text.
- In the Vega-Lite compiler a specification's keyword (a mark or scale
  type, an orient, a selection's type) stays a `Value` up to the site that
  reads it, and the site reads it as upstream does there: `strIs`, `strIn`,
  `setHas` for `===`, `switch`, `includes` and `Set.has`; `propKey` for
  `obj[v]`; `tmpl` for a template literal; `looseEq` for `==`. An
  undefined name is the key `"undefined"`, never `""`. Never compare a
  value's text with a name: `TestKeywordsAreNotComparedAsText` fails on it
  unless `internal/vegalite/testdata/keyword-text.allow` says why upstream
  compares the text there ([ADR 0026](adr/0026-keywords-read-as-upstream-reads-them.md)).
- A walk of a value (its String, JSON, stringValue, a deep or relational
  comparison, an elementwise interpolation) is made with the call's
  `jsval.Walker`, which the run's state carries (the view, its operators'
  parameters, the scenegraph, the scales, the expression scope), so its
  steps and its text are charged to the call ([ADR 0018](adr/0018-value-walks-bounded-by-the-call.md)).
  The detached forms (`AsString`, `jsval.Equal`, …) are for a
  specification's literals; `TestEngineWalksUseTheCallsWalker` holds every
  other use to a reviewed entry in `scripts/walk.allow`.
- A test fixture's encoding of what JSON cannot carry (the serialized
  scenegraph's `{"$num": "Infinity"}`, `$gid`/`$gref` gradient identity and
  recorded `{"path", "ops"}` generators) is read by its loader only
  (`scene.FromJSON`), never where a live value is read: `Item.Set` reads a
  value as JavaScript does, whatever keys an object a spec computes has
  (`TestRecordingConventionsStayInTheLoader`).

### Tuples

A datum is a `jsval.Value` of kind object. Its identity is its tuple id
(`Object.TupleID`), given when it is ingested, as vega-dataflow's `tupleid`:
where upstream keys tuples by id (a `modify()` or `setdata()` change set,
`treelinks`, `isTuple`), so does the engine, never by the equality of fields.
Ingest copies an object from outside the run and the copy keeps its id, so
within one operator's flow the `*jsval.Object` pointer stands for the same
tuple. Transforms that derive new tuples create new objects;
transforms that annotate (formula, stack, window, …) write fields into the
object they were given, as upstream does, so do not share one object between two
unrelated datasets without copying.

### Errors, not panics

Anything reachable from a specification returns an
error or is reported; it never panics. A top-level `recover` in the
public API converts an unexpected panic into an error, but relying on it is a
bug. In the root package it is `recoverStage`, which makes a stage's panic
the stage's error through `stageErr`, so a bound or a deadline reached deep
inside a stage is named as one returned would be.

The caller's code (a Loader's methods, the error handler, a `WithSignal`
value's `MarshalJSON`) is called only through `Converter.callback`: the one
boundary that marks the goroutine as inside a call, for `Close`, and turns a
panic in that code into its error, whatever goroutine runs it. A goroutine
the library starts recovers a panic first thing, since no other recover
reaches it. `TestPanicBoundaries` checks the three on the source ([ADR 0021](adr/0021-caller-code-behind-one-boundary.md)).

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
- The expression environment (the methods of `internal/vega`'s `rtContext`
  that `expr`'s interfaces name) is held to the same
  ([ADR 0025](adr/0025-expression-environment-state-bounded-by-the-call.md)): a method that keeps
  state for the call charges the ledger for it as it records it (a `modify()`
  change set, given back once applied), an array it hands an expression past
  256 elements is charged as the evaluator charges its own (`domain()`,
  `gradient()`), and a value that stands for engine state (a shape generator,
  a guide's operator reference) is a Go value on its object
  (`jsval.HostFunction`, `Object.SetHost`): garbage once nothing refers to it,
  and no JSON a specification or its data spell can stand for one.
  `TestEveryEnvMethodIsMetered` enumerates the methods and calls each
  repeatedly. Every dataflow operator polls through the rows it visits:
  `TestEveryOperatorPollsThroughout` enumerates the operator registry, so a
  new operator needs a case or a reason it visits no rows.
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
- The Vega-Lite compiler charges its memory to the call's ledger as well as
  its work ([ADR 0019](adr/0019-vega-lite-compiler-byte-meter.md)). In
  `internal/vegalite` a string whose length the specification controls is
  built only through `sbuf` (a metered `strings.Builder`), `joinCC`,
  `cc.cat` or `jsReplaceAll`, each charged before it allocates; no other
  function holds a `strings.Builder`, joins or repeats a list, replaces with
  a replacement that is not a constant, appends to a string in a loop or
  dedupes a list by a scan in a loop, unless `TestNoUnmeteredBuilders`
  allows it with why it is linear in one input. A copy or merge per child,
  per node or per frame (an ancestor parse, a field set, a parse merged
  into its parent, a union of domains) is made linear (shared until
  written, merged in place on the larger side, extended in place) and
  charged with `cc.alloc`; a new family of such copies gets a spec in
  `complexitySpecs` and `TestCompileAllocGrowsLinearly`. What a view
  compiles once per view it is copied into (the config, a layer's shared
  encoding, the selections pushed into it) is weighed in `countViews`
  before any view is built.
- An input that once exhausted a bound goes into the bounded-input corpus
  (`testdata/security/bounded`, run by `TestBoundedInputs` in every test run;
  see [testing.md](testing.md#robustness)).
- Nothing attacker-controlled is written raw into the SVG: every attribute and
  text goes through the escaping writer (which also drops characters XML 1.0
  forbids), a CSS-bound value (`blend`) is written only when it cannot break
  out of its declaration (none of `; { } ( ) : \ " ' /` nor a control character; upstream
  writes any value, so such a value is the one deliberate divergence), and image and link URLs
  pass both the `Loader` and vega-loader's scheme allow-list. In `internal/svg`
  this holds by construction: only markup.go touches the output buffer, and
  raw markup (names, constant values) is of type `lit`, which a string variable
  cannot become without a conversion `TestNoRawMarkup` rejects. A value that is
  a word where a number is expected (`x: "wide"`) is spec text too: upstream
  prints it, and it is escaped like any other. The writer owns the size bound
  and the deadline the same way: each method that appends checks the size
  where it writes (and polls the context every 64 KiB of output), the error is
  sticky and later writes are no-ops, so no caller checks the size and one
  item cannot grow the document past it.
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
`expr.CompileCached`. The HTTP transports `internal/loader` derives from a
caller's `*http.Transport` are a cache keyed by a weak pointer to it, under a
lock, dropped with it. New package-level mutable state needs a lock and an
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
`github.com/mgilbir/goecma262`, `github.com/mgilbir/forme`,
`github.com/mgilbir/pdf0` (PDF output) and the module's own packages.

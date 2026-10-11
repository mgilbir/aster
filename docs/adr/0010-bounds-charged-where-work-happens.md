# 0010: Bounds are charged where the work happens

**Status:** accepted, 2026-10-11.

## Context

[ADR 0005](0005-bounded-untrusted-input.md) made every render bounded in time and memory. The bounds were real, but they were charged at chokepoints: at a stage's boundary, at a geographic sink, after a transform returned, per operator. The work that mattered often happened upstream of the chokepoint. The 2026-10-11 audit found about thirty findings that are this one bug repeated: a 4 KB spec that flattens glyphs into 917 MB before any canvas is charged, a 419-byte spec whose regular expressions compile to 923 MB, a `clipAngle` that loops forever before the point budget sees a point, caches that keep megabyte entries because they count entries. Every new transform or function had to remember to charge and to poll, and many did not. The guards that would have shown it were opt in (`ASTER_SECURITY`), so CI never ran them.

## Decision

- **One ledger per call.** `internal/budget.Ledger` is created with the call's context in `opContext` and travels in it, so every layer reaches it (`budget.LedgerFrom(ctx)`) without a signature change. It spans every stage: compile, the Vega run, SVG, PNG, PDF. Its methods are atomic, allocation-free and nil-safe, and it is safe for the concurrency the engine has (images are fetched in parallel).
- **Work is metered in the loop.** Every loop whose trip count the input controls charges its steps, `Ledger.Work(n)` for coarse steps and a `budget.Meter` for tight loops, and charging polls the call's context: `WithTimeout` and `Close` end the loop that is running, within a millisecond of work. Work is counted, not capped; time is bounded by `WithTimeout`.
- **Memory is charged before it is allocated.** Every allocation the input sizes is charged first, `Ledger.Alloc(n)` (given back with `Free` when released), to its own meter if it has one (rows, loaded bytes, canvases, path points, strings, SVG size) or to the ledger's byte meter otherwise. Code that cannot return an error uses `MustAlloc`/`MustWork`, which panic with a `*budget.Stop` the public API turns back into the call's error.
- **One place derives the bounds.** `deriveLimits` (limits.go) turns `WithMemoryLimit(n)` into every bound of a call, the ledger's `n` among them; [limits.md](../limits.md) documents each and a test checks them against the code.
- **State that outlives a call is bounded by bytes.** A cache keyed by what a specification, its data or a font chooses is an `internal/bcache.Cache`: two generations, as vega-util's `lruCache` has, the older dropped whole and an entry used from it promoted, and hits served without a lock from an immutable snapshot; bounded exactly by the bytes its entries hold as estimated by the caller (a compiled regular expression counts its program, a parsed path its commands), keeping no entry too large to share it. A cache that lives for one render may instead charge what it keeps to the ledger, and drop it when the ledger refuses.
- **The guard runs by default.** `TestBoundedInputs` renders every input under `testdata/security/bounded` to every output it has, each in a child process, under `WithMemoryLimit(64 MiB)` and `WithTimeout(2 s)`, and fails on a kill, a panic, an internal error, an error that is neither a limit nor the deadline, a late return, or a heap past four times the limit. It runs in `go test -short ./...`. A finding fixed adds its reproducer there.

## Why

A bound charged at the allocation cannot be bypassed by code upstream of a chokepoint: whoever allocates charges. A ledger in the context reaches every package without plumbing a new parameter through pure APIs. Byte-bounded caches make "bounded" mean what a host cares about. A guard that CI runs keeps the next transform honest.

## Consequences

- The rule for new code: every spec-sized loop meters work, and every spec-sized allocation is charged first ([design-rules.md](../design-rules.md#security)). A reviewer can ask, of any loop or `make`, which meter pays for it.
- Packages whose APIs have no context (`geo.ConfigureProjection`, `TopologyFeatures`, scale tickers) gain one as their findings are fixed; the ledger is designed to be passed that way.
- Charging adds a few atomic operations per coarse step and an addition per tight step. `BenchmarkScenes` holds.
- A render near a bound can now fail where it did not, if it was holding memory no bound counted. The ledger's default sits far above the corpus (2 GiB against a peak of 130 MiB).

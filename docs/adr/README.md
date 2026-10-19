# Architecture decision records

Decisions whose reason is not visible in the code. Each says what was decided, why, and what it costs; a decision that is replaced is marked superseded, not deleted.

| # | Decision | Status |
|---|---|---|
| [0001](0001-pure-go-engine.md) | A pure-Go engine instead of Vega in QuickJS/WebAssembly | accepted, 2026-10-01 |
| [0002](0002-forme-text-engine.md) | forme for text shaping and measurement | accepted, 2026-09-30 |
| [0003](0003-unrounded-advances.md) | Measure text with unrounded advances by default | accepted, 2026-09-30 |
| [0004](0004-node-as-oracle.md) | Upstream in node is the test oracle | accepted, 2026-10-01 |
| [0005](0005-bounded-untrusted-input.md) | Specifications are untrusted: errors, never panics, and bounded work | accepted |
| [0006](0006-errors-reach-the-caller.md) | What upstream logs fails the call unless a handler lets it go | accepted, 2026-10-11 |
| [0007](0007-system-fonts-never-fallback.md) | System fonts only by name, never as a fallback by default | accepted |
| [0008](0008-cli-language-detection.md) | The CLI detects Vega vs Vega-Lite by schema, then by shape, and refuses to guess | accepted, 2026-10-11 |
| [0009](0009-caller-chooses-fonts.md) | Registered families are the caller's, and so is the fallback chain | accepted, 2026-10-11 |
| [0010](0010-bounds-charged-where-work-happens.md) | Bounds are charged where the work happens: a per-call ledger, byte-bounded caches, and a bounded-input corpus that CI runs | accepted, 2026-10-11 |
| [0011](0011-faithful-output-free-algorithm.md) | Faithful in output, free in algorithm: bounded algorithms with identical output, and degenerate parameters refused | accepted, 2026-10-11 |
| [0012](0012-rows-held-by-their-maker.md) | Rows are held by the operator that made them: one charge point, replaced per operator, checked by the tests | accepted, 2026-10-12 |
| [0013](0013-invalid-input-fails-where-upstream-throws.md) | Invalid Vega-Lite input fails where upstream throws, in both versions, with upstream's message | accepted, 2026-10-12 |
| [0014](0014-library-memory-bounded-by-ledger-room.md) | Memory a library allocates as it works, unsized beforehand, is bounded by the ledger's room rather than charged | accepted, 2026-10-13 |
| [0015](0015-filters-follow-the-specification.md) | PNG filters follow the Filter Effects spec where resvg departs from it | accepted, 2026-10-14 |
| [0016](0016-custom-projections.md) | Custom projections are a raw projection and its parameters, per Converter | accepted, 2026-10-14 |
| [0018](0018-value-walks-bounded-by-the-call.md) | Walks of a value (its text, JSON, comparisons) are bounded by the call that makes them, with the call's jsval.Walker; identity is a key, not a walk | accepted, 2026-10-19 |
| [0019](0019-vega-lite-compiler-byte-meter.md) | The Vega-Lite compiler charges its memory to the call's ledger through metered builders, makes its per-child copies linear, and weighs views by what they compile | accepted, 2026-10-18 |
| [0025](0025-expression-environment-state-bounded-by-the-call.md) | The expression environment's state is bounded by the call; engine handles are Go values; tuples are identified by id | accepted, 2026-10-19 |

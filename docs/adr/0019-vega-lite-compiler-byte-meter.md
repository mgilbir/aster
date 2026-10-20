# 0019: The Vega-Lite compiler charges its memory, and weighs its views

**Status:** accepted, 2026-10-18.

## Context

[ADR 0010](0010-bounds-charged-where-work-happens.md) charges every allocation the input sizes to the call's ledger before it happens. The Vega-Lite compiler charged its work (every spec-sized loop polls the deadline) and counted its views, but never charged a byte. Its memory was bounded only by time, so any structure that grows faster than the specification went past `WithMemoryLimit` (audit 2026-10-18, C3, C6, C9, C10, C11, C26):

- strings upstream also builds, quadratic in their input: the guard of every prefix of a long field path (an 8 KB spec made 56 MB of Vega, allocated in one step), a label expression whose format's `` $` `` copies the text before each match;
- copies and merges per child, node or frame that upstream makes and aster had ported as they are: the ancestor parse every child model copied, the field set every frame of a time-unit chain copied, the parse a chain of filters merged into its parent one field at a time, every prefix of a dotted field joined into a string; 186–262 KB specifications reached 1.4–1.9 GB under a 256 MiB limit;
- fan-out the view count did not see: every top-level selection copied into every unit (a 37 KB specification was OOM-killed), and text every view compiles again (a 200 KB number format in the config, written into each of 256 repeated views, made 257 MB from 200 KB under a 64 MiB limit).

Upstream builds the same strings and stops only at V8's string length or heap. The owner decided that a compile past its budget fails with `ErrLimit` at the call's budget, without emulating V8's `RangeError`.

## Decision

- **The compiler meters bytes to the call's ledger.** `compileCtx.alloc(n)` charges `n` bytes before they are allocated and fails the compilation with an error wrapping `ErrLimit` past what the ledger has left. It takes the ledger's room 64 KiB at a time, so a charge is an addition and a comparison; it also counts a step of work per kilobyte, so a large allocation polls the deadline first.
- **One kind of builder.** A string whose length the specification controls is built only through `sbuf` (a `strings.Builder` that charges each growth before it grows, at the size `Grow` will allocate), `joinCC` (a join charged for its result), `cc.cat` (a concatenation charged first) and `jsReplaceAll` (which works out its result's length, a step per match, before building it). The producers that copy input text into expressions per field, view or selection (`stringValue`, the format expressions, `getName`) charge what they make. `TestNoUnmeteredBuilders` type-checks the package and fails on any other builder, join, repeat, non-constant replacement, string appended to in a loop, or list deduped by a scan in a loop, unless an allowlist says why it is linear in one input.
- **Copies per child are made linear, with the same output** ([ADR 0011](0011-faithful-output-free-algorithm.md)): the ancestor parse is shared until a child first writes to it (copy on write, where the copy would read the same: primitive values, and in 5.8 no key the clone package leaves out); `RemoveDuplicateTimeUnits` keeps one count per field for the whole walk; a parse node's fields are a linked list that merges into the larger side in place, with an index of their path prefixes kept as fields join, so `MoveParseUp` costs a chain linear time; `fieldIntersection` compares the prefixes' hashes (a polynomial modulo 2^61−1 at a base drawn per process), checking a match against the text; sibling time units, the union of child domains and merged titles are extended in place. Each is compared with upstream's form in `algo_test.go`, and each family is in `TestCompileAllocGrowsLinearly`.
- **Views are weighed, not only counted.** Before any view is built, a unit weighs one view, plus one per selection a composite view above it declares, plus one per 4 KiB (`ViewBytes`/8) of specification it compiles: its own (inline data aside), a layer's shared encoding, the selections it receives, the config and theme. The view budget (`WithMemoryLimit(n)` gives n/32768) bounds the weight.
- **Charges are kept until the call ends.** The compiled specification holds most of what was charged; a scratch structure (an index, a hashed text, a count) gives its charge back when it is dropped, and the meter's unused reservation goes back to the ledger when the compilation ends.

## Why

The ledger is the bound the rest of the engine already answers to, and `WithMemoryLimit` sizes it: charging the compiler to it makes one limit cover the whole call. A builder that charges itself cannot be bypassed by the code that uses it, and the structural test keeps new code on it, as `TestEveryBuiltinIsMetered` does for the expression evaluator. Weighing views before they are built refuses a fan-out before it allocates anything; text copied per view cannot be charged at every one of the hundreds of concatenations that copy it, but it can be counted where the views are.

## Consequences

- A compilation that upstream finishes can fail with `ErrLimit` where it needs more than the call's budget; the corpora's heaviest chart charges 1.1 MiB and weighs 51 views (`TestCompileMarginOverCorpus` holds every chart to an eighth of a 64 MiB render's bounds).
- Charges count what was allocated, not what is alive: a compilation's garbage stays charged until the call ends. A chart that allocates far more than it keeps can fail earlier than its peak would; the margin above covers the corpus.
- Concatenations of a charged string (`"(" + s + ")"`) are not charged; each copies a charged string a fixed number of times, so the memory stays within a small multiple of the charges. `TestCompileBytesBounded` holds each family of quadratic or fanned-out output to three times the limit in bytes allocated before it fails.
- A view whose text is large weighs more, so fewer such views fit in a budget than plain ones.

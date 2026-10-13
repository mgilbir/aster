# 0014: Memory a library allocates as it works is bounded by the ledger's room

**Status:** accepted, 2026-10-13.

## Context

[ADR 0010](0010-bounds-charged-where-work-happens.md) charges memory to the call's ledger before it is allocated. That needs a size known before the allocation. A regular expression search does not have one: goecma262 grows its backtrack stack and failure memo as it searches, and how far depends on the pattern and the input together. Its default bound is fixed by the input's length (256 MiB and 32 bytes a byte), whatever the call's `WithMemoryLimit` says, so a 64 MiB render could hold 256 MiB in one search. Charging the default before each search, and giving it back after, would make every `test()` reserve 256 MiB: a call with less room could match nothing, and the ledger's peak would report memory that was never used.

The compile of a pattern is different: goecma262 (since v0.3.0) bounds it before it allocates (`parser.AllocBound`, `compiler.BoundContext`) and holds it to a budget (`WithBudget`), so it is charged first as ADR 0010 says.

## Decision

- **A search is bounded by what the ledger has left, not charged to it.** Each search runs with `SetMaxMemory` at the ledger's room (`Ledger.Room`), or goecma262's default when that is less (`jsval.MatchMemory`). A search past it fails with `ErrLimit`, as one out of steps does.
- **What a library cannot size beforehand is handled the same way.** It is bounded by the ledger's room at the time it starts and stops with an error past it; what it can size is charged first.

## Why

Bounding by the room keeps the property the ledger exists for: no call holds more than its limit, the search included, because the search may only use what nothing else has been charged for. It costs a load of an atomic counter per search. Nothing is reserved, so a search that needs a few bytes does not stand in the way of anything, and the ledger's peak stays a measure of what was charged.

## Consequences

- The ledger's counters do not include a search's memory. Expressions are evaluated on one goroutine, and only image fetching charges the ledger in parallel, in a stage of its own, so the room a search was given is not taken by anything else while it runs.
- A render near its limit fails a search that needs more than is left, where goecma262's default would have let it run.
- goecma262 checks its bound after the stack grows, so a search may hold up to about twice the room for an instant before it fails.

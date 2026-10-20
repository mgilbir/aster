# 0025: The expression environment's state is bounded by the call; engine handles are Go values; tuples are identified by id

**Status:** accepted, 2026-10-19.

## Context

Expressions call into the view through an environment (`internal/vega`'s `rtContext`, the methods `expr`'s interfaces name): `data()`, `scale()`, `modify()`, `geoShape()` and some thirty more. The evaluator charges what it builds itself (its strings and arrays, `TestEveryBuiltinIsMetered`), but nothing held the environment to the same. The 2026-10-18 audit found two methods that kept state for the whole render outside every budget:

- every `geoShape()` and `pathShape()` call appended its generator to a slice the view kept, and handed the expression `{"$shape": n}`, an index into it (C4: 900 MB under `WithMemoryLimit(64 MiB)`);
- `modify()` built a map of every row a call named and kept it until the propagation ended (C5: 2 GiB from 4,000 rows).

The handles were JSON, so a specification or its data could spell one: `{"$shape": 0}` bound another mark's generator, and `from: {"$ref": "$h1"}` joined a mark to an axis's internal ticks operator (C92). And `modify()` reconstructed identity from field equality: removing a tuple removed every row with equal fields, and inserting a tuple the data set held duplicated it (C33), where upstream's change sets go by tuple id.

## Decision

- **A method of the environment that keeps state for the call charges the call's ledger for it** as it records it, and gives it back when it is released: a `modify()` change set is charged as its lists grow and given back once applied. An array a method hands an expression past 256 elements (`domain()`, `range()`, `gradient()`, `invert()`, `treePath()`, `treeAncestors()`, `data()` of a mark) is charged as the evaluator charges an array it builds, with a step of work per 16 elements: an expression can keep it, in every row.
- **A value that stands for engine state is a Go value on its object**, never JSON: a shape generator is a `jsval.HostFunction` (its generator rides on the value; it prints as upstream's function source and JSON leaves it out, as a function), and a guide plan's reference to an operator is an object of no members whose host payload is the operator (`Object.SetHost`). Both are garbage once nothing refers to them. A `$ref` a specification spells is upstream's "Operator not defined" error: upstream's ids are its own numbering, which a specification cannot know.
- **Tuples are identified by their tuple id where upstream's code uses `tupleid`**: a change set (`modify()`, `setdata()`) removes, inserts and modifies by id, with upstream's quirks (a value without an id shares the `undefined` key); `treelinks` and `isTuple` go by id. The `*jsval.Object` pointer stays the identity within one operator's flow, where ingest's copies, which keep the id, never meet the original.
- **`TestEveryEnvMethodIsMetered` enumerates the environment's methods** by reflection and calls each k and 2k times a row: what the heap retains more, after a collection, while the change sets are pending, must be charged. A new method fails until it has a case. Its sibling for the dataflow, `TestEveryOperatorPollsThroughout`, enumerates the operator registry: every operator that visits rows polls the call's context while it does (C21).

## Why

The evaluator's rule was where the bounds were checked, so code outside it (the environment) could forget it; enumerating the methods makes forgetting a test failure. A handle that carries its implementation needs no registry, so nothing grows with the calls, and nothing a specification writes can be one. Ids are upstream's identity; reproducing them makes `modify()` and `setdata()` give upstream's results, and replaces the per-call maps.

## Consequences

- The corpus renders byte-identically (the baseline harness): no chart of it uses these paths heavily.
- A render that calls `domain()` of a large domain once per row pays for each copy, as upstream allocates each.
- `modify()`'s change sets and `setdata()` follow upstream's semantics (the regress specs `modify-*`, `TestSetDataKeepsTheTuplesItHolds`), including its quirks: an insert of a value with no tuple id is cancelled by a removal of another such value in the same change set.

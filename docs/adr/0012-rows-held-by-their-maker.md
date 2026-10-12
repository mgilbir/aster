# 0012: Rows are held by the operator that made them

**Status:** accepted, 2026-10-12.

## Context

`WithMemoryLimit` turns into a bound on data rows (`MaxRows`), and the budget that carries it was charged at two call sites, as deltas: after a transform returned, `len(out) - len(in)`, and once per data set when it was seeded. A filter was a negative delta, which credited the budget with rows that were still alive in its source, so ten filters that drop everything paid for 9,000 rows made elsewhere. The rows that Load-with-a-signal, `modify()` and trigger inserts, `setdata()`, a prefacet's cells, nest, treelinks and aggregate made were never charged at all. And a transform that ran again (a signal write) was charged again on top of its last run, so a sequence rerun from 600 to 601 rows failed as 1,201 (audit C13). The bound bounded neither the rows alive nor the memory.

## Decision

- **A row is held by exactly one operator: the one that made it.** Each operator has a `budget.Held`, and `Budget.Hold(h, rows, excess)` *replaces* what it holds: a rerun is charged once, an operator that holds fewer rows gives the difference back, and nothing gives back more than it holds. An operator that only passes tuples on (filter, collect, sort, sieve, a relay that does not derive) holds none, so it can neither add to the count nor take from it. The bound is then the rows alive, with their weight past `RowBytes` counted in rows.
- **One charge point.** `internal/vega/rows.go` is the only place rows are charged. A transform's output is scanned once (`holdCreated`), as the kernel already did to give new tuples their ids: the tuples that have no id yet are the ones the transform made, and the operator holds them, with the weight its run added to the tuples it passed on. The paths that copy or ingest (a data set's seed, a Load, `modify()`, `setdata()`, a derived relay, a prefacet's cells) hold all they made, before they make them where the count is known first.
- **Tuple lists are charged too, the same way.** A filter, a sort or a collect makes a list of the tuples it passes on; the rows are not new, but the list is memory, and four hundred filters over the same 120,000 rows made a gigabyte. The kernel charges the list each operator outputs to the call's ledger (unless it is its input's own), replacing its previous list, as soon as it exists, and gives them all back when the run is over.
- **Reservations see the replacement.** While an operator runs, what it held is marked as being replaced (`Budget.Replacing`), so a transform that reserves the rows it is about to make (`budget.Reserve`, before it allocates) may use them. Transforms reserve the rows they make, not the growth over their input.
- **The tests enforce the one charge point.** With `CheckHeldForTest` on (every test of `internal/vega` and of the root package), a transform that passes on an object with no tuple id fails the render as an internal error: some path made tuples without holding them.

## Why

Replacing per operator is what the dataflow does with memory: an operator's previous output is garbage once its new one exists, and a tuple passed along is the same object, not a new one. Tying the charge to the id the kernel already assigns makes every new transform charged by construction, with no per-transform list to keep up to date, and costs nothing on the hot path that was not already there (the id loop). Making a tuple without an id reach a target is now a test failure, not a silent hole.

## Consequences

- Rows that were free before are charged now: an aggregate's groups, a relay's copies, a prefacet's cells. A chart near `MaxRows` can fail where it did not; the defaults are far above the corpus, which renders unchanged.
- The tuple-id assignment moved: aggregate, nest, treelinks, graticule and the axis and legend tuples now get their ids when they are made, as upstream's `ingest` does, instead of from the next transform. A projected tuple (`project` without fields) gets an id of its own, as upstream's `derive` gives it.
- Operators are never torn down (facet cells are kept for the run); a cell whose tuples are gone runs with none and holds none.

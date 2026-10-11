# 0008: The CLI detects Vega vs Vega-Lite by schema, then by shape, and refuses to guess

**Status:** accepted, 2026-10-11.

## Context

The CLI's `svg`, `png` and `pdf` take either language. It used to decide by `$schema` alone, and treated a spec *without* one as Vega, although its documentation said Vega-Lite. Hand-written and generated Vega-Lite specs often omit `$schema`, and rendering one as Vega draws an empty 0×0 chart with exit status 0.

## Decision

1. A `$schema` naming a Vega-Lite or Vega schema decides.
2. Otherwise, the top-level properties only one language has decide. Vega-Lite: `mark`, `encoding`, a composition, `params`, `transform`, …, or an object `data`. Vega: `marks`, `signals`, `scales`, `axes`, `legends`, `projections`, `encode`, or an array `data`.
3. Properties of both, or of neither, are an error asking for `-from vega|vega-lite`.

## Why

The two languages' top-level properties barely overlap, and rule 2 classifies every spec in the repository's corpora correctly with `$schema` removed (`TestDetectSpecOnTheCorpora`, about 1,400 specs). Where the shape is ambiguous, an error costs the user one flag. A wrong guess costs a silently empty chart.

## Consequences

`aster svg` on `{}` or `{"width": 10}` now fails instead of drawing nothing. The library is unaffected: its methods name the language.

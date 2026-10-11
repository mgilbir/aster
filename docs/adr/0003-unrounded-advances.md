# 0003: Measure text with unrounded advances by default

**Status:** accepted, 2026-09-30 (a7a7e6a).

## Context

HarfBuzz-style measurement, which earlier versions of aster used, rounds the font size up to whole pixels and each advance to 1/64 px. Browsers and node-canvas, and therefore upstream Vega's layouts, use unrounded advances at the exact size.

## Decision

Measure with unrounded advances at the exact font size by default. `WithHarfBuzzTextMetrics()` restores the rounded model for output byte-identical to earlier versions.

## Evidence

With exact advances, the engine matched upstream's own renderings of the 627 Vega-Lite examples byte for byte on 624, against 61 with rounding, and the old engine's twelve 1-pixel known failures passed.

## Consequences

Output changed by sub-pixel amounts for every chart with text when this landed. Pipelines that diff images against output from older versions need `WithHarfBuzzTextMetrics()`.

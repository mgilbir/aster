# 0002: forme for text shaping and measurement

**Status:** accepted, 2026-09-30 (a7a7e6a). Replaces go-text/typesetting.

## Decision

Measure, shape and outline all text (layout, PNG glyphs, PDF text) with [forme](https://github.com/mgilbir/forme), through `internal/text`, and drop go-text/typesetting.

## Why

The evaluation is [the forme evaluation](../history/forme-evaluation-2026.md). In short:

- forme reproduced go-text's widths exactly (3,846 of 3,846) apart from two go-text bugs: bold or italic emoji fell to `.notdef`, and Hebrew was shaped as Latin;
- its cold measurements were faster, and they run in parallel;
- one shaping stack serves layout, PNG and PDF, so they cannot disagree.

## Consequences

- The reference widths are now a recording (`internal/text/testdata/textmeasure_golden.json.gz`), since go-text is gone.
- The evaluation's recommendation to keep the reference (HarfBuzz-rounded) metric model as the default was overtaken the same day by [0003](0003-unrounded-advances.md).

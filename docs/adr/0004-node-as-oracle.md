# 0004: Upstream in node is the test oracle

**Status:** accepted, 2026-10-01.

## Decision

Behaviour is defined by upstream Vega and Vega-Lite at pinned versions (`testdata/oracle-node*`), not by a specification document. The engine's output is compared with upstream running in node on the same input: the SVG structurally, numbers within half a pixel; the compiled Vega exactly; PNG against resvg. Every difference is listed with its reason, or the test fails ([testing.md](../testing.md)).

## Why

Vega's behaviour lives in its code, odd corners included. Only running it answers "what should this spec draw?" for the tens of thousands of inputs the sweeps generate. Half a pixel is the tolerance because the two sides shape text with different engines (forme, and node-canvas with Pango). Everything else matches exactly.

## Consequences

- Tests that need node skip without it. CI requires it (`ASTER_ORACLE=require`), and unit tests replay recorded vectors so `go test -short` needs no node.
- Some answers depend on the platform node runs on (emoji measurement, V8's last bit on x86-64). The expectation files say so per platform.
- Comparisons render as upstream does, past the errors it logs ([0006](0006-errors-reach-the-caller.md)).

# 0001: A pure-Go engine instead of Vega in QuickJS/WebAssembly

**Status:** accepted, 2026-10-01 (d1b6046).

## Context

Until October 2026 aster ran upstream Vega's JavaScript in QuickJS-NG, compiled to WebAssembly and run by a wazero fork (andsifr), and rasterized PNG with resvg compiled to WebAssembly. That kept it CGO-free and exactly upstream in behaviour. But it was slow, held large heaps, and every call crossed a JS↔Go bridge.

## Decision

Replace it with an implementation of the Vega runtime and the Vega-Lite compiler in Go, organised along upstream's module boundaries, with its own rasterizer and PDF writer. Fidelity is kept by testing against upstream rather than by running it ([0004](0004-node-as-oracle.md)).

## Evidence

The 2026-09-30 benchmark ([RESULTS.md](../../internal/cmd/enginebench/RESULTS.md#2026-09-30-the-run-that-retired-the-quickjs-engine)), on the 627 Vega-Lite examples and 87 Vega gallery specs:

- the Go engine was 13–62× faster than the QuickJS engine (SVG geomean 2.40 ms against 101 ms) and faster than node itself;
- peak memory was 306 MB against 2,074 MB;
- it failed fewer specs (10 against 13).

## Consequences

- No JavaScript or WebAssembly runtime: plain Go, any platform, millisecond renders, safe concurrency.
- Every upstream behaviour must be reimplemented, odd corners included, and kept equal by tests. Floating point has to match V8's bit for bit ([design-rules.md](../design-rules.md#floating-point-parity)).
- Upgrading Vega means porting the change, not bumping a dependency.

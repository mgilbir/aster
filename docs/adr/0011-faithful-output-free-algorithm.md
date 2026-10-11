# 0011: Faithful in output, free in algorithm

**Status:** accepted, 2026-10-11.

## Context

Aster reproduces upstream Vega and Vega-Lite byte for byte ([ADR 0004](0004-node-as-oracle.md)), and [design-rules.md](../design-rules.md) says to keep upstream's semantics exactly. Read strictly, that ports upstream's algorithms too, including where they are unbounded or super-linear on input a browser user would never write but an attacker will: d3-geo's circle stream, which loops forever on a huge `clipAngle`; the `tickMinStep` reduction, linear in a tick count of 1e9; JavaScript's index-key ordering, kept on every insert and cubic over a spec's keys. A port made verbatim inherits each of these as a denial of service ([ADR 0005](0005-bounded-untrusted-input.md)). The audit of 2026-10-11 found several, and reviewers could not tell a deliberate verbatim port from an incidental one.

## Decision

- **Fidelity is a property of the output.** Where upstream's algorithm is unbounded or super-linear in the input, aster may use a different algorithm that is bounded, provided its output is identical: same SVG, same compiled Vega, same values, on every input upstream handles. A binary search for a tick count, keys ordered once when they are written, a circle stream that counts its steps.
- **Degenerate parameters may be refused.** Past a point where upstream's output is degenerate (a clip angle of 1e18 radians, a precision of 1e-300, a frame offset of 1e300), aster may reject the parameter with an error wrapping `ErrLimit` instead of computing the degenerate output. Each such bound is far above any meaningful value, and is documented in [limits.md](../limits.md).
- **Each deviation says so.** The code names the upstream function it replaces and why the replacement gives the same output; a test compares the two (against node, or against a port of upstream's loop on inputs small enough to run it).

## Why

Callers care that a chart renders as Vega renders it, not that the same loop computed it. Bounding the algorithm keeps that promise and the promise of ADR 0005 together. Refusing a degenerate parameter costs nothing a user could see: upstream would hang or draw nothing.

## Consequences

- The oracle comparisons and the baseline hashes are the proof that an algorithm change is faithful; a change that alters them is a correctness bug, not a trade-off.
- A rejected parameter is a divergence upstream does not have, so it is listed in limits.md with its bound, and errors name it.
- Ports stay readable against upstream wherever upstream is already bounded; this ADR licenses departures, it does not require them.

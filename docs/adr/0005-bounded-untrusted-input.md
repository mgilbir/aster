# 0005: Specifications are untrusted: errors, never panics, and bounded work

**Status:** accepted.

## Decision

Treat every specification, data set, SVG and font as hostile:

- **Errors, never panics:** anything reachable from input returns an error, and the public API's `recover` is a last resort.
- **Bounded work and memory:** a per-render budget, charged *before* each allocation. A spec that exceeds it fails with `ErrLimit`.
- **Bounded time:** one timeout covers the whole call.
- **Bounded recursion:** every recursive cycle is listed with its bound and audited in CI.
- **Mediated resources:** every resource goes through a Loader, and the default Loader refuses all.

Upstream has no such limits. A spec that hits one is refused, not rendered differently.

## Why

Aster's main use is servers that render charts other people wrote. The 2026 security review ([the security review](../history/security-review-2026.md)) found 200-byte specs that needed tens of gigabytes before these bounds existed.

## Consequences

- Defaults sit far above any real chart. The bounds and how to tune them are documented in one place, and a test keeps that page true ([limits.md](../limits.md)).
- Every new transform that multiplies rows must charge the budget ([design-rules.md](../design-rules.md#security)).

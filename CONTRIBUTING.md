# Contributing

Aster is a Go implementation of Vega and Vega-Lite whose correctness is defined by upstream: a change is right when the engine still agrees with upstream Vega running in node. This page gets you from a clone to a change that passes the gates CI runs.

## Setup

```bash
git clone https://github.com/mgilbir/aster && cd aster
go build ./...          # everything is committed; works offline
make test               # go test -short ./... — no node needed
```

For the comparison with upstream (most changes need it), install the pinned oracle once. It needs node; the version is pinned in `testdata/oracle-node/package.json` (volta):

```bash
(cd testdata/oracle-node && npm ci) && (cd testdata/oracle-node-vl5 && npm ci)
(cd testdata/mermaid && npm ci)    # renders the docs' diagrams (TestDocsMermaid)
ASTER_ORACLE=require go test ./...
```

## Before you open a pull request

`make check` runs the gates CI runs (`scripts/check.sh`) and prints a ledger of what ran, failed or was skipped. `scripts/check.sh -fast` runs the quick ones (gofmt, golangci-lint, vet, `go mod tidy`, the `-short` suite). CI also runs on macOS, reads PDFs back with PDFium, and gates on benchmarks; see [docs/testing.md](docs/testing.md).

- A behaviour change that makes the engine differ from upstream fails `TestCompareWithNode` or a sweep. Fix the engine, or, if the difference is deliberate, list it with its reason in the expectation file (`testdata/oracle-expect.txt`, `testdata/sweeps/*.txt`).
- A bug found by fuzzing gets a regression spec under `testdata/corpus/regress`.
- `docs/limits.md` and `docs/architecture.md` are checked by tests: change them with the code.
- A performance regression over the gate's thresholds needs the `perf-regression-ok` label and a reason.

## Where things are and how to write them

- [docs/architecture.md](docs/architecture.md): which package does what, and which upstream module it follows.
- [docs/design-rules.md](docs/design-rules.md): the rules the code follows. Read it before writing engine code. In particular: keep upstream's semantics exactly; return errors, never panic; charge the budget before allocating; report what upstream logs; round products explicitly where V8 does; use `jsmath`, not `math`, for transcendental functions.
- [docs/adr/](docs/adr/README.md): the decisions behind the design; a change to one adds a new record.
- Comments explain *why* and name the upstream rule that is not obvious. gofmt and `go vet` clean.

## Changelog

User-visible changes go in [CHANGELOG.md](CHANGELOG.md) under "Unreleased", with **breaking** marked.

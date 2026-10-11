# Documentation

Start with the [README](../README.md): what aster is, installation, and a quick start. Then find your task below.

## Using aster

| Doc | For |
|---|---|
| [cli.md](cli.md) | the `aster` command: flags, Vega/Vega-Lite detection, exit status |
| [api.md](api.md) | every method and option, with defaults |
| [loading-data.md](loading-data.md) | data and images from URLs and files; the loaders; writing your own |
| [errors.md](errors.md) | what can fail, telling errors apart, rendering past them (`WithErrorHandler`) |
| [limits.md](limits.md) | timeouts and memory budgets for untrusted specs |
| [fonts.md](fonts.md) | custom, system and colour fonts; which font draws a character |
| [pdf.md](pdf.md) | PDF text modes; sharing fonts across many charts |
| [security.md](security.md) | the threat model and a hardening checklist |
| [troubleshooting.md](troubleshooting.md) | symptom → cause → fix |
| [performance.md](performance.md) | how fast, and concurrency |

## How it works

| Doc | For |
|---|---|
| [architecture.md](architecture.md) | the render pipeline, resources and errors, a Converter's life, the package map |
| [adr/](adr/README.md) | why it is built this way: one record per decision |

## Working on aster

| Doc | For |
|---|---|
| [CONTRIBUTING.md](../CONTRIBUTING.md) | setup, the gates a change must pass, conventions |
| [design-rules.md](design-rules.md) | the rules the engine's code follows (values, errors, security, floating point, concurrency) |
| [testing.md](testing.md) | the upstream oracle, the corpus and sweeps, fuzzing, environment variables |
| [security-review-2026.md](history/security-review-2026.md) | the 2026 security review: findings, reproducers, fixes |
| [enginebench](../internal/cmd/enginebench/README.md) and its [results](../internal/cmd/enginebench/RESULTS.md) | timing the engine against upstream in node |
| [forme-evaluation-2026.md](history/forme-evaluation-2026.md) | the evaluation behind the text engine (historical) |
| [testdata/corpora](../testdata/corpora/README.md) | the external corpora and their pins |
| [audits/](audits/) | documentation and code audits |

Changes between versions: [CHANGELOG.md](../CHANGELOG.md).

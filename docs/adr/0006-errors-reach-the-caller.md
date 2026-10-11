# 0006: What upstream logs fails the call unless a handler lets it go

**Status:** accepted, 2026-10-11.

## Context

Upstream Vega logs some errors and carries on: a data URL that fails to load leaves the data set empty, an image that fails is not drawn, an expression that throws leaves its value unchanged. Aster followed it and dropped the logs. So a Go program got a chart missing its data and a nil error, for example with the default Loader, which refuses every URL. That is the failure Go's error convention exists to prevent.

## Decision

- By default, the first error upstream would log and carry on past fails the call. The error is a `*LoadError` for data and images, and the evaluation error otherwise, wrapped in the returned error.
- `WithErrorHandler(func(error) error)`, a per-call option, is called with each such error and each warning, as `go/types`' `Config.Error` is. Returning nil goes on as upstream does; returning an error fails the call with it.
- Warnings never fail a call by default.
- Errors that never depended on upstream's leniency (invalid specs, limits, timeouts, `Close`) are unchanged.
- Embedded PNG, JPEG and GIF `data:` images are part of the spec and need no Loader. Other `data:` URLs still go through it, so a Loader that refuses them keeps them out of the output.

## Why per call and not per Converter

One Converter serves concurrent calls. A handler per call is told only of that call's problems, on the calling goroutine, before the call returns, so it can be a closure over the request.

## Consequences

- A behaviour change for specs that rendered with silent failures: they now return an error. Callers who want the old output pass `WithErrorHandler(func(error) error { return nil })`, which is what the upstream comparisons do.
- The CLI fails on such errors, prints warnings to stderr, and renders past errors with `-lenient`.

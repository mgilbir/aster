# Changelog

What changed between versions, for users of the library and the CLI. Breaking changes are marked **breaking**.

## Unreleased

### Errors reach the caller ([ADR 0006](docs/adr/0006-errors-reach-the-caller.md))

- **breaking:** a render fails when upstream Vega would log a problem and draw without it. This covers a data URL the Loader refuses or fails to load (including every URL under the default `DenyLoader`), loaded data that does not parse, an image that cannot be loaded or decoded, and an exception an expression or operator raises during evaluation. Before, these rendered a chart missing the data or the image, with a nil error. Data and image failures are an `*aster.LoadError` (`Resource`, `URL`, `Err`).
- New per-call option `WithErrorHandler(func(error) error)`: told of each such error and of each `aster.Warning`, it decides whether the call goes on. To keep the previous output, pass `aster.WithErrorHandler(func(error) error { return nil })`. See [docs/errors.md](docs/errors.md).
- New `aster.ErrClosed`, returned by every call after `Close` (the error was not matchable before).
- Fixed: embedded `data:` images drew nothing with the default loader, because they were sent through it and refused. Embedded PNG, JPEG and GIF images (`data:image/png`, `…/jpeg`, `…/gif`) are part of the specification and now draw with any loader; other `data:` URLs are still the Loader's to allow.

### Fonts are the caller's to choose ([ADR 0009](docs/adr/0009-caller-chooses-fonts.md))

- **breaking:** a family you register faces for (`WithFont`, `WithFontFace`, `WithFontInstance`) is made of your faces alone, as a CSS `@font-face` family is. Registering "Liberation Sans" or "Arial" now replaces the embedded font of that name; before, the embedded face won. Of faces of equal fit, the last registered now wins; before, the first did. Both are what the documentation said.
- **breaking:** `New` loads the fonts given with `WithFont`, `WithFontFace` and `WithFontInstance` and fails for one that does not load. Before, the first render that needed text failed, and every render after it.
- New `WithFontFallback(families ...string)`: the families that draw, in order, the characters a spec's fonts lack; with none, no fallback. It is also the way to make a system font a fallback.

### CLI

- **breaking:** a Vega-Lite spec without `$schema` is rendered as Vega-Lite. It used to be rendered as Vega, which drew an empty 0×0 chart with exit status 0. Input is now detected by `$schema`, else by its top-level properties, and an ambiguous input is an error. New `-from vega|vega-lite|auto` ([ADR 0008](docs/adr/0008-cli-language-detection.md)).
- **breaking:** failed loads and evaluation errors fail the command (exit status 1), with a hint when the cause is that no `-allow-http`/`-allow-domain` was given. New `-lenient` renders past them, printing each. Warnings are printed to stderr.
- `compile` refuses a spec it detects as Vega.

### Documentation

The README was split into task-oriented guides under [docs/](docs/README.md), and architecture decision records were added. Tests now keep the documentation true: the limits table, the package layers, every Mermaid diagram (rendered with Mermaid's renderer) and every relative link. The documented memory-limit costs, font priority and Loader contract, which did not match the code, were corrected.

## v0.1.1 (2026-10-10)

- Depends on pdf0 v0.5.0, golang.org/x/text v0.43.0 and golang.org/x/image v0.47.0.

## v0.1.0 (2026-10-10)

The first tagged release, with the pure-Go engine ([ADR 0001](docs/adr/0001-pure-go-engine.md)): Vega 6.4.0 runtime, Vega-Lite 6.4.3 and 5.8.0 compilers, SVG, PNG and vector PDF output, and the CLI. Untagged versions from before 2026-10-01 ran Vega in QuickJS on WebAssembly. Users of untagged versions may notice:

- `HTTPLoader` blocks private networks by default; `BlockPrivateNetworks` is ignored (use `AllowPrivateNetworks` to opt out).
- Text is measured with unrounded advances, as browsers do; `WithHarfBuzzTextMetrics()` restores the previous rounding ([ADR 0003](docs/adr/0003-unrounded-advances.md)).
- Renders are bounded by `WithTimeout` and `WithMemoryLimit` and fail with `ErrLimit` past a bound ([docs/limits.md](docs/limits.md)).

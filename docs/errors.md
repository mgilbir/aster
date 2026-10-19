# Errors

Every rendering method returns an `error`, and a non-nil one means the output is not the chart the specification describes. This page lists what can go wrong, how to tell the cases apart, and how to render past problems when a partial chart is what you want.

```go
svg, err := c.VegaLiteToSVG(spec)
var le *aster.LoadError
switch {
case err == nil:
case errors.As(err, &le):
    // data or an image could not be loaded: le.Resource, le.URL, le.Err
case errors.Is(err, aster.ErrLimit):
    // the spec asks for more than this converter allows (see limits.md)
case errors.Is(err, context.DeadlineExceeded):
    // the render took longer than WithTimeout, or than the deadline of its
    // WithContext context
case errors.Is(err, aster.ErrClosed), errors.Is(err, context.Canceled):
    // the Converter was closed (before, or during, the call), or the call's
    // WithContext context was canceled
default:
    // an invalid spec, or an error raised while evaluating it
}
```

## Why a call fails where a browser draws something

Upstream Vega, in a browser, logs some problems to the console and draws what it can: a data set whose URL fails to load is empty, an image that fails is not drawn, an expression that throws leaves its signal unchanged. A Go program usually has nobody watching a console, so aster turns each of these into an error by default. A chart that silently lacks its data is the failure mode this avoids.

When the partial chart *is* what you want (a preview, a gallery of user charts, parity with what the browser shows), pass `WithErrorHandler` to the call. It works like `go/types`' `Config.Error`:

```go
var problems []error
svg, err := c.VegaLiteToSVG(spec, aster.WithErrorHandler(func(err error) error {
    problems = append(problems, err) // or log it
    return nil                       // go on, as upstream does
}))
```

- **No handler (the default):** the first such error fails the call. Warnings are dropped.
- **With a handler:** it is called with each error and each warning, in the order they arise. Return `nil` to go on, or return an error to fail the call with it. For example, return `err` for a `*LoadError` but `nil` for everything else.
- The handler is called on the goroutine that made the call, never concurrently, and before the call returns. Data and evaluation errors reach it as they happen. Image failures reach it when the stage that fetched or decoded them (SVG, PNG or PDF) is over, once per image URL and call, under the URL the specification names, whichever stages drew it.
- The handler may call `Close` (it cancels the call, which then fails with `context.Canceled`) or start another call on the same Converter; neither deadlocks.

`WithErrorHandler` is a per-call option for every method (`RenderOption`, `PNGOption`, `PDFOption`), so concurrent calls on one `Converter` each have their own.

## What the handler is told of

| Value | When | Default (no handler) |
|---|---|---|
| `*aster.LoadError` with `Resource == "data"` | a `data.url` the Loader refused or failed to load, or whose body does not parse in its `format`, or a JSON body with a `null` row (which upstream's ingestion rejects after the view is made) | the call fails |
| `*aster.LoadError` with `Resource == "image"` | an image mark (or an `<image>` in SVG input) whose URL the Loader refused, failed to load, or that is not a PNG, JPEG or GIF that decodes; `URL` is the mark's (or the SVG's), not what the Loader's `Sanitize` made of it | the call fails |
| another `error` | an exception an expression or an operator raised while the chart was evaluated (`TypeError`, `RangeError`, an invalid parameter, an unknown projection type; a time format or parse reaching a `config.locale.time` pattern that is not a string, as d3 throws; a scale `interpolate` of `"transform-css"` or `"transform-svg"`, whose parsers need a browser, as node throws) | the call fails |
| `aster.Warning` | a Vega warning (`Infinite extent for field "x": [Infinity, -Infinity]`, `Unsupported scale property: …`), an expression's `warn(…)` (`EXPRESSION` and its arguments as `String()` prints them), a Vega-Lite compiler warning with upstream's text (`Ignoring an invalid transform: {"calcualte":"1"}.`, `size dropped as it is incompatible with "area".`), told as often as upstream logs it, or a link (`href`) the Loader refused, which the output then lacks | ignored |

`LoadError.Err` is the Loader's own error (for example the `HTTPLoader`'s "private or loopback address (blocked …)", which does not say what address a name resolved to), so `errors.Is` reaches errors your Loader returns; `HTTPLoader`'s own refusals have no exported sentinel, so match on the `*LoadError`. When the handler lets a failure go, the result is upstream's: the data set is empty (or, for a body that does not parse, the text as one value), and the image is undrawn. A `null` row in a loaded JSON body leaves the data set empty: upstream's view never finishes rendering it, so there is no upstream result to match.

Warnings carry text from the specification, so every warning reaches the handler with its text cut to 1 KiB. Vega's repeat in every cell of a faceted group, so the handler hears of each distinct Vega warning once per call, and of at most 256 of them; the 257th says that the rest are not reported. The Vega-Lite compiler's are told as often as upstream logs them. Errors are each reported. An expression's `info()` and `debug()` are below the warning level and are not reported, as upstream's logger drops them at its warning level.

Embedded PNG, JPEG and GIF images (`data:image/png;base64,…`, and `image/jpeg`, `image/gif`) are part of the specification, not resources: they need no Loader, and draw with the default one. Any other `data:` URL goes through the Loader like a remote one.

## Errors the handler never sees

These always fail the call:

- **An invalid specification:** JSON that does not parse, a Vega-Lite spec the compiler rejects, an expression that does not parse, an unknown signal name in `WithSignal`, or a `WithSignal` value `encoding/json` cannot encode. The Vega-Lite compiler rejects what upstream's throws on, in the version selected: a value of the wrong type where upstream calls a method on it fails with the TypeError's text (`spec.layer.map is not a function` for `"layer": {}`), and the legacy `"in"` predicate, which upstream accepts but never reads, fails as upstream's does ([ADR 0013](adr/0013-invalid-input-fails-where-upstream-throws.md)).
- **An invalid PNG scale:** a `WithScale` that is not positive and finite.
- **`ErrLimit`:** a resource bound was exceeded, in every output alike: an image over the per-image limit fails `VegaToSVG` (read for the size of a mark that has none) as it fails PNG and PDF. This includes a response over the Loader's size cap (the message names the cap: `HTTPLoader.MaxResponseBytes`, `FileLoader.MaxBytes`, or what is left of the call's load budget), a PNG render past its pixel work budget (raise it with `WithPixelWorkLimit`; the message names it), a `config.locale` member past its bound, and an axis, legend or scheme that would list more ticks, entries or colours than the engine keeps (where it once drew none, silently). See [limits.md](limits.md). A bound reached inside an expression is one too, never a JavaScript exception the handler could let go: a string past aster's length limit, a regular expression search that runs out of steps or of the memory it may hold, or a pattern past the compiler's size, a `timeSequence` of too many dates, a value nested too deep to print. So is a walk of a value past the call's bounds anywhere in the engine (the text of a large shared-reference array in a text mark, a key, a label; a comparison of two such arrays), and a value nested past 1,024 levels wherever it is printed, compared or interpolated. Only a string past V8's own limit (`pad(x, 1e12)`) is the `RangeError` upstream throws.
- **`context.DeadlineExceeded`:** `WithTimeout` expired. The message names the stage, and the cause once ("aster: rendering Vega timed out after 30s: context deadline exceeded"). Past the deadline of a `WithContext` context, it says that instead of the timeout.
- **`context.Canceled`:** `Close` was called during the call, or its `WithContext` context was canceled.
- **`ErrClosed`:** the call was made after `Close`.
- **Configuration errors** are `New`'s: an unknown Vega-Lite version or time zone (or `"Local"`), a theme that is not a JSON object, a nil pointer as the Loader, a negative face index, a font that does not load, a fallback or default family with no font. See [api.md](api.md#errors-from-new).
- **PDF of unsupported SVG:** `SVGToPDF` on SVG outside what Vega writes (an element, attribute or style property it does not draw, patterns other than Vega's, entities, `<use>`; `<a>` is drawn, see [pdf.md](pdf.md)) returns a descriptive error rather than an incomplete PDF. A value that merely does not parse is not an error: it is ignored, as SVG ignores it ([pdf.md](pdf.md)). A drawing whose coordinates overflow to infinity is an error.

## Error messages

Messages name the stage and wrap the cause: `aster: rendering Vega: data "data/cars.json" could not be loaded: aster: resource loading denied for "data/cars.json" (no loader configured)`. The stages are `parsing Vega spec`, `parsing Vega-Lite spec`, `compiling Vega-Lite`, `rendering Vega`, `writing SVG`, `rendering PNG`, `optimizing PNG` (`WithQuantizePNG`, `WithRecodePNG`) and `rendering PDF`, or `rendering` when the call's context ended after its last stage. An error names its package and its cause once. An error that evaluation raised carries upstream's message (`TypeError: Cannot read properties of undefined (reading 'b') (in expression datum.a.b)`), so it can be looked up against Vega's own behaviour.

The CLI prints warnings to stderr, fails on errors, and with `-lenient` prints errors and goes on; see [cli.md](cli.md).

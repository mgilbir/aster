# aster

Go library and CLI that renders [Vega](https://vega.github.io/vega/) and [Vega-Lite](https://vega.github.io/vega-lite/) specifications to SVG, PNG and vector PDF. Pure Go: no JavaScript runtime, no WebAssembly, no CGO, no native libraries.

Reach for it when a Go program (a report generator, a server, a CLI pipeline) has to turn chart specs into images without shipping node or a browser. Aster implements the Vega runtime (following Vega 6.4.0) and the Vega-Lite compiler (6.4.3, and 5.8.0) in Go, measures text with [forme](https://github.com/mgilbir/forme), and has its own rasterizer and PDF writer. Its output is checked against upstream Vega running in node on thousands of specs ([how](docs/testing.md)).

```
go get github.com/mgilbir/aster                     # library; Go 1.26+
go install github.com/mgilbir/aster/cmd/aster@latest # CLI
```

## Quick start

```go
c, err := aster.New() // safe for concurrent use; one per process is enough
if err != nil {
    log.Fatal(err)
}
defer c.Close()

spec := []byte(`{
    "data": {"values": [{"a": "A", "b": 28}, {"a": "B", "b": 55}]},
    "mark": "bar",
    "encoding": {
        "x": {"field": "a", "type": "nominal"},
        "y": {"field": "b", "type": "quantitative"}
    }
}`)

svg, err := c.VegaLiteToSVG(spec)                      // string
png, err := c.VegaLiteToPNG(spec, aster.WithScale(2))  // []byte, 2x
pdf, err := c.VegaLiteToPDF(spec)                      // []byte, vector, selectable text
```

```bash
aster svg -i chart.vl.json -o chart.svg
aster png -i chart.vl.json -o chart.png -scale 2
aster pdf -i chart.vl.json -o chart.pdf
cat chart.vl.json | aster svg > chart.svg
aster compile -i chart.vl.json -o chart.vg.json      # Vega-Lite → Vega
aster svg -i chart.vl.json -allow-domain cdn.jsdelivr.net   # let it load data
```

Four things to know before the first real chart:

- **Nothing is loaded unless you allow it.** A spec that reads `data.url` or draws a remote image needs a [Loader](docs/loading-data.md) (`aster.NewHTTPLoader(nil)`, `&aster.FileLoader{BaseDir: "data"}`; CLI `-allow-http`, `-allow-domain`, `-allow-file DIR`, `-base-url URL`). Without one the render **fails** with an `*aster.LoadError` naming the URL.
- **Errors are errors.** Where upstream Vega logs a problem and draws what it can (a failed load, an expression that throws), aster returns an error by default; `WithErrorHandler` lets a call render past them as a browser does. See [Errors](docs/errors.md).
- **Specs are untrusted input.** Every render is bounded in time (`WithTimeout`, 30 s, and `WithContext` per call) and memory (`WithMemoryLimit`); exceeding a memory or size bound fails with `aster.ErrLimit`, running out of time with `context.DeadlineExceeded`. See [Limits](docs/limits.md).
- **Fonts:** Liberation Sans/Serif/Mono (metrically Arial/Times/Courier) and monochrome Noto Emoji are embedded. Register others with `WithFont`. See [Fonts](docs/fonts.md).

## Features

- Vega-Lite to SVG, PNG, PDF or compiled Vega JSON; Vega to SVG, PNG or PDF; any SVG to PNG or PDF
- PDF is fully vector, with subset-embedded fonts and selectable text, or fonts by name for assemble-once pipelines ([PDF](docs/pdf.md))
- Text measured the way browsers measure it, so layouts match upstream
- Colour emoji and colour fonts (COLRv0/v1, sbix, CBDT, OpenType SVG) in PNG and PDF from a font you register
- Vega-Lite 6.4 (default) and 5.8; any IANA time zone; themes; signals and parameters set before rendering (`WithSignal`)
- Concurrent: one `Converter` serves a whole process; renders take milliseconds ([Performance](docs/performance.md))

## Documentation

| I want to… | Read |
|---|---|
| use the command line | [docs/cli.md](docs/cli.md) |
| see every option and method | [docs/api.md](docs/api.md) · [pkg.go.dev](https://pkg.go.dev/github.com/mgilbir/aster) |
| load data or images, or write a Loader | [docs/loading-data.md](docs/loading-data.md) |
| handle errors, or render past them | [docs/errors.md](docs/errors.md) |
| bound time and memory for untrusted specs | [docs/limits.md](docs/limits.md) · [docs/security.md](docs/security.md) |
| use my own fonts, emoji, colour fonts | [docs/fonts.md](docs/fonts.md) |
| make PDFs, or share fonts across many | [docs/pdf.md](docs/pdf.md) |
| fix an empty chart, a timeout, a wrong font | [docs/troubleshooting.md](docs/troubleshooting.md) |
| understand how it works | [docs/architecture.md](docs/architecture.md) |
| contribute | [CONTRIBUTING.md](CONTRIBUTING.md) |

The full index is [docs/README.md](docs/README.md); changes between versions are in [CHANGELOG.md](CHANGELOG.md).

## Limitations

- **No interaction.** A chart is rendered at its initial state, or at the state `WithSignal` sets; pointer, timer and input events are not simulated.
- **No colour font is bundled.** Emoji draw in monochrome unless you register a colour font; SVG output leaves emoji as text for the viewer to draw.
- **The wordcloud transform needs text measurement**, which is on by default (`WithTextMeasurement(false)` makes it fail, as upstream does without a canvas).
- **`SVGToPDF` takes the SVG Vega writes**; other SVG (`<use>`, entities, filters, elements Vega does not write) fails with an error rather than giving an incomplete PDF. `SVGToPNG` draws general SVG, filters included ([which primitives](docs/api.md#svg-input)).
- Tested on Linux (amd64 and 386) and macOS (arm64), and built for Windows; being pure Go, it is expected to work wherever Go does.

## Acknowledgments

Aster stands on the shoulders of giants. Special thanks to the [vl-convert](https://github.com/vega/vl-convert) project, whose architecture, test suite, and font choices were invaluable references throughout this project's development.

Thanks also to the [Vega](https://vega.github.io/vega/) and [Vega-Lite](https://vega.github.io/vega-lite/) teams for building such excellent visualization grammars, whose implementations this one follows closely, and to the authors of the dependencies that make this possible: [forme](https://github.com/mgilbir/forme), [goecma262](https://github.com/mgilbir/goecma262) and [pdf0](https://github.com/mgilbir/pdf0). Earlier versions of aster (deprecated, and removed before v0.1.0) ran Vega in [QuickJS](https://bellard.org/quickjs/) (via [QuickJS-NG](https://github.com/quickjs-ng/quickjs) and [fastschema/qjs](https://github.com/fastschema/qjs)) on [wazero](https://github.com/tetratelabs/wazero) (via [andsifr](https://github.com/mgilbir/andsifr)), with [resvg](https://github.com/linebender/resvg) for PNG; resvg remains the reference the rasterizer is tested against ([why it was replaced](docs/adr/0001-pure-go-engine.md)).

## License

[BSD 3-Clause](LICENSE). Third-party notices for the projects the engine derives from are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

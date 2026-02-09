# aster

Go library and CLI for rendering [Vega](https://vega.github.io/vega/) and [Vega-Lite](https://vega.github.io/vega-lite/) visualization specs to SVG and PNG. Pure Go, no CGO required.

Aster embeds the full Vega/Vega-Lite runtime inside [QuickJS](https://bellard.org/quickjs/) (compiled to WASM), with accurate text measurement via [go-text/typesetting](https://github.com/go-text/typesetting) and PNG rendering via [resvg](https://github.com/linebender/resvg) (also compiled to WASM). Everything runs in-process with no external dependencies.

## Features

- Vega-Lite to SVG, PNG, or compiled Vega JSON
- Vega to SVG or PNG
- Arbitrary SVG to PNG conversion
- Accurate HarfBuzz text shaping with embedded Liberation Sans
- Configurable scale factor for high-DPI PNG output
- Multiple Vega-Lite versions (5.8, 6.4)
- Custom fonts, themes, data loaders, memory limits, and timeouts
- Works on any platform Go supports — no native libraries needed

## Install

```
go get github.com/mgilbir/aster
```

Requires Go 1.24+.

## Quick start

### Library

```go
package main

import (
    "log"
    "os"

    "github.com/mgilbir/aster"
)

func main() {
    spec := []byte(`{
        "$schema": "https://vega.github.io/schema/vega-lite/v5.json",
        "data": {"values": [{"a": "A", "b": 28}, {"a": "B", "b": 55}]},
        "mark": "bar",
        "encoding": {
            "x": {"field": "a", "type": "nominal"},
            "y": {"field": "b", "type": "quantitative"}
        }
    }`)

    c, err := aster.New()
    if err != nil {
        log.Fatal(err)
    }
    defer c.Close()

    // Render to SVG.
    svg, err := c.VegaLiteToSVG(spec)
    if err != nil {
        log.Fatal(err)
    }
    os.WriteFile("chart.svg", []byte(svg), 0644)

    // Render to PNG at 2x scale.
    png, err := c.VegaLiteToPNG(spec, aster.WithScale(2.0))
    if err != nil {
        log.Fatal(err)
    }
    os.WriteFile("chart.png", png, 0644)
}
```

### CLI

```bash
# Install the CLI
go install github.com/mgilbir/aster/cmd/aster@latest

# Render a spec to SVG
aster svg -i chart.vl.json -o chart.svg

# Pipe from stdin to stdout
cat chart.vl.json | aster svg > chart.svg

# Compile Vega-Lite to Vega JSON
aster compile -i chart.vl.json -o chart.vg.json

# Allow specs that load data over HTTP
aster svg -i chart.vl.json -o chart.svg -allow-http
```

The CLI auto-detects Vega vs Vega-Lite from the `$schema` field. If absent, Vega-Lite is assumed.

## API

### Converter

All rendering goes through a `Converter`, created with `aster.New()`. A converter is not safe for concurrent use — create one per goroutine if needed.

```go
c, err := aster.New(
    aster.WithVegaLiteVersion("5.8"),
    aster.WithTimeout(60 * time.Second),
    aster.WithLoader(aster.NewHTTPLoader(nil)),
)
if err != nil {
    log.Fatal(err)
}
defer c.Close()
```

**Rendering methods:**

| Method | Input | Output |
|--------|-------|--------|
| `VegaLiteToSVG(spec)` | Vega-Lite JSON | SVG string |
| `VegaLiteToPNG(spec, ...PNGOption)` | Vega-Lite JSON | PNG bytes |
| `VegaLiteToVega(spec)` | Vega-Lite JSON | Vega JSON |
| `VegaToSVG(spec)` | Vega JSON | SVG string |
| `VegaToPNG(spec, ...PNGOption)` | Vega JSON | PNG bytes |
| `SVGToPNG(svg, ...PNGOption)` | SVG string | PNG bytes |

### Options

Options passed to `aster.New()`:

| Option | Default | Description |
|--------|---------|-------------|
| `WithVegaLiteVersion(v)` | `"6.4"` | Vega-Lite version (`"5.8"` or `"6.4"`) |
| `WithLoader(l)` | `DenyLoader{}` | Data loading strategy (see [Loaders](#loaders)) |
| `WithTimeout(d)` | 30s | Max duration per render |
| `WithMemoryLimit(bytes)` | 0 (unlimited) | QuickJS heap limit |
| `WithTextMeasurement(bool)` | `true` | HarfBuzz text shaping for accurate layout |
| `WithFont(family, ttf)` | — | Register a custom TTF font |
| `WithDefaultFontFamily(name)` | `"Liberation Sans"` | Fallback family for sans-serif resolution |
| `WithSystemFonts()` | disabled | Scan system-installed fonts |
| `WithTheme(json)` | — | Vega theme config applied to all renders |
| `WithTimezone(tz)` | `"UTC"` | Timezone for JS Date operations (only UTC supported) |

**PNG options** passed per render:

| Option | Default | Description |
|--------|---------|-------------|
| `WithScale(f)` | `1.0` | Scale factor; 2.0 produces 2x dimensions |

### Loaders

Loaders control how Vega fetches external data. The default denies all loading for security. Loaders that hold resources (like `FileLoader` and `FallbackLoader`) are automatically closed when `Converter.Close()` is called.

```go
// Deny all external data (default).
aster.New()

// Allow HTTP/HTTPS requests.
aster.New(aster.WithLoader(aster.NewHTTPLoader(nil)))

// Allow HTTP with a custom client (timeouts, proxies, etc).
aster.New(aster.WithLoader(aster.NewHTTPLoader(customClient)))

// HTTP with domain whitelisting — only these hosts are permitted.
aster.New(aster.WithLoader(&aster.HTTPLoader{
    Client:         http.DefaultClient,
    AllowedDomains: []string{"cdn.jsdelivr.net"},
}))

// HTTP with base URL — relative URIs in specs are resolved against it.
aster.New(aster.WithLoader(&aster.HTTPLoader{
    Client:  http.DefaultClient,
    BaseURL: "https://cdn.jsdelivr.net/npm/vega-datasets@v1.29.0/",
}))

// Serve files from a local directory (uses os.Root for path containment).
aster.New(aster.WithLoader(&aster.FileLoader{BaseDir: "./data"}))

// Static test data — returns a JSON payload for any URI, no server needed.
aster.New(aster.WithLoader(&aster.StaticLoader{
    Value: []map[string]any{{"a": "A", "b": 28}, {"a": "B", "b": 55}},
}))

// Composite: try local files first, fall back to HTTP.
aster.New(aster.WithLoader(aster.NewFallbackLoader(
    &aster.FileLoader{BaseDir: "./data"},
    aster.NewHTTPLoader(nil),
)))
```

**Available loaders:**

| Loader | Description |
|--------|-------------|
| `DenyLoader` | Rejects all loading (default) |
| `HTTPLoader` | HTTP/HTTPS with optional `AllowedDomains` and `BaseURL` |
| `FileLoader` | Local files from a base directory, secured with `os.Root` |
| `StaticLoader` | Returns a fixed JSON value for any URI (test stub) |
| `FallbackLoader` | Tries child loaders in order until one succeeds |

`HTTPLoader` rejects non-HTTP schemes (`ftp:`, `javascript:`, `data:`, `file:`), URIs with userinfo (`user:pass@host`), and domains not in the allowlist. Domain matching is case-insensitive.

`FileLoader` rejects absolute paths, path traversal (`..`), and URIs with schemes. It uses Go's `os.Root` for OS-level path containment, which also blocks symlink escapes.

`FallbackLoader` naturally routes by URI shape — `FileLoader` accepts relative paths while `HTTPLoader` accepts absolute URLs — so combining them covers specs that reference both local and remote data.

### Custom fonts

The embedded Liberation Sans covers most Latin text. For other scripts or specific fonts:

```go
ttf, _ := os.ReadFile("MyFont-Regular.ttf")
bold, _ := os.ReadFile("MyFont-Bold.ttf")

c, err := aster.New(
    aster.WithFont("My Font", ttf),
    aster.WithFont("My Font", bold),
    aster.WithDefaultFontFamily("My Font"),
)
```

Custom fonts are used for both text measurement (SVG layout) and PNG rendering.

## Performance

**Startup:** Creating a `Converter` loads the full Vega/Vega-Lite module graph (~53-55 ES modules) and initializes the QuickJS WASM runtime. This takes roughly 100-200ms. The PNG renderer (resvg WASM) is lazy-initialized on first PNG render.

**Rendering:** Most specs render in under 100ms. Geographic visualizations with TopoJSON projections are significantly slower (2-40s) due to the computational cost of coordinate transforms in the JS runtime.

**Memory:** Each `Converter` holds a QuickJS WASM instance. Use `WithMemoryLimit()` to cap heap usage if running untrusted specs.

**Concurrency:** A `Converter` is **not safe for concurrent use** — the underlying WASM runtime is single-threaded. For parallel rendering, create multiple `Converter` instances.

**Reuse:** A single `Converter` can render many specs sequentially. Amortizing startup across renders is the recommended pattern.

## Developer notes

### Architecture

The rendering pipeline is:

1. **Vega-Lite → Vega** — Vega-Lite compiler runs in QuickJS (WASM)
2. **Vega → SVG** — Vega runtime runs in QuickJS with Go callbacks for text measurement and data loading
3. **SVG → PNG** — resvg (Rust, compiled to WASM) rasterizes the SVG with embedded fonts

Both WASM runtimes (QuickJS via [qjs](https://github.com/fastschema/qjs), resvg via [wazero](https://github.com/tetratelabs/wazero)) run in pure Go with no CGO.

### QuickJS polyfills

The JS environment provides polyfills for APIs that Vega expects but QuickJS lacks:

- `structuredClone` — partial (fails on some geo projection edge cases)
- `setTimeout` / `clearTimeout` — synchronous (d3-timer, vega-scenegraph)
- `requestAnimationFrame` — synchronous (vega-view)
- `performance.now` — monotonic clock
- `Date` methods — redirected to UTC equivalents (QuickJS WASM has no timezone config)

### Building from source

```bash
# Vendor JavaScript modules (requires network)
make vendor-js

# Vendor vega-datasets test data
make vendor-datasets

# Rebuild resvg WASM binary (requires Docker)
make vendor-resvg

# Build
go build ./...

# Run tests (fast, skips slow geo specs)
go test -short ./...

# Run full test suite
go test ./...
```

### Known limitations

- **Timezone:** Only UTC is supported. Specs with local-time temporal axes will produce different output than browser-rendered charts.
- **Emoji:** No emoji font is bundled. Specs using emoji characters will render with missing glyphs.
- **`structuredClone`:** The polyfill does not handle `undefined` values in objects, which affects a few geographic projection specs.
- **Interactive features:** Selection and signal interactivity are evaluated at initial state only; there is no event loop.

## License

MIT

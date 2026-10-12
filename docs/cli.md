# The `aster` command

`aster` renders a Vega or Vega-Lite spec file to SVG, PNG or PDF, or compiles Vega-Lite to Vega, from the command line or a script. It covers the common options of the library: loaders for HTTP and local files, fonts, themes, memory limits and time zones. The rest is [library-only](#library-only).

```bash
go install github.com/mgilbir/aster/cmd/aster@latest

aster svg -i chart.vl.json -o chart.svg
aster png -i chart.vl.json -o chart.png -scale 2 -recode
aster pdf -i chart.vl.json -o chart.pdf -text named
aster compile -i chart.vl.json -o chart.vg.json
cat chart.json | aster svg > chart.svg                      # stdin to stdout
aster svg -i chart.vl.json -allow-domain cdn.jsdelivr.net    # load data from one host
aster svg -i chart.vl.json -signal cutoff=10 -signal 'label=Q3 sales'
aster svg -i chart.vl.json -lenient                          # render past failed loads
aster svg -i examples/cars.vl.json -allow-file examples      # data/cars.json from examples/data/
aster svg -i chart.vl.json -base-url https://cdn.jsdelivr.net/npm/vega-datasets@3.2.1/
aster png -i chart.vl.json -font 'Inter=Inter.ttf' -theme dark.json -timezone Europe/Madrid -memory-limit 256MiB
```

## Vega or Vega-Lite?

`svg`, `png` and `pdf` take either language and decide which the input is:

1. by `$schema`, when it names a Vega-Lite schema (`…/schema/vega-lite/…`) or a Vega one (`…/schema/vega/…`);
2. else by the top-level properties only one language has. Vega-Lite: `mark`, `encoding`, `layer`, `facet`, `repeat`, `concat`, `hconcat`, `vconcat`, `spec`, `params`, `transform`, `resolve`, `selection`, `datasets`, `projection`, `view`, or a `data` that is an object. Vega: `marks`, `signals`, `scales`, `axes`, `legends`, `projections`, `encode`, or a `data` that is an array;
3. else, or when it has properties of both, the command fails and asks for `-from vega` or `-from vega-lite`. It does not guess, because a spec rendered as the other language draws an empty chart without failing.

This classifies every spec in the repository's corpora (about 1,400, with their `$schema` removed) correctly. `compile` takes Vega-Lite only and refuses a spec it detects as Vega.

The `$schema`'s *version* does not choose the compiler: a `v5.json` spec is compiled by Vega-Lite 6.4 unless `-version 5.8`.

## Flags

| Flag | `svg` | `png` | `pdf` | `compile` | Meaning |
|---|:-:|:-:|:-:|:-:|---|
| `-i file` | ✓ | ✓ | ✓ | ✓ | input spec; `-` or omitted: stdin |
| `-o file` | ✓ | ✓ | ✓ | ✓ | output; omitted: stdout |
| `-version v` | ✓ | ✓ | ✓ | ✓ | Vega-Lite compiler: `6.4` (default) or `5.8`, or the full `6.4.3`, `5.8.0` (a leading `v` is accepted) |
| `-from lang` | ✓ | ✓ | ✓ | | `auto` (default), `vega` or `vega-lite` |
| `-timeout d` | ✓ | ✓ | ✓ | ✓ | per render, e.g. `60s`; default 30s; `0` disables |
| `-memory-limit size` | ✓ | ✓ | ✓ | ✓ | `WithMemoryLimit`: bytes, or with a unit (`KiB`, `MiB`, `GiB`, `KB`, `MB`, `GB`), e.g. `256MiB`; see [limits.md](limits.md) |
| `-theme file` | ✓ | ✓ | ✓ | ✓ | `WithTheme`: a JSON file holding a Vega config object |
| `-timezone zone` | ✓ | ✓ | ✓ | ✓ | `WithTimezone`: an IANA zone, e.g. `America/New_York`; default UTC |
| `-font family=file` | ✓ | ✓ | ✓ | | `WithFont`: register a font file (TrueType, OpenType, WOFF, a collection) under a family name (repeatable) |
| `-allow-file dir` | ✓ | ✓ | ✓ | | load data and images named by a relative path from the files under `dir` (`FileLoader`; repeatable, the first that has the file serves it) |
| `-allow-http` | ✓ | ✓ | ✓ | | load data and images over HTTP(S) from public hosts |
| `-allow-domain host` | ✓ | ✓ | ✓ | | only from this host (repeatable); implies `-allow-http` |
| `-base-url url` | ✓ | ✓ | ✓ | | resolve relative URLs against this `http`/`https` URL and load them over HTTP (`HTTPLoader.BaseURL`); implies `-allow-http` |
| `-allow-private-networks` | ✓ | ✓ | ✓ | | let HTTP loading reach loopback, link-local and private addresses |
| `-signal name=value` | ✓ | ✓ | ✓ | | set a top-level signal (a Vega-Lite parameter) before rendering; a value that parses as JSON is that JSON, else a string (repeatable) |
| `-lenient` | ✓ | ✓ | ✓ | | render past failed data and image loads and evaluation errors, as a browser does, printing each |
| `-scale f` | | ✓ | | | PNG scale factor (default 1) |
| `-recode` | | ✓ | | | losslessly re-encode the PNG smaller (indexed or truecolor) |
| `-text mode` | | | ✓ | | `embed` (default), `named` or `outlines`; see [pdf.md](pdf.md) |

With `-allow-file` and HTTP loading (`-allow-http`, `-allow-domain`, `-base-url`) together, a URL is tried against the directories first, in order, then over HTTP: the loaders compose as a `FallbackLoader`. Relative URLs, as the Vega-Lite examples' `data/cars.json`, need `-allow-file` or `-base-url`.

## Errors, warnings and exit status

| Exit | When |
|---|---|
| 0 | the output was written (warnings may have been printed) |
| 1 | any error: unreadable input, a spec that is invalid or is neither Vega nor Vega-Lite detectably, a failed load or evaluation error (without `-lenient`), a limit, a timeout, an unwritable output |
| 2 | an invalid command line: no command, an unknown command or flag (the flag package's usage is printed), or a flag value that is not valid (`-text x`, `-from x`, `-timeout -1s`, `-scale 0`, an unknown `-version` or `-timezone`, a `-memory-limit` that is not a size, a `-theme` or `-font` file that is missing or does not load, an `-allow-file` directory that does not open, a `-base-url` that is not an absolute http(s) URL) |

Messages go to stderr, prefixed `aster:`. Vega's and the Vega-Lite compiler's warnings (`compile` prints the compiler's too) are printed as `aster: warning: …` and do not change the exit status. With `-lenient`, errors the render goes past are printed as `aster: ignored: …`. A load no flag allowed adds a hint: `-allow-file` or `-base-url` for a relative URL, `-allow-http` or `-allow-domain` for an http(s) one. See [errors.md](errors.md) for what counts as an error.

## Library-only

These have no flag: use the library (see [api.md](api.md)).

- fonts beyond `-font`: `WithFontFace`, `WithFontInstance`, `WithFontPalette`, `WithFontFallback`, `WithDefault*Family`, `WithSystemFonts`;
- `WithTextMeasurement`, `WithHarfBuzzTextMetrics`, `WithContext`;
- custom loaders, `StaticLoader`, `HTTPLoader`'s `Client`, `MaxResponseBytes` and `Timeout`, `FileLoader.MaxBytes`;
- `WithQuantizePNG`, PDF font usage (`…PDFUsage`, `SubsetFont`);
- `SVGToPNG` / `SVGToPDF` of an arbitrary SVG.

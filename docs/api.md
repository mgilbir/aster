# API reference

Every exported name of `github.com/mgilbir/aster`, grouped by task. The godoc ([pkg.go.dev](https://pkg.go.dev/github.com/mgilbir/aster)) has each in full; this page is the map.

## Converter

`aster.New(opts ...Option) (*Converter, error)` creates a converter. It is safe for concurrent use by any number of goroutines, and sharing one is cheaper than making one per goroutine (fonts load once, into one set every output uses). `Close()` cancels calls in flight (they fail with `context.Canceled`), waits for them, closes the Loader if it implements `io.Closer`, and makes every later call fail with `ErrClosed`. Closing twice is harmless, and so is closing from inside a call: from an error handler or a Loader method, `Close` cancels and returns at once, and the Loader is closed when the last call ends. A call started from a handler or a Loader (a nested render) is allowed too; after `Close` it fails with `ErrClosed`.

To cancel one call rather than the Converter, pass it `WithContext(ctx)`: every method takes it. The call ends when `ctx` ends, the Converter's `WithTimeout` runs out, or `Close` is called, whichever is first, and fails with `context.Canceled` or `context.DeadlineExceeded`; the Loader's methods get a context derived from `ctx`, with its values. A call whose context ends never returns output.

| Method | Input → output | Per-call options |
|---|---|---|
| `VegaLiteToSVG(spec)` | Vega-Lite JSON → SVG `string` | `RenderOption` |
| `VegaLiteToPNG(spec, …)` | Vega-Lite JSON → PNG `[]byte` | `PNGOption` |
| `VegaLiteToPDF(spec, …)` | Vega-Lite JSON → PDF `[]byte` | `PDFOption` |
| `VegaLiteToPDFUsage(spec, …)` | Vega-Lite JSON → PDF + `[]FontUsage` | `PDFOption` |
| `VegaLiteToVega(spec, …)` | Vega-Lite JSON → Vega JSON `[]byte` | `RenderOption`: `WithContext`, and `WithErrorHandler`, told of the compiler's warnings; `WithSignal` is ignored, nothing being rendered |
| `VegaToSVG` / `VegaToPNG` / `VegaToPDF` / `VegaToPDFUsage` | the same, from Vega JSON | as above |
| `SVGToPNG(svg, …)` / `SVGToPDF(svg, …)` / `SVGToPDFUsage(svg, …)` | an SVG string (best: one Vega wrote) → PNG / PDF | `PNGOption` / `PDFOption` |

The language of a spec is the method's, whatever its `$schema` says, and the `$schema`'s version does not choose the Vega-Lite compiler (`WithVegaLiteVersion` does).

### SVG input

`SVGToPNG` draws general SVG as [resvg](https://github.com/linebender/resvg) does, the reference its output is tested against, except filters where resvg departs from the Filter Effects specification (below). `SVGToPDF` draws only what Vega's SVG renderer writes ([pdf.md](pdf.md#what-is-supported)) and fails for anything else.

Filters (`<filter>` and the `filter` attribute) are drawn by `SVGToPNG` only. Vega never writes them, so they reach the engine only in SVG given to `SVGToPNG`; `SVGToPDF` fails for a `<filter>` element or a `filter` attribute ("svgpdf: unsupported SVG element <filter>").

Filters follow [W3C Filter Effects Module Level 1](https://www.w3.org/TR/filter-effects-1/) (FE1 below), and SVG 2 where it defers to it. Where resvg departs from the specification, aster follows the specification ([ADR 0015](adr/0015-filters-follow-the-specification.md)); each departure is asserted by a test that the output differs from resvg's and by one of what the specification draws.

- **Drawn:** every primitive of SVG 1.1 and `feDropShadow`: `feBlend`, `feColorMatrix`, `feComponentTransfer`, `feComposite` (`lighter` included), `feConvolveMatrix`, `feDiffuseLighting` and `feSpecularLighting` (distant, point and spot lights), `feDisplacementMap`, `feDropShadow`, `feFlood`, `feGaussianBlur`, `feImage`, `feMerge`, `feMorphology`, `feOffset`, `feTile` and `feTurbulence`. `kernelUnitLength` spaces `feConvolveMatrix`'s cells and the lighting normals' samples, each sample between pixels interpolated (bilinear) as Firefox does: FE1 §9.9, "kernelUnitLength, in combination with the other attributes, defines an implicit pixel grid in the filter effects coordinate system"; §9.10, the normal's "dx and dy values ... are determined by the value (explicit or implicit) of attribute kernelUnitLength". resvg ignores it.
- **`feImage`** draws an image or an element of the document. An image is a `data:` URI or an href fetched through the [Loader](loading-data.md#what-goes-through-the-loader), as an `<image>` is, with the same limits; one that cannot be read gives a transparent result and is reported as an `<image>` would be. An element (`href="#id"`) is drawn with the render's own bounds (elements, pixel work, layer depth). A chain of feImages drawing filtered elements is bounded by the layer depth ([limits.md](limits.md)).
- **What a filter does to its element:** a `filter` naming no element, or one that is not a `<filter>`, applies no filter at all, the whole list included (FE1 §5: "the whole filter chain is ignored. No filter is applied to the object"; resvg does not draw the element). A `<filter>` with no primitive hides its element (§9.3), as does one whose region has no width or height. `objectBoundingBox` units on an element whose box has no width or height (a horizontal line) leave that filter out (SVG 2 §8.11: "the given effect (e.g., a gradient or a filter) will be ignored"; resvg does not draw the element).
- **Sub-regions** (FE1 §9.4): under `primitiveUnits="objectBoundingBox"` a primitive's `x`, `y`, `width` and `height` are fractions of the bounding box ("values in the coordinate system established by attribute primitiveUnits"; resvg maps the filter region by them); one left out is the union of the sub-regions of the results the primitive reads ("default to the union (i.e., tightest fitting bounding box) of the subregions defined for all referenced nodes"), or the filter region for a standard input, no input and `feTile`; every result, `feOffset`'s too, is clipped to its sub-region ("a hard clip clipping rectangle on the filter primitive result"; resvg leaves an `feOffset` unclipped). A width or height that is not positive gives a transparent result and the filter goes on (§9.2: "the result is a transparent black image"; resvg drops the primitives from there).
- **A pass through filter** (FE1 §4: "output is equal to the primary input of the filter primitive") where the specification makes one, and resvg a transparent result or a default: an `feConvolveMatrix` whose `kernelMatrix` is not `orderX` × `orderY` numbers (§9.9: "the filter primitive acts as a pass through filter"), or, of its other preconditions, whose order is not positive or whose target lies outside the kernel (the changes: "Make feColorMatrix and feConvolveMatrix a pass through on unfulfilled pre-conditions"); an `feColorMatrix` whose `values` are not the type's count (§9.6), or left out (the identity, exactly); an `feMorphology` whose radius is zero, negative, left out or not one or two numbers (§9.17: "A negative or zero value disables the effect ... (i.e., the result is the filter input image)"; resvg makes such radii 1); an `feGaussianBlur` with a negative `stdDeviation` (§9.14). An `feConvolveMatrix` divisor of 0 is the default, the kernel's sum (§9.9: "the default value will be used instead"); resvg gives a transparent result.
- **Transparent results**, as in resvg: a lighting with no light source (its content model wants "exactly one light source element"; FE1 says no more); an `feImage` with no href, whose `#id` is no element, or whose image cannot be read (§9.15: "fills the filter primitive subregion with transparent black"); an `feImage` on a circle of references, an element whose filter's feImage draws it again, directly or through others (SVG 2 §16.1.2: "URL references that directly or indirectly reference themselves are treated as invalid circular references"). resvg sets the filter of an element that names its own feImage's filter to none, in the feImage and in the document.
- **Where resvg departs from FE1, aster follows FE1:**
  - `feDisplacementMap` (§9.11, "P'(x,y) ← P( x + scale * (XC(x,y) - .5), y + scale * (YC(x,y) - .5))"): the offset is the scale times the channel's distance from .5, once (resvg multiplies by `scale` twice); the map is read non-premultiplied ("The calculations using the pixel values from in2 are performed using non-premultiplied color values"; resvg reads it premultiplied); `in` stays in its colour space, `color-interpolation-filters` applying to `in2` alone ("The in source image must remain in its current color space"; resvg converts both); under `objectBoundingBox` the x offset is a fraction of the box's width and the y offset of its height (resvg scales both by the mean of its sides).
  - `feTile` (§9.20): a tile's period is its input's sub-region, from its corner, the part of it outside the filter region transparent ("width and height represent the width and height of the input image's filter primitive subregion"; resvg cuts the tile to the image and repeats what is left, and fails the filter for a tile wholly outside it); the result is in its input's colour space (§10: color-interpolation-filters "has no effect on filter primitives like feOffset, feImage, feTile or feFlood"; resvg tags it sRGB).
  - `feTurbulence` (§9.21): the gradients are the reference code's, a gradient drawn again while it is zero or longer than 1 ("discard the current random vector; try it again"; resvg follows SVG 1.1's code, which normalizes every one, and so do Chromium and Firefox, Firefox drawing a zero one again: here aster matches no browser); a stitched tile is the primitive's sub-region in user space ("The filter primitive subregion is to be passed as the arguments fTileX, fTileY, fTileWidth and fTileHeight"; resvg passes the filter region's size in pixels and each pixel's own index); the seed is the truncated number, whatever its size (resvg saturates it to 32 bits); a `numOctaves` that is not a whole number is the initial 1 ("<integer>"; resvg rounds it).
  - Lighting (§9.10, §9.19, §11): `lighting-color` is converted to the primitive's colour space (§9.1: "The color space in which a particular filter primitive performs its operations is determined by the value of the property color-interpolation-filters"; Chromium converts it too; resvg uses it unconverted, as a linearRGB value); a light's `x`, `y`, `z` and `pointsAt` are in `primitiveUnits` (§11.3: "in the coordinate system established by attribute primitiveUnits"), z under `objectBoundingBox` a fraction of the box's normalized diagonal, sqrt((w² + h²)/2), as SVG 2 §8.9 measures a length along no axis and Chromium resolves it (resvg reads them as user space); `lighting-color="inherit"` is the parent's value (§3: properties "accept the inherit keyword"; resvg gives white); a `specularExponent` outside 1–128 is used (FE1 gives none: "Exponent for specular term, larger is more "shiny""; resvg gives a transparent result), and so is a spot light's negative one (resvg makes it 1); an image two pixels wide or high is lit, with the specification's corner and edge kernels (resvg leaves one under three pixels transparent).
  - `feImage` (§9.15: "the referenced resource is rendered according to the behavior of the use element. In either case, the current user coordinate system depends on the value of attribute primitiveUnits"): an element is drawn at its own coordinates in the filtered element's user space, with its whole transform, and under `objectBoundingBox` in the space of the box; an image is an `<image>` whose viewport is the sub-region, with the whole transform; an element's own filter has the filter region it has (resvg draws an element from the sub-region's corner with the device scale alone, an image with the device scale and translation alone, and cuts the layer of a filter inside an feImage to the feImage's region). Chromium places an element from the sub-region's corner, as resvg does; FE1 does not say so.
  - `feColorMatrix` (§9.6): `hueRotate`'s angle, cosine, sine and matrix are f64, which agrees with exact arithmetic (resvg's f32 does not: at 90° its cosine is -4.37e-8, and a whole byte truncates one lower); a `saturate` value outside 0–1 is used ("Values outside the 0..1 range under- or oversaturates the filter input image respectively"; resvg clamps it); `luminanceToAlpha` is 0.2126 R + 0.7152 G + 0.0722 B ("The precision of the luminance coefficients increased in comparison to previous specification texts"; resvg keeps SVG 1.1's 0.2125, 0.7154, 0.0721).
  - `feMorphology` (§9.17, "a rectangle with a width of 2*x-radius"): a radius from 2^31 is a kernel of the whole image (resvg's 32-bit size wraps to zero and leaves every pixel the operator's identity).
  - `feComponentTransfer` (§9.7: "If more than one transfer function element of the same kind is specified, the last occurrence is to be used"): the last one counts, a `type` that is no type being identity (resvg keeps an earlier valid one); `feComposite operator="lighter"` is plus-lighter (§9.8; resvg draws it as `over`); an `feDropShadow` `stdDeviation` that is not one or two numbers is the initial 2 (§9.12; resvg: 0).
- **Choices FE1 leaves open,** kept as resvg makes them: `feTurbulence` samples a pixel at its top-left corner (the reference code takes a point and does not say which; Firefox samples the corner, Chromium the centre); `feDisplacementMap` reads the nearest pixel ("A future version of this spec will define the interpolation method"), as `feOffset` moves by whole pixels; a morphology kernel of 2·⌈r⌉ pixels from ⌈r⌉ before the pixel; a blur of five box passes from σ 2 ("an approximation can be used"); values in [0, 1] to bytes, truncated or rounded as resvg does.
- **Not drawn as FE1 defines:** CSS filter functions in the `filter` attribute (`blur(2px)`, `drop-shadow(…)`, FE1 §6) are ignored, and an element whose `filter` has only functions is drawn unfiltered, where resvg applies them; the inputs `BackgroundImage`, `BackgroundAlpha`, `FillPaint` and `StrokePaint` are read as `SourceGraphic`, as by resvg; `feGaussianBlur`'s `edgeMode` other than `none` and `feBlend`'s `no-composite` are not read. An element in a filter that is not a primitive (an unknown name) is ignored.

A filter's work and memory are bounded ([limits.md](limits.md)): its region's pixels, the pixel memory its results hold, and the pixel operations its passes make.

## Options for New

| Option | Default | |
|---|---|---|
| `WithLoader(l Loader)` | `DenyLoader{}` (also for `nil`) | what data and images may be loaded; [loading-data.md](loading-data.md). A nil pointer (`(*FileLoader)(nil)`) is an error |
| `WithTimeout(d)` | 30 s; `<= 0` none | the whole call; [limits.md](limits.md) |
| `WithMemoryLimit(bytes)` | 0: the fixed default budgets | replaces the memory budgets; [limits.md](limits.md) |
| `WithPixelWorkLimit(ops)` | 128 Mi | the rasterizer's work budget at scale 1, times the scale squared above it and at least 16 per output pixel; raise it for SVG with large filters; 0 is refused; [limits.md](limits.md) |
| `WithVegaLiteVersion(v)` | `"6.4"` | `"6.4"` or `"6.4.3"` (Vega-Lite 6.4.3), `"5.8"` or `"5.8.0"`; a leading `v` is accepted |
| `WithTheme(json string)` | none | a Vega config applied to every render, to the Vega-Lite compiler as well (so `VegaLiteToVega` reflects it); must be a JSON object |
| `WithTimezone(tz)` | `"UTC"` | local time for time scales, `timeFormat`, dates without a zone; any IANA name Go knows. **`"Local"` is refused**: it would make the output depend on the host |
| `WithFont`, `WithFontFace`, `WithFontInstance`, `WithFontPalette`, `WithFontFallback`, `WithDefaultFontFamily`, `WithDefaultSerifFamily`, `WithDefaultMonospaceFamily`, `WithSystemFonts`, `WithTextMeasurement`, `WithHarfBuzzTextMetrics` | see [fonts.md](fonts.md) | |

### Errors from New

`New` fails for an option that is not valid, rather than silently replacing it by a default: an unknown Vega-Lite version (the message lists the available ones), an unknown time zone or `"Local"`, a theme that is not a JSON object, a nil pointer as the Loader, a negative `WithFontFace` index, a font given with `WithFont`, `WithFontFace` or `WithFontInstance` that does not load, and a `WithFontFallback`, `WithDefaultFontFamily`, `WithDefaultSerifFamily` or `WithDefaultMonospaceFamily` family with no font. It loads the fonts when any font option is given (a copy of the bytes: the caller may reuse its buffer); with the embedded fonts alone it loads nothing, and they load on first use.

`AvailableVersions() ([]VersionInfo, error)` lists the bundled compilers (`Key` such as `"vl6_4"`, `VegaVersion`, `VegaLiteVersion` such as `"6.4.3"`); `WithVegaLiteVersion` takes a `VegaLiteVersion` or its `major.minor`. Its error is always nil, and is kept so that existing callers compile.

## Per-call options

| Option | Type | Default | |
|---|---|---|---|
| `WithContext(ctx)` | `ContextOption` (render, PNG, PDF) | none | run the call under `ctx` as well: cancel it, give it a deadline, pass values to the Loader; see above |
| `WithSignal(name, value)` | `SignalOption` (render, PNG, PDF) | none | set a top-level signal (a Vega-Lite parameter) before drawing, as `view.signal(name, value)`; `value` is anything `encoding/json` encodes; an undefined name fails; ignored by `SVGTo…` and `VegaLiteToVega` |
| `WithErrorHandler(h)` | `ErrorHandlerOption` (render, PNG, PDF) | none: errors fail the call | see [errors.md](errors.md) |
| `WithScale(f)` | `PNGOption` | 1 | PNG size factor; must be positive and finite |
| `WithRecodePNG()` | `PNGOption` | off | losslessly re-encode into the cheapest equivalent PNG (indexed or truecolor); same pixels, typically several times smaller |
| `WithQuantizePNG(maxColors)` | `PNGOption` | off | lossily quantize to at most `maxColors` colours (clamped to 2–256), 8-bit indexed with dithering and a quality guard; falls back to `WithRecodePNG` when the guard trips, and without trying for an image of more than 65,536 distinct colours; wins over `WithRecodePNG` |
| `WithPDFText(mode PDFTextMode)` | `PDFOption` | `PDFTextEmbed` | `PDFTextEmbed`, `PDFTextNamed`, `PDFTextOutlines`; [pdf.md](pdf.md) |

## Errors and diagnostics

| Name | |
|---|---|
| `ErrLimit` | wrapped by every error for an exceeded resource bound |
| `ErrClosed` | returned by every call after `Close` |
| `*LoadError` | `Resource` (`"data"` or `"image"`), `URL`, `Err`: a resource that could not be loaded or read |
| `Warning` | a Vega warning or a dropped link, seen only through `WithErrorHandler` |
| `context.DeadlineExceeded`, `context.Canceled` | wrapped by the error of a call that ran out of time (`WithTimeout`, or the deadline of a `WithContext` context) or was canceled (its context, or `Close`) |

`HTTPLoader`'s refusal of a private address has no exported sentinel: it is the `Err` of a `*LoadError`.

See [errors.md](errors.md).

## Loaders

`Loader` (interface: `Sanitize`, `Load`), `DenyLoader`, `HTTPLoader` / `NewHTTPLoader`, `FileLoader` / `NewFileLoader`, `StaticLoader` (its methods take a pointer: `&aster.StaticLoader{Value: v}`), `FallbackLoader` / `NewFallbackLoader`. See [loading-data.md](loading-data.md) for their fields and for writing your own.

## PDF fonts

`FontUsage` (`PostScriptName`, `Source`, `GIDs`) and `SubsetFont(source, gids) (subset, postScriptName, error)`; see [pdf.md](pdf.md#sharing-fonts-across-many-charts).

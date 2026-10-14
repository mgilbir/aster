# 0016: Custom projections are a raw projection and its parameters, per Converter

**Status:** accepted, 2026-10-14.

## Context

Vega registers 17 projection types: d3-geo's 16 and `mollweide`. A page that wants another one, such as one of d3-geo-projection's roughly 95, registers it with `vega.projection(name, constructor)`. The constructor is any JavaScript that returns a d3 projection. The Vega editor does this for the gallery's `projections` example. Without the registration, upstream fails on that spec with "Unrecognized projection type: airy", as aster does. aster runs no JavaScript, so a Go caller needed its own way to register a projection.

## Decision

- **`WithProjection(name, Projection)` registers a type on one Converter.** It is looked up before the built-in types, case-insensitively, as Vega's are, so a registered type may replace a built-in one, as `vega.projection` replaces it. Other Converters do not see it. Upstream's registry is global to the page; a Go process may hold Converters for different callers, so it is not.
- **A type is described as d3-geo-projection's constructors build one**, not as arbitrary code:
  - `Raw(params)` returns a raw projection, a forward function on the unit sphere and an optional inverse. It is called again whenever a parameter changes, as d3's `geoProjectionMutator` calls its factory.
  - `Params` are its parameters and their defaults. Each one is a property Vega forwards (`coefficient`, `distance`, `fraction`, `lobes`, `parallel`, `radius`, `ratio`, `spacing`, `tilt`).
  - `Scale`, `ClipAngle`, `Center` and `Rotate` are the defaults its constructor sets. Apart from its parameters, those four are the only setters any d3-geo-projection constructor calls.
- **A custom projection is the engine's standard projection**, as every built-in type except `identity` and `albersUsa` is. A spec's scale, translate, center, rotate, clipAngle, clipExtent, precision, reflectX/Y and fit work on it exactly as on a built-in.
- **Parameters keep the value a spec gives.** Raw converts units: degrees to radians, for example. Vega's `copy()` reads a parameter back through its getter, and some getters round-trip through radians. A copy can therefore differ from upstream's in the last bit. Nothing in aster copies a projection except a spec's `scaleCopy`.
- **New validates a definition**: it calls `Raw` once with the defaults, and refuses an empty name, a missing `Raw` or `Forward`, a parameter Vega does not forward, and a default that is not finite. A Raw that fails later, during a render (a panic, or no Forward for a parameter value), fails that render with an error.

## Why

d3-geo-projection's constructors share this shape: a raw projection, a mutator over named parameters, and a few setter calls. Describing a type by that shape covers almost the whole library. Because the engine's own projection machinery does the rest, a custom projection gets resampling, clipping, fitting and bounds exactly as a built-in one does. The tests check that, value for value, against upstream with the real d3-geo-projection constructors registered.

Taking arbitrary code instead would mean exposing the engine's stream pipeline (the d3 stream protocol, clipping, resampling) as public API. That is a large surface, and nearly every projection a caller wants does not need it.

## Consequences

- A projection that replaces d3's clipping stream cannot be described: d3-geo-projection's interrupted projections (`interrupt()`), its polyhedral projections, and those whose constructors install their own clipping (`armadillo`, `berghaus`, `gilbert`, `gingery`, `hammerRetroazimuthal`, `healpix`). Supporting them would mean exposing the stream pipeline, a separate decision. Nor can `wagner`: Vega forwards `parallels` to it, but a parameter here is a number and `parallels` is a pair, which the engine applies only to its built-in conic projections.
- The functions run inside renders, from several renders at once, and must be safe for that. They are not preempted by the deadline: the walk checks the deadline between points, so a function that never returns holds its render.
- The CLI cannot register a projection: it has no way to give Go functions.
- `internal/geo/testdata/gen_pipeline.mjs` registers six d3-geo-projection constructors in Vega (`airy`, `hammer`, `bottomley`, `bonne`, `winkel3`, `littrow`). `TestVegaPipeline` replays their cases with Go ports registered through the same mechanism and compares them exactly. That covers fitting, every projection property, parameters across their branches, and the default scale, clip angle and center.
- Exact vectors for `invert` are not recorded. jsmath reproduces V8 on arm64, where the other vectors come from; V8 on x86-64 differs in the last bit near rounding ties (`asin` in one built-in case). An inverse is checked by round trips and, through `WithProjection`, against the built-in `equirectangular`.

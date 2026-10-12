// Package geo is a pure-Go port of the cartographic machinery Vega uses:
// d3-geo (streams, spherical maths, clipping, resampling, projections, path
// generation, graticules and circles), the one d3-geo-projection projection
// vega-projection registers (Mollweide), topojson-client's feature/mesh, the
// projection registry of vega-projection, and vega-geo: its Projection operator
// and the transforms geojson, geopath, geopoint, geoshape and graticule. The
// density transforms (contour, isocontour, kde2d, heatmap) live in
// transforms/contour.
//
// Geometry is always read straight from jsval values (GeoJSON objects) and
// pushed through the Stream interface, the d3-geo stream protocol: every stage
// (rotation, clipping, resampling, path generation, bounds, centroid ...) is a
// Stream wrapping the next one. Nothing here panics on malformed geometry;
// coordinates that are not numbers read as NaN exactly as they do in
// JavaScript arithmetic.
//
// Results are bit-identical to d3's, which needs care in three places. The
// transcendental functions come from package jsmath (V8's fdlibm, with its fused
// multiply-adds). Every product that is added to something is written
// float64(x*y)+z, because Go fuses x*y+z on arm64 while JavaScript rounds the
// product first. And the angle constants are typed float64 constants: as
// untyped ones, 30*radians would be evaluated exactly instead of as
// 30 * double(pi/180), a different double.
//
// GeoCircle, GeoContains, GeoDistance, GeoInterpolate, GeoLength and
// GeoRotation are not called by the engine: Vega exposes no expression for
// them. They complete the d3-geo port so that d3-geo's own test suite replays
// against it (upstream_test.go), which tests, against upstream, the rotation,
// circle and length streams the projections and clipping share with them.
//
// Nothing is safe for concurrent use: a Projection (and its Path) carries the
// mutable state of d3's closures.
package geo

package geo

import "github.com/mgilbir/aster/internal/jsval"

// Stream is d3-geo's stream protocol: geometry is delivered as a sequence of
// points bracketed by line and polygon markers. A polygon is a PolygonStart,
// one LineStart..LineEnd per ring (the closing point is not repeated), then a
// PolygonEnd. Sphere delivers the whole sphere and is turned into a polygon by
// the first clipping stage; sinks that cannot represent it ignore it.
type Stream interface {
	Point(x, y float64)
	LineStart()
	LineEnd()
	PolygonStart()
	PolygonEnd()
	Sphere()
}

// pointMStream is implemented by sinks that keep the extra marker argument d3
// clipping passes along with points that lie on the clip edge (the third
// argument of stream.point). Sinks that do not implement it never see it, as
// in d3, where every other stream ignores the argument.
type pointMStream interface {
	pointM(x, y, m float64)
}

// maxGeometryDepth bounds GeometryCollection nesting; deeper collections are
// ignored rather than recursed into.
const maxGeometryDepth = 256

// StreamObject streams a GeoJSON object (Feature, FeatureCollection, a bare
// geometry, or {type: "Sphere"}) into s, as d3.geoStream does. Anything else is
// silently ignored, as upstream does, and so is malformed structure (missing
// coordinates, non-array collections) where upstream would throw.
func StreamObject(object jsval.Value, s Stream) {
	if !object.IsObj() {
		return
	}
	switch typeOf(object) {
	case "Feature":
		streamGeometry(object.Get("geometry"), s, 0)
	case "FeatureCollection":
		for _, f := range object.Get("features").Items() {
			streamGeometry(f.Get("geometry"), s, 0)
		}
	default:
		streamGeometry(object, s, 0)
	}
}

func typeOf(v jsval.Value) string {
	t := v.Get("type")
	if t.IsStr() {
		return t.StrValue()
	}
	if t.IsUndefined() {
		return ""
	}
	return t.AsString()
}

func streamGeometry(g jsval.Value, s Stream, depth int) {
	if !g.IsObj() {
		return
	}
	switch typeOf(g) {
	case "Sphere":
		s.Sphere()
	case "Point":
		streamPoint(g.Get("coordinates"), s)
	case "MultiPoint":
		for _, c := range g.Get("coordinates").Items() {
			streamPoint(c, s)
		}
	case "LineString":
		streamLine(g.Get("coordinates"), s, 0)
	case "MultiLineString":
		for _, l := range g.Get("coordinates").Items() {
			streamLine(l, s, 0)
		}
	case "Polygon":
		streamPolygon(g.Get("coordinates"), s)
	case "MultiPolygon":
		for _, p := range g.Get("coordinates").Items() {
			streamPolygon(p, s)
		}
	case "GeometryCollection":
		if depth >= maxGeometryDepth {
			return
		}
		for _, sub := range g.Get("geometries").Items() {
			streamGeometry(sub, s, depth+1)
		}
	}
}

// coord reads a coordinate as JavaScript arithmetic would: null is 0,
// undefined and non-numeric strings are NaN.
func coord(v jsval.Value) float64 {
	if v.IsNum() {
		return v.NumValue()
	}
	return jsval.ToNumber(v)
}

func streamPoint(c jsval.Value, s Stream) {
	if !c.IsArr() {
		return
	}
	items := c.Items()
	var x, y float64
	switch {
	case len(items) >= 2:
		x, y = coord(items[0]), coord(items[1])
	case len(items) == 1:
		x, y = coord(items[0]), nan
	default:
		x, y = nan, nan
	}
	s.Point(x, y)
}

func streamLine(coords jsval.Value, s Stream, closed int) {
	items := coords.Items()
	n := len(items) - closed
	s.LineStart()
	for i := 0; i < n; i++ {
		streamPoint(items[i], s)
	}
	s.LineEnd()
}

func streamPolygon(coords jsval.Value, s Stream) {
	s.PolygonStart()
	for _, ring := range coords.Items() {
		streamLine(ring, s, 1)
	}
	s.PolygonEnd()
}

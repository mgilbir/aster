package geo

import (
	"strconv"

	"github.com/mgilbir/aster/internal/jsval"
)

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
// geometry, or {type: "Sphere"}) into s, as d3.geoStream does, with its
// JavaScript semantics for an object of any shape: an object whose type
// names no geometry is ignored, a position is read as `p[0], p[1]` of
// whatever value it is (a string's characters, an object's members: NaN
// unless they convert to numbers), a list's length bounds its loop, and a
// member read of null or undefined throws the TypeError it throws upstream,
// raised as a *jsval.Thrown panic, which the dataflow, an expression and a
// render recover as upstream's exception.
func StreamObject(object jsval.Value, s Stream) {
	if object.IsTruthy() {
		switch typeOf(streamMember(object, "type")) {
		case "Feature":
			streamGeometry(streamMember(object, "geometry"), s, 0)
			return
		case "FeatureCollection":
			features := streamMember(object, "features")
			n := streamLength(features)
			for i := 0; float64(i) < n; i++ {
				f := streamAt(features, i)
				streamGeometry(streamMember(f, "geometry"), s, 0)
			}
			return
		}
	}
	streamGeometry(object, s, 0)
}

// streamMember is `v[key]`, with the TypeError of null or undefined.
func streamMember(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		of := "undefined"
		if v.IsNull() {
			of = "null"
		}
		panic(&jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of " + of + " (reading '" + key + "')"})
	}
	return jsval.Member(v, key)
}

// streamAt is `v[i]`: an array's element without making its key, the
// common case, else streamMember.
func streamAt(v jsval.Value, i int) jsval.Value {
	if v.IsArr() {
		return v.Index(i)
	}
	return streamMember(v, strconv.Itoa(i))
}

// streamLength is `v.length` as a loop bound (`++i < n`).
func streamLength(v jsval.Value) float64 {
	if v.IsArr() {
		return float64(v.Len())
	}
	return jsval.ToNumber(streamMember(v, "length"))
}

// typeOf is the key `streamGeometryType.hasOwnProperty(type)` looks up: the
// type's property key, "" for undefined (which names no geometry).
func typeOf(t jsval.Value) string {
	if t.IsStr() {
		return t.StrValue()
	}
	if t.IsUndefined() {
		return ""
	}
	// No geometry type is longer than 32 bytes.
	key, _ := t.PropertyKey(32)
	return key
}

func streamGeometry(g jsval.Value, s Stream, depth int) {
	if !g.IsTruthy() {
		return
	}
	switch typeOf(streamMember(g, "type")) {
	case "Sphere":
		s.Sphere()
	case "Point":
		streamPoint(streamMember(g, "coordinates"), s)
	case "MultiPoint":
		coords := streamMember(g, "coordinates")
		n := streamLength(coords)
		for i := 0; float64(i) < n; i++ {
			streamPoint(streamAt(coords, i), s)
		}
	case "LineString":
		streamLine(streamMember(g, "coordinates"), s, 0)
	case "MultiLineString":
		coords := streamMember(g, "coordinates")
		n := streamLength(coords)
		for i := 0; float64(i) < n; i++ {
			streamLine(streamAt(coords, i), s, 0)
		}
	case "Polygon":
		streamPolygon(streamMember(g, "coordinates"), s)
	case "MultiPolygon":
		coords := streamMember(g, "coordinates")
		n := streamLength(coords)
		for i := 0; float64(i) < n; i++ {
			streamPolygon(streamAt(coords, i), s)
		}
	case "GeometryCollection":
		geoms := streamMember(g, "geometries")
		n := streamLength(geoms)
		if depth >= maxGeometryDepth {
			return
		}
		for i := 0; float64(i) < n; i++ {
			streamGeometry(streamAt(geoms, i), s, depth+1)
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

// streamPoint is `stream.point(c[0], c[1], c[2])`: the coordinates read as
// members of any value (NaN where they are no numbers), the TypeError of
// null or undefined.
func streamPoint(c jsval.Value, s Stream) {
	if items := c.Items(); c.IsArr() {
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
		return
	}
	s.Point(coord(streamMember(c, "0")), coord(streamMember(c, "1")))
}

// streamLine is streamLine(coordinates, stream, closed): `n =
// coordinates.length - closed` points, each `coordinates[i]`.
func streamLine(coords jsval.Value, s Stream, closed int) {
	n := streamLength(coords) - float64(closed)
	s.LineStart()
	for i := 0; float64(i) < n; i++ {
		streamPoint(streamAt(coords, i), s)
	}
	s.LineEnd()
}

func streamPolygon(coords jsval.Value, s Stream) {
	n := streamLength(coords)
	s.PolygonStart()
	for i := 0; float64(i) < n; i++ {
		streamLine(streamAt(coords, i), s, 1)
	}
	s.PolygonEnd()
}

// sphereUnsupported is the Sphere of a stage d3-geo gives no sphere method:
// the planar path sinks and the rectangle clip. A projection's clipping turns
// a sphere into a polygon before them; without one (geoIdentity), upstream's
// stream.sphere() is a TypeError.
func sphereUnsupported() {
	panic(&jsval.Thrown{Name: "TypeError", Msg: "this.stream.sphere is not a function"})
}

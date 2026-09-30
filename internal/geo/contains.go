package geo

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// GeoContains is d3.geoContains: whether the GeoJSON object contains the
// [lon, lat] point (in degrees), by spherical containment for polygons and
// exact proximity for points and lines.
func GeoContains(object jsval.Value, point [2]float64) bool {
	if !object.IsObj() {
		return false
	}
	switch typeOf(object) {
	case "Feature":
		return containsGeometry(object.Get("geometry"), point, 0)
	case "FeatureCollection":
		for _, f := range object.Get("features").Items() {
			if containsGeometry(f.Get("geometry"), point, 0) {
				return true
			}
		}
		return false
	}
	return containsGeometry(object, point, 0)
}

func coordPair(v jsval.Value) [2]float64 {
	return [2]float64{coord(v.Index(0)), coord(v.Index(1))}
}

func containsGeometry(g jsval.Value, point [2]float64, depth int) bool {
	if !g.IsObj() {
		return false
	}
	coords := g.Get("coordinates")
	switch typeOf(g) {
	case "Sphere":
		return true
	case "Point":
		return containsPoint(coordPair(coords), point)
	case "MultiPoint":
		for _, c := range coords.Items() {
			if containsPoint(coordPair(c), point) {
				return true
			}
		}
	case "LineString":
		return containsLine(coords, point)
	case "MultiLineString":
		for _, l := range coords.Items() {
			if containsLine(l, point) {
				return true
			}
		}
	case "Polygon":
		return containsPolygon(coords, point)
	case "MultiPolygon":
		for _, p := range coords.Items() {
			if containsPolygon(p, point) {
				return true
			}
		}
	case "GeometryCollection":
		if depth >= maxGeometryDepth {
			return false
		}
		for _, sub := range g.Get("geometries").Items() {
			if containsGeometry(sub, point, depth+1) {
				return true
			}
		}
	}
	return false
}

func containsPoint(a, b [2]float64) bool { return GeoDistance(a, b) == 0 }

func containsLine(coords jsval.Value, point [2]float64) bool {
	var ao float64
	items := coords.Items()
	for i, c := range items {
		cp := coordPair(c)
		bo := GeoDistance(cp, point)
		if bo == 0 {
			return true
		}
		if i > 0 {
			ab := GeoDistance(cp, coordPair(items[i-1]))
			t := (ao - bo) / ab
			if ab > 0 && ao <= ab && bo <= ab && float64((ao+bo-ab)*(1-float64(t*t))) < epsilon2*ab {
				return true
			}
		}
		ao = bo
	}
	return false
}

func containsPolygon(coords jsval.Value, point [2]float64) bool {
	rings := coords.Items()
	polygon := make([][]cpoint, len(rings))
	for i, r := range rings {
		pts := r.Items()
		ring := make([]cpoint, 0, len(pts))
		for _, p := range pts {
			pp := coordPair(p)
			ring = append(ring, cpoint{pp[0] * radians, pp[1] * radians, 0})
		}
		if len(ring) > 0 {
			ring = ring[:len(ring)-1] // drop the closing point
		}
		polygon[i] = ring
	}
	return polygonContains(polygon, point[0]*radians, point[1]*radians)
}

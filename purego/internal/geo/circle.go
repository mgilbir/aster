package geo

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// maxCirclePoints bounds the ring a circle generator may produce.
const maxCirclePoints = 1 << 22

type ringCollector struct {
	rot    rotateDegrees
	points []jsval.Value
	limit  bool
}

func (r *ringCollector) Point(x, y float64) {
	if len(r.points) >= maxCirclePoints {
		r.limit = true
		return
	}
	lon, lat := r.rot.r.invert(x, y)
	r.points = append(r.points, jsval.ArrOf(jsval.Num(lon*degrees), jsval.Num(lat*degrees)))
}
func (r *ringCollector) LineStart()    {}
func (r *ringCollector) LineEnd()      {}
func (r *ringCollector) PolygonStart() {}
func (r *ringCollector) PolygonEnd()   {}
func (r *ringCollector) Sphere()       {}

// GeoCircle is d3.geoCircle()() for constant parameters: a GeoJSON Polygon
// approximating the circle of angular radius `radius` degrees around
// center [lon, lat], sampled every `precision` degrees. d3's defaults are
// center [0, 0], radius 90, precision 2. It returns ok=false if the parameters
// would generate an unreasonable number of points.
func GeoCircle(center [2]float64, radius, precision float64) (jsval.Value, bool) {
	r := radius * radians
	p := precision * radians
	rc := &ringCollector{rot: rotateDegrees{newRotation(-center[0]*radians, -center[1]*radians, 0)}}
	// The circle is sampled every p radians around 2*pi: refuse absurd counts
	// before generating them.
	if truthy(p) && tau/math.Abs(p) > maxCirclePoints {
		return jsval.Undefined, false
	}
	circleStream(rc, r, p, 1, nil, nil)
	if rc.limit {
		return jsval.Undefined, false
	}
	ring := jsval.Arr(rc.points)
	return jsval.Obj(jsval.ObjectOf(
		"type", jsval.Str("Polygon"),
		"coordinates", jsval.ArrOf(ring),
	)), true
}

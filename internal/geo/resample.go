package geo

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
)

const (
	resampleMaxDepth = 16 // maximum depth of subdivision
)

// cosMinDistance is cos(minimum angular distance): 30 degrees.
var cosMinDistance = jsmath.Cos(30 * radians)

// newResample returns the resampling stage for a projection: adaptive
// subdivision (Visvalingam-style midpoint test) of every projected line segment
// until the projected midpoint is within sqrt(delta2) pixels of the straight
// line, or the segment is short. A delta2 of zero (or NaN) disables
// resampling, as upstream does.
func newResample(pt *projTransform, delta2 float64, sink Stream) Stream {
	if !truthy(delta2) {
		return &resampleNone{pt: pt, sink: sink}
	}
	return &resampler{pt: pt, delta2: delta2, sink: sink}
}

type resampleNone struct {
	pt   *projTransform
	sink Stream
}

func (r *resampleNone) Point(x, y float64) {
	x, y = r.pt.fwd(x, y)
	r.sink.Point(x, y)
}
func (r *resampleNone) LineStart()    { r.sink.LineStart() }
func (r *resampleNone) LineEnd()      { r.sink.LineEnd() }
func (r *resampleNone) PolygonStart() { r.sink.PolygonStart() }
func (r *resampleNone) PolygonEnd()   { r.sink.PolygonEnd() }
func (r *resampleNone) Sphere()       { r.sink.Sphere() }

// sample is a previously emitted point: projected position, longitude, and the
// unit vector of its spherical position.
type sample struct{ x, y, lambda, a, b, c float64 }

const (
	modePoint = iota
	modeLine
	modeRing
)

type resampler struct {
	pt     *projTransform
	delta2 float64
	sink   Stream

	first, prev sample // first point of a ring, previous point

	mode      int  // which point handler is current
	polygon   bool // inside a polygon: lines are rings
	ringEndOn bool // lineEnd is ringEnd
}

func (r *resampler) Point(lambda, phi float64) {
	switch r.mode {
	case modeLine:
		r.linePoint(lambda, phi)
	case modeRing:
		r.first.lambda = lambda
		r.linePoint(lambda, phi)
		r.first = r.prev
		r.mode = modeLine
	default:
		x, y := r.pt.fwd(lambda, phi)
		r.sink.Point(x, y)
	}
}

func (r *resampler) lineStartPlain() {
	r.prev.x = nan
	r.mode = modeLine
	r.sink.LineStart()
}

func (r *resampler) LineStart() {
	if r.polygon {
		r.lineStartPlain()
		r.mode = modeRing
		r.ringEndOn = true
		return
	}
	r.lineStartPlain()
}

func (r *resampler) linePoint(lambda, phi float64) {
	c := cartesian(lambda, phi)
	x, y := r.pt.fwd(lambda, phi)
	p := r.prev
	r.lineTo(p, sample{x, y, lambda, c[0], c[1], c[2]}, resampleMaxDepth)
	r.prev = sample{x, y, lambda, c[0], c[1], c[2]}
	r.sink.Point(x, y)
}

func (r *resampler) lineEndPlain() {
	r.mode = modePoint
	r.sink.LineEnd()
}

func (r *resampler) LineEnd() {
	if r.ringEndOn {
		r.lineTo(r.prev, r.first, resampleMaxDepth)
		r.ringEndOn = false
	}
	r.lineEndPlain()
}

func (r *resampler) PolygonStart() {
	r.sink.PolygonStart()
	r.polygon = true
}

func (r *resampler) PolygonEnd() {
	r.sink.PolygonEnd()
	r.polygon = false
}

func (r *resampler) Sphere() {}

// lineTo subdivides the projected segment s0-s1, emitting the interior points
// (but not s1) in order.
func (r *resampler) lineTo(s0, s1 sample, depth int) {
	dx := s1.x - s0.x
	dy := s1.y - s0.y
	d2 := float64(dx*dx) + float64(dy*dy)
	if !(d2 > float64(4*r.delta2) && depth > 0) {
		return
	}
	depth--
	a := s0.a + s1.a
	b := s0.b + s1.b
	c := s0.c + s1.c
	m := math.Sqrt(float64(a*a) + float64(b*b) + float64(c*c))
	c /= m
	phi2 := asin(c)
	var lambda2 float64
	if abs(abs(c)-1) < epsilon || abs(s0.lambda-s1.lambda) < epsilon {
		lambda2 = (s0.lambda + s1.lambda) / 2
	} else {
		lambda2 = jsmath.Atan2(b, a)
	}
	x2, y2 := r.pt.fwd(lambda2, phi2)
	dx2 := x2 - s0.x
	dy2 := y2 - s0.y
	dz := float64(dy*dx2) - float64(dx*dy2)
	if float64(dz*dz)/d2 > r.delta2 || // perpendicular projected distance
		abs((float64(dx*dx2)+float64(dy*dy2))/d2-0.5) > 0.3 || // midpoint close to an end
		float64(s0.a*s1.a)+float64(s0.b*s1.b)+float64(s0.c*s1.c) < cosMinDistance { // angular distance
		a /= m
		b /= m
		mid := sample{x2, y2, lambda2, a, b, c}
		r.lineTo(s0, mid, depth)
		r.sink.Point(x2, y2)
		r.lineTo(mid, s1, depth)
	}
}

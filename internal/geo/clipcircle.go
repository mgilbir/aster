package geo

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
)

// newClipCircle is small-circle clipping: geometry outside the circle of
// angular radius `radius` around [0°, 0°] (in the rotated frame) is removed.
func newClipCircle(radius float64) *clipper {
	cr := jsmath.Cos(radius)
	delta := 2 * radians
	smallRadius := cr > 0
	notHemisphere := abs(cr) > epsilon

	cc := &clipCircle{radius: radius, cr: cr, smallRadius: smallRadius, notHemisphere: notHemisphere}
	c := &clipper{
		visible: cc.visibleAt,
		line:    cc.clipLine,
		interpolate: func(from, to *cpoint, direction float64, s Stream) {
			circleStream(s, radius, delta, direction, from, to)
		},
	}
	if smallRadius {
		c.startLambda, c.startPhi = 0, -radius
	} else {
		c.startLambda, c.startPhi = -pi, radius-pi
	}
	return c
}

type clipCircle struct {
	radius        float64
	cr            float64
	smallRadius   bool
	notHemisphere bool
}

// visibleAt is cos(lambda)cos(phi) > cos(radius).
func (cc *clipCircle) visibleAt(lambda, phi float64) bool {
	return jsmath.Cos(lambda)*jsmath.Cos(phi) > cc.cr
}

// code is a 4-bit vector representing the location of a point relative to the
// small circle's bounding box.
func (cc *clipCircle) code(lambda, phi float64) int {
	r := cc.radius
	if !cc.smallRadius {
		r = pi - cc.radius
	}
	code := 0
	if lambda < -r {
		code |= 1 // left
	} else if lambda > r {
		code |= 2 // right
	}
	if phi < -r {
		code |= 4 // below
	} else if phi > r {
		code |= 8 // above
	}
	return code
}

// intersect intersects the great circle between a and b with the clip circle.
// With two=false it returns the first intersection point q (ok=false if none,
// or `a` itself for two polar points, as upstream does); with two=true it
// returns both points.
func (cc *clipCircle) intersect(a, b cpoint, two bool) (q, q1 cpoint, ok bool) {
	pa := cartesian(a.x, a.y)
	pb := cartesian(b.x, b.y)

	// We have two planes, n1.p = d1 and n2.p = d2. Find intersection line
	// p(t) = c1 n1 + c2 n2 + t (n1 ⨯ n2).
	n1 := vec3{1, 0, 0} // normal
	n2 := cartesianCross(pa, pb)
	n2n2 := cartesianDot(n2, n2)
	n1n2 := n2[0] // cartesianDot(n1, n2)
	determinant := n2n2 - float64(n1n2*n1n2)

	// Two polar points.
	if determinant == 0 {
		if !two {
			return a, cpoint{}, true
		}
		return cpoint{}, cpoint{}, false
	}

	c1 := float64(cc.cr*n2n2) / determinant
	c2 := float64(-cc.cr*n1n2) / determinant
	n1xn2 := cartesianCross(n1, n2)
	A := cartesianAdd(cartesianScale(n1, c1), cartesianScale(n2, c2))

	// Solve |p(t)|^2 = 1.
	u := n1xn2
	w := cartesianDot(A, u)
	uu := cartesianDot(u, u)
	t2 := float64(w*w) - float64(uu*(cartesianDot(A, A)-1))

	if t2 < 0 {
		return cpoint{}, cpoint{}, false
	}

	t := math.Sqrt(t2)
	qv := cartesianAdd(cartesianScale(u, (-w-t)/uu), A)
	ql, qp := spherical(qv)
	q = cpoint{ql, qp, 0}

	if !two {
		return q, cpoint{}, true
	}

	// Two intersection points.
	lambda0, lambda1 := a.x, b.x
	phi0, phi1 := a.y, b.y

	if lambda1 < lambda0 {
		lambda0, lambda1 = lambda1, lambda0
	}

	delta := lambda1 - lambda0
	polar := abs(delta-pi) < epsilon
	meridian := polar || delta < epsilon

	if !polar && phi1 < phi0 {
		phi0, phi1 = phi1, phi0
	}

	// Check that the first point is between a and b.
	var between bool
	if meridian {
		if polar {
			ref := phi1
			if abs(q.x-lambda0) < epsilon {
				ref = phi0
			}
			between = (phi0+phi1 > 0) != (q.y < ref)
		} else {
			between = phi0 <= q.y && q.y <= phi1
		}
	} else {
		between = (delta > pi) != (lambda0 <= q.x && q.x <= lambda1)
	}
	if between {
		q1v := cartesianAdd(cartesianScale(u, (-w+t)/uu), A)
		l1, p1 := spherical(q1v)
		return q, cpoint{l1, p1, 0}, true
	}
	return cpoint{}, cpoint{}, false
}

type circleLine struct {
	cc     *clipCircle
	stream Stream
	sm     pointMStream // stream, when it wants the edge markers

	point0  cpoint
	has0    bool // point0 is set
	c0      int
	v0      bool
	v00     bool
	isClean int
}

func (cc *clipCircle) clipLine(stream Stream) lineClipper {
	l := &circleLine{cc: cc, stream: stream}
	l.sm, _ = stream.(pointMStream)
	return l
}

func (l *circleLine) emit(x, y, m float64) {
	if l.sm != nil {
		l.sm.pointM(x, y, m)
		return
	}
	l.stream.Point(x, y)
}

func (l *circleLine) LineStart() {
	l.v00, l.v0 = false, false
	l.isClean = 1
}

func (l *circleLine) Point(lambda, phi float64) {
	cc := l.cc
	point1 := cpoint{lambda, phi, 0}
	v := cc.visibleAt(lambda, phi)
	var c int
	if cc.smallRadius {
		if !v {
			c = cc.code(lambda, phi)
		}
	} else if v {
		if lambda < 0 {
			c = cc.code(lambda+pi, phi)
		} else {
			c = cc.code(lambda-pi, phi)
		}
	}
	if !l.has0 {
		l.v0 = v
		l.v00 = v
		if v {
			l.stream.LineStart()
		}
	}
	if v != l.v0 {
		// Only the marker on point1 matters upstream when it is later used as
		// an endpoint of a clipped segment; it never reaches a stream.
		q, _, ok := cc.intersect(l.point0, point1, false)
		if !ok || pointEqual(l.point0.x, l.point0.y, q.x, q.y) || pointEqual(point1.x, point1.y, q.x, q.y) {
			point1.m = 1
		}
	}
	if v != l.v0 {
		l.isClean = 0
		if v {
			// outside going in
			l.stream.LineStart()
			q, _, _ := cc.intersect(point1, l.point0, false)
			l.stream.Point(q.x, q.y)
			l.point0 = q
		} else {
			// inside going out
			q, _, _ := cc.intersect(l.point0, point1, false)
			l.emit(q.x, q.y, 2)
			l.stream.LineEnd()
			l.point0 = q
		}
		l.has0 = true
	} else if cc.notHemisphere && l.has0 && cc.smallRadius != v {
		// If the codes for two points are different, or are both zero, and
		// there this segment intersects with the small circle.
		if l.c0&c == 0 {
			if t0, t1, ok := cc.intersect(point1, l.point0, true); ok {
				l.isClean = 0
				if cc.smallRadius {
					l.stream.LineStart()
					l.stream.Point(t0.x, t0.y)
					l.stream.Point(t1.x, t1.y)
					l.stream.LineEnd()
				} else {
					l.stream.Point(t1.x, t1.y)
					l.stream.LineEnd()
					l.stream.LineStart()
					l.emit(t0.x, t0.y, 3)
				}
			}
		}
	}
	if v && (!l.has0 || !pointEqual(l.point0.x, l.point0.y, point1.x, point1.y)) {
		l.stream.Point(point1.x, point1.y)
	}
	l.point0, l.has0 = point1, true
	l.v0, l.c0 = v, c
}

func (l *circleLine) LineEnd() {
	if l.v0 {
		l.stream.LineEnd()
	}
	l.has0 = false
}

// clean rejoins first and last segments if there were intersections and the
// first and last points were visible.
func (l *circleLine) clean() int {
	c := l.isClean
	if l.v00 && l.v0 {
		c |= 2
	}
	return c
}

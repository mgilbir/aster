package geo

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// hypot3 is Math.hypot for three arguments as V8 computes it: scaled by the
// largest magnitude and summed with Kahan compensation, which is not
// bit-identical to a plain sqrt of the sum of squares.
func hypot3(a, b, c float64) float64 {
	vals := [3]float64{abs(a), abs(b), abs(c)}
	max := 0.0
	nanArg := false
	for _, v := range vals {
		if math.IsNaN(v) {
			nanArg = true
		} else if v > max {
			max = v
		}
	}
	switch {
	case math.IsInf(max, 1):
		return math.Inf(1)
	case nanArg:
		return nan
	case max == 0:
		return 0
	}
	sum, comp := 0.0, 0.0
	for _, v := range vals {
		n := v / max
		summand := float64(n*n) - comp
		prelim := sum + summand
		comp = (prelim - sum) - summand
		sum = prelim
	}
	return math.Sqrt(sum) * max
}

// sphereCentroidStream is d3.geoCentroid's stream: an area-weighted centroid
// for polygons, falling back to length-weighted for lines and the mean for
// points.
type sphereCentroidStream struct {
	W0, W1                 float64
	X0, Y0, Z0, X1, Y1, Z1 float64
	X2, Y2, Z2             adder
	lambda00, phi00        float64
	x0, y0, z0             float64
	inPolygon              bool
	mode                   int
}

func (s *sphereCentroidStream) Sphere() {}

func (s *sphereCentroidStream) pointCartesian(x, y, z float64) {
	s.W0++
	s.X0 += (x - s.X0) / s.W0
	s.Y0 += (y - s.Y0) / s.W0
	s.Z0 += (z - s.Z0) / s.W0
}

func (s *sphereCentroidStream) PolygonStart() { s.inPolygon = true }
func (s *sphereCentroidStream) PolygonEnd()   { s.inPolygon = false }
func (s *sphereCentroidStream) LineStart() {
	if s.inPolygon {
		s.mode = cFirstRing
	} else {
		s.mode = cFirstLine
	}
}
func (s *sphereCentroidStream) LineEnd() {
	if s.inPolygon {
		s.ringPoint(s.lambda00, s.phi00)
		s.mode = cPoint
	} else {
		s.mode = cPoint
	}
}

func (s *sphereCentroidStream) Point(lambda, phi float64) {
	switch s.mode {
	case cPoint:
		lambda = float64(lambda * radians)
		phi = float64(phi * radians)
		cosPhi := jsmath.Cos(phi)
		s.pointCartesian(float64(cosPhi*jsmath.Cos(lambda)), float64(cosPhi*jsmath.Sin(lambda)), jsmath.Sin(phi))
	case cFirstLine:
		lambda = float64(lambda * radians)
		phi = float64(phi * radians)
		cosPhi := jsmath.Cos(phi)
		s.x0 = float64(cosPhi * jsmath.Cos(lambda))
		s.y0 = float64(cosPhi * jsmath.Sin(lambda))
		s.z0 = jsmath.Sin(phi)
		s.mode = cLine
		s.pointCartesian(s.x0, s.y0, s.z0)
	case cLine:
		lambda = float64(lambda * radians)
		phi = float64(phi * radians)
		cosPhi := jsmath.Cos(phi)
		x := float64(cosPhi * jsmath.Cos(lambda))
		y := float64(cosPhi * jsmath.Sin(lambda))
		z := jsmath.Sin(phi)
		w1 := float64(s.y0*z) - float64(s.z0*y)
		w2 := float64(s.z0*x) - float64(s.x0*z)
		w3 := float64(s.x0*y) - float64(s.y0*x)
		w := jsmath.Atan2(math.Sqrt(float64(w1*w1)+float64(w2*w2)+float64(w3*w3)), float64(s.x0*x)+float64(s.y0*y)+float64(s.z0*z))
		s.W1 += w
		s.X1 += float64(w * (s.x0 + x))
		s.x0 = x
		s.Y1 += float64(w * (s.y0 + y))
		s.y0 = y
		s.Z1 += float64(w * (s.z0 + z))
		s.z0 = z
		s.pointCartesian(s.x0, s.y0, s.z0)
	case cFirstRing:
		s.lambda00, s.phi00 = lambda, phi
		lambda = float64(lambda * radians)
		phi = float64(phi * radians)
		s.mode = cRing
		cosPhi := jsmath.Cos(phi)
		s.x0 = float64(cosPhi * jsmath.Cos(lambda))
		s.y0 = float64(cosPhi * jsmath.Sin(lambda))
		s.z0 = jsmath.Sin(phi)
		s.pointCartesian(s.x0, s.y0, s.z0)
	case cRing:
		s.ringPoint(lambda, phi)
	}
}

func (s *sphereCentroidStream) ringPoint(lambda, phi float64) {
	lambda = float64(lambda * radians)
	phi = float64(phi * radians)
	cosPhi := jsmath.Cos(phi)
	x := float64(cosPhi * jsmath.Cos(lambda))
	y := float64(cosPhi * jsmath.Sin(lambda))
	z := jsmath.Sin(phi)
	cx := float64(s.y0*z) - float64(s.z0*y)
	cy := float64(s.z0*x) - float64(s.x0*z)
	cz := float64(s.x0*y) - float64(s.y0*x)
	m := hypot3(cx, cy, cz)
	w := asin(m) // line weight = angle
	v := m       // area weight multiplier
	if truthy(m) {
		v = -w / m
	}
	s.X2.add(float64(v * cx))
	s.Y2.add(float64(v * cy))
	s.Z2.add(float64(v * cz))
	s.W1 += w
	s.X1 += float64(w * (s.x0 + x))
	s.x0 = x
	s.Y1 += float64(w * (s.y0 + y))
	s.y0 = y
	s.Z1 += float64(w * (s.z0 + z))
	s.z0 = z
	s.pointCartesian(s.x0, s.y0, s.z0)
}

// GeoCentroid is d3.geoCentroid: the spherical centroid [lon, lat] in degrees
// (NaN, NaN if undefined).
func GeoCentroid(object jsval.Value) [2]float64 {
	s := &sphereCentroidStream{}
	StreamObject(object, s)

	x, y, z := s.X2.value(), s.Y2.value(), s.Z2.value()
	m := hypot3(x, y, z)

	// If the area-weighted centroid is undefined, fall back to length-weighted.
	if m < epsilon2 {
		x, y, z = s.X1, s.Y1, s.Z1
		// If the feature has zero length, fall back to arithmetic mean of point
		// vectors.
		if s.W1 < epsilon {
			x, y, z = s.X0, s.Y0, s.Z0
		}
		m = hypot3(x, y, z)
		// If the feature still has an undefined centroid, then return.
		if m < epsilon2 {
			return [2]float64{nan, nan}
		}
	}
	return [2]float64{float64(jsmath.Atan2(y, x) * degrees), float64(asin(z/m) * degrees)}
}

// sphereLengthStream is d3.geoLength's stream: great-circle length.
type sphereLengthStream struct {
	sum                       adder
	lambda0, sinPhi0, cosPhi0 float64
	mode                      int // 0 ignore, 1 first, 2 following
}

func (s *sphereLengthStream) Sphere()       {}
func (s *sphereLengthStream) PolygonStart() {}
func (s *sphereLengthStream) PolygonEnd()   {}
func (s *sphereLengthStream) LineStart()    { s.mode = 1 }
func (s *sphereLengthStream) LineEnd()      { s.mode = 0 }
func (s *sphereLengthStream) Point(lambda, phi float64) {
	lambda = float64(lambda * radians)
	phi = float64(phi * radians)
	switch s.mode {
	case 1:
		s.lambda0, s.sinPhi0, s.cosPhi0 = lambda, jsmath.Sin(phi), jsmath.Cos(phi)
		s.mode = 2
	case 2:
		sinPhi, cosPhi := jsmath.Sin(phi), jsmath.Cos(phi)
		delta := abs(lambda - s.lambda0)
		cosDelta, sinDelta := jsmath.Cos(delta), jsmath.Sin(delta)
		x := float64(cosPhi * sinDelta)
		y := float64(s.cosPhi0*sinPhi) - float64(float64(s.sinPhi0*cosPhi)*cosDelta)
		z := float64(s.sinPhi0*sinPhi) + float64(float64(s.cosPhi0*cosPhi)*cosDelta)
		s.sum.add(jsmath.Atan2(math.Sqrt(float64(x*x)+float64(y*y)), z))
		s.lambda0, s.sinPhi0, s.cosPhi0 = lambda, sinPhi, cosPhi
	}
}

// GeoLength is d3.geoLength: the great-circle length of a GeoJSON object in
// radians.
func GeoLength(object jsval.Value) float64 {
	s := &sphereLengthStream{}
	StreamObject(object, s)
	return s.sum.value()
}

// GeoDistance is d3.geoDistance: the great-circle distance between two
// [lon, lat] points in radians.
func GeoDistance(a, b [2]float64) float64 {
	s := &sphereLengthStream{}
	s.LineStart()
	s.Point(a[0], a[1])
	s.Point(b[0], b[1])
	s.LineEnd()
	return s.sum.value()
}

// GeoInterpolate is d3.geoInterpolate: a function mapping t in [0, 1] to the
// point along the great circle from a to b, plus the distance in radians.
func GeoInterpolate(a, b [2]float64) (interpolate func(t float64) [2]float64, distance float64) {
	x0, y0 := float64(a[0]*radians), float64(a[1]*radians)
	x1, y1 := float64(b[0]*radians), float64(b[1]*radians)
	cy0, sy0 := jsmath.Cos(y0), jsmath.Sin(y0)
	cy1, sy1 := jsmath.Cos(y1), jsmath.Sin(y1)
	kx0, ky0 := float64(cy0*jsmath.Cos(x0)), float64(cy0*jsmath.Sin(x0))
	kx1, ky1 := float64(cy1*jsmath.Cos(x1)), float64(cy1*jsmath.Sin(x1))
	d := 2 * asin(math.Sqrt(haversin(y1-y0)+float64(float64(cy0*cy1)*haversin(x1-x0))))
	k := jsmath.Sin(d)
	if truthy(d) {
		return func(t float64) [2]float64 {
			t = float64(t * d)
			B := jsmath.Sin(t) / k
			A := jsmath.Sin(d-t) / k
			x := float64(A*kx0) + float64(B*kx1)
			y := float64(A*ky0) + float64(B*ky1)
			z := float64(A*sy0) + float64(B*sy1)
			return [2]float64{
				float64(jsmath.Atan2(y, x) * degrees),
				float64(jsmath.Atan2(z, math.Sqrt(float64(x*x)+float64(y*y))) * degrees),
			}
		}, d
	}
	return func(float64) [2]float64 { return [2]float64{x0 * degrees, y0 * degrees} }, d
}

// GeoRotation is d3.geoRotation: a rotation of [lon, lat] degrees by
// [lambda, phi, gamma] degrees, with its inverse.
func GeoRotation(rotate []float64) (forward, invert func(lon, lat float64) (float64, float64)) {
	get := func(i int) float64 {
		if i < len(rotate) {
			return rotate[i]
		}
		return nan
	}
	g := 0.0
	if len(rotate) > 2 {
		g = get(2)
	}
	r := newRotateDegrees(get(0), get(1), g)
	return r.forward, r.invert
}

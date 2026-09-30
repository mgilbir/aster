package geo

import (
	"math"
	"sort"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// sphereAreaStream computes spherical polygon area: each ring's signed
// spherical excess (Cagnoli's theorem summed over triangles with the south
// pole), normalised to [0, 4pi) per ring.
type sphereAreaStream struct {
	sum, ringSum     adder
	inPolygon        bool
	mode             int // 0 ignore, 1 first point of ring, 2 following
	lambda00, phi00  float64
	lambda0          float64
	cosPhi0, sinPhi0 float64
}

func (s *sphereAreaStream) LineStart() {
	if s.inPolygon {
		s.mode = 1
	}
}
func (s *sphereAreaStream) LineEnd() {
	if s.inPolygon {
		s.areaPoint(s.lambda00, s.phi00)
	}
}
func (s *sphereAreaStream) PolygonStart() {
	s.ringSum = adder{}
	s.inPolygon = true
}
func (s *sphereAreaStream) PolygonEnd() {
	ring := s.ringSum.value()
	if ring < 0 {
		s.sum.add(tau + ring)
	} else {
		s.sum.add(ring)
	}
	s.inPolygon = false
	s.mode = 0
}
func (s *sphereAreaStream) Sphere() { s.sum.add(tau) }
func (s *sphereAreaStream) Point(lambda, phi float64) {
	switch s.mode {
	case 1:
		s.mode = 2
		s.lambda00, s.phi00 = lambda, phi
		lambda = float64(lambda * radians)
		phi = float64(phi * radians)
		s.lambda0 = lambda
		phi = phi/2 + quarterPi
		s.cosPhi0, s.sinPhi0 = jsmath.Cos(phi), jsmath.Sin(phi)
	case 2:
		s.areaPoint(lambda, phi)
	}
}

func (s *sphereAreaStream) areaPoint(lambda, phi float64) {
	lambda = float64(lambda * radians)
	phi = float64(phi * radians)
	phi = phi/2 + quarterPi // half the angular distance from south pole

	// Spherical excess E for a spherical triangle with vertices: south pole,
	// previous point, current point. Uses a formula derived from Cagnoli's
	// theorem. See Todhunter, Spherical Trig. (1871), Sec. 103, Eq. (2).
	dLambda := lambda - s.lambda0
	sdLambda := 1.0
	if dLambda < 0 {
		sdLambda = -1
	}
	adLambda := sdLambda * dLambda
	cosPhi, sinPhi := jsmath.Cos(phi), jsmath.Sin(phi)
	k := float64(s.sinPhi0 * sinPhi)
	u := float64(s.cosPhi0*cosPhi) + float64(k*jsmath.Cos(adLambda))
	v := float64(float64(k*sdLambda) * jsmath.Sin(adLambda))
	s.ringSum.add(jsmath.Atan2(v, u))

	// Advance the previous points.
	s.lambda0, s.cosPhi0, s.sinPhi0 = lambda, cosPhi, sinPhi
}

// GeoArea is d3.geoArea: the spherical area of a GeoJSON object in steradians.
func GeoArea(object jsval.Value) float64 {
	s := &sphereAreaStream{}
	StreamObject(object, s)
	return s.sum.value() * 2
}

// boundsState is d3.geoBounds' stream.
type boundsState struct {
	lambda0, phi0, lambda1, phi1 float64
	lambda2                      float64 // previous lambda
	lambda00, phi00              float64
	p0                           vec3
	has0                         bool
	deltaSum                     adder
	ranges                       []*[2]float64
	rng                          *[2]float64
	area                         sphereAreaStream

	inPolygon bool
	inLine    bool // point is linePoint
}

func (s *boundsState) Sphere() {
	s.lambda1, s.phi1 = 180, 90
	s.lambda0, s.phi0 = -180, -90
}

func (s *boundsState) Point(lambda, phi float64) {
	switch {
	case s.inPolygon:
		s.ringPoint(lambda, phi)
	case s.inLine:
		s.linePoint(lambda, phi)
	default:
		s.newRange(lambda)
		if phi < s.phi0 {
			s.phi0 = phi
		}
		if phi > s.phi1 {
			s.phi1 = phi
		}
	}
}

func (s *boundsState) newRange(lambda float64) {
	s.lambda0, s.lambda1 = lambda, lambda
	s.rng = &[2]float64{lambda, lambda}
	s.ranges = append(s.ranges, s.rng)
}

func (s *boundsState) LineStart() {
	if s.inPolygon {
		s.area.LineStart()
		return
	}
	s.inLine = true
}

func (s *boundsState) LineEnd() {
	if s.inPolygon {
		s.ringPoint(s.lambda00, s.phi00)
		s.area.LineEnd()
		if abs(s.deltaSum.value()) > epsilon {
			s.lambda1 = 180
			s.lambda0 = -s.lambda1
		}
		s.setRange()
		s.has0 = false
		return
	}
	s.setRange()
	s.inLine = false
	s.has0 = false
}

func (s *boundsState) PolygonStart() {
	s.inPolygon = true
	s.deltaSum = adder{}
	s.area.PolygonStart()
}

func (s *boundsState) PolygonEnd() {
	s.area.PolygonEnd()
	s.inPolygon = false
	ds := s.deltaSum.value()
	switch {
	case s.area.ringSum.value() < 0:
		s.lambda1, s.phi1 = 180, 90
		s.lambda0, s.phi0 = -180, -90
	case ds > epsilon:
		s.phi1 = 90
	case ds < -epsilon:
		s.phi0 = -90
	}
	s.setRange()
}

// setRange records the current longitude extent in the current range. Upstream
// writes through a range that may not exist (a line without points) and throws;
// here that is a no-op.
func (s *boundsState) setRange() {
	if s.rng != nil {
		s.rng[0], s.rng[1] = s.lambda0, s.lambda1
	}
}

// angle is the left-right distance between two longitudes: almost
// (lambda1 - lambda0 + 360) % 360, except that the distance between +-180 is
// 360.
func angle(lambda0, lambda1 float64) float64 {
	lambda1 -= lambda0
	if lambda1 < 0 {
		return lambda1 + 360
	}
	return lambda1
}

func (s *boundsState) ringPoint(lambda, phi float64) {
	if s.has0 {
		delta := lambda - s.lambda2
		if abs(delta) > 180 {
			if delta > 0 {
				delta += 360
			} else {
				delta -= 360
			}
		}
		s.deltaSum.add(delta)
	} else {
		s.lambda00, s.phi00 = lambda, phi
	}
	s.area.Point(lambda, phi)
	s.linePoint(lambda, phi)
}

func (s *boundsState) linePoint(lambda, phi float64) {
	p := cartesian(float64(lambda*radians), float64(phi*radians))
	if s.has0 {
		normal := cartesianCross(s.p0, p)
		equatorial := vec3{normal[1], -normal[0], 0}
		inflection := cartesianCross(equatorial, normal)
		inflection = cartesianNormalize(inflection)
		il, ip := spherical(inflection)
		delta := lambda - s.lambda2
		sgn := -1.0
		if delta > 0 {
			sgn = 1
		}
		lambdai := float64(float64(il*degrees) * sgn)
		antimeridian := abs(delta) > 180
		if antimeridian != (float64(sgn*s.lambda2) < lambdai && lambdai < float64(sgn*lambda)) {
			phii := float64(ip * degrees)
			if phii > s.phi1 {
				s.phi1 = phii
			}
		} else if lambdai = jsMod(lambdai+360, 360) - 180; antimeridian != (float64(sgn*s.lambda2) < lambdai && lambdai < float64(sgn*lambda)) {
			phii := float64(-ip * degrees)
			if phii < s.phi0 {
				s.phi0 = phii
			}
		} else {
			if phi < s.phi0 {
				s.phi0 = phi
			}
			if phi > s.phi1 {
				s.phi1 = phi
			}
		}
		if antimeridian {
			if lambda < s.lambda2 {
				if angle(s.lambda0, lambda) > angle(s.lambda0, s.lambda1) {
					s.lambda1 = lambda
				}
			} else if angle(lambda, s.lambda1) > angle(s.lambda0, s.lambda1) {
				s.lambda0 = lambda
			}
		} else if s.lambda1 >= s.lambda0 {
			if lambda < s.lambda0 {
				s.lambda0 = lambda
			}
			if lambda > s.lambda1 {
				s.lambda1 = lambda
			}
		} else if lambda > s.lambda2 {
			if angle(s.lambda0, lambda) > angle(s.lambda0, s.lambda1) {
				s.lambda1 = lambda
			}
		} else if angle(lambda, s.lambda1) > angle(s.lambda0, s.lambda1) {
			s.lambda0 = lambda
		}
	} else {
		s.newRange(lambda)
	}
	if phi < s.phi0 {
		s.phi0 = phi
	}
	if phi > s.phi1 {
		s.phi1 = phi
	}
	s.p0, s.has0 = p, true
	s.lambda2 = lambda
}

func rangeContains(r *[2]float64, x float64) bool {
	if r[0] <= r[1] {
		return r[0] <= x && x <= r[1]
	}
	return x < r[0] || r[1] < x
}

// GeoBounds is d3.geoBounds: the spherical bounding box
// [[west, south], [east, north]] in degrees; west may exceed east when the box
// crosses the antimeridian.
func GeoBounds(object jsval.Value) [2][2]float64 {
	s := &boundsState{lambda0: math.Inf(1), phi0: math.Inf(1), lambda1: math.Inf(-1), phi1: math.Inf(-1)}
	StreamObject(object, s)

	if n := len(s.ranges); n > 0 {
		// First, sort ranges by their minimum longitudes.
		sort.SliceStable(s.ranges, func(i, j int) bool { return s.ranges[i][0]-s.ranges[j][0] < 0 })

		// Then, merge any ranges that overlap.
		a := s.ranges[0]
		merged := []*[2]float64{a}
		for i := 1; i < n; i++ {
			b := s.ranges[i]
			if rangeContains(a, b[0]) || rangeContains(a, b[1]) {
				if angle(a[0], b[1]) > angle(a[0], a[1]) {
					a[1] = b[1]
				}
				if angle(b[0], a[1]) > angle(a[0], a[1]) {
					a[0] = b[0]
				}
			} else {
				a = b
				merged = append(merged, a)
			}
		}

		// Finally, find the largest gap between the merged ranges. The final
		// bounding box will be the inverse of this gap.
		deltaMax := math.Inf(-1)
		last := len(merged) - 1
		a = merged[last]
		for i := 0; i <= last; i++ {
			b := merged[i]
			if delta := angle(a[1], b[0]); delta > deltaMax {
				deltaMax = delta
				s.lambda0, s.lambda1 = b[0], a[1]
			}
			a = b
		}
	}

	if s.lambda0 == math.Inf(1) || s.phi0 == math.Inf(1) {
		return [2][2]float64{{nan, nan}, {nan, nan}}
	}
	return [2][2]float64{{s.lambda0, s.phi0}, {s.lambda1, s.phi1}}
}

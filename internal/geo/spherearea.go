package geo

import (
	"context"
	"math"
	"sort"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// sphereAreaStream computes spherical polygon area: each ring's signed
// spherical excess (Cagnoli's theorem summed over triangles with the south
// pole), normalised to [0, 4pi) per ring.
type sphereAreaStream struct {
	sum, ringSum adder
	inPolygon    bool
	mode         int // 0 ignore, 1 first point of ring, 2 following
	*areaModule
}

// areaModule is the state d3-geo's area module keeps between calls (module
// variables, not reset by area()): the first point of the last ring and the
// previous point. A ring without points ends with areaPoint(lambda00, phi00),
// so it reads what the last ring left, or undefined (NaN) before any.
type areaModule struct {
	lambda00, phi00  float64
	lambda0          float64
	cosPhi0, sinPhi0 float64
}

func newAreaModule() areaModule {
	return areaModule{lambda00: nan, phi00: nan, lambda0: nan, cosPhi0: nan, sinPhi0: nan}
}

// boundsModule is the bounds module's state kept between calls: the first
// point of the last ring, as given (see boundsState).
type boundsModule struct{ lambda00, phi00 jsval.Value }

// centroidModule is the centroid module's state kept between calls.
type centroidModule struct {
	lambda00, phi00 float64
	x0, y0, z0      float64
}

// MeasureState holds the module-level variables of d3-geo's area, bounds and
// centroid, which upstream shares between every call of a process. One
// belongs to a render: its calls see what the earlier ones left, the first
// sees undefined.
type MeasureState struct {
	area areaModule
	bnd  boundsModule
	cen  centroidModule
}

// NewMeasureState is the state of a fresh process.
func NewMeasureState() *MeasureState {
	return &MeasureState{area: newAreaModule(), bnd: boundsModule{jsval.Undefined, jsval.Undefined}, cen: centroidModule{nan, nan, nan, nan, nan}}
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
// The walk meters its work into the ledger in ctx and stops with ctx's error.
func GeoArea(ctx context.Context, object jsval.Value) (float64, error) {
	return GeoAreaWith(ctx, NewMeasureState(), object)
}

// GeoAreaWith is GeoArea over the module state of a render.
func GeoAreaWith(ctx context.Context, ms *MeasureState, object jsval.Value) (float64, error) {
	s := &sphereAreaStream{areaModule: &ms.area}
	if err := streamBounded(ctx, object, func(b *walkBound) Stream { return meterInput(s, b) }); err != nil {
		return nan, err
	}
	return s.sum.value() * 2, nil
}

// boundsState is d3.geoBounds' stream.
//
// d3's bounds hold the coordinates they are given, not their numbers: a
// longitude or latitude is assigned to the extent and the ranges as it is,
// compared with JavaScript's < (between two strings, by their characters),
// and only computed with as a number. A coordinate that is a string of a
// number ("1e999") so comes back as that string, and an extent of such
// strings is no infinite one (lambda0 === Infinity is false for "1e999"). So
// the values are kept here as given (RawPoint), with the arithmetic on their
// numbers (coord) and the comparisons as JavaScript's (less).
type boundsState struct {
	lambda0, phi0, lambda1, phi1 jsval.Value
	lambda2                      jsval.Value // previous lambda
	*boundsModule
	p0       vec3
	has0     bool
	deltaSum adder
	ranges   []*[2]jsval.Value
	rng      *[2]jsval.Value
	err      error // the exception upstream throws at a line without points
	area     sphereAreaStream
	w        jsval.Walker // String of an array or object compared

	inPolygon bool
	inLine    bool // point is linePoint
}

// primitive is ToPrimitive(v, number), as < takes it.
func (s *boundsState) primitive(v jsval.Value) jsval.Value {
	switch v.Kind() {
	case jsval.KindTimestamp:
		return jsval.Num(v.NumValue())
	case jsval.KindArr, jsval.KindObj, jsval.KindPattern:
		return jsval.Str(s.w.String(v))
	}
	return v
}

// less is a < b, and whether it is defined (false where NaN is compared).
func (s *boundsState) less(a, b jsval.Value) (lt, ok bool) {
	if a.IsNum() && b.IsNum() {
		x, y := a.NumValue(), b.NumValue()
		return x < y, !math.IsNaN(x) && !math.IsNaN(y)
	}
	c, ok := jsval.RelatePrimitives(s.primitive(a), s.primitive(b))
	return ok && c < 0, ok
}

func (s *boundsState) lt(a, b jsval.Value) bool { r, _ := s.less(a, b); return r }
func (s *boundsState) gt(a, b jsval.Value) bool { r, _ := s.less(b, a); return r }
func (s *boundsState) le(a, b jsval.Value) bool { r, ok := s.less(b, a); return ok && !r }
func (s *boundsState) ge(a, b jsval.Value) bool { r, ok := s.less(a, b); return ok && !r }

var (
	num180, numNeg180 = jsval.Num(180), jsval.Num(-180)
	num90, numNeg90   = jsval.Num(90), jsval.Num(-90)
)

func (s *boundsState) Sphere() {
	s.lambda1, s.phi1 = num180, num90
	s.lambda0, s.phi0 = numNeg180, numNeg90
}

func (s *boundsState) Point(lambda, phi float64) { s.RawPoint(jsval.Num(lambda), jsval.Num(phi)) }

// RawPoint is Point with the coordinates as given (rawPointer).
func (s *boundsState) RawPoint(lambda, phi jsval.Value) {
	switch {
	case s.inPolygon:
		s.ringPoint(lambda, phi)
	case s.inLine:
		s.linePoint(lambda, phi)
	default:
		s.newRange(lambda)
		if s.lt(phi, s.phi0) {
			s.phi0 = phi
		}
		if s.gt(phi, s.phi1) {
			s.phi1 = phi
		}
	}
}

func (s *boundsState) newRange(lambda jsval.Value) {
	s.lambda0, s.lambda1 = lambda, lambda
	s.rng = &[2]jsval.Value{lambda, lambda}
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
			s.lambda1 = num180
			s.lambda0 = numNeg180
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
		s.lambda1, s.phi1 = num180, num90
		s.lambda0, s.phi0 = numNeg180, numNeg90
	case ds > epsilon:
		s.phi1 = num90
	case ds < -epsilon:
		s.phi0 = numNeg90
	}
	s.setRange()
}

// setRange records the current longitude extent in the current range. Upstream
// writes through a range that may not exist (a line without points) and
// throws; the error is kept in err and GeoBounds returns it.
func (s *boundsState) setRange() {
	if s.rng != nil {
		s.rng[0], s.rng[1] = s.lambda0, s.lambda1
	} else if s.err == nil {
		// `range[0] = lambda0` with no range: the module-level range is null
		// until a point makes one.
		s.err = &jsval.Thrown{Name: "TypeError", Msg: "Cannot set properties of null (setting '0')"}
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

// angleOf is angle of two longitudes as d3 holds them.
func angleOf(lambda0, lambda1 jsval.Value) float64 { return angle(coord(lambda0), coord(lambda1)) }

func (s *boundsState) ringPoint(lambda, phi jsval.Value) {
	if s.has0 {
		delta := coord(lambda) - coord(s.lambda2)
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
	s.area.Point(coord(lambda), coord(phi))
	s.linePoint(lambda, phi)
}

func (s *boundsState) linePoint(lambda, phi jsval.Value) {
	lam, ph := coord(lambda), coord(phi)
	p := cartesian(float64(lam*radians), float64(ph*radians))
	if s.has0 {
		normal := cartesianCross(s.p0, p)
		equatorial := vec3{normal[1], -normal[0], 0}
		inflection := cartesianCross(equatorial, normal)
		inflection = cartesianNormalize(inflection)
		il, ip := spherical(inflection)
		lam2 := coord(s.lambda2)
		delta := lam - lam2
		sgn := -1.0
		if delta > 0 {
			sgn = 1
		}
		lambdai := float64(float64(il*degrees) * sgn)
		antimeridian := abs(delta) > 180
		if antimeridian != (float64(sgn*lam2) < lambdai && lambdai < float64(sgn*lam)) {
			phii := jsval.Num(float64(ip * degrees))
			if s.gt(phii, s.phi1) {
				s.phi1 = phii
			}
		} else if lambdai = jsMod(lambdai+360, 360) - 180; antimeridian != (float64(sgn*lam2) < lambdai && lambdai < float64(sgn*lam)) {
			phii := jsval.Num(float64(-ip * degrees))
			if s.lt(phii, s.phi0) {
				s.phi0 = phii
			}
		} else {
			if s.lt(phi, s.phi0) {
				s.phi0 = phi
			}
			if s.gt(phi, s.phi1) {
				s.phi1 = phi
			}
		}
		if antimeridian {
			if s.lt(lambda, s.lambda2) {
				if angleOf(s.lambda0, lambda) > angleOf(s.lambda0, s.lambda1) {
					s.lambda1 = lambda
				}
			} else if angleOf(lambda, s.lambda1) > angleOf(s.lambda0, s.lambda1) {
				s.lambda0 = lambda
			}
		} else if s.ge(s.lambda1, s.lambda0) {
			if s.lt(lambda, s.lambda0) {
				s.lambda0 = lambda
			}
			if s.gt(lambda, s.lambda1) {
				s.lambda1 = lambda
			}
		} else if s.gt(lambda, s.lambda2) {
			if angleOf(s.lambda0, lambda) > angleOf(s.lambda0, s.lambda1) {
				s.lambda1 = lambda
			}
		} else if angleOf(lambda, s.lambda1) > angleOf(s.lambda0, s.lambda1) {
			s.lambda0 = lambda
		}
	} else {
		s.newRange(lambda)
	}
	if s.lt(phi, s.phi0) {
		s.phi0 = phi
	}
	if s.gt(phi, s.phi1) {
		s.phi1 = phi
	}
	s.p0, s.has0 = p, true
	s.lambda2 = lambda
}

func (s *boundsState) rangeContains(r *[2]jsval.Value, x jsval.Value) bool {
	if s.le(r[0], r[1]) {
		return s.le(r[0], x) && s.le(x, r[1])
	}
	return s.lt(x, r[0]) || s.lt(r[1], x)
}

// GeoBounds is d3.geoBounds: the spherical bounding box
// [[west, south], [east, north]] in degrees; west may exceed east when the box
// crosses the antimeridian.
//
// The walk meters its work into the ledger in ctx and stops with ctx's error.
func GeoBounds(ctx context.Context, object jsval.Value) ([2][2]float64, error) {
	return GeoBoundsWith(ctx, NewMeasureState(), object)
}

// GeoBoundsWith is GeoBounds over the module state of a render, its values as
// numbers.
func GeoBoundsWith(ctx context.Context, ms *MeasureState, object jsval.Value) ([2][2]float64, error) {
	b, err := GeoBoundsValuesWith(ctx, ms, object)
	return [2][2]float64{{coord(b[0][0]), coord(b[0][1])}, {coord(b[1][0]), coord(b[1][1])}}, err
}

// GeoBoundsValuesWith is GeoBoundsWith with each value as d3 returns it: a
// coordinate as the geometry gives it, which need not be a number
// (boundsState).
func GeoBoundsValuesWith(ctx context.Context, ms *MeasureState, object jsval.Value) ([2][2]jsval.Value, error) {
	nanBox := [2][2]jsval.Value{{jsval.Num(nan), jsval.Num(nan)}, {jsval.Num(nan), jsval.Num(nan)}}
	inf, negInf := jsval.Num(math.Inf(1)), jsval.Num(math.Inf(-1))
	s := &boundsState{boundsModule: &ms.bnd, lambda0: inf, phi0: inf, lambda1: negInf, phi1: negInf, w: jsval.WalkerFor(ctx)}
	s.area.areaModule = &ms.area
	if err := streamBounded(ctx, object, func(b *walkBound) Stream { return meterInput(s, b) }); err != nil {
		return nanBox, err
	}
	if s.err != nil {
		return nanBox, s.err
	}

	if n := len(s.ranges); n > 0 {
		// First, sort ranges by their minimum longitudes.
		sort.SliceStable(s.ranges, func(i, j int) bool { return coord(s.ranges[i][0])-coord(s.ranges[j][0]) < 0 })

		// Then, merge any ranges that overlap.
		a := s.ranges[0]
		merged := []*[2]jsval.Value{a}
		for i := 1; i < n; i++ {
			b := s.ranges[i]
			if s.rangeContains(a, b[0]) || s.rangeContains(a, b[1]) {
				if angleOf(a[0], b[1]) > angleOf(a[0], a[1]) {
					a[1] = b[1]
				}
				if angleOf(b[0], a[1]) > angleOf(a[0], a[1]) {
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
			if delta := angleOf(a[1], b[0]); delta > deltaMax {
				deltaMax = delta
				s.lambda0, s.lambda1 = b[0], a[1]
			}
			a = b
		}
	}

	// lambda0 === Infinity: the number, not a value that reads as it.
	if isInf(s.lambda0) || isInf(s.phi0) {
		return nanBox, nil
	}
	return [2][2]jsval.Value{{s.lambda0, s.phi0}, {s.lambda1, s.phi1}}, nil
}

// isInf is v === Infinity.
func isInf(v jsval.Value) bool { return v.IsNum() && math.IsInf(v.NumValue(), 1) }

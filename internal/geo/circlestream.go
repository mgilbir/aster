package geo

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
)

// maxCircleSteps bounds circleStream's loop whatever its arguments: twice
// the most points GeoCircle lets a circle have, plus slack (see
// circleStream), so it never binds where upstream's loop ends.
const maxCircleSteps = 2*maxCirclePoints + 4

// circleStream generates a circle centred at [0°, 0°] with the given radius,
// sampled every delta radians, as points into s. With from and to nil it is the
// full circle; otherwise the arc between the two points, walked in direction
// (+1 or -1). Clipping uses it to follow the clip circle between two
// intersections.
func circleStream(s Stream, radius, delta, direction float64, from, to *cpoint) {
	if !truthy(delta) {
		return
	}
	cosRadius, sinRadius := jsmath.Cos(radius), jsmath.Sin(radius)
	step := float64(direction * delta)
	var t0, t1 float64
	if from == nil {
		t0 = radius + float64(direction*tau)
		t1 = radius - step/2
	} else {
		t0 = circleRadius(cosRadius, from.x, from.y)
		t1 = circleRadius(cosRadius, to.x, to.y)
		if direction > 0 && t0 < t1 || direction <= 0 && t0 > t1 {
			t0 += float64(direction * tau)
		}
	}
	// d3's loop, `for (t = t0; direction > 0 ? t > t1 : t < t1; t -= step)`,
	// never ends once t is so large that t -= step leaves it unchanged (C3).
	// A step that changes t moves it by at least |step|/2: the double nearest
	// t-step is within half the spacing of doubles there, and if that spacing
	// exceeds |step| the nearest doubles are t itself or at least half a
	// spacing away. So wherever upstream's loop ends, it ends within
	// 2|t0-t1|/|step|+1 steps, and bounding the loop there (ADR 0011) keeps
	// its output while ending the loops that never would. A NaN or infinite
	// count belongs to a loop that either does not start or never ends.
	steps := 0
	if n := math.Ceil(2*abs(t0-t1)/abs(step)) + 2; n < maxCircleSteps {
		steps = int(n)
	} else if !math.IsNaN(n) && !math.IsInf(n, 0) {
		steps = maxCircleSteps
	}
	for t := t0; steps > 0 && (direction > 0 && t > t1 || direction <= 0 && t < t1); t -= step {
		steps--
		lambda, phi := spherical(vec3{cosRadius, float64(-sinRadius * jsmath.Cos(t)), float64(-sinRadius * jsmath.Sin(t))})
		s.Point(lambda, phi)
	}
}

// circleRadius returns the signed angle of a point relative to
// [cosRadius, 0, 0].
func circleRadius(cosRadius, lambda, phi float64) float64 {
	p := cartesian(lambda, phi)
	p[0] -= cosRadius
	p = cartesianNormalize(p)
	radius := acos(-p[1])
	if -p[2] < 0 {
		radius = -radius
	}
	return jsMod(radius+tau-epsilon, tau)
}

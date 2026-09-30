package geo

import "github.com/mgilbir/aster/internal/jsmath"

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
	for t := t0; direction > 0 && t > t1 || direction <= 0 && t < t1; t -= step {
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

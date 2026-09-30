package geo

import "github.com/mgilbir/aster/purego/internal/jsmath"

// cpoint is a point of a clipped line or ring: coordinates plus the marker
// d3's clip circle attaches to points that were moved onto the clip edge
// (point[2] upstream; zero means unmarked).
type cpoint struct{ x, y, m float64 }

func longitude(l float64) float64 {
	if abs(l) <= pi {
		return l
	}
	return sign(l) * (jsMod(abs(l)+pi, tau) - pi)
}

// polygonContains reports whether the point (in radians) lies inside the
// polygon (rings in radians, without the closing point), by winding numbers on
// the sphere. It answers whether the polygon contains the point; clip stages
// use it to decide whether the clip region's start point is inside.
func polygonContains(polygon [][]cpoint, lambda, phi float64) bool {
	lambda = longitude(lambda)
	sinPhi := jsmath.Sin(phi)
	normal := vec3{jsmath.Sin(lambda), -jsmath.Cos(lambda), 0}
	angle := 0.0
	winding := 0
	var sum adder

	if sinPhi == 1 {
		phi = halfPi + epsilon
	} else if sinPhi == -1 {
		phi = -halfPi - epsilon
	}

	for _, ring := range polygon {
		m := len(ring)
		if m == 0 {
			continue
		}
		point0 := ring[m-1]
		lambda0 := longitude(point0.x)
		phi0 := point0.y/2 + quarterPi
		sinPhi0, cosPhi0 := jsmath.Sin(phi0), jsmath.Cos(phi0)

		for j := 0; j < m; j++ {
			point1 := ring[j]
			lambda1 := longitude(point1.x)
			phi1 := point1.y/2 + quarterPi
			sinPhi1, cosPhi1 := jsmath.Sin(phi1), jsmath.Cos(phi1)
			delta := lambda1 - lambda0
			sgn := 1.0
			if delta < 0 {
				sgn = -1
			}
			absDelta := sgn * delta
			antimeridian := absDelta > pi
			k := sinPhi0 * sinPhi1

			sum.add(jsmath.Atan2(float64(float64(k*sgn)*jsmath.Sin(absDelta)), float64(cosPhi0*cosPhi1)+float64(k*jsmath.Cos(absDelta))))
			if antimeridian {
				angle += delta + float64(sgn*tau)
			} else {
				angle += delta
			}

			// Are the longitudes either side of the point's meridian (lambda),
			// and are the latitudes smaller than the parallel (phi)?
			if antimeridian != (lambda0 >= lambda) != (lambda1 >= lambda) {
				arc := cartesianCross(cartesian(point0.x, point0.y), cartesian(point1.x, point1.y))
				arc = cartesianNormalize(arc)
				intersection := cartesianCross(normal, arc)
				intersection = cartesianNormalize(intersection)
				var phiArc float64
				if antimeridian != (delta >= 0) {
					phiArc = -asin(intersection[2])
				} else {
					phiArc = asin(intersection[2])
				}
				if phi > phiArc || phi == phiArc && (arc[0] != 0 || arc[1] != 0) {
					if antimeridian != (delta >= 0) {
						winding++
					} else {
						winding--
					}
				}
			}
			lambda0, sinPhi0, cosPhi0, point0 = lambda1, sinPhi1, cosPhi1, point1
		}
	}

	// The South pole is inside if the polygon winds around it clockwise, or
	// does not cumulatively wind around it but has a negative (counter-
	// clockwise) area; then the parity of crossings between the point and the
	// South pole flips the answer.
	south := angle < -epsilon || angle < epsilon && sum.value() < -epsilon2
	return south != (winding&1 == 1)
}

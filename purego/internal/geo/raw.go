package geo

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

// raw is a raw (unit-sphere) projection: forward maps [lambda, phi] radians to
// planar coordinates, inv (nil when the projection has no inverse) maps back.
type raw struct {
	fwd func(x, y float64) (float64, float64)
	inv func(x, y float64) (float64, float64)
	// mercator is true for the plain Mercator raw projection, which the
	// mercator wrapper clips differently from transverse Mercator.
	mercator bool
}

// jsPow is Math.pow: unlike jsmath.Pow, 1 raised to an infinite or NaN power is
// NaN.
func jsPow(x, y float64) float64 {
	if math.IsNaN(y) || (math.IsInf(y, 0) && abs(x) == 1) {
		return nan
	}
	return jsmath.Pow(x, y)
}

func azimuthalRaw(scale func(cxcy float64) float64) func(x, y float64) (float64, float64) {
	return func(x, y float64) (float64, float64) {
		cx := jsmath.Cos(x)
		cy := jsmath.Cos(y)
		k := scale(cx * cy)
		if math.IsInf(k, 1) {
			return 2, 0
		}
		return float64(float64(k*cy) * jsmath.Sin(x)), float64(k * jsmath.Sin(y))
	}
}

func azimuthalInvert(angle func(z float64) float64) func(x, y float64) (float64, float64) {
	return func(x, y float64) (float64, float64) {
		z := math.Sqrt(float64(x*x) + float64(y*y))
		c := angle(z)
		sc, cc := jsmath.Sin(c), jsmath.Cos(c)
		// `z && y*sc/z`: zero (and NaN) z is passed through.
		s := z
		if truthy(z) {
			s = float64(y*sc) / z
		}
		return jsmath.Atan2(float64(x*sc), float64(z*cc)), asin(s)
	}
}

var (
	azimuthalEqualAreaRaw = &raw{
		fwd: azimuthalRaw(func(cxcy float64) float64 { return math.Sqrt(2 / (1 + cxcy)) }),
		inv: azimuthalInvert(func(z float64) float64 { return 2 * asin(z/2) }),
	}
	azimuthalEquidistantRaw = &raw{
		fwd: azimuthalRaw(func(c float64) float64 {
			c = acos(c)
			if !truthy(c) {
				return c
			}
			return c / jsmath.Sin(c)
		}),
		inv: azimuthalInvert(func(z float64) float64 { return z }),
	}
	gnomonicRaw = &raw{
		fwd: func(x, y float64) (float64, float64) {
			cy := jsmath.Cos(y)
			k := jsmath.Cos(x) * cy
			return float64(cy*jsmath.Sin(x)) / k, jsmath.Sin(y) / k
		},
		inv: azimuthalInvert(jsmath.Atan),
	}
	orthographicRaw = &raw{
		fwd: func(x, y float64) (float64, float64) { return float64(jsmath.Cos(y) * jsmath.Sin(x)), jsmath.Sin(y) },
		inv: azimuthalInvert(asin),
	}
	stereographicRaw = &raw{
		fwd: func(x, y float64) (float64, float64) {
			cy := jsmath.Cos(y)
			k := 1 + float64(jsmath.Cos(x)*cy)
			return float64(cy*jsmath.Sin(x)) / k, jsmath.Sin(y) / k
		},
		inv: azimuthalInvert(func(z float64) float64 { return 2 * jsmath.Atan(z) }),
	}
	equirectangularRaw = &raw{
		fwd: func(lambda, phi float64) (float64, float64) { return lambda, phi },
		inv: func(x, y float64) (float64, float64) { return x, y },
	}
	mercatorRaw = &raw{
		fwd: func(lambda, phi float64) (float64, float64) {
			return lambda, jsmath.Log(jsmath.Tan((halfPi + phi) / 2))
		},
		inv: func(x, y float64) (float64, float64) {
			return x, 2*jsmath.Atan(jsmath.Exp(y)) - halfPi
		},
		mercator: true,
	}
	transverseMercatorRaw = &raw{
		fwd: func(lambda, phi float64) (float64, float64) {
			return jsmath.Log(jsmath.Tan((halfPi + phi) / 2)), -lambda
		},
		inv: func(x, y float64) (float64, float64) {
			return -y, 2*jsmath.Atan(jsmath.Exp(x)) - halfPi
		},
	}
)

func cylindricalEqualAreaRaw(phi0 float64) *raw {
	cosPhi0 := jsmath.Cos(phi0)
	return &raw{
		fwd: func(lambda, phi float64) (float64, float64) {
			return float64(lambda * cosPhi0), jsmath.Sin(phi) / cosPhi0
		},
		inv: func(x, y float64) (float64, float64) { return x / cosPhi0, asin(float64(y * cosPhi0)) },
	}
}

func conicEqualAreaRaw(y0, y1 float64) *raw {
	sy0 := jsmath.Sin(y0)
	n := (sy0 + jsmath.Sin(y1)) / 2

	// Are the parallels symmetrical around the Equator?
	if abs(n) < epsilon {
		return cylindricalEqualAreaRaw(y0)
	}

	c := 1 + float64(sy0*(2*n-sy0))
	r0 := math.Sqrt(c) / n

	return &raw{
		fwd: func(x, y float64) (float64, float64) {
			r := math.Sqrt(c-float64(float64(2*n)*jsmath.Sin(y))) / n
			x = float64(x * n)
			return float64(r * jsmath.Sin(x)), r0 - float64(r*jsmath.Cos(x))
		},
		inv: func(x, y float64) (float64, float64) {
			r0y := r0 - y
			l := jsmath.Atan2(x, abs(r0y)) * sign(r0y)
			if r0y*n < 0 {
				l -= float64(float64(pi*sign(x)) * sign(r0y))
			}
			return l / n, asin((c - float64(float64((float64(x*x)+float64(r0y*r0y))*n)*n)) / (2 * n))
		},
	}
}

func tany(y float64) float64 { return jsmath.Tan((halfPi + y) / 2) }

func conicConformalRaw(y0, y1 float64) *raw {
	cy0 := jsmath.Cos(y0)
	var n float64
	if y0 == y1 {
		n = jsmath.Sin(y0)
	} else {
		n = jsmath.Log(cy0/jsmath.Cos(y1)) / jsmath.Log(tany(y1)/tany(y0))
	}
	f := float64(cy0*jsPow(tany(y0), n)) / n

	if !truthy(n) {
		return mercatorRaw
	}

	return &raw{
		fwd: func(x, y float64) (float64, float64) {
			if f > 0 {
				if y < -halfPi+epsilon {
					y = -halfPi + epsilon
				}
			} else if y > halfPi-epsilon {
				y = halfPi - epsilon
			}
			r := f / jsPow(tany(y), n)
			nx := float64(n * x)
			return float64(r * jsmath.Sin(nx)), f - float64(r*jsmath.Cos(nx))
		},
		inv: func(x, y float64) (float64, float64) {
			fy := f - y
			r := sign(n) * math.Sqrt(float64(x*x)+float64(fy*fy))
			l := jsmath.Atan2(x, abs(fy)) * sign(fy)
			if fy*n < 0 {
				l -= float64(float64(pi*sign(x)) * sign(fy))
			}
			return l / n, 2*jsmath.Atan(jsPow(f/r, 1/n)) - halfPi
		},
	}
}

func conicEquidistantRaw(y0, y1 float64) *raw {
	cy0 := jsmath.Cos(y0)
	var n float64
	if y0 == y1 {
		n = jsmath.Sin(y0)
	} else {
		n = (cy0 - jsmath.Cos(y1)) / (y1 - y0)
	}
	g := cy0/n + y0

	if abs(n) < epsilon {
		return equirectangularRaw
	}

	return &raw{
		fwd: func(x, y float64) (float64, float64) {
			gy := g - y
			nx := float64(n * x)
			return float64(gy * jsmath.Sin(nx)), g - float64(gy*jsmath.Cos(nx))
		},
		inv: func(x, y float64) (float64, float64) {
			gy := g - y
			l := jsmath.Atan2(x, abs(gy)) * sign(gy)
			if gy*n < 0 {
				l -= float64(float64(pi*sign(x)) * sign(gy))
			}
			return l / n, g - float64(sign(n)*math.Sqrt(float64(x*x)+float64(gy*gy)))
		},
	}
}

// The coefficients are typed float64 constants so that products of two of them
// (3*A2, 7*A3, ...) are rounded like the double arithmetic JavaScript does,
// rather than evaluated exactly as untyped constants would be.
const (
	equalEarthA1         float64 = 1.340264
	equalEarthA2         float64 = -0.081106
	equalEarthA3         float64 = 0.000893
	equalEarthA4         float64 = 0.003796
	equalEarthIterations         = 12
)

var equalEarthM = math.Sqrt(3) / 2

// equalEarthPolys evaluates the two polynomials of Equal Earth at l2 = l^2
// (l6 = l2^3): the y polynomial and its derivative.
func equalEarthPolys(l2, l6 float64) (poly, deriv float64) {
	poly = equalEarthA1 + float64(equalEarthA2*l2) + float64(l6*(equalEarthA3+float64(equalEarthA4*l2)))
	deriv = equalEarthA1 + float64(float64(3*equalEarthA2)*l2) +
		float64(l6*(float64(7*equalEarthA3)+float64(float64(9*equalEarthA4)*l2)))
	return poly, deriv
}

var equalEarthRaw = &raw{
	fwd: func(lambda, phi float64) (float64, float64) {
		l := asin(float64(equalEarthM * jsmath.Sin(phi)))
		l2 := float64(l * l)
		l6 := float64(float64(l2*l2) * l2)
		poly, deriv := equalEarthPolys(l2, l6)
		return float64(lambda*jsmath.Cos(l)) / float64(equalEarthM*deriv), float64(l * poly)
	},
	inv: func(x, y float64) (float64, float64) {
		l := y
		l2 := float64(l * l)
		l6 := float64(float64(l2*l2) * l2)
		for i := 0; i < equalEarthIterations; i++ {
			poly, deriv := equalEarthPolys(l2, l6)
			fy := float64(l*poly) - y
			delta := fy / deriv
			l -= delta
			l2 = float64(l * l)
			l6 = float64(float64(l2*l2) * l2)
			if abs(delta) < epsilon2 {
				break
			}
		}
		_, deriv := equalEarthPolys(l2, l6)
		return float64(float64(equalEarthM*x)*deriv) / jsmath.Cos(l), asin(jsmath.Sin(l) / equalEarthM)
	},
}

// Natural Earth coefficients (typed for the same reason as Equal Earth's).
const (
	ne1 float64 = 0.8707
	ne2 float64 = 0.131979
	ne3 float64 = 0.013791
	ne4 float64 = 0.003971
	ne5 float64 = 0.001529
	ny1 float64 = 1.007226
	ny2 float64 = 0.015085
	ny3 float64 = 0.044475
	ny4 float64 = 0.028874
	ny5 float64 = 0.005916
)

func naturalEarth1Y(phi, phi2, phi4 float64) float64 {
	// phi * (1.007226 + phi2*(0.015085 + phi4*(-0.044475 + 0.028874*phi2 - 0.005916*phi4)))
	inner := -ny3 + float64(ny4*phi2) - float64(ny5*phi4)
	return float64(phi * (ny1 + float64(phi2*(ny2+float64(phi4*inner)))))
}

var naturalEarth1Raw = &raw{
	fwd: func(lambda, phi float64) (float64, float64) {
		phi2 := float64(phi * phi)
		phi4 := float64(phi2 * phi2)
		// lambda * (0.8707 - 0.131979*phi2 + phi4*(-0.013791 + phi4*(0.003971*phi2 - 0.001529*phi4)))
		k := ne1 - float64(ne2*phi2) + float64(phi4*(-ne3+float64(phi4*(float64(ne4*phi2)-float64(ne5*phi4)))))
		return float64(lambda * k), naturalEarth1Y(phi, phi2, phi4)
	},
	inv: func(x, y float64) (float64, float64) {
		phi := y
		i := 25
		var phi2 float64
		for {
			phi2 = float64(phi * phi)
			phi4 := float64(phi2 * phi2)
			num := naturalEarth1Y(phi, phi2, phi4) - y
			den := ny1 + float64(phi2*(float64(ny2*3)+float64(phi4*(float64(-ny3*7)+float64(float64(ny4*9)*phi2)-float64(float64(ny5*11)*phi4)))))
			delta := num / den
			phi -= delta
			i--
			if !(abs(delta) > epsilon && i > 0) {
				break
			}
		}
		phi2 = float64(phi * phi)
		// 0.8707 + phi2*(-0.131979 + phi2*(-0.013791 + phi2*phi2*phi2*(0.003971 - 0.001529*phi2)))
		t := ne4 - float64(ne5*phi2)
		u := -ne3 + float64(float64(float64(phi2*phi2)*phi2)*t)
		d := ne1 + float64(phi2*(-ne2+float64(phi2*u)))
		return x / d, phi
	},
}

// mollweideBromleyTheta solves the auxiliary angle of the Mollweide family.
func mollweideBromleyTheta(cp, phi float64) float64 {
	cpsinPhi := float64(cp * jsmath.Sin(phi))
	i := 30
	for {
		delta := (phi + jsmath.Sin(phi) - cpsinPhi) / (1 + jsmath.Cos(phi))
		phi -= delta
		i--
		if !(abs(delta) > epsilon && i > 0) {
			break
		}
	}
	return phi / 2
}

func mollweideBromleyRaw(cx, cy, cp float64) *raw {
	return &raw{
		fwd: func(lambda, phi float64) (float64, float64) {
			phi = mollweideBromleyTheta(cp, phi)
			return cx * lambda * jsmath.Cos(phi), cy * jsmath.Sin(phi)
		},
		inv: func(x, y float64) (float64, float64) {
			y = asin(y / cy)
			return x / (cx * jsmath.Cos(y)), asin((2*y + jsmath.Sin(2*y)) / cp)
		},
	}
}

var mollweideRaw = mollweideBromleyRaw(math.Sqrt2/halfPi, math.Sqrt2, pi)

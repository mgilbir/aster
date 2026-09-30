package geo

import "github.com/mgilbir/aster/purego/internal/jsmath"

// rotation is d3-geo's rotateRadians: a rotation of the sphere by
// deltaLambda about the pole followed (if present) by the phi/gamma rotation.
// It is a small value type so the projection stream can apply it without a
// closure call per point.
type rotation struct {
	hasLambda bool
	dLambda   float64

	hasPhiGamma                                            bool
	cosDeltaPhi, sinDeltaPhi, cosDeltaGamma, sinDeltaGamma float64
}

func newRotation(deltaLambda, deltaPhi, deltaGamma float64) rotation {
	var r rotation
	deltaLambda = jsMod(deltaLambda, tau)
	if truthy(deltaLambda) {
		r.hasLambda = true
		r.dLambda = deltaLambda
	}
	if truthy(deltaPhi) || truthy(deltaGamma) {
		r.hasPhiGamma = true
		r.cosDeltaPhi, r.sinDeltaPhi = jsmath.Cos(deltaPhi), jsmath.Sin(deltaPhi)
		r.cosDeltaGamma, r.sinDeltaGamma = jsmath.Cos(deltaGamma), jsmath.Sin(deltaGamma)
	}
	return r
}

func wrapLambda(lambda float64) float64 {
	if abs(lambda) > pi {
		lambda -= float64(jsRound(lambda/tau) * tau)
	}
	return lambda
}

func (r *rotation) phiGamma(lambda, phi float64) (float64, float64) {
	cosPhi := jsmath.Cos(phi)
	x := jsmath.Cos(lambda) * cosPhi
	y := jsmath.Sin(lambda) * cosPhi
	z := jsmath.Sin(phi)
	k := float64(z*r.cosDeltaPhi) + float64(x*r.sinDeltaPhi)
	return jsmath.Atan2(float64(y*r.cosDeltaGamma)-float64(k*r.sinDeltaGamma), float64(x*r.cosDeltaPhi)-float64(z*r.sinDeltaPhi)),
		asin(float64(k*r.cosDeltaGamma) + float64(y*r.sinDeltaGamma))
}

func (r *rotation) phiGammaInvert(lambda, phi float64) (float64, float64) {
	cosPhi := jsmath.Cos(phi)
	x := jsmath.Cos(lambda) * cosPhi
	y := jsmath.Sin(lambda) * cosPhi
	z := jsmath.Sin(phi)
	k := float64(z*r.cosDeltaGamma) - float64(y*r.sinDeltaGamma)
	return jsmath.Atan2(float64(y*r.cosDeltaGamma)+float64(z*r.sinDeltaGamma), float64(x*r.cosDeltaPhi)+float64(k*r.sinDeltaPhi)),
		asin(float64(k*r.cosDeltaPhi) - float64(x*r.sinDeltaPhi))
}

func (r *rotation) forward(lambda, phi float64) (float64, float64) {
	switch {
	case r.hasLambda:
		lambda = wrapLambda(lambda + r.dLambda)
		if r.hasPhiGamma {
			return r.phiGamma(lambda, phi)
		}
		return lambda, phi
	case r.hasPhiGamma:
		return r.phiGamma(lambda, phi)
	}
	return wrapLambda(lambda), phi
}

func (r *rotation) invert(lambda, phi float64) (float64, float64) {
	switch {
	case r.hasLambda:
		if r.hasPhiGamma {
			// compose.invert applies the second rotation's inverse first.
			lambda, phi = r.phiGammaInvert(lambda, phi)
		}
		return wrapLambda(lambda - r.dLambda), phi
	case r.hasPhiGamma:
		return r.phiGammaInvert(lambda, phi)
	}
	return wrapLambda(lambda), phi
}

// rotateDegrees is d3.geoRotation: a rotation given in degrees operating on
// [lon, lat] degrees.
type rotateDegrees struct{ r rotation }

func newRotateDegrees(lambda, phi, gamma float64) rotateDegrees {
	return rotateDegrees{newRotation(lambda*radians, phi*radians, gamma*radians)}
}

func (d *rotateDegrees) forward(lon, lat float64) (float64, float64) {
	l, p := d.r.forward(float64(lon*radians), float64(lat*radians))
	return float64(l * degrees), float64(p * degrees)
}

func (d *rotateDegrees) invert(lon, lat float64) (float64, float64) {
	l, p := d.r.invert(float64(lon*radians), float64(lat*radians))
	return float64(l * degrees), float64(p * degrees)
}

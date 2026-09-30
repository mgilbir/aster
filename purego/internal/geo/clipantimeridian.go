package geo

import "github.com/mgilbir/aster/purego/internal/jsmath"

// clipAntimeridian cuts geometry along the antimeridian (lambda = ±pi) so that
// no line crosses it; the default pre-clip of every projection.
var clipAntimeridian = &clipper{
	visible:     func(lambda, phi float64) bool { return true },
	line:        newAntimeridianLine,
	interpolate: antimeridianInterpolate,
	startLambda: -pi,
	startPhi:    -halfPi,
}

type antimeridianLine struct {
	stream               Stream
	lambda0, phi0, sign0 float64
	isClean              int
}

func newAntimeridianLine(stream Stream) lineClipper {
	return &antimeridianLine{stream: stream, lambda0: nan, phi0: nan, sign0: nan}
}

func (l *antimeridianLine) LineStart() {
	l.stream.LineStart()
	l.isClean = 1
}

func (l *antimeridianLine) Point(lambda1, phi1 float64) {
	sign1 := -pi
	if lambda1 > 0 {
		sign1 = pi
	}
	delta := abs(lambda1 - l.lambda0)
	if abs(delta-pi) < epsilon { // line crosses a pole
		if (l.phi0+phi1)/2 > 0 {
			l.phi0 = halfPi
		} else {
			l.phi0 = -halfPi
		}
		l.stream.Point(l.lambda0, l.phi0)
		l.stream.Point(l.sign0, l.phi0)
		l.stream.LineEnd()
		l.stream.LineStart()
		l.stream.Point(sign1, l.phi0)
		l.stream.Point(lambda1, l.phi0)
		l.isClean = 0
	} else if l.sign0 != sign1 && delta >= pi { // line crosses antimeridian
		if abs(l.lambda0-l.sign0) < epsilon { // handle degeneracies
			l.lambda0 -= float64(l.sign0 * epsilon)
		}
		if abs(lambda1-sign1) < epsilon {
			lambda1 -= float64(sign1 * epsilon)
		}
		l.phi0 = antimeridianIntersect(l.lambda0, l.phi0, lambda1, phi1)
		l.stream.Point(l.sign0, l.phi0)
		l.stream.LineEnd()
		l.stream.LineStart()
		l.stream.Point(sign1, l.phi0)
		l.isClean = 0
	}
	l.lambda0, l.phi0 = lambda1, phi1
	l.stream.Point(l.lambda0, l.phi0)
	l.sign0 = sign1
}

func (l *antimeridianLine) LineEnd() {
	l.stream.LineEnd()
	l.lambda0, l.phi0 = nan, nan
}

// clean is 2 - clean: if there were intersections, rejoin first and last
// segments.
func (l *antimeridianLine) clean() int { return 2 - l.isClean }

func antimeridianIntersect(lambda0, phi0, lambda1, phi1 float64) float64 {
	sinLambda0Lambda1 := jsmath.Sin(lambda0 - lambda1)
	if abs(sinLambda0Lambda1) > epsilon {
		cosPhi0 := jsmath.Cos(phi0)
		cosPhi1 := jsmath.Cos(phi1)
		return jsmath.Atan((float64(float64(jsmath.Sin(phi0)*cosPhi1)*jsmath.Sin(lambda1)) -
			float64(float64(jsmath.Sin(phi1)*cosPhi0)*jsmath.Sin(lambda0))) /
			float64(float64(cosPhi0*cosPhi1)*sinLambda0Lambda1))
	}
	return (phi0 + phi1) / 2
}

func antimeridianInterpolate(from, to *cpoint, direction float64, stream Stream) {
	switch {
	case from == nil:
		phi := direction * halfPi
		stream.Point(-pi, phi)
		stream.Point(0, phi)
		stream.Point(pi, phi)
		stream.Point(pi, 0)
		stream.Point(pi, -phi)
		stream.Point(0, -phi)
		stream.Point(-pi, -phi)
		stream.Point(-pi, 0)
		stream.Point(-pi, phi)
	case abs(from.x-to.x) > epsilon:
		lambda := -pi
		if from.x < to.x {
			lambda = pi
		}
		phi := float64(direction*lambda) / 2
		stream.Point(-lambda, phi)
		stream.Point(0, phi)
		stream.Point(lambda, phi)
	default:
		stream.Point(to.x, to.y)
	}
}

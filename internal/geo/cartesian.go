package geo

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
)

type vec3 [3]float64

func spherical(c vec3) (lambda, phi float64) {
	return jsmath.Atan2(c[1], c[0]), asin(c[2])
}

func cartesian(lambda, phi float64) vec3 {
	cosPhi := jsmath.Cos(phi)
	return vec3{cosPhi * jsmath.Cos(lambda), cosPhi * jsmath.Sin(lambda), jsmath.Sin(phi)}
}

func cartesianDot(a, b vec3) float64 {
	return float64(a[0]*b[0]) + float64(a[1]*b[1]) + float64(a[2]*b[2])
}

func cartesianCross(a, b vec3) vec3 {
	return vec3{float64(a[1]*b[2]) - float64(a[2]*b[1]), float64(a[2]*b[0]) - float64(a[0]*b[2]), float64(a[0]*b[1]) - float64(a[1]*b[0])}
}

func cartesianAdd(a, b vec3) vec3 { return vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }

func cartesianScale(v vec3, k float64) vec3 { return vec3{v[0] * k, v[1] * k, v[2] * k} }

func cartesianNormalize(d vec3) vec3 {
	l := math.Sqrt(float64(d[0]*d[0]) + float64(d[1]*d[1]) + float64(d[2]*d[2]))
	return vec3{d[0] / l, d[1] / l, d[2] / l}
}

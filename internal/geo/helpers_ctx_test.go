package geo

import (
	"context"

	"github.com/mgilbir/aster/internal/jsval"
)

// bg is the context of the tests that bound nothing.
var bg = context.Background()

// areaOf, boundsOf and centroidOf are the spherical measures, unbounded.
func areaOf(o jsval.Value) float64 {
	a, _ := GeoArea(bg, o)
	return a
}

func boundsOf(o jsval.Value) [2][2]float64 {
	b, _ := GeoBounds(bg, o)
	return b
}

func centroidOf(o jsval.Value) [2]float64 {
	c, _ := GeoCentroid(bg, o)
	return c
}

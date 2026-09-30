// Package contour implements vega-geo's contour-family transforms:
// Contour (marching squares over a value grid or a kernel-density estimate),
// Isocontour (level sets of raster-grid fields), KDE2D (2-D kernel density
// rasters) and Heatmap (raster grid to RGBA pixels).
//
// The marching-squares implementation is vega-geo's own (adapted from
// d3-contour), not d3-contour's: it stitches segments with the same
// fragment tables, smooths crossings with linear interpolation, and assigns
// holes to the first exterior ring that contains them, so ring order and
// vertex order match upstream exactly. Output geometries are GeoJSON
// MultiPolygons; projecting them to paths is done elsewhere.
//
// Rasters produced by KDE2D are float32 in upstream (Float32Array); the
// values here are float64 but always hold exactly-representable float32
// numbers, so thresholds compare identically.
package contour

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// Limits on spec-driven work. They are far above any real chart.
const (
	// MaxGridCells bounds width*height of any raster (input, density or
	// heatmap image).
	MaxGridCells = 1 << 24
	// MaxThresholds bounds the number of contour levels.
	MaxThresholds = 1 << 12
)

var (
	errInvalidSize = errors.New("contour: invalid size")
	errTooLarge    = fmt.Errorf("contour: raster larger than %d cells", MaxGridCells)
)

func checkCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// jsRound is Math.round: halves round toward +Infinity.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

// toInt32 is JavaScript's ToInt32 applied to a double (used by |0 and >>).
func toInt32(x float64) int32 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	x = math.Trunc(x)
	if x >= -2147483648 && x <= 2147483647 {
		return int32(x)
	}
	m := math.Mod(x, 4294967296)
	if m < 0 {
		m += 4294967296
	}
	return int32(uint32(m))
}

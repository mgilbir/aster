package aster

import "github.com/mgilbir/aster/internal/pngopt"

// The PNG post-processing helpers live in internal/pngopt; these names keep
// the package's own call sites and tests short.
var (
	quantizePNG         = pngopt.Quantize
	quantizeOrRecodePNG = pngopt.QuantizeOrRecode
)

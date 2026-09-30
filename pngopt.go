package aster

import "github.com/mgilbir/aster/internal/pngopt"

// The PNG post-processing helpers live in internal/pngopt so both engines
// share them; these names keep the package's own call sites and tests short.
var (
	recodePNG           = pngopt.Recode
	quantizePNG         = pngopt.Quantize
	quantizeOrRecodePNG = pngopt.QuantizeOrRecode
)

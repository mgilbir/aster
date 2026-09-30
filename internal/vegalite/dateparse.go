package vegalite

import (
	"math"

	"github.com/mgilbir/aster/internal/format"
)

// dateParseNaN reports whether JavaScript's Date.parse(s) is NaN. Whether a
// string parses does not depend on the time zone, so UTC is used.
func dateParseNaN(s string) bool { return math.IsNaN(format.ParseDate(s, format.UTC)) }

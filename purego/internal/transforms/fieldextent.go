package transforms

import (
	"context"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// FieldExtent is vega-transforms' extent: the minimum and maximum of the
// field coerced with Number(), skipping null and "" (NaN never compares). ok is
// false when nothing finite was found, or when either bound is infinite;
// upstream then reports an undefined extent.
func FieldExtent(ctx context.Context, data []jsval.Value, f Field) (lo, hi float64, ok bool, err error) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return 0, 0, false, err
		}
		v := f.Apply(t)
		if v.IsNullish() || isEmptyString(v) {
			continue
		}
		x := jsval.ToNumber(v)
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	if math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return 0, 0, false, nil
	}
	return lo, hi, true, nil
}

package transforms

import (
	"context"
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/jsval"
)

// PieParams configures Pie (vega-encode's pie transform).
type PieParams struct {
	// Field sizes the sectors; the null Field gives every tuple 1.
	Field      Field
	StartAngle float64
	// EndAngle nil means 2π (upstream tests `!= null`, so an explicit 0 is kept).
	EndAngle *float64
	// Sort lays sectors out in ascending value order; the tuples themselves are
	// not reordered.
	Sort bool
	// As names the output fields; empty entries default to startAngle/endAngle.
	As [2]string
}

// Pie writes each tuple's sector angles into the tuple and returns data. The
// scale factor is (end-start)/sum(values) where the sum skips null, NaN and
// zero-valued entries (d3.sum); sector sizes use the raw values coerced by
// arithmetic, so a null value has size 0.
func Pie(ctx context.Context, data []jsval.Value, p PieParams) ([]jsval.Value, error) {
	sa, ea := p.As[0], p.As[1]
	if sa == "" {
		sa = "startAngle"
	}
	if ea == "" {
		ea = "endAngle"
	}
	n := len(data)
	values := make([]float64, n)
	sum := 0.0
	for i, t := range data {
		v := jsval.Num(1)
		if !p.Field.IsNil() {
			v = p.Field.Get(t)
		}
		x := jsval.ToNumber(v)
		values[i] = x
		// d3.sum: `if (value = +value) sum += value` skips 0 and NaN.
		if !v.IsNullish() && x != 0 && !math.IsNaN(x) {
			sum += x
		}
	}
	start := p.StartAngle
	stop := 2 * math.Pi
	if p.EndAngle != nil {
		stop = *p.EndAngle
	}
	k := (stop - start) / sum
	index := make([]int, n)
	for i := range index {
		index[i] = i
	}
	if p.Sort {
		pieSortIndex(index, values)
	}
	a := start
	for i := 0; i < n; i++ {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		v := values[index[i]]
		if o := data[index[i]].ObjValue(); o != nil {
			o.Set(sa, jsval.Num(a))
			a += float64(v * k)
			o.Set(ea, jsval.Num(a))
		}
	}
	return data, nil
}

// pieSortIndex sorts index by values[a]-values[b] the way V8's Array.sort
// does. Values can be NaN (a missing field), which makes the comparator
// inconsistent, so the result depends on the algorithm: below 64 elements V8
// finds the leading run and finishes with a binary insertion sort, which is
// reproduced here; longer inputs use an ordinary stable sort (identical
// whenever the comparator is consistent).
func pieSortIndex(index []int, values []float64) {
	n := len(index)
	if n >= 64 {
		slices.SortStableFunc(index, func(a, b int) int {
			d := values[a] - values[b]
			switch {
			case d < 0:
				return -1
			case d > 0:
				return 1
			}
			return 0
		})
		return
	}
	if n < 2 {
		return
	}
	less := func(a, b int) bool { return values[a]-values[b] < 0 }
	// CountAndMakeRun
	run := 2
	desc := less(index[1], index[0])
	for i := 2; i < n; i++ {
		l := less(index[i], index[i-1])
		if desc && !l || !desc && l {
			break
		}
		run++
	}
	if desc {
		slices.Reverse(index[:run])
	}
	// BinaryInsertionSort of the rest.
	for start := run; start < n; start++ {
		left, right := 0, start
		pivot := index[start]
		for left < right {
			mid := left + (right-left)>>1
			if less(pivot, index[mid]) {
				right = mid
			} else {
				left = mid + 1
			}
		}
		copy(index[left+1:start+1], index[left:start])
		index[left] = pivot
	}
}

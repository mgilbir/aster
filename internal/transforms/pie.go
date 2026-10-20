package transforms

import (
	"context"
	"math"

	"github.com/mgilbir/aster/internal/jssort"

	"github.com/mgilbir/aster/internal/jsval"
)

// PieParams configures Pie (vega-encode's pie transform).
type PieParams struct {
	// Field sizes the sectors; the null Field gives every tuple 1.
	Field      Field
	StartAngle float64
	// StartValue, when set (not undefined), is `_.startAngle || 0` as it
	// is: the first sector starts at it and each end is `a += v * k`, which
	// concatenates when it is a string (or an array, an object).
	StartValue jsval.Value
	// EndAngle nil means 2π (upstream tests `!= null`, so an explicit 0 is kept).
	EndAngle *float64
	// Sort lays sectors out in ascending value order; the tuples themselves are
	// not reordered.
	Sort bool
	// As names the output fields; empty entries default to startAngle/endAngle.
	As [2]string
	// AsExact reports that As are the names as the runtime resolved them
	// (`_.as || defaults`, then each entry's text): an empty one is the
	// empty name, not the default.
	AsExact bool
}

// Pie writes each tuple's sector angles into the tuple and returns data. The
// scale factor is (end-start)/sum(values) where the sum skips null, NaN and
// zero-valued entries (d3.sum); sector sizes use the raw values coerced by
// arithmetic, so a null value has size 0.
func Pie(ctx context.Context, data []jsval.Value, p PieParams) ([]jsval.Value, error) {
	sa, ea := p.As[0], p.As[1]
	if sa == "" && !p.AsExact {
		sa = "startAngle"
	}
	if ea == "" && !p.AsExact {
		ea = "endAngle"
	}
	if p.Field.Uncallable { // data.map(field)
		return nil, &jsval.Thrown{Name: "TypeError", Msg: "object is not a function"}
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
	startV := p.StartValue
	generic := !startV.IsUndefined() && !startV.IsNum()
	if !startV.IsUndefined() {
		start = jsval.ToNumber(startV)
	}
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
	w := jsval.WalkerFor(ctx)
	av := startV
	for i := 0; i < n; i++ {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		v := values[index[i]]
		if generic {
			if o := jsval.FieldsFor(data[index[i]], sa); o != nil {
				o.Set(sa, av)
				av = jsAdd(w, av, jsval.Num(v*k))
				o.Set(ea, av)
			}
			continue
		}
		if o := jsval.FieldsFor(data[index[i]], sa); o != nil {
			o.Set(sa, jsval.Num(a))
			a += float64(v * k)
			o.Set(ea, jsval.Num(a))
		}
	}
	return data, nil
}

// pieSortIndex sorts index by values[a]-values[b] as V8's Array.sort does:
// a missing value is NaN, which makes the comparator inconsistent, so the
// order depends on which pairs the algorithm compares (jssort is V8's).
func pieSortIndex(index []int, values []float64) {
	jssort.Sort(index, func(a, b int) int {
		d := values[a] - values[b]
		switch {
		case d < 0:
			return -1
		case d > 0:
			return 1
		}
		return 0 // equal, or NaN, which V8 reads as not less
	})
}

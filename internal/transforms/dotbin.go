package transforms

import (
	"context"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// DotBin is vega-statistics' dotbin: dot density binning for dot plots after
// Wilkinson (1999). sorted must already be ascending; it returns each value's
// stack position (the centre of its stack), optionally smoothed to reduce
// variance.
func DotBin(sorted []float64, step float64, smooth bool) []float64 {
	n := len(sorted)
	v := make([]float64, n)
	if n == 0 {
		return v
	}
	i, j := 0, 1
	a := sorted[0]
	b := a
	w := a + step
	for ; j < n; j++ {
		x := sorted[j]
		if x >= w {
			b = (a + b) / 2
			for ; i < j; i++ {
				v[i] = b
			}
			w = x + step
			a = x
		}
		b = x
	}
	b = (a + b) / 2
	for ; i < j; i++ {
		v[i] = b
	}
	if smooth {
		dotbinSmoothing(v, step+step/4)
	}
	return v
}

// dotbinSmoothing swaps points between "adjacent" stacks, which Wilkinson
// defines as lying within step/4 of each other.
func dotbinSmoothing(v []float64, thresh float64) {
	n := len(v)
	a, b := 0, 1
	// get left stack
	for b < n && v[a] == v[b] {
		b++
	}
	for b < n {
		// get right stack
		c := b + 1
		for c < n && v[b] == v[c] {
			c++
		}
		// are stacks adjacent? if so, compare sizes and swap as needed
		if v[b]-v[b-1] < thresh {
			d := b + ((a + c - b - b) >> 1)
			for d < b && d >= 0 {
				v[d] = v[b]
				d++
			}
			for d > b && d < n {
				v[d] = v[a]
				d--
			}
		}
		// update left stack indices
		a = b
		b = c
	}
}

// DotBinParams configures DotBinTuples.
type DotBinParams struct {
	Field   Field
	GroupBy []Field
	// Step is the stack width; 0 means one thirtieth of the field's extent.
	Step   float64
	Smooth bool
	// As is the output field; empty means "bin".
	As string
}

// DotBinResult reports the range of the assigned bins and the step used.
type DotBinResult struct{ Start, Stop, Step float64 }

// DotBinTuples assigns each tuple its dot-plot bin in place. Within each group
// tuples are ordered by field value (stably; NaN comparisons keep source
// order) before binning. The step defaults to span(extent)/30 over all tuples.
func DotBinTuples(ctx context.Context, data []jsval.Value, p DotBinParams) (DotBinResult, error) {
	as := p.As
	if as == "" {
		as = "bin"
	}
	get := p.Field.Get
	step := p.Step
	if step == 0 || math.IsNaN(step) {
		lo, hi, ok, err := ExtentOf(data, p.Field)
		if err != nil {
			return DotBinResult{}, err
		}
		step = 0
		if ok {
			// span(): (last - first) || 0
			if s := jsval.ToNumber(hi) - jsval.ToNumber(lo); s == s {
				step = s
			}
		}
		step /= 30
	}
	res := DotBinResult{Start: math.Inf(1), Stop: math.Inf(-1), Step: step}
	for _, g := range Partition(data, p.GroupBy) {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if len(g.Tuples) == 0 {
			// dotbin() starts with f(array[0]).
			if err := ReadsUndefined(p.Field); err != nil {
				return res, err
			}
		}
		tuples := append([]jsval.Value(nil), g.Tuples...)
		keys := make(map[*jsval.Object]float64, len(tuples))
		num := func(t jsval.Value) float64 {
			o := t.ObjValue()
			if f, ok := keys[o]; ok {
				return f
			}
			f := jsval.ToNumber(get(t))
			keys[o] = f
			return f
		}
		SortTuples(tuples, StableComparator(func(a, b jsval.Value) int {
			switch d := num(a) - num(b); {
			case d < 0:
				return -1
			case d > 0:
				return 1
			}
			return 0
		}))
		vals := make([]float64, len(tuples))
		for i, t := range tuples {
			vals[i] = num(t)
		}
		for i, v := range DotBin(vals, step, p.Smooth) {
			if v < res.Start {
				res.Start = v
			}
			if v > res.Stop {
				res.Stop = v
			}
			if o := tuples[i].ObjValue(); o != nil {
				o.Set(as, jsval.Num(v))
			}
		}
	}
	return res, nil
}

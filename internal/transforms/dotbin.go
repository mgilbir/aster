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
	Step float64
	// StepValue, when truthy, is `_.step` as the specification gave it,
	// which replaces Step: upstream adds it to the values with JavaScript's
	// + (a string concatenates).
	StepValue jsval.Value
	Smooth    bool
	// As is the output field; empty means "bin".
	As string
}

// DotBinResult reports the range of the assigned bins and the step used
// (StepValue: as upstream keeps it, a value of any type).
type DotBinResult struct {
	Start, Stop, Step float64
	StepValue         jsval.Value
}

// DotBinTuples assigns each tuple its dot-plot bin in place. Within each group
// tuples are ordered by field value (stably; NaN comparisons keep source
// order) before binning. The step defaults to span(extent)/30 over all tuples.
func DotBinTuples(ctx context.Context, data []jsval.Value, p DotBinParams) (DotBinResult, error) {
	as := p.As
	if as == "" {
		as = "bin"
	}
	get := p.Field.Get
	w := jsval.WalkerFor(ctx)
	step := p.Step
	stepV := p.StepValue
	if stepV.IsTruthy() {
		step = jsval.ToNumber(stepV)
	} else if step == 0 || math.IsNaN(step) {
		lo, hi, ok, err := ExtentOf(jsval.WalkerFor(ctx), data, p.Field)
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
	if !stepV.IsTruthy() {
		stepV = jsval.Num(step)
	}
	res := DotBinResult{Start: math.Inf(1), Stop: math.Inf(-1), Step: step, StepValue: stepV}
	groups, err := Partition(keysOf(ctx), data, p.GroupBy)
	if err != nil {
		return res, err
	}
	for _, g := range groups {
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
			o := jsval.Fields(t)
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
		numeric := !addsText(stepV)
		for i, t := range tuples {
			vals[i] = num(t)
			numeric = numeric && !addsText(get(t))
		}
		var bins []float64
		if numeric {
			bins = DotBin(vals, step, p.Smooth)
		} else {
			raw := make([]jsval.Value, len(tuples))
			for i, t := range tuples {
				raw[i] = get(t)
			}
			bins = dotBinJS(w, raw, stepV, p.Smooth)
		}
		for i, v := range bins {
			if v < res.Start {
				res.Start = v
			}
			if v > res.Stop {
				res.Stop = v
			}
			if o := jsval.FieldsFor(tuples[i], as); o != nil {
				o.Set(as, jsval.Num(v))
			}
		}
	}
	return res, nil
}

// dotBinJS is DotBin over the field's values as they are, with a step of any
// type: `a + step`, `(a + b) / 2` and `step + step / 4` are JavaScript's +
// (a string concatenates), `x >= w` its comparison, and the stack positions
// are numbers (a Float64Array).
func dotBinJS(w jsval.Walker, vals []jsval.Value, step jsval.Value, smooth bool) []float64 {
	n := len(vals)
	v := make([]float64, n)
	if n == 0 {
		return v
	}
	i, j := 0, 1
	a := vals[0]
	b := a
	end := jsAdd(w, a, step)
	for ; j < n; j++ {
		x := vals[j]
		if GreaterEq(w, x, end) {
			mid := jsval.ToNumber(jsAdd(w, a, b)) / 2
			for ; i < j; i++ {
				v[i] = mid
			}
			end = jsAdd(w, x, step)
			a = x
		}
		b = x
	}
	mid := jsval.ToNumber(jsAdd(w, a, b)) / 2
	for ; i < j; i++ {
		v[i] = mid
	}
	if smooth {
		dotbinSmoothing(v, jsval.ToNumber(jsAdd(w, step, jsval.Num(jsval.ToNumber(step)/4))))
	}
	return v
}

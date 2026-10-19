package transforms

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// ImputeParams configures Impute.
type ImputeParams struct {
	Field   Field
	Key     Field
	KeyVals []jsval.Value
	GroupBy []Field
	// Method is "value" (default), "mean", "median", "min" or "max".
	Method string
	// Value is the constant for method "value"; undefined means 0.
	Value jsval.Value
}

// imputeReduce is the d3-array reducer of a method over a group's values.
func imputeReduce(w jsval.Walker, method string, vals []jsval.Value) jsval.Value {
	switch method {
	case "mean":
		sum, n := 0.0, 0
		for _, v := range vals {
			if v.IsNullish() {
				continue
			}
			if x := jsval.ToNumber(v); x >= x {
				sum += x
				n++
			}
		}
		if n == 0 {
			return jsval.Undefined
		}
		return jsval.Num(sum / float64(n))
	case "median":
		nums := numbersOf(len(vals), func(i int) jsval.Value { return vals[i] })
		slices.Sort(nums)
		if q, ok := QuantileSorted(nums, 0.5); ok {
			return jsval.Num(q)
		}
		return jsval.Undefined
	case "min", "max":
		best := jsval.Undefined
		for _, v := range vals {
			if v.IsNullish() {
				continue
			}
			if best.IsUndefined() {
				if GreaterEq(w, v, v) {
					best = v
				}
				continue
			}
			if (method == "min" && Greater(w, best, v)) || (method == "max" && Less(w, best, v)) {
				best = v
			}
		}
		return best
	}
	return jsval.Undefined
}

// Impute returns data followed by one new tuple for every (group, key) pair
// that is missing, in group order then key-domain order. The key domain is
// KeyVals followed by the keys observed in data (first-seen order); keys and
// groups are matched by string form. If a key repeats within a group the last
// tuple wins for the "present" test and for the statistics. Imputed tuples
// have fields _impute, the group-by values, the key and the field, in that
// order. Statistics are computed once per group and reused for its gaps.
func Impute(ctx context.Context, data []jsval.Value, p ImputeParams) ([]jsval.Value, error) {
	method := p.Method
	if method == "" {
		method = "value"
	}
	switch method {
	case "value", "mean", "median", "min", "max":
	default:
		return nil, fmt.Errorf("Unrecognized imputation method: %s", method)
	}
	constant := p.Value
	if constant.IsUndefined() {
		constant = jsval.Num(0)
	}

	type group struct {
		vals    []jsval.Value // indexed by key domain position; undefined = missing
		values  []jsval.Value
		present int // number of key positions with a tuple
	}
	keys := keysOf(ctx)
	domain := append([]jsval.Value(nil), p.KeyVals...)
	kMap := make(map[string]int, len(domain))
	for i, k := range domain {
		kMap[keys.String(k)] = i + 1
	}
	var groups []*group
	cells := 0
	gMap := map[string]*group{}
	var sb strings.Builder
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		k := p.Key.Apply(t)
		ks := keys.String(k)
		j := kMap[ks]
		if j == 0 {
			domain = append(domain, k)
			j = len(domain)
			kMap[ks] = j
		}
		sb.Reset()
		for gi, f := range p.GroupBy {
			if gi > 0 {
				sb.WriteByte(',')
			}
			if v := f.Get(t); !v.IsNullish() {
				sb.WriteString(keys.String(v))
			}
		}
		g := gMap[sb.String()]
		if g == nil {
			g = &group{values: dimValues(p.GroupBy, t)}
			gMap[sb.String()] = g
			groups = append(groups, g)
		}
		if grow := j - len(g.vals); grow > 0 {
			// One cell per (group, key) pair exists from here on: pay for
			// them before allocating, or a few thousand distinct groups and
			// keys cost gigabytes.
			cells += grow
			if err := reserveRows(ctx, cells-len(data)); err != nil {
				return nil, err
			}
			for len(g.vals) < j {
				g.vals = append(g.vals, jsval.Undefined)
			}
		}
		if g.vals[j-1].IsUndefined() {
			g.present++
		}
		g.vals[j-1] = t
	}

	missing := 0
	for _, g := range groups {
		missing += len(domain) - g.present
	}
	if err := reserveRows(ctx, missing); err != nil {
		return nil, err
	}

	out := data[:len(data):len(data)]
	for _, g := range groups {
		value := jsval.Num(math.NaN())
		for j, kVal := range domain {
			if j < len(g.vals) && !g.vals[j].IsUndefined() {
				continue
			}
			if err := poll(ctx, len(out)); err != nil {
				return nil, err
			}
			t := jsval.NewObject(len(p.GroupBy) + 3)
			t.Set("_impute", jsval.True)
			for i, gv := range g.values {
				t.Set(p.GroupBy[i].Key(), gv)
			}
			t.Set(p.Key.Key(), kVal)
			if value.IsNum() && math.IsNaN(value.NumValue()) {
				if method == "value" {
					value = constant
				} else {
					present := make([]jsval.Value, 0, len(g.vals))
					for _, tv := range g.vals {
						if !tv.IsUndefined() {
							present = append(present, p.Field.Get(tv))
						}
					}
					value = imputeReduce(keys.w, method, present)
				}
			}
			t.Set(p.Field.Key(), value)
			out = append(out, jsval.Obj(t))
		}
	}
	return out, nil
}

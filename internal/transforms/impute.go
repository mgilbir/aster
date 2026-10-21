package transforms

import (
	"context"
	"errors"
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
	// MethodNotStrictValue: the method named "value" without being the string
	// (["value"]). getValue then returns Methods["value"], the string, not
	// the constant function, and calling it is a TypeError.
	MethodNotStrictValue bool
	// ProtoMember, with Method "proto", is the Object.prototype member the
	// method named: Methods[m] finds it on the plain object, so it is neither
	// unrecognized nor the constant, and is called as impute(group, field).
	ProtoMember string
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
		// d3-array's numbers: an empty string is 0 here, where
		// vega-statistics' numbers skips it
		nums := make([]float64, 0, len(vals))
		for _, v := range vals {
			if v.IsNullish() {
				continue
			}
			if x := jsval.ToNumber(v); x >= x {
				nums = append(nums, x)
			}
		}
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
//
// Upstream keeps the key positions and the groups in plain objects keyed by
// their text (`kMap[k] || ...`, `gMap[gKey]`), and Impute reproduces what
// that does with a name Object.prototype has: a key so named finds that
// member, no position, and its tuple is in no cell; a group so named finds
// the member and writes its tuples into it, so it is never imputed, and for
// "__proto__" the member is Object.prototype itself, whose index properties
// every other group's empty cells then read.
func Impute(ctx context.Context, data []jsval.Value, p ImputeParams) ([]jsval.Value, error) {
	method := p.Method
	if method == "" {
		method = "value"
	}
	switch method {
	case "value", "mean", "median", "min", "max":
	case "proto":
		if p.ProtoMember == "" {
			return nil, fmt.Errorf("Unrecognized imputation method: %s", method)
		}
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
		// kMap is a plain object: assigning "__proto__" a number sets no
		// property, so a read of it keeps finding Object.prototype.
		if ks := keys.String(k); ks != ProtoKey {
			kMap[ks] = i + 1
		}
	}
	// proto holds what the tuples of a group named "__proto__" wrote onto
	// Object.prototype, by key position: the value an empty cell reads.
	var proto map[int]jsval.Value
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
		j, own := kMap[ks]
		if !own && !jsval.ObjectPrototypeHas(ks) {
			domain = append(domain, k)
			j = len(domain)
			kMap[ks] = j
		}
		// j is 0 for a key Object.prototype has: `group[NaN] = t` below
		// puts the tuple in no cell
		sb.Reset()
		for gi, f := range p.GroupBy {
			if gi > 0 {
				sb.WriteByte(',')
			}
			if v := f.Get(t); !v.IsNullish() {
				sb.WriteString(keys.String(v))
			}
		}
		gKey := sb.String()
		g := gMap[gKey]
		if g == nil {
			if jsval.ObjectPrototypeHas(gKey) {
				if gKey == "__proto__" && j > 0 {
					if proto == nil {
						proto = map[int]jsval.Value{}
					}
					proto[j-1] = t
				}
				continue
			}
			g = &group{values: dimValues(p.GroupBy, t)}
			gMap[gKey] = g
			groups = append(groups, g)
		}
		if j == 0 {
			continue
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
	// cell is group[j]: its own element, or for a hole what Object.prototype
	// holds at that index.
	cell := func(g *group, j int) jsval.Value {
		if j < len(g.vals) && !g.vals[j].IsUndefined() {
			return g.vals[j]
		}
		return proto[j]
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
			if !cell(g, j).IsNullish() {
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
					if p.MethodNotStrictValue {
						return nil, errors.New("TypeError: impute is not a function")
					}
					value = constant
				} else if method == "proto" {
					switch p.ProtoMember {
					case "constructor": // Object(group) is the group itself
						value = jsval.Arr(slices.Clone(g.vals))
					case "toString": // called without a this
						value = jsval.Str("[object Undefined]")
					case "__proto__": // Object.prototype is no function
						return nil, errors.New("TypeError: impute is not a function")
					default: // ToObject(undefined)
						return nil, errors.New("TypeError: Cannot convert undefined or null to object")
					}
				} else {
					// d3's reducers iterate the group as an array, so its
					// holes read through to Object.prototype too
					present := make([]jsval.Value, 0, len(g.vals))
					for j := range g.vals {
						if tv := cell(g, j); !tv.IsUndefined() {
							if p.Field.IsNil() { // getField's `f(t)` with f null
								panic(&jsval.Thrown{Name: "TypeError", Msg: "f is not a function"})
							}
							present = append(present, p.Field.Get(tv))
						}
					}
					value = imputeReduce(keys.w, method, present)
				}
			}
			// accessorName(null) is null, a property named "null"
			fieldKey := p.Field.Key()
			if p.Field.IsNil() {
				fieldKey = "null"
			}
			t.Set(fieldKey, value)
			out = append(out, jsval.Obj(t))
		}
	}
	return out, nil
}

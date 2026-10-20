package transforms

import (
	"context"
	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jssort"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// PivotParams configures Pivot.
type PivotParams struct {
	GroupBy []Field
	// Field supplies the pivot values, which become output column names.
	Field Field
	// Value supplies the values aggregated into each column.
	Value Field
	// Op is the aggregate op (default "sum"); the runtime passes upstream's
	// internal __count__ for the string "count", as Pivot.js does.
	Op string
	// Limit, when not nil, is upstream's truthy `limit` as a number: the
	// sorted columns are cut to list.slice(0, limit), JavaScript's slice
	// (a fraction truncated, a negative one counted from the end, NaN 0).
	Limit *float64
	// Key optionally overrides the group cell key, as in Aggregate.
	Key  Field
	Rand Rand
}

// Pivot is an aggregate whose measures are generated from the data: one column
// per distinct Field value (sorted with Ascending, undefined last, optionally
// limited), each aggregating Value over the tuples whose Field equals it.
// Column names are the string form of the pivot value. Tuples outside a column
// contribute NaN, which the aggregator ignores, so a column with no matching
// tuples is undefined (0 for count).
func Pivot(ctx context.Context, data []jsval.Value, p PivotParams) ([]jsval.Value, error) {
	// `(_.op === 'count' ? '__count__' : _.op) || 'sum'`: the caller maps
	// the string "count" to __count__, the missing and valid values of the
	// column, which leaves out a NaN.
	op := p.Op
	if op == "" {
		op = "sum"
	}
	if _, ok := opKinds[op]; !ok {
		return nil, errUnknownOp(op)
	}
	kf := keysOf(ctx)
	// The keys are collected in a plain object (`if (!map[k])`), keyed by
	// String(k): a value that names a member of Object.prototype finds it
	// there and makes no column.
	seen := map[string]bool{}
	var keys []jsval.Value
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		k := p.Field.Apply(t)
		// pivotKeys tests `!map[k]` on a plain object: a name every object
		// inherits finds that member, truthy, and is never collected.
		if s := kf.String(k); !seen[s] && !jsval.ObjectPrototypeHas(s) {
			seen[s] = true
			keys = append(keys, k)
		}
	}
	// Array.prototype.sort puts undefined last without consulting the comparator.
	defined := keys[:0:0]
	undefs := 0
	for _, k := range keys {
		if k.IsUndefined() {
			undefs++
		} else {
			defined = append(defined, k)
		}
	}
	jssort.Sort(defined, func(a, b jsval.Value) int { return Ascending(kf.w, a, b) })
	keys = defined
	for ; undefs > 0; undefs-- {
		keys = append(keys, jsval.Undefined)
	}
	if p.Limit != nil {
		keys = keys[:jsSliceEnd(*p.Limit, len(keys))]
	}

	// Every tuple is offered to every column: rows x columns accessor calls.
	if work := budget.Mul(int64(len(data)), int64(len(keys))); work > MaxPivotWork {
		return nil, limitErr("pivot work (rows x columns)", float64(work), MaxPivotWork)
	}
	measures := make([]Measure, len(keys))
	for i, k := range keys {
		k := k
		name := kf.String(k)
		get := func(t jsval.Value) jsval.Value {
			if jsval.SameRef(p.Field.Apply(t), k) {
				return p.Value.Apply(t)
			}
			return jsval.Num(math.NaN())
		}
		measures[i] = Measure{Op: op, Field: NamedField(name, nil, get), As: name, Param: math.NaN()}
	}
	out, err := Aggregate(ctx, data, AggregateParams{GroupBy: p.GroupBy, Measures: measures, Key: p.Key, Rand: p.Rand})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// jsSliceEnd is the end Array.prototype.slice(0, end) cuts a list of n at:
// end truncated toward zero (NaN is 0), counted from the list's end when
// negative, and clamped to [0, n].
func jsSliceEnd(end float64, n int) int {
	switch {
	case math.IsNaN(end):
		return 0
	case end < 0:
		end = math.Ceil(end) + float64(n)
		if end < 0 {
			return 0
		}
	default:
		end = math.Floor(end)
	}
	if end > float64(n) {
		return n
	}
	return int(end)
}

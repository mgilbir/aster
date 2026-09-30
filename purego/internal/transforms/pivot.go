package transforms

import (
	"context"
	"github.com/mgilbir/aster/purego/internal/jssort"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// PivotParams configures Pivot.
type PivotParams struct {
	GroupBy []Field
	// Field supplies the pivot values, which become output column names.
	Field Field
	// Value supplies the values aggregated into each column.
	Value Field
	// Op is the aggregate op (default "sum").
	Op string
	// Limit, when positive, keeps only the first Limit columns in sorted order.
	Limit int
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
	op := p.Op
	if op == "" {
		op = "sum"
	}
	if !IsAggregateOp(op) {
		return nil, errUnknownOp(op)
	}
	seen := map[string]bool{}
	var keys []jsval.Value
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		k := p.Field.Apply(t)
		if s := k.AsString(); !seen[s] {
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
	jssort.Sort(defined, Ascending)
	keys = defined
	for ; undefs > 0; undefs-- {
		keys = append(keys, jsval.Undefined)
	}
	if p.Limit > 0 && len(keys) > p.Limit {
		keys = keys[:p.Limit]
	}

	measures := make([]Measure, len(keys))
	for i, k := range keys {
		k := k
		name := k.AsString()
		var get Accessor
		mop := op
		if op == "count" {
			// upstream swaps in the internal missing+valid counter, which
			// counts every tuple of the column, null values included; `valid`
			// over a constant 1 does the same here.
			mop = "valid"
			get = func(t jsval.Value) jsval.Value {
				if jsval.SameRef(p.Field.Apply(t), k) {
					return jsval.Num(1)
				}
				return jsval.Num(math.NaN())
			}
		} else {
			get = func(t jsval.Value) jsval.Value {
				if jsval.SameRef(p.Field.Apply(t), k) {
					return p.Value.Apply(t)
				}
				return jsval.Num(math.NaN())
			}
		}
		measures[i] = Measure{Op: mop, Field: NamedField(name, nil, get), As: name}
	}
	out, err := Aggregate(ctx, data, AggregateParams{GroupBy: p.GroupBy, Measures: measures, Key: p.Key, Rand: p.Rand})
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		// An empty measure list is not "count" upstream: cells carry only the
		// group-by fields. Aggregate substitutes a count, so drop it again.
		for _, t := range out {
			t.ObjValue().Delete("count")
		}
	}
	return out, nil
}

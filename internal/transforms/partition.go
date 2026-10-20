package transforms

import (
	"context"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// Group is a partition of tuples that share group-by values.
type Group struct {
	// Dims are the group-by values of the group (nil for the single group of an
	// ungrouped partition).
	Dims   []jsval.Value
	Tuples []jsval.Value
}

// Partition splits data by the group-by fields, groups in order of first
// appearance, as vega-transforms' partition() does. Groups are identified by
// the comma-joined string form of the dimension values (null and undefined
// join as empty, like Array.prototype.toString, and keys as k makes them), so
// 1 and "1" are one group.
// With no group-by fields everything is one group with nil Dims, even when data
// is empty.
func Partition(k Keys, data []jsval.Value, groupby []Field) []Group {
	if len(groupby) == 0 {
		return []Group{{Tuples: data}}
	}
	var groups []Group
	index := make(map[string]int)
	var sb strings.Builder
	for i, t := range data {
		// A batch of rows is charged at a time, a step a field of each,
		// polling the call's context (ADR 0010): a field can be an
		// expression.
		if i&(partitionBatch-1) == 0 {
			k.w.Work(int64(min(partitionBatch, len(data)-i) * (len(groupby) + 1)))
		}
		sb.Reset()
		for i, f := range groupby {
			if i > 0 {
				sb.WriteByte(',')
			}
			if v := f.Get(t); !v.IsNullish() {
				sb.WriteString(k.String(v))
			}
		}
		k := sb.String()
		gi, ok := index[k]
		if !ok {
			if jsval.ObjectPrototypeHas(k) {
				// util.partition's `map[k]` of a plain object finds the
				// member Object.prototype has under that name, which is no
				// array: `g.push(t)` is a TypeError.
				panic(&jsval.Thrown{Name: "TypeError", Msg: "g.push is not a function"})
			}
			gi = len(groups)
			index[k] = gi
			dims := make([]jsval.Value, len(groupby))
			for i, f := range groupby {
				dims[i] = f.Get(t)
			}
			groups = append(groups, Group{Dims: dims})
		}
		groups[gi].Tuples = append(groups[gi].Tuples, t)
	}
	return groups
}

// partitionBatch is how many rows Partition and GroupByKey go between two
// charges of their work.
const partitionBatch = 256

// GroupByKey partitions data by a key function, groups in order of first
// appearance. It is the partitioning the facet transform needs: one subflow per
// distinct key. Each row is a step of work charged to ctx's ledger, a batch
// at a time, which polls ctx: the key can be an expression.
func GroupByKey(ctx context.Context, data []jsval.Value, key KeyFunc) ([]KeyedGroup, error) {
	return groupByKey(ctx, data, key, false)
}

// GroupByKeyFacet is GroupByKey as Facet applies it to a plain object that
// maps keys to subflows: the key "__proto__" names no property of that object
// (`flows['__proto__'] = sf` sets its prototype, and hasOwnProperty never
// finds it), so every tuple with that key opens a subflow of its own, in the
// place its tuple comes in.
func GroupByKeyFacet(ctx context.Context, data []jsval.Value, key KeyFunc) ([]KeyedGroup, error) {
	return groupByKey(ctx, data, key, true)
}

func groupByKey(ctx context.Context, data []jsval.Value, key KeyFunc, soloProto bool) ([]KeyedGroup, error) {
	var groups []KeyedGroup
	index := make(map[string]int)
	m := budget.NewMeter(ctx)
	for i, t := range data {
		if i&(partitionBatch-1) == 0 {
			if err := m.Work(int64(min(partitionBatch, len(data)-i))); err != nil {
				return nil, err
			}
		}
		k := key(t)
		if soloProto && k == "__proto__" {
			groups = append(groups, KeyedGroup{Key: k, Tuples: []jsval.Value{t}})
			continue
		}
		gi, ok := index[k]
		if !ok {
			gi = len(groups)
			index[k] = gi
			groups = append(groups, KeyedGroup{Key: k})
		}
		groups[gi].Tuples = append(groups[gi].Tuples, t)
	}
	return groups, nil
}

// KeyedGroup is a set of tuples sharing one key.
type KeyedGroup struct {
	Key    string
	Tuples []jsval.Value
}

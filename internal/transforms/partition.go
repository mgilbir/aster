package transforms

import (
	"strings"

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
// join as empty, like Array.prototype.toString), so 1 and "1" are one group.
// With no group-by fields everything is one group with nil Dims, even when data
// is empty.
func Partition(data []jsval.Value, groupby []Field) []Group {
	if len(groupby) == 0 {
		return []Group{{Tuples: data}}
	}
	var groups []Group
	index := make(map[string]int)
	var sb strings.Builder
	for _, t := range data {
		sb.Reset()
		for i, f := range groupby {
			if i > 0 {
				sb.WriteByte(',')
			}
			if v := f.Get(t); !v.IsNullish() {
				sb.WriteString(v.AsString())
			}
		}
		k := sb.String()
		gi, ok := index[k]
		if !ok {
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

// GroupByKey partitions data by a key function, groups in order of first
// appearance. It is the partitioning the facet transform needs: one subflow per
// distinct key.
func GroupByKey(data []jsval.Value, key KeyFunc) []KeyedGroup {
	var groups []KeyedGroup
	index := make(map[string]int)
	for _, t := range data {
		k := key(t)
		gi, ok := index[k]
		if !ok {
			gi = len(groups)
			index[k] = gi
			groups = append(groups, KeyedGroup{Key: k})
		}
		groups[gi].Tuples = append(groups[gi].Tuples, t)
	}
	return groups
}

// KeyedGroup is a set of tuples sharing one key.
type KeyedGroup struct {
	Key    string
	Tuples []jsval.Value
}

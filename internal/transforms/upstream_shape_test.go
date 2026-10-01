package transforms

import (
	"cmp"
	"context"
	"math"
	"slices"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// numbers reads a recorded array (or Set) of plain numbers.
func numbers(v any) ([]float64, bool) {
	if m, ok := v.(map[string]any); ok && m["$"] == "set" {
		v = m["values"]
	}
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]float64, len(items))
	for i, it := range items {
		x, isNum := it.(float64)
		if !isNum {
			return nil, false
		}
		out[i] = x
	}
	return out, true
}

// TestUpstreamD3ShapePie replays d3-shape's own pie tests (see internal/upstream) through the engine's
// Pie transform, on tuples that hold one value each. Vega's pie lays the sectors out in input order,
// or in ascending order of value, where d3's sorts by a comparator and, by default, in descending
// order; so the adapter orders the tuples as d3 does and has Pie (unsorted) place the angles, except
// for an ascending sort, which Pie does itself. The recording keeps a comparator only as an anonymous
// function: the tests set a - b first, then b - a, so the nth sortValues of a chain is read as one or
// the other by its parity. d3 also restricts the sweep to a turn, skips a negative value when it sums,
// guards a zero sum and has a pad angle, none of which Vega's pie does: the vectors that exercise
// them are not replayed (the answer for those is Vega's own, not d3's). Accessor functions are not
// in the recording either.
func TestUpstreamD3ShapePie(t *testing.T) {
	r := upstream.Start(t, "d3-shape")
	ctx := context.Background()
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "pie()" {
			continue
		}
		if c.Method != "" || len(c.ViaSteps()) != 0 || len(c.Args) != 1 {
			r.Skip("pie questions of another shape (accessor reads and calls with no data)")
			continue
		}
		values, ok := numbers(c.Args[0])
		if !ok {
			r.Skip("data that is not an array of numbers (strings, objects with valueOf)")
			continue
		}
		start, end := 0.0, 2*math.Pi
		ascending, sortValues := false, 0
		configured := true
		for _, step := range c.ChainSteps() {
			switch {
			case step.Method == "startAngle" && len(step.Args) == 1 && !upstream.Contains(step.Args, "function"):
				start = upstream.Number(step.Args[0])
			case step.Method == "endAngle" && len(step.Args) == 1 && !upstream.Contains(step.Args, "function"):
				end = upstream.Number(step.Args[0])
			case step.Method == "sortValues" && len(step.Args) == 1:
				sortValues++
				ascending = sortValues%2 == 1
			default:
				configured = false
			}
		}
		if !configured {
			r.Skip("pie configuration Vega's pie does not have (padAngle, value and sort accessors)")
			continue
		}
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		if sum <= 0 || math.IsNaN(sum) || slices.ContainsFunc(values, func(v float64) bool { return v < 0 }) {
			r.Skip("values d3 reads differently (a negative is skipped, a zero sum gives no angle)")
			continue
		}
		if math.Abs(end-start) > 2*math.Pi {
			r.Skip("sweeps beyond a turn (d3 clamps them to one)")
			continue
		}

		// index is the layout order: positions in values, first sector first.
		index := make([]int, len(values))
		for j := range index {
			index[j] = j
		}
		slices.SortStableFunc(index, func(a, b int) int {
			if ascending {
				return cmp.Compare(values[a], values[b])
			}
			return cmp.Compare(values[b], values[a])
		})
		p := PieParams{Field: FieldOf("v"), StartAngle: start, EndAngle: &end}
		// Pie sorts an ascending layout itself and leaves the tuples where they are; otherwise they
		// are handed over in layout order. at is the tuple of each position in values.
		p.Sort = ascending
		tuples := make([]jsval.Value, len(values))
		at := make([]jsval.Value, len(values))
		for j, k := range index {
			if ascending {
				j = k
			}
			tuples[j] = obj("v", jsval.Num(values[k]))
			at[k] = tuples[j]
		}
		if _, err := Pie(ctx, tuples, p); err != nil {
			r.Check(c, nil, true)
			continue
		}
		data, _ := c.Args[0].([]any)
		if set, isSet := c.Args[0].(map[string]any); isSet {
			data = set["values"].([]any)
		}
		arcs := make([]any, len(values))
		for rank, k := range index {
			arcs[k] = map[string]any{
				"data": data[k], "index": float64(rank), "value": upstream.Enc(values[k]),
				"startAngle": upstream.Enc(at[k].Get("startAngle").NumValue()),
				"endAngle":   upstream.Enc(at[k].Get("endAngle").NumValue()),
				"padAngle":   float64(0),
			}
		}
		r.Check(c, arcs, false)
	}
	r.Done(6)
}

// TestUpstreamD3ShapeStack replays d3-shape's own stack tests (see internal/upstream) through the
// engine's Stack transform. d3 stacks series, one per key, across the rows of its data; Vega stacks
// the tuples of a group. The adapter makes a tuple of every (row, key) cell, grouped by row and
// ordered by key, which is the same computation: series k, point i is the [y0, y1] of cell (i, k).
// Of d3's orders, none is the tuples' own order and reverse their descending order by key; Vega has
// no other, and its offsets are zero (d3's none) and normalize, which is not d3's expand: expand
// divides each value by the column total and then adds them up, normalize scales the running sum by
// 1 / total, so the last bit differs. The recording holds the offset and order functions alone
// (stackOffsetNone(series, order), stackOrderAscending(series)) as the arguments they were given,
// with no trace of the series they changed, and d3's sort of the series by their sums is not a
// comparator of tuples, so they are not replayed. Accessor functions are not in the recording.
func TestUpstreamD3ShapeStack(t *testing.T) {
	r := upstream.Start(t, "d3-shape")
	ctx := context.Background()
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "stack()" {
			continue
		}
		if c.Method != "" || len(c.ViaSteps()) != 0 || len(c.Args) != 1 {
			r.Skip("stack questions of another shape (accessor reads, extra arguments)")
			continue
		}
		rows, ok := c.Args[0].([]any)
		if !ok {
			r.Skip("data that is not an array of rows")
			continue
		}
		var keys int
		order, expand := "stackOrderNone", false
		configured := true
		for _, step := range c.ChainSteps() {
			switch {
			case step.Method == "keys" && len(step.Args) == 1:
				ks, isNums := numbers(step.Args[0])
				keys = len(ks)
				for j, k := range ks {
					configured = configured && isNums && k == float64(j)
				}
				configured = configured && isNums
			case step.Method == "order" && len(step.Args) == 1:
				origin, isFn := upstream.IsFunction(step.Args[0])
				name, _ := origin["export"].(string)
				configured = configured && isFn && (name == "stackOrderNone" || name == "stackOrderReverse")
				order = name
			case step.Method == "offset" && len(step.Args) == 1:
				origin, isFn := upstream.IsFunction(step.Args[0])
				name, _ := origin["export"].(string)
				configured = configured && isFn
				switch name {
				case "stackOffsetNone":
				case "stackOffsetExpand":
					expand = true
				default:
					configured = false
				}
			default:
				configured = false
			}
		}
		if !configured || keys == 0 {
			r.Skip("stack configuration Vega's stack does not have (key and value accessors, orders besides none and reverse, offsets besides none and expand)")
			continue
		}
		if expand {
			r.Skip("stackOffsetExpand, whose arithmetic is not Vega's normalize")
			continue
		}
		var tuples []jsval.Value
		for ri, row := range rows {
			cells, isNums := numbers(row)
			if !isNums || len(cells) < keys {
				tuples = nil
				break
			}
			for k := 0; k < keys; k++ {
				tuples = append(tuples, obj("row", jsval.Int(ri), "key", jsval.Int(k), "v", jsval.Num(cells[k])))
			}
		}
		if tuples == nil {
			r.Skip("rows that are not arrays of numbers")
			continue
		}
		p := StackParams{Field: FieldOf("v"), GroupBy: FieldsOf("row"), Offset: StackZero}
		if order == "stackOrderReverse" {
			p.Sort = CompareBy(FieldsOf("key"), []string{Desc})
		}
		if _, err := Stack(ctx, tuples, p); err != nil {
			r.Check(c, nil, true)
			continue
		}
		series := make([]any, keys)
		for k := range series {
			points := make([]any, len(rows))
			series[k] = points
		}
		for _, tu := range tuples {
			k, ri := int(tu.Get("key").NumValue()), int(tu.Get("row").NumValue())
			series[k].([]any)[ri] = []any{upstream.Enc(tu.Get("y0").NumValue()), upstream.Enc(tu.Get("y1").NumValue())}
		}
		r.Check(c, series, false)
	}
	r.Done(2)
}

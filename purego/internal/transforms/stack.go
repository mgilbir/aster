package transforms

import (
	"context"
	"math"
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Stack offsets.
const (
	StackZero      = "zero"
	StackCenter    = "center"
	StackNormalize = "normalize"
)

// StackParams configures Stack (vega-encode's stack transform).
type StackParams struct {
	// Field is the value to stack; the null Field stacks 1 per tuple.
	Field   Field
	GroupBy []Field
	// Sort orders the tuples within each stack; nil keeps input order.
	Sort Comparator
	// Offset is StackZero (default), StackCenter or StackNormalize.
	Offset string
	// As names the two output fields; empty entries default to y0 and y1.
	As [2]string
}

// stackGroupKey mirrors JSON.stringify(values): distinct types stay distinct
// (1 vs "1") while null, undefined and non-finite numbers all write null.
func stackGroupKey(fields []Field, t jsval.Value, sb *strings.Builder) string {
	sb.Reset()
	for _, f := range fields {
		v := f.Get(t)
		switch {
		case v.IsNullish():
			sb.WriteString("n")
		case v.IsNum():
			if n := v.NumValue(); math.IsNaN(n) || math.IsInf(n, 0) {
				sb.WriteString("n")
			} else {
				sb.WriteByte('d')
				sb.WriteString(v.AsString())
			}
		case v.IsStr():
			sb.WriteByte('s')
			sb.WriteString(v.StrValue())
		default:
			sb.WriteByte('o')
			sb.WriteString(v.Kind().String())
			sb.WriteString(v.AsString())
		}
		sb.WriteByte(0)
	}
	return sb.String()
}

// Stack writes the stacked extent [y0, y1] of each tuple into the tuple itself
// and returns data. Within a stack, "zero" accumulates non-negative and
// negative values separately from the baseline; "center" and "normalize" stack
// absolute values (so negatives add), with center aligning each stack about
// the widest one. Values are coerced with unary plus; the group sums use
// Math.abs, so NaN poisons a whole stack exactly as upstream.
func Stack(ctx context.Context, data []jsval.Value, p StackParams) ([]jsval.Value, error) {
	y0, y1 := p.As[0], p.As[1]
	if y0 == "" {
		y0 = "y0"
	}
	if y1 == "" {
		y1 = "y1"
	}
	get := func(t jsval.Value) float64 { return 1 }
	if !p.Field.IsNil() {
		g := p.Field.Get
		get = func(t jsval.Value) float64 { return jsval.ToNumber(g(t)) }
	}

	var groups [][]jsval.Value
	if len(p.GroupBy) == 0 {
		groups = [][]jsval.Value{append([]jsval.Value(nil), data...)}
	} else {
		idx := map[string]int{}
		var sb strings.Builder
		for i, t := range data {
			if err := poll(ctx, i); err != nil {
				return nil, err
			}
			k := stackGroupKey(p.GroupBy, t, &sb)
			gi, ok := idx[k]
			if !ok {
				gi = len(groups)
				idx[k] = gi
				groups = append(groups, nil)
			}
			groups[gi] = append(groups[gi], t)
		}
	}
	sums := make([]float64, len(groups))
	maxSum := 0.0
	for gi, g := range groups {
		s := 0.0
		for _, t := range g {
			s += math.Abs(get(t))
		}
		sums[gi] = s
		if s > maxSum {
			maxSum = s
		}
		SortTuples(g, p.Sort)
	}

	set := func(t jsval.Value, a, b float64) {
		if o := t.ObjValue(); o != nil {
			o.Set(y0, jsval.Num(a))
			o.Set(y1, jsval.Num(b))
		}
	}
	for gi, g := range groups {
		if err := poll(ctx, gi); err != nil {
			return nil, err
		}
		switch p.Offset {
		case StackCenter:
			last := (maxSum - sums[gi]) / 2
			for _, t := range g {
				a := last
				last += math.Abs(get(t))
				set(t, a, last)
			}
		case StackNormalize:
			scale := 1 / sums[gi]
			last, v := 0.0, 0.0
			for _, t := range g {
				a := last
				v += math.Abs(get(t))
				last = scale * v
				set(t, a, last)
			}
		default:
			lastPos, lastNeg := 0.0, 0.0
			for _, t := range g {
				v := get(t)
				if v < 0 {
					a := lastNeg
					lastNeg += v
					set(t, a, lastNeg)
				} else {
					a := lastPos
					lastPos += v
					set(t, a, lastPos)
				}
			}
		}
	}
	return data, nil
}

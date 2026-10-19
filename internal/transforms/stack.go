package transforms

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
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

// errCircular is the TypeError JSON.stringify throws on a value that
// contains itself.
var errCircular = errors.New("TypeError: Converting circular structure to JSON")

// appendStackKey appends upstream's stack group key,
// JSON.stringify(groupby.map(get)), to dst. Its walk of the values is
// metered by m and its text charged while it is built (ADR 0018).
func appendStackKey(dst []byte, fields []Field, t jsval.Value, j *stringifier) ([]byte, error) {
	dst = append(dst, '[')
	for i, f := range fields {
		if i > 0 {
			dst = append(dst, ',')
		}
		var err error
		// In an array, undefined is written as null.
		if dst, err = j.append(dst, f.Get(t), 0); err != nil {
			return dst, err
		}
	}
	return append(dst, ']'), nil
}

// stringifier writes JSON.stringify of the values of one call: w walks them
// and m meters the walk of the arrays and objects (on, once the key being
// written has one); path holds the objects being written. A key of strings
// and numbers, the common one, is no walk.
type stringifier struct {
	w    jsval.Walker
	m    jsval.WalkMeter
	on   bool
	path []*jsval.Object
}

// append is JSON.stringify(v) for a value in an array, or an object member
// that is not undefined: null, undefined and non-finite numbers are null; a
// date is its toJSON, the ISO string (null for an Invalid Date); an object's
// members are in its key order (JavaScript's), undefined ones omitted; a
// value holding itself is the TypeError upstream throws.
func (j *stringifier) append(dst []byte, v jsval.Value, depth int) ([]byte, error) {
	switch v.Kind() {
	case jsval.KindStr:
		dst = jsval.AppendQuoted(dst, v.StrValue())
		j.text(dst)
		return dst, nil
	case jsval.KindNum:
		if f := v.NumValue(); math.IsNaN(f) || math.IsInf(f, 0) {
			return append(dst, "null"...), nil
		}
		dst = jsval.AppendJSNumber(dst, v.NumValue())
		j.text(dst)
		return dst, nil
	case jsval.KindTimestamp:
		iso, ok := format.ISOFormat(v.NumValue())
		if !ok {
			return append(dst, "null"...), nil
		}
		dst = jsval.AppendQuoted(dst, iso)
		j.text(dst)
		return dst, nil
	case jsval.KindArr, jsval.KindObj:
		if !j.on {
			j.m, j.on = j.w.Meter(), true
		}
		j.m.Step(depth)
	default:
		dst = j.w.AppendJSON(dst, v)
		j.text(dst)
		return dst, nil
	}
	if v.IsArr() {
		dst = append(dst, '[')
		for i, it := range v.Items() {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = j.append(dst, it, depth+1); err != nil {
				return dst, err
			}
		}
		return append(dst, ']'), nil
	}
	o := v.ObjValue()
	if slices.Contains(j.path, o) {
		return dst, errCircular
	}
	j.path = append(j.path, o)
	defer func() { j.path = j.path[:len(j.path)-1] }()
	dst = append(dst, '{')
	n := 0
	for i := 0; i < o.Len(); i++ {
		val := o.ValueAt(i)
		if val.IsUndefined() {
			continue
		}
		if n > 0 {
			dst = append(dst, ',')
		}
		n++
		dst = jsval.AppendQuoted(dst, o.KeyAt(i))
		dst = append(dst, ':')
		var err error
		if dst, err = j.append(dst, val, depth+1); err != nil {
			return dst, err
		}
	}
	return append(dst, '}'), nil
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
		var buf []byte
		j := &stringifier{w: jsval.WalkerFor(ctx)}
		for i, t := range data {
			if err := poll(ctx, i); err != nil {
				return nil, err
			}
			var err error
			buf, err = appendStackKey(buf[:0], p.GroupBy, t, j)
			if j.on {
				j.m.Done()
				j.on = false
			}
			if err != nil {
				return nil, err
			}
			gi, ok := idx[string(buf)]
			if !ok {
				gi = len(groups)
				idx[string(buf)] = gi
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

// text charges the key's text, once a walk of an array or object is under
// way (before, the key is a few values the data holds).
func (j *stringifier) text(dst []byte) {
	if j.on {
		j.m.Text(len(dst))
	}
}

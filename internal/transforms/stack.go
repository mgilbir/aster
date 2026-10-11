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
// JSON.stringify(groupby.map(get)), to dst.
func appendStackKey(dst []byte, fields []Field, t jsval.Value, path *[]*jsval.Object) ([]byte, error) {
	dst = append(dst, '[')
	for i, f := range fields {
		if i > 0 {
			dst = append(dst, ',')
		}
		var err error
		// In an array, undefined is written as null.
		if dst, err = appendStringify(dst, f.Get(t), path, 0); err != nil {
			return dst, err
		}
	}
	return append(dst, ']'), nil
}

// appendStringify is JSON.stringify(v) for a value in an array, or an object
// member that is not undefined: null, undefined and non-finite numbers are
// null; a date is its toJSON, the ISO string (null for an Invalid Date); an
// object's members are in JavaScript's key order (integer keys ascending,
// then the rest as inserted), undefined ones omitted; a value holding itself
// is the TypeError upstream throws. path holds the objects being written.
func appendStringify(dst []byte, v jsval.Value, path *[]*jsval.Object, depth int) ([]byte, error) {
	switch v.Kind() {
	case jsval.KindTimestamp:
		iso, ok := format.ISOFormat(v.NumValue())
		if !ok {
			return append(dst, "null"...), nil
		}
		return jsval.AppendJSON(dst, jsval.Str(iso)), nil
	case jsval.KindArr, jsval.KindObj:
		if depth > jsval.MaxValueDepth {
			return dst, errors.New("RangeError: Maximum call stack size exceeded")
		}
	default:
		return jsval.AppendJSON(dst, v), nil
	}
	if v.IsArr() {
		dst = append(dst, '[')
		for i, it := range v.Items() {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = appendStringify(dst, it, path, depth+1); err != nil {
				return dst, err
			}
		}
		return append(dst, ']'), nil
	}
	o := v.ObjValue()
	if slices.Contains(*path, o) {
		return dst, errCircular
	}
	*path = append(*path, o)
	defer func() { *path = (*path)[:len(*path)-1] }()
	keys := make([]string, 0, o.Len())
	for i := 0; i < o.Len(); i++ {
		keys = append(keys, o.KeyAt(i))
	}
	dst = append(dst, '{')
	n := 0
	for _, k := range OrderedKeys(keys) {
		val := o.Lookup(k)
		if val.IsUndefined() {
			continue
		}
		if n > 0 {
			dst = append(dst, ',')
		}
		n++
		dst = jsval.AppendJSON(dst, jsval.Str(k))
		dst = append(dst, ':')
		var err error
		if dst, err = appendStringify(dst, val, path, depth+1); err != nil {
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
		var path []*jsval.Object
		for i, t := range data {
			if err := poll(ctx, i); err != nil {
				return nil, err
			}
			var err error
			if buf, err = appendStackKey(buf[:0], p.GroupBy, t, &path); err != nil {
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

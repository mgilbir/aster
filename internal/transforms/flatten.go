package transforms

import (
	"context"
	"fmt"

	"github.com/mgilbir/aster/internal/jsval"
)

// FlattenParams configures Flatten.
type FlattenParams struct {
	Fields []Field
	// As names the output fields; missing entries fall back to the field name.
	As []string
	// Index, when non-empty, receives the position within the arrays.
	Index string
}

// Flatten unrolls array fields: each tuple becomes max(len) copies, the i-th
// carrying element i of every field under its output name (null past the end
// of a shorter array, and for null elements). Values with no length (numbers,
// objects, booleans) count as length NaN upstream, which suppresses the
// tuple's output entirely; null and undefined make upstream throw, which is
// reported as an error here.
func Flatten(ctx context.Context, data []jsval.Value, p FlattenParams) ([]jsval.Value, error) {
	names := make([]string, len(p.Fields))
	for i, f := range p.Fields {
		names[i] = f.Name
		if i < len(p.As) && p.As[i] != "" {
			names[i] = p.As[i]
		}
	}
	var out []jsval.Value
	arrays := make([][]jsval.Value, len(p.Fields))
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		maxlen, valid := 0, true
		for j, f := range p.Fields {
			v := f.Apply(t)
			switch v.Kind() {
			case jsval.KindArr:
				arrays[j] = v.Items()
			case jsval.KindStr:
				arrays[j] = flattenChars(v.StrValue())
			case jsval.KindNull, jsval.KindUndefined:
				return nil, fmt.Errorf("flatten: field %q is %s, not an array", f.Name, v.Kind())
			default:
				valid = false
				arrays[j] = nil
			}
			maxlen = max(maxlen, len(arrays[j]))
		}
		if !valid {
			continue
		}
		if err := reserveOut(ctx, len(out)+maxlen, len(data)); err != nil {
			return nil, err
		}
		for k := 0; k < maxlen; k++ {
			if err := poll(ctx, len(out)); err != nil {
				return nil, err
			}
			d := shallowCopyTuple(t)
			for j := range p.Fields {
				v := jsval.Null
				if k < len(arrays[j]) && !arrays[j][k].IsNullish() {
					v = arrays[j][k]
				}
				d.Set(names[j], v)
			}
			if p.Index != "" {
				d.Set(p.Index, jsval.Int(k))
			}
			out = append(out, jsval.Obj(d))
		}
	}
	return out, nil
}

// flattenChars indexes a string as JavaScript does; only the BMP is exact.
func flattenChars(s string) []jsval.Value {
	rs := []rune(s)
	out := make([]jsval.Value, len(rs))
	for i, r := range rs {
		out[i] = jsval.Str(string(r))
	}
	return out
}

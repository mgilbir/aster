package jsval

import "strings"

// StringValue is vega-util's stringValue as a template literal prints it: the
// source text of a value for splicing into an expression. An array prints
// its elements (null as null, undefined as nothing, the rest recursively);
// a string or object is its JSON, with U+2028 and U+2029 escaped because the
// expression parser takes them for line terminators; anything else is
// String(v). An array nested past MaxValueDepth prints as nothing there.
func StringValue(v Value) string { return stringValue(v, 0) }

func stringValue(v Value, depth int) string {
	switch v.Kind() {
	case KindArr:
		if depth > MaxValueDepth {
			return ""
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, it := range v.Items() {
			if i > 0 {
				b.WriteByte(',')
			}
			switch {
			case it.IsNull():
				b.WriteString("null")
			case !it.IsUndefined():
				b.WriteString(stringValue(it, depth+1))
			}
		}
		b.WriteByte(']')
		return b.String()
	case KindStr, KindObj:
		s := string(AppendJSON(nil, v))
		s = strings.ReplaceAll(s, "\u2028", "\\u2028")
		return strings.ReplaceAll(s, "\u2029", "\\u2029")
	}
	return v.AsString()
}

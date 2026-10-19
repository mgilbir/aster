package vegalite

import (
	"strings"
	"unicode/utf16"

	"github.com/mgilbir/aster/internal/jsval"
)

// Field-path helpers from vega-lite/src/util.ts.

// splitAccessPath is vega-util's, which throws on a path with a bracket or
// quote left open.
func splitAccessPath(p string) []string {
	segs, err := jsval.SplitFieldPath(p)
	if err != nil {
		throw("%s", err.Error())
	}
	return segs
}

// splitAccessPathV is splitAccessPath of a spec value, as vega-util's reads
// it: n = p.length is taken before p becomes a string, so null and undefined
// are TypeErrors, an array is split over the first length units of its text
// (["a.b"] splits as "a", "b"; [{}] is "[object Object]" cut at one unit, a
// bracket left open), and a number, boolean or object has no length and is
// the empty path.
func splitAccessPathV(v Value) []string {
	switch {
	case v.IsStr():
		return splitAccessPath(v.StrValue())
	case v.IsNullish():
		throw("Cannot read properties of %s (reading 'length')", nullishName(v))
	case v.IsArr():
		return splitAccessPathN(v.AsString(), v.Len())
	}
	return nil
}

// splitAccessPathN is vega-util's splitAccessPath of the text p with its
// loop bound n (p.length before the conversion to text), on UTF-16 units.
func splitAccessPathN(text string, n int) []string {
	p := utf16Len(text)
	sub := func(i, j int) string {
		i, j = min(max(i, 0), len(p)), min(max(j, 0), len(p))
		if i >= j {
			return ""
		}
		return string(utf16.Decode(p[i:j]))
	}
	var path []string
	q := uint16(0)
	inQuote := false
	b, s := 0, ""
	i, j := 0, 0
	push := func() {
		path = append(path, s+sub(i, j))
		s = ""
		i = j + 1
	}
	for i, j = 0, 0; j < n; j++ {
		var c uint16
		if j < len(p) {
			c = p[j]
		}
		switch {
		case j >= len(p):
			// p[j] is undefined past the text: no case matches.
		case c == '\\':
			s += sub(i, j)
			j++
			i = j
		case inQuote && c == q:
			push()
			inQuote = false
			b = -1
		case inQuote:
			continue
		case i == b && (c == '"' || c == '\''):
			i = j + 1
			q, inQuote = c, true
		case c == '.' && b == 0:
			if j > i {
				push()
			} else {
				i = j + 1
			}
		case c == '[':
			if j > i {
				push()
			}
			b = j + 1
			i = b
		case c == ']':
			if b == 0 {
				throw("Access path missing open bracket: %s", text)
			}
			if b > 0 {
				push()
			}
			b = 0
			i = j + 1
		}
	}
	if b != 0 {
		throw("Access path missing closing bracket: %s", text)
	}
	if inQuote {
		throw("Access path missing closing quote: %s", text)
	}
	if j > i {
		j++
		push()
	}
	return path
}

func replacePathInFieldV(v Value) string {
	parts := splitAccessPathV(v)
	for i, p := range parts {
		parts[i] = escapePathAccess(p)
	}
	return strings.Join(parts, "\\.")
}

func removePathFromFieldV(v Value) string { return strings.Join(splitAccessPathV(v), ".") }

func flatAccessWithDatumV(cc *compileCtx, v Value, datum string) string {
	return datum + "[" + jsonString(cc, strings.Join(splitAccessPathV(v), ".")) + "]"
}

// accessPathDepthV is accessPathDepth of a spec value: 0 for a falsy one.
func accessPathDepthV(v Value) int {
	if !v.IsTruthy() {
		return 0
	}
	return len(splitAccessPathV(v))
}

// unescapeSingleQuoteAndPathDotV is unescapeSingleQuoteAndPathDot of a spec
// value, which calls escapedPath.replaceAll.
func unescapeSingleQuoteAndPathDotV(v Value) string {
	switch {
	case v.IsStr():
		return unescapeSingleQuoteAndPathDot(v.StrValue())
	case v.IsNullish():
		throw("Cannot read properties of %s (reading 'replaceAll')", nullishName(v))
	}
	throw("escapedPath.replaceAll is not a function")
	return ""
}

// accessWithDatumToUnescapedPathV is accessWithDatumToUnescapedPath of a spec
// value, which calls unescapedPath.replaceAll.
func accessWithDatumToUnescapedPathV(v Value) string {
	switch {
	case v.IsStr():
		return accessWithDatumToUnescapedPath(v.StrValue())
	case v.IsNullish():
		throw("Cannot read properties of %s (reading 'replaceAll')", nullishName(v))
	}
	throw("unescapedPath.replaceAll is not a function")
	return ""
}

// accessPathWithDatum turns "a.b" into "datum["a"] && datum["a"]["b"]".
func accessPathWithDatum(cc *compileCtx, path, datum string) string {
	return accessPathWithDatumPieces(cc, splitAccessPath(path), datum)
}

// accessPathWithDatumV is accessPathWithDatum of a field as upstream holds
// it: splitAccessPath of a value that is not a string reads its length, so a
// number, a boolean or an object gives no pieces at all.
func accessPathWithDatumV(cc *compileCtx, path Value, datum string) string {
	return accessPathWithDatumPieces(cc, splitAccessPathV(path), datum)
}

// accessPathWithDatumPieces writes upstream's guard of each prefix of the
// path in turn. The expression is quadratic in the pieces, as upstream's is
// (a field of k pieces makes k prefixes of up to k): it is written once,
// each piece quoted once, and charged to cc as it grows, a step per 16
// bytes, so a long field path is paid for and polled, not built unseen.
func accessPathWithDatumPieces(cc *compileCtx, pieces []string, datum string) string {
	if len(pieces) == 0 {
		return ""
	}
	qs := make([]string, len(pieces))
	for i, p := range pieces {
		cc.work(1)
		qs[i] = jsonString(cc, p)
	}
	// The size is known: made at once, the buffer is not copied as it grows.
	size, prefix := 0, len(datum)+1 // datum[
	for i, q := range qs {
		cc.work(1)
		prefix += len(q) + 2 // the piece and "][" or "]"
		size += prefix + 4*min(i, 1)
	}
	var b strings.Builder
	b.Grow(size)
	prefix = len(datum) + 1
	for i, q := range qs {
		if i > 0 {
			b.WriteString(" && ")
		}
		cc.work(int64(prefix+len(q)+2*i) / 16)
		b.WriteString(datum)
		b.WriteByte('[')
		for j := 0; j <= i; j++ {
			if j > 0 {
				b.WriteString("][")
			}
			b.WriteString(qs[j])
		}
		b.WriteByte(']')
		prefix += len(q) + 2
	}
	return b.String()
}

// flatAccessWithDatum accesses a flattened field: datum["a.b"].
func flatAccessWithDatum(cc *compileCtx, path, datum string) string {
	return datum + "[" + jsonString(cc, strings.Join(splitAccessPath(path), ".")) + "]"
}

func accessWithDatumToUnescapedPath(p string) string {
	return "datum['" + strings.ReplaceAll(p, "'", "\\'") + "']"
}

func unescapeSingleQuoteAndPathDot(p string) string {
	return strings.ReplaceAll(strings.ReplaceAll(p, "\\'", "'"), "\\.", ".")
}

func escapePathAccess(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '[', ']', '.', '\'', '"':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// replacePathInField escapes the path so that it addresses one flat column.
func replacePathInField(path string) string {
	parts := splitAccessPath(path)
	for i, p := range parts {
		parts[i] = escapePathAccess(p)
	}
	return strings.Join(parts, "\\.")
}

func removePathFromField(path string) string { return strings.Join(splitAccessPath(path), ".") }

func accessPathDepth(path string) int {
	if path == "" {
		return 0
	}
	return len(splitAccessPath(path))
}

func internalField(name string) string {
	if isInternalField(name) {
		return name
	}
	return "__" + name
}

func isInternalField(name string) bool { return strings.HasPrefix(name, "__") }

// prefixGenerator returns every prefix of every dotted path: "a.b.c" gives
// a, a[b], a[b][c].
func prefixGenerator(cc *compileCtx, a *sset) map[string]bool {
	out := make(map[string]bool)
	for _, x := range a.list() {
		cc.work(1)
		split := splitAccessPathV(a.value(x))
		wrapped := make([]string, len(split))
		for i, y := range split {
			cc.work(1)
			if i == 0 {
				wrapped[i] = y
			} else {
				wrapped[i] = "[" + y + "]"
			}
		}
		for i := range wrapped {
			cc.work(1)
			out[strings.Join(wrapped[:i+1], "")] = true
		}
	}
	return out
}

// fieldIntersection is upstream's: true when either set is unknown (nil) or
// the two share a field path prefix.
func fieldIntersection(cc *compileCtx, a, b *sset) bool {
	if a == nil || b == nil {
		return true
	}
	pa, pb := prefixGenerator(cc, a), prefixGenerator(cc, b)
	for k := range pa {
		cc.work(1)
		if pb[k] {
			return true
		}
	}
	return false
}

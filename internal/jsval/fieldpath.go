package jsval

import (
	"math"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ParseFieldPath splits a Vega field reference into its path segments, as
// vega-util's splitAccessPath does: "a.b" is [a b], "a[0]" is [a 0],
// "a['b.c']" is [a b.c], and a backslash escapes the next character ("a\.b"
// is the single segment "a.b").
//
// Where upstream throws on a malformed path (an unterminated bracket or
// quote), this returns the unterminated remainder as one literal segment, so
// the lookup misses instead of the chart failing. SplitFieldPath reports what
// upstream throws.
func ParseFieldPath(path string) []string {
	segments, _ := SplitFieldPath(path)
	return segments
}

// PathError is the exception vega-util's splitAccessPath throws for a path it
// cannot split.
type PathError struct{ Msg string }

func (e *PathError) Error() string { return e.Msg }

// SplitFieldPath is ParseFieldPath with the error upstream throws: a closing
// bracket with none open, or a bracket or quote left open. The segments are
// the lenient ones whatever the error.
func SplitFieldPath(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	var err error
	var (
		segments []string
		carried  strings.Builder // text carried over a backslash escape
		start    int
		// bracket is 0 while no bracket is open, the index after the '[' while
		// one is, and -1 once a quoted bracket segment has been pushed and only
		// its ']' remains.
		bracket      int
		quote        byte
		openAt       = -1
		openSegments int
	)
	push := func(end int) {
		segments = append(segments, carried.String()+path[start:end])
		carried.Reset()
		start = end + 1
	}
	i := 0
	for i < len(path) {
		ch := path[i]
		switch {
		case ch == '\\':
			carried.WriteString(path[start:i])
			start = i + 1
			i += 2
			continue
		case quote != 0:
			if ch == quote {
				push(i)
				quote = 0
				bracket = -1
			}
		case start == bracket && (ch == '"' || ch == '\''):
			if bracket == 0 {
				openAt = i
				openSegments = len(segments)
			}
			start = i + 1
			quote = ch
		case ch == '.' && bracket == 0:
			if i > start {
				push(i)
			} else {
				start = i + 1
			}
		case ch == '[':
			if i > start {
				push(i)
			}
			openAt = i
			openSegments = len(segments)
			bracket = i + 1
			start = i + 1
		case ch == ']':
			switch {
			case bracket > 0:
				push(i)
				bracket = 0
				openAt = -1
			case bracket < 0:
				start = i + 1
				bracket = 0
				openAt = -1
			default:
				if err == nil {
					err = &PathError{"Access path missing open bracket: " + path}
				}
			}
		}
		i++
	}
	if bracket != 0 || quote != 0 {
		if err == nil {
			if bracket != 0 {
				err = &PathError{"Access path missing closing bracket: " + path}
			} else {
				err = &PathError{"Access path missing closing quote: " + path}
			}
		}
		segments = segments[:openSegments]
		return append(segments, path[openAt:]), err
	}
	if i > start {
		end := i
		if end > len(path) {
			end = len(path)
		}
		push(end)
	}
	return segments, err
}

// SplitAccessPathOf is vega-util's splitAccessPath(p) of any value, as the
// runtime calls it on a field a specification gives (a string is
// SplitFieldPath's): the loop runs while j < p.length, which an array has
// (its elements' count), an object may have (its length member) and a
// number or boolean has not (no segment at all), over the characters of
// String(p); the last segment is cut one character past the loop's end. So
// ["a", "b"] splits to the one segment "a,b" and [null] to "". text is
// String(v), which the caller makes (with its call's Walker: v can be a
// value a run built).
func SplitAccessPathOf(v Value, text string) ([]string, error) {
	if v.Kind() == KindStr {
		return SplitFieldPath(v.s())
	}
	var n float64
	switch v.Kind() {
	case KindArr:
		n = float64(len(v.arr().list()))
	case KindObj:
		n = ToNumber(Member(v, "length"))
	}
	p := text
	units := utf16Units16(p)
	at := func(j int) int { // p[j], -1 past the end (undefined)
		if j < len(units) {
			return int(units[j])
		}
		return -1
	}
	sub := func(i, j int) string { // p.substring(i, j)
		if i > j {
			i, j = j, i
		}
		i, j = max(min(i, len(units)), 0), max(min(j, len(units)), 0)
		return unitsString(units[i:j])
	}
	var (
		path []string
		q    = -1
		b    = 0
		s    string
		i, j int
	)
	push := func() {
		path = append(path, s+sub(i, j))
		s = ""
		i = j + 1
	}
	// j < n: n may be fractional or NaN; past len(units) every character
	// is undefined and matches nothing, so the loop can stop there.
	for ; float64(j) < n; j++ {
		if j > len(units) {
			j = int(math.Ceil(n))
			break
		}
		c := at(j)
		switch {
		case c == '\\':
			s += sub(i, j)
			j++
			i = j
		case c == q && q >= 0:
			push()
			q = -1
			b = -1
		case q >= 0:
			continue
		case i == b && (c == '"' || c == '\''):
			i = j + 1
			q = c
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
				return path, &PathError{"Access path missing open bracket: " + p}
			}
			if b > 0 {
				push()
			}
			b = 0
			i = j + 1
		}
	}
	if b != 0 {
		return path, &PathError{"Access path missing closing bracket: " + p}
	}
	if q >= 0 {
		return path, &PathError{"Access path missing closing quote: " + p}
	}
	if j > i {
		j++
		push()
	}
	return path, nil
}

// unitsString is UTF-16 code units as a string: pairs as their character,
// a lone surrogate as its WTF-8 bytes.
func unitsString(u []uint16) string {
	var b []byte
	for k := 0; k < len(u); k++ {
		r := rune(u[k])
		switch {
		case utf16.IsSurrogate(r) && r < 0xDC00 && k+1 < len(u) && u[k+1] >= 0xDC00 && u[k+1] <= 0xDFFF:
			b = utf8.AppendRune(b, utf16.DecodeRune(r, rune(u[k+1])))
			k++
		case utf16.IsSurrogate(r):
			b = AppendSurrogate(b, r)
		default:
			b = utf8.AppendRune(b, r)
		}
	}
	return string(b)
}

// FieldPathText is a field path whose SplitFieldPath is segs: each segment
// that a path could not spell plainly is written as a quoted bracket
// segment, with its quotes and backslashes escaped.
func FieldPathText(segs []string) string {
	var b strings.Builder
	for k, s := range segs {
		if s != "" && !strings.ContainsAny(s, ".[]\"'\\") {
			if k > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s)
			continue
		}
		b.WriteString(`["`)
		for i := 0; i < len(s); i++ {
			if s[i] == '"' || s[i] == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(s[i])
		}
		b.WriteString(`"]`)
	}
	return b.String()
}

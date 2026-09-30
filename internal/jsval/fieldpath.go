package jsval

import "strings"

// ParseFieldPath splits a Vega field reference into its path segments, as
// vega-util's splitAccessPath does: "a.b" is [a b], "a[0]" is [a 0],
// "a['b.c']" is [a b.c], and a backslash escapes the next character ("a\.b"
// is the single segment "a.b").
//
// Where upstream throws on a malformed path (an unterminated bracket or
// quote), this returns the unterminated remainder as one literal segment, so
// the lookup misses instead of the chart failing.
func ParseFieldPath(path string) []string {
	if path == "" {
		return nil
	}
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
			}
		}
		i++
	}
	if bracket != 0 || quote != 0 {
		segments = segments[:openSegments]
		return append(segments, path[openAt:])
	}
	if i > start {
		end := i
		if end > len(path) {
			end = len(path)
		}
		push(end)
	}
	return segments
}

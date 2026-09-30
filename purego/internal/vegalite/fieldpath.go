package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Field-path helpers from vega-lite/src/util.ts.

func splitAccessPath(p string) []string { return jsval.ParseFieldPath(p) }

// accessPathWithDatum turns "a.b" into "datum["a"] && datum["a"]["b"]".
func accessPathWithDatum(path, datum string) string {
	pieces := splitAccessPath(path)
	prefixes := make([]string, 0, len(pieces))
	for i := 1; i <= len(pieces); i++ {
		qs := make([]string, i)
		for j := 0; j < i; j++ {
			qs[j] = jsonString(pieces[j])
		}
		prefixes = append(prefixes, datum+"["+strings.Join(qs, "][")+"]")
	}
	return strings.Join(prefixes, " && ")
}

// flatAccessWithDatum accesses a flattened field: datum["a.b"].
func flatAccessWithDatum(path, datum string) string {
	return datum + "[" + jsonString(strings.Join(splitAccessPath(path), ".")) + "]"
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
func prefixGenerator(a map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for x := range a {
		split := splitAccessPath(x)
		wrapped := make([]string, len(split))
		for i, y := range split {
			if i == 0 {
				wrapped[i] = y
			} else {
				wrapped[i] = "[" + y + "]"
			}
		}
		for i := range wrapped {
			out[strings.Join(wrapped[:i+1], "")] = true
		}
	}
	return out
}

// fieldIntersection: nil (undefined) means "unknown", which intersects everything.
func fieldIntersection(a, b map[string]bool) bool {
	if a == nil || b == nil {
		return true
	}
	pa, pb := prefixGenerator(a), prefixGenerator(b)
	for k := range pa {
		if pb[k] {
			return true
		}
	}
	return false
}

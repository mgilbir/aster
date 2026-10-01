package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// parseEventSelector is vega-event-selector's parse: an event selector string
// becomes an array of event stream definitions.

var defaultEventMarks = strSet([]string{"*", "arc", "area", "group", "image", "line", "path", "rect", "rule", "shape", "symbol", "text", "trail"})

type eventParser struct {
	defaultSource string
	depth         int // nesting of between selectors being parsed
}

// maxEventNesting bounds how deeply between selectors ("[[a, b] > c, d] > e")
// nest: parseBetween recurses once per level, and the selector is a string of
// any length. Real selectors nest once.
const maxEventNesting = 64

func parseSelector(selector, source string) []Value {
	if source == "" {
		source = "view"
	}
	p := &eventParser{defaultSource: source}
	parts := p.parseMerge(strings.TrimSpace(selector))
	out := make([]Value, len(parts))
	for i, s := range parts {
		out[i] = p.parseOne(s)
	}
	return out
}

func evFind(s string, i int, endChar byte, pushChar, popChar string) int {
	n := len(s)
	count := 0
	for ; i < n; i++ {
		c := s[i]
		if count == 0 && c == endChar {
			return i
		} else if popChar != "" && strings.IndexByte(popChar, c) >= 0 {
			count--
		} else if pushChar != "" && strings.IndexByte(pushChar, c) >= 0 {
			count++
		}
	}
	return i
}

func (p *eventParser) parseMerge(s string) []string {
	var out []string
	n := len(s)
	start, i := 0, 0
	for i < n {
		i = evFind(s, i, ',', "[{", "]}")
		out = append(out, strings.TrimSpace(s[start:i]))
		i++
		start = i
	}
	if len(out) == 0 {
		throw("Empty event selector: %s", s)
	}
	return out
}

func (p *eventParser) parseOne(s string) Value {
	if len(s) > 0 && s[0] == '[' {
		return p.parseBetween(s)
	}
	return p.parseStream(s)
}

func (p *eventParser) parseBetween(s string) Value {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxEventNesting {
		exceeded("Event selector nested too deeply: %s", s[:min(len(s), 40)])
	}
	n := len(s)
	i := evFind(s, 1, ']', "[", "]")
	if i == n {
		throw("Empty between selector: %s", s)
	}
	b := p.parseMerge(s[1:i])
	if len(b) != 2 {
		throw("Between selector must have two elements: %s", s)
	}
	s = strings.TrimSpace(s[i+1:])
	if len(s) == 0 || s[0] != '>' {
		throw("Expected '>' after between selector: %s", s)
	}
	between := arr(p.parseOne(b[0]), p.parseOne(b[1]))
	stream := p.parseOne(strings.TrimSpace(s[1:]))
	if stream.Get("between").IsTruthy() {
		return mkv("between", between, "stream", stream)
	}
	stream.ObjValue().Set("between", between)
	return stream
}

func (p *eventParser) parseStream(s string) Value {
	stream := mk("source", p.defaultSource)
	var source []string
	throttle := [2]float64{0, 0}
	markname := 0
	start := 0
	n := len(s)
	i := 0
	var filter []Value
	haveFilter := false
	if n > 0 && s[n-1] == '}' {
		i = strings.LastIndex(s, "{")
		if i >= 0 {
			t, ok := parseThrottle(s[i+1 : n-1])
			if !ok {
				throw("Invalid throttle specification: %s", s)
			}
			throttle = t
			s = strings.TrimSpace(s[:i])
			n = len(s)
		} else {
			throw("Unmatched right brace: %s", s)
		}
		i = 0
	}
	if n == 0 {
		throw("%s", s)
	}
	if s[0] == '@' {
		i++
		markname = i
	}
	j := evFind(s, i, ':', "", "")
	if j < n {
		source = append(source, strings.TrimSpace(s[start:j]))
		j++
		start, i = j, j
	}
	i = evFind(s, i, '[', "", "")
	if i == n {
		source = append(source, strings.TrimSpace(s[start:n]))
	} else {
		source = append(source, strings.TrimSpace(s[start:i]))
		haveFilter = true
		i++
		start = i
		if start == n {
			throw("Unmatched left bracket: %s", s)
		}
	}
	for i < n {
		i = evFind(s, i, ']', "", "")
		if i == n {
			throw("Unmatched left bracket: %s", s)
		}
		filter = append(filter, jsval.Str(strings.TrimSpace(s[start:i])))
		if i < n-1 {
			i++
			if s[i] != '[' {
				throw("Expected left bracket: %s", s)
			}
		}
		i++
		start = i
	}
	n = len(source)
	if n == 0 || strings.ContainsAny(source[n-1], "[]{}") {
		throw("Invalid event selector: %s", s)
	}
	var typ string
	if n > 1 {
		typ = source[1]
		switch {
		case markname != 0:
			stream.Set("markname", jsval.Str(source[0][1:]))
		case defaultEventMarks[source[0]]:
			stream.Set("marktype", jsval.Str(source[0]))
		default:
			stream.Set("source", jsval.Str(source[0]))
		}
	} else {
		typ = source[0]
	}
	consume := false
	if strings.HasSuffix(typ, "!") {
		consume = true
		typ = typ[:len(typ)-1]
	}
	// upstream assigns type before markname/marktype/source.
	ordered := mk("source", stream.Lookup("source"), "type", typ)
	for _, k := range []string{"markname", "marktype"} {
		if v := stream.Lookup(k); !v.IsUndefined() {
			ordered.Set(k, v)
		}
	}
	if consume {
		ordered.Set("consume", jsval.True)
	}
	if haveFilter {
		ordered.Set("filter", jsval.Arr(filter))
	}
	if throttle[0] != 0 {
		ordered.Set("throttle", jsval.Num(throttle[0]))
	}
	if throttle[1] != 0 {
		ordered.Set("debounce", jsval.Num(throttle[1]))
	}
	return jsval.Obj(ordered)
}

func parseThrottle(s string) ([2]float64, bool) {
	a := strings.Split(s, ",")
	if len(s) == 0 || len(a) > 2 {
		return [2]float64{}, false
	}
	var out [2]float64
	for i, x := range a {
		f := jsval.StringToNumber(x)
		if f != f {
			return out, false
		}
		out[i] = f
	}
	return out, true
}

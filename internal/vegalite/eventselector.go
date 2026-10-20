package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// parseEventSelector is vega-event-selector's parse: an event selector string
// becomes an array of event stream definitions.

var defaultEventMarks = strSet([]string{"*", "arc", "area", "group", "image", "line", "path", "rect", "rule", "shape", "symbol", "text", "trail"})

type eventParser struct {
	// cc is charged for the selector's parts, by their length: a selector
	// is a string of any length, read a few times over.
	cc            *compileCtx
	defaultSource string
	depth         int // nesting of between selectors being parsed

	// root is the selector, trimmed, and the parsers pass spans of it, so
	// that a position in a span is a position in root, which nextComma and
	// nextClose are keyed by: nextComma[i] is the first ',' at or after i at
	// the same level of "[{" and "]}" as i, and nextClose[i] the first ']' at
	// the level of "[" and "]" (len(root) if none; evFind without the scan).
	// A between selector rescans the rest of the selector for its closing
	// bracket, and its parts for their commas, at every level it nests, so
	// that a selector of any length refused at maxEventNesting levels was
	// read maxEventNesting times over; the index reads it twice.
	root                 string
	nextComma, nextClose []int32
}

// span is the part root[lo:hi] of the selector being parsed.
type span struct{ lo, hi int }

func (p *eventParser) text(sp span) string { return p.root[sp.lo:sp.hi] }

// trim is jsval.JSTrim of sp's text, as a span.
func (p *eventParser) trim(sp span) span {
	t := p.text(sp)
	lead := len(t) - len(jsval.JSTrimStart(t))
	t = jsval.JSTrim(t)
	return span{sp.lo + lead, sp.lo + lead + len(t)}
}

// maxEventNesting bounds how deeply between selectors ("[[a, b] > c, d] > e")
// nest: parseBetween recurses once per level, and the selector is a string of
// any length. Real selectors nest once.
const maxEventNesting = 64

// parseSelectorOf is parseSelector of a spec value, which
// vega-event-selector reads with selector.trim(): a TypeError for anything
// but a string.
func parseSelectorOf(cc *compileCtx, v Value, source string) []Value {
	switch {
	case v.IsStr():
		return parseSelector(cc, v.StrValue(), source)
	case v.IsNullish():
		throw("Cannot read properties of %s (reading 'trim')", nullishName(v))
	}
	throw("selector.trim is not a function")
	return nil
}

func parseSelector(cc *compileCtx, selector, source string) []Value {
	if source == "" {
		source = "view"
	}
	p := &eventParser{cc: cc, defaultSource: source, root: jsval.JSTrim(selector)}
	if len(p.root) >= jsval.MaxLevelText {
		exceeded("Event selector too long")
	}
	p.nextComma = jsval.NextAtLevel(p.root, ',', "[{", "]}")
	p.nextClose = jsval.NextAtLevel(p.root, ']', "[", "]")
	parts := p.parseMerge(span{0, len(p.root)})
	out := make([]Value, len(parts))
	for i, sp := range parts {
		out[i] = p.parseOne(sp)
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

func (p *eventParser) parseMerge(sp span) []span {
	var out []span
	start, i := sp.lo, sp.lo
	for i < sp.hi {
		i = min(int(p.nextComma[i]), sp.hi)
		p.cc.work(1 + int64(i-start)/16)
		out = append(out, p.trim(span{start, i}))
		i++
		start = i
	}
	if len(out) == 0 {
		throw("Empty event selector: %s", p.text(sp))
	}
	return out
}

func (p *eventParser) parseOne(sp span) Value {
	// A part is read a few times over: a step per 4 bytes.
	p.cc.work(4 + int64(sp.hi-sp.lo)/4)
	if sp.lo < sp.hi && p.root[sp.lo] == '[' {
		return p.parseBetween(sp)
	}
	return p.parseStream(p.text(sp))
}

func (p *eventParser) parseBetween(sp span) Value {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxEventNesting {
		exceeded("Event selector nested too deeply: %s", p.text(sp)[:min(sp.hi-sp.lo, 40)])
	}
	i := min(int(p.nextClose[sp.lo+1]), sp.hi)
	if i == sp.hi {
		throw("Empty between selector: %s", p.text(sp))
	}
	b := p.parseMerge(span{sp.lo + 1, i})
	if len(b) != 2 {
		throw("Between selector must have two elements: %s", p.text(sp))
	}
	rest := p.trim(span{i + 1, sp.hi})
	if rest.lo == rest.hi || p.root[rest.lo] != '>' {
		throw("Expected '>' after between selector: %s", p.text(rest))
	}
	between := arr(p.parseOne(b[0]), p.parseOne(b[1]))
	stream := p.parseOne(p.trim(span{rest.lo + 1, rest.hi}))
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
			s = jsval.JSTrim(s[:i])
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
		source = append(source, jsval.JSTrim(s[start:j]))
		j++
		start, i = j, j
	}
	i = evFind(s, i, '[', "", "")
	if i == n {
		source = append(source, jsval.JSTrim(s[start:n]))
	} else {
		source = append(source, jsval.JSTrim(s[start:i]))
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
		filter = append(filter, jsval.Str(jsval.JSTrim(s[start:i])))
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
		case defaultEventMarks[source[0]] || isInheritedKey(source[0]):
			// MARKS[type], a plain object: a name every object inherits
			// ("constructor", "__proto__") is truthy there too (C80 of the
			// 2026-10-18 audit).
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

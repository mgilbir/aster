package vega

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// This file ports vega-event-selector and vega-parser's parsers/stream.js:
// the event streams a signal's `on` handlers listen to. A static render
// never fires a DOM or timer event, so the streams are parsed for what
// upstream's parse does with them, which is to fail where the selector or
// the stream specification is invalid, and to compile their filter
// expressions (which fail on an unknown signal or function as any
// expression does).

// selectorError is the text vega-event-selector throws (a string, not an
// Error, which reaches the caller as it is).
func selectorError(msg string) { perr("%s", msg) }

var defaultMarkTypes = map[string]bool{
	"*": true, "arc": true, "area": true, "group": true, "image": true, "line": true, "path": true,
	"rect": true, "rule": true, "shape": true, "symbol": true, "text": true, "trail": true,
}

// parseEventSelector is vega-event-selector's parse(selector, source): the
// stream definitions an event selector string stands for. source is the
// default event source ("view", or "scope" in a nested scope).
func parseEventSelector(scope *Scope, selector, source string) []jsval.Value {
	return newSelectorParser(scope, selector, source).parse()
}

// selectorFind is event-selector's find: the index of endChar in s from i
// at nesting level zero (pushChar and popChar open and close a level), or
// len(s). The characters it looks for are ASCII, so bytes stand for UTF-16
// units here. The between and merge parsers do not call it on the selector
// (selectorParser.findComma and findClose answer the same question from an
// index); the stream parser does, on one stream's text.
func selectorFind(s string, i int, endChar byte, pushChars, popChars string) int {
	count := 0
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case count == 0 && c == endChar:
			return i
		case popChars != "" && strings.IndexByte(popChars, c) >= 0:
			count--
		case pushChars != "" && strings.IndexByte(pushChars, c) >= 0:
			count++
		}
	}
	return i
}

// span is the part root[lo:hi] of the selector being parsed: the parsers
// pass spans, not substrings, so that a position in a span is a position in
// the whole selector, which the index of selectorParser is keyed by.
type span struct{ lo, hi int }

// selectorParser parses one selector. Upstream's between selector parser
// rescans its whole remainder at every level of nesting for the bracket that
// closes the level (find), and its merge parser rescans the same text for
// the comma that ends a part, so that a selector of n brackets costs n
// squared; here that is bounded only by maxSelectorNesting, which still
// leaves 256 scans of a selector as long as the specification allows before
// the refusal. So both questions are answered from an index built in one
// pass over the selector, and a selector costs the same however it nests.
type selectorParser struct {
	scope  *Scope
	source string
	root   string

	// nextComma[i] is the index of the first ',' at or after i that is at the
	// same nesting level of "[{" and "]}" as i, and len(root) if there is none:
	// selectorFind(root, i, ',', "[{", "]}") without the scan. nextClose is the
	// same for ']' and the level of "[" and "]". Both have len(root)+1 entries.
	nextComma, nextClose []int32

	// scanned counts the bytes the parser looked at to answer those
	// questions, for the complexity guard: a count of work, which a time of
	// it would not be.
	scanned int
}

func newSelectorParser(scope *Scope, selector, source string) *selectorParser {
	p := &selectorParser{scope: scope, source: source, root: jsval.JSTrim(selector)}
	p.buildIndex()
	return p
}

func (p *selectorParser) parse() []jsval.Value {
	parts := p.merge(span{0, len(p.root)})
	out := make([]jsval.Value, len(parts))
	for i, sp := range parts {
		out[i] = p.one(sp, 0)
	}
	return out
}

// buildIndex fills nextComma and nextClose, a pass over the selector each.
func (p *selectorParser) buildIndex() {
	s := p.root
	if len(s) >= jsval.MaxLevelText {
		perrLimit("event selector too long")
	}
	p.scanned += 2 * len(s)
	p.nextComma = jsval.NextAtLevel(s, ',', "[{", "]}")
	p.nextClose = jsval.NextAtLevel(s, ']', "[", "]")
}

// text is the text of sp.
func (p *selectorParser) text(sp span) string { return p.root[sp.lo:sp.hi] }

// trim is jsval.JSTrim of sp's text, as a span.
func (p *selectorParser) trim(sp span) span {
	t := p.text(sp)
	lead := len(t) - len(jsval.JSTrimStart(t))
	t = jsval.JSTrim(t)
	return span{sp.lo + lead, sp.lo + lead + len(t)}
}

// findComma is selectorFind(text(sp), from-sp.lo, ',', "[{", "]}") for a
// from in sp, as a position in the selector: sp.hi where there is none.
func (p *selectorParser) findComma(sp span, from int) int {
	return min(int(p.nextComma[from]), sp.hi)
}

// findClose is selectorFind(text(sp), from-sp.lo, ']', "[", "]"), as findComma.
func (p *selectorParser) findClose(sp span, from int) int {
	return min(int(p.nextClose[from]), sp.hi)
}

func (p *selectorParser) merge(sp span) []span {
	var out []span
	start, i := sp.lo, sp.lo
	for i < sp.hi {
		p.scope.work(1)
		i = p.findComma(sp, i)
		out = append(out, p.trim(span{start, i}))
		i++
		start = i
	}
	if len(out) == 0 {
		selectorError("Empty event selector: " + p.text(sp))
	}
	return out
}

// maxSelectorNesting bounds the recursion of between selectors, which the
// selector's length bounds too.
const maxSelectorNesting = 256

func (p *selectorParser) one(sp span, depth int) jsval.Value {
	if depth > maxSelectorNesting {
		perrLimit("event selector nested too deep")
	}
	p.scope.work(1)
	if sp.lo < sp.hi && p.root[sp.lo] == '[' {
		return p.between(sp, depth)
	}
	return parseSelectorStream(p.text(sp), p.source)
}

func (p *selectorParser) between(sp span, depth int) jsval.Value {
	i := p.findClose(sp, sp.lo+1)
	if i == sp.hi {
		selectorError("Empty between selector: " + p.text(sp))
	}
	b := p.merge(span{sp.lo + 1, i})
	if len(b) != 2 {
		selectorError("Between selector must have two elements: " + p.text(sp))
	}
	rest := p.trim(span{i + 1, sp.hi})
	if rest.lo == rest.hi || p.root[rest.lo] != '>' {
		selectorError("Expected '>' after between selector: " + p.text(rest))
	}
	between := jsval.ArrOf(p.one(b[0], depth+1), p.one(b[1], depth+1))
	stream := p.one(p.trim(span{rest.lo + 1, rest.hi}), depth+1)
	if stream.Get("between").IsTruthy() {
		return obj("between", between, "stream", stream)
	}
	stream.ObjValue().Set("between", between)
	return stream
}

func parseSelectorStream(s, source string) jsval.Value {
	stream := jsval.NewObject(4)
	stream.Set("source", sv(source))
	var parts []string
	throttle := [2]float64{}
	markname := false
	start, i, n := 0, 0, len(s)

	if n > 0 && s[n-1] == '}' {
		i = strings.LastIndexByte(s, '{')
		if i < 0 {
			selectorError("Unmatched right brace: " + s)
		}
		t, ok := parseSelectorThrottle(s[i+1 : n-1])
		if !ok {
			selectorError("Invalid throttle specification: " + s)
		}
		throttle = t
		s = jsval.JSTrim(s[:i])
		n = len(s)
		i = 0
	}
	if n == 0 {
		selectorError(s)
	}
	if s[0] == '@' {
		markname = true
		i = 1
	}
	if j := selectorFind(s, i, ':', "", ""); j < n {
		parts = append(parts, jsval.JSTrim(s[start:j]))
		j++
		start, i = j, j
	}
	var filter []jsval.Value
	hasFilter := false
	i = selectorFind(s, i, '[', "", "")
	if i == n {
		parts = append(parts, jsval.JSTrim(s[start:n]))
	} else {
		parts = append(parts, jsval.JSTrim(s[start:i]))
		hasFilter = true
		i++
		start = i
		if start == n {
			selectorError("Unmatched left bracket: " + s)
		}
	}
	for i < n {
		i = selectorFind(s, i, ']', "", "")
		if i == n {
			selectorError("Unmatched left bracket: " + s)
		}
		filter = append(filter, sv(jsval.JSTrim(s[start:i])))
		if i < n-1 {
			i++
			if s[i] != '[' {
				selectorError("Expected left bracket: " + s)
			}
		}
		i++
		start = i
	}
	if last := parts[len(parts)-1]; strings.ContainsAny(last, "[]{}") {
		selectorError("Invalid event selector: " + s)
	}
	var typ string
	if len(parts) > 1 {
		typ = parts[1]
		switch {
		case markname:
			// source[0].slice(1): the name after the '@'
			stream.Set("markname", sv(strings.TrimPrefix(parts[0], "@")))
		case defaultMarkTypes[parts[0]]:
			stream.Set("marktype", sv(parts[0]))
		default:
			stream.Set("source", sv(parts[0]))
		}
	} else {
		typ = parts[0]
	}
	if strings.HasSuffix(typ, "!") {
		stream.Set("consume", jsval.True)
		typ = typ[:len(typ)-1]
	}
	stream.Set("type", sv(typ))
	if hasFilter {
		stream.Set("filter", jsval.Arr(filter))
	}
	if throttle[0] != 0 && throttle[0] == throttle[0] {
		stream.Set("throttle", jsval.Num(throttle[0]))
	}
	if throttle[1] != 0 && throttle[1] == throttle[1] {
		stream.Set("debounce", jsval.Num(throttle[1]))
	}
	return jsval.Obj(stream)
}

// parseSelectorThrottle is event-selector's parseThrottle: one or two
// comma-separated numbers; ok is false where it throws.
func parseSelectorThrottle(s string) (out [2]float64, ok bool) {
	a := strings.Split(s, ",")
	if s == "" || len(a) > 2 {
		return out, false
	}
	for i, x := range a {
		f := jsval.ToNumber(sv(x))
		if f != f {
			return out, false
		}
		out[i] = f
	}
	return out, true
}

// parseEventStream is stream.js's parseStream: it validates a stream
// specification as upstream's parse does and compiles its filters. Nothing
// is registered: no event fires in a static render.
func parseEventStream(stream jsval.Value, scope *Scope, depth int) {
	if depth > maxSelectorNesting {
		perrLimit("event stream nested too deep")
	}
	scope.work(1)
	switch {
	case specGet(stream, "merge").IsTruthy():
		for _, s := range listOf(specGet(stream, "merge"), "stream.merge", "map") {
			parseEventStream(s, scope, depth+1)
		}
	case specGet(stream, "stream").IsTruthy():
		parseEventStream(specGet(stream, "stream"), scope, depth+1)
	case specGet(stream, "type").IsTruthy():
		if strictIs(specGet(stream, "type"), "timer") {
			stream = obj("between", specGet(stream, "between"), "filter", specGet(stream, "filter"))
		} else {
			viewEventListener(stream, scope)
		}
	default:
		perr("Invalid stream specification: %s", stringValue(stream))
	}
	streamParameters(stream, scope, depth)
}

// streamParameters is stream.js's streamParameters: a between pair is
// parsed, and the filters (with the mark and scope tests a stream adds) are
// compiled as one expression.
func streamParameters(stream jsval.Value, scope *Scope, depth int) {
	if between := specGet(stream, "between"); between.IsTruthy() {
		if n := specIndex(between, "length"); !n.IsNum() || n.NumValue() != 2 {
			perr("Stream \"between\" parameter must have 2 entries: %s", stringValue(stream))
		}
		parseEventStream(specIndex(between, "0"), scope, depth+1)
		parseEventStream(specIndex(between, "1"), scope, depth+1)
	}
	var param []string
	if f := specGet(stream, "filter"); f.IsTruthy() {
		// [].concat(filter), joined: null and undefined as nothing
		for _, x := range arrayOf(f) {
			scope.work(1)
			if x.IsNullish() {
				param = append(param, "")
			} else {
				param = append(param, specText(x))
			}
		}
	}
	marktype, markname, markrole := specGet(stream, "marktype"), specGet(stream, "markname"), specGet(stream, "markrole")
	if marktype.IsTruthy() || markname.IsTruthy() || markrole.IsTruthy() {
		const item = "event.item"
		code := item
		if marktype.IsTruthy() && !strictIs(marktype, "*") {
			code += "&&" + item + ".mark.marktype==='" + specText(marktype) + "'"
		}
		if markrole.IsTruthy() {
			code += "&&" + item + ".mark.role==='" + specText(markrole) + "'"
		}
		if markname.IsTruthy() {
			code += "&&" + item + ".mark.name==='" + specText(markname) + "'"
		}
		param = append(param, code)
	}
	if strictIs(specGet(stream, "source"), "scope") {
		param = append(param, "inScope(event.item)")
	}
	if len(param) > 0 {
		scope.parseExpression("(" + strings.Join(param, ")&&(") + ")")
	}
}

// viewEventListener reproduces what the view does with an event stream of
// the view itself (source "view", "scope" or none) as it is constructed:
// vega-scenegraph's handler registers the listener under the event name
// (the type up to its first '.'), `h[name] || (h[name] = [])` on a plain
// object, so a name that is a member of Object.prototype finds that member:
// a method with parameters is scanned as a list of handlers (its length),
// which reads the type of undefined, and any other has no push. A type that
// is no string or array has no indexOf. The stream never fires in a static render,
// but the view fails before it renders. A sub-scope's streams are registered
// when its context is made, by the dataflow, which logs the error and goes
// on: the scope records the first (flowSpec.streamErr).
func viewEventListener(stream jsval.Value, scope *Scope) {
	msg := viewListenerError(stream)
	switch {
	case msg == "":
	case scope.parentScope == nil:
		perr("%s", msg)
	case scope.streamErr == "":
		scope.streamErr = msg
	}
}

// viewListenerError is the error registering the stream with the view raises
// ("" for none).
func viewListenerError(stream jsval.Value) string {
	src := specGet(stream, "source")
	if src.IsTruthy() && !strictIs(src, "scope") && !strictIs(src, "view") {
		return ""
	}
	t := specGet(stream, "type")
	var name string
	switch {
	case t.IsStr():
		name = t.StrValue()
		if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
	case t.IsArr():
		// an array has indexOf (of an element "." itself) and slice: the
		// name is an array, read as the key String(name)
		items := t.Items()
		for i, it := range items {
			if strictIs(it, ".") {
				items = items[:i]
				break
			}
		}
		name = specText(jsval.Arr(items))
	default:
		return "name.indexOf is not a function"
	}
	if name == "__proto__" {
		return "(h[name] || h[name]).push is not a function"
	}
	f, inherited := jsval.Inherited(name)
	if !inherited {
		return ""
	}
	if n := jsval.Member(f, "length"); n.IsNum() && n.NumValue() > 0 {
		return "Cannot read properties of undefined (reading 'type')"
	}
	return "(h[name] || h[name]).push is not a function"
}

package vegalite

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// refEventParser is the event selector parser as it was before eventParser
// indexed the selector: substrings and a scan with evFind for every
// question. It is the reference the indexed parser must answer like, byte for
// byte, for every input, accepted or refused. It borrows parseStream, which
// the index does not touch.
type refEventParser struct{ eventParser }

func (p *refEventParser) parseMerge(s string) []string {
	var out []string
	n := len(s)
	start, i := 0, 0
	for i < n {
		i = evFind(s, i, ',', "[{", "]}")
		p.cc.work(1 + int64(i-start)/16)
		out = append(out, jsval.JSTrim(s[start:i]))
		i++
		start = i
	}
	if len(out) == 0 {
		throw("Empty event selector: %s", s)
	}
	return out
}

func (p *refEventParser) parseOne(s string) Value {
	// A part is read a few times over: a step per 4 bytes.
	p.cc.work(4 + int64(len(s))/4)
	if len(s) > 0 && s[0] == '[' {
		return p.parseBetween(s)
	}
	return p.eventParser.parseStream(s)
}

func (p *refEventParser) parseBetween(s string) Value {
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
	s = jsval.JSTrim(s[i+1:])
	if len(s) == 0 || s[0] != '>' {
		throw("Expected '>' after between selector: %s", s)
	}
	between := arr(p.parseOne(b[0]), p.parseOne(b[1]))
	stream := p.parseOne(jsval.JSTrim(s[1:]))
	if stream.Get("between").IsTruthy() {
		return mkv("between", between, "stream", stream)
	}
	stream.ObjValue().Set("between", between)
	return stream
}

func refParseSelector(cc *compileCtx, selector, source string) []Value {
	p := &refEventParser{eventParser{cc: cc, defaultSource: source}}
	parts := p.parseMerge(jsval.JSTrim(selector))
	out := make([]Value, len(parts))
	for i, s := range parts {
		out[i] = p.parseOne(s)
	}
	return out
}

// selectorOutcome runs parse and reports what it returned or how it failed,
// as one string.
func selectorOutcome(parse func() []Value) (out string) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case compileError:
				out = fmt.Sprintf("error: %v", e)
			case limitError:
				out = fmt.Sprintf("limit: %v", e)
			default:
				panic(r)
			}
		}
	}()
	var parts []string
	for _, v := range parse() {
		parts = append(parts, string(jsval.AppendJSON(nil, v)))
	}
	return strings.Join(parts, "\n")
}

func checkSelector(t *testing.T, sel string) {
	t.Helper()
	want := selectorOutcome(func() []Value { return refParseSelector(&compileCtx{}, sel, "view") })
	got := selectorOutcome(func() []Value { return parseSelector(&compileCtx{}, sel, "view") })
	if got != want {
		t.Fatalf("selector %q:\n got %s\nwant %s", sel, got, want)
	}
}

// TestEventSelectorMatchesReference compares the indexed parser with the
// scanning one on documented selectors and on random strings of the
// characters that matter to it: unbalanced and mismatched brackets, braces,
// commas, whitespace of several kinds, and nesting past the limit.
func TestEventSelectorMatchesReference(t *testing.T) {
	for _, sel := range []string{
		"click", "mousedown, mouseup", "[mousedown, mouseup] > mousemove", "[mousedown, mouseup] > mousemove{10}",
		"[mousedown[event.x > 5], mouseup] > mousemove", "@name:click[a][b]", "rect:mouseover!", "window:keydown[event.key=='['] ",
		"[[a,b]>c, d] > e", "[a, [b,c]>d]>e", "[a,b]>[c,d]>e", "[a,b] > ", "[a,b]", "[a", "[]", "[,]>x", "", " ", ",", "a,", ",a",
		"click{1,2}", "click{", "click}", "[a,b,c]>d", "\u00a0[a,\ufeffb]\u2028>\tc",
	} {
		checkSelector(t, sel)
	}
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"[", "]", "{", "}", ",", ">", ":", "@", " ", "\u00a0", "a", "b", "!", "1", "'", "x"}
	for range 60000 {
		var b strings.Builder
		for range rng.Intn(24) {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		checkSelector(t, b.String())
	}
	for _, d := range []int{1, 2, maxEventNesting - 1, maxEventNesting, maxEventNesting + 1, 300} {
		checkSelector(t, strings.Repeat("[", d)+"a"+strings.Repeat(", b] > c", d))
		checkSelector(t, strings.Repeat("[a, ", d)+"b"+strings.Repeat("] > c", d))
		checkSelector(t, strings.Repeat("[a,b] > ", d)+"c")
	}
}

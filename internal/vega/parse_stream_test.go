package vega

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// refSelectorMerge, refSelectorOne and refSelectorBetween are the event
// selector parsers as they were before selectorParser indexed the selector:
// substrings and a scan with selectorFind for every question. They are the
// reference the indexed parser must answer like, byte for byte, for every
// input, accepted or refused.
func refSelectorMerge(scope *Scope, s string) []string {
	var out []string
	start, i := 0, 0
	for i < len(s) {
		i = selectorFind(s, i, ',', "[{", "]}")
		out = append(out, jsval.JSTrim(s[start:i]))
		i++
		start = i
	}
	if len(out) == 0 {
		selectorError("Empty event selector: " + s)
	}
	return out
}

func refSelectorOne(scope *Scope, s, source string, depth int) jsval.Value {
	if depth > maxSelectorNesting {
		perrLimit("event selector nested too deep")
	}
	if strings.HasPrefix(s, "[") {
		return refSelectorBetween(scope, s, source, depth)
	}
	return parseSelectorStream(s, source)
}

func refSelectorBetween(scope *Scope, s, source string, depth int) jsval.Value {
	i := selectorFind(s, 1, ']', "[", "]")
	if i == len(s) {
		selectorError("Empty between selector: " + s)
	}
	b := refSelectorMerge(scope, s[1:i])
	if len(b) != 2 {
		selectorError("Between selector must have two elements: " + s)
	}
	s = jsval.JSTrim(s[i+1:])
	if !strings.HasPrefix(s, ">") {
		selectorError("Expected '>' after between selector: " + s)
	}
	between := jsval.ArrOf(refSelectorOne(scope, b[0], source, depth+1), refSelectorOne(scope, b[1], source, depth+1))
	stream := refSelectorOne(scope, jsval.JSTrim(s[1:]), source, depth+1)
	if stream.Get("between").IsTruthy() {
		return obj("between", between, "stream", stream)
	}
	stream.ObjValue().Set("between", between)
	return stream
}

// selectorOutcome runs parse and reports what it returned or how it failed,
// as one string.
func selectorOutcome(parse func() []jsval.Value) (out string) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*parseError)
			if !ok {
				panic(r)
			}
			out = fmt.Sprintf("error(limit=%v): %s", e.limit, e.msg)
		}
	}()
	vs := parse()
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = quote(v)
	}
	return strings.Join(parts, "\n")
}

func refParseEventSelector(scope *Scope, selector, source string) []jsval.Value {
	parts := refSelectorMerge(scope, jsval.JSTrim(selector))
	out := make([]jsval.Value, len(parts))
	for i, p := range parts {
		out[i] = refSelectorOne(scope, p, source, 0)
	}
	return out
}

func checkSelector(t *testing.T, sel string) {
	t.Helper()
	scope := newScope(jsval.Undefined, &parseOptions{})
	want := selectorOutcome(func() []jsval.Value { return refParseEventSelector(scope, sel, "view") })
	got := selectorOutcome(func() []jsval.Value { return parseEventSelector(scope, sel, "view") })
	if got != want {
		t.Fatalf("selector %q:\n got %s\nwant %s", sel, got, want)
	}
}

// TestEventSelectorMatchesReference compares the indexed parser with the
// scanning one on the selectors of Vega's documentation and on random strings
// of the characters that matter to it, in the shapes that exercise the index:
// unbalanced and mismatched brackets, braces, commas, whitespace of several
// kinds, and nesting past the limit.
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
	// Nesting around the limit, balanced, so that the parse goes as deep as
	// the selector does: 256 levels parse, 257 are refused.
	for _, d := range []int{1, 2, 255, 256, 257, 258, 600} {
		checkSelector(t, strings.Repeat("[", d)+"a"+strings.Repeat(", b] > c", d))
		checkSelector(t, strings.Repeat("[a, ", d)+"b"+strings.Repeat("] > c", d))
		checkSelector(t, strings.Repeat("[a,b] > ", d)+"c")
	}
}

// TestEventSelectorWorkIsLinear is the complexity guard of the event
// selector parser: refusing a selector nested far past maxSelectorNesting
// looks at each byte of it a constant number of times, so that doubling the
// selector doubles the count. Before the parser indexed the selector it
// scanned the rest of the text at each of the 257 levels it descended: 100,000
// levels (1.5 MB) took 7.6 s on a development machine and 30 s on a CI runner.
// Counted, not timed (internal/testutil).
func TestEventSelectorWorkIsLinear(t *testing.T) {
	count := func(depth int) int {
		sel := strings.Repeat("[", depth) + "mousedown" + strings.Repeat(", mouseup] > mousemove", depth)
		scope := newScope(jsval.Undefined, &parseOptions{})
		p := newSelectorParser(scope, sel, "view")
		got := selectorOutcome(p.parse)
		if want := "error(limit=true): event selector nested too deep"; got != want {
			t.Fatalf("depth %d: got %q, want %q", depth, got, want)
		}
		return p.scanned
	}
	n1, n2 := count(10000), count(20000)
	t.Logf("scanned %d bytes at 10,000 levels, %d at 20,000", n1, n2)
	if float64(n2) > 2.2*float64(n1) {
		t.Errorf("scanned %d bytes at twice the depth of a selector that took %d: more than linear", n2, n1)
	}
	// Linear is also small: a few passes over the text, not 257 of them.
	if text := 10000*len("[")*1 + len("mousedown") + 10000*len(", mouseup] > mousemove"); n1 > 8*text {
		t.Errorf("scanned %d bytes of a %d-byte selector: want a few passes", n1, text)
	}
}

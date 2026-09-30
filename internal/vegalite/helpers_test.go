package vegalite

import (
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func loadHelperVectors(t *testing.T) jsval.Value {
	t.Helper()
	data, err := os.ReadFile("testdata/helpers.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsval.ParseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseSelectorMatchesUpstream(t *testing.T) {
	for _, c := range loadHelperVectors(t).Get("selectors").Items() {
		got := jsval.Arr(parseSelector(c.Get("input").StrValue(), c.Get("source").StrValue()))
		want := c.Get("output")
		if string(jsval.AppendJSON(nil, got)) != string(jsval.AppendJSON(nil, want)) {
			t.Errorf("parseSelector(%q, %q)\n got %s\nwant %s", c.Get("input").StrValue(), c.Get("source").StrValue(),
				jsval.AppendJSON(nil, got), jsval.AppendJSON(nil, want))
		}
	}
}

func TestDependentFieldsMatchUpstream(t *testing.T) {
	for _, c := range loadHelperVectors(t).Get("expressions").Items() {
		in := c.Get("input").StrValue()
		got := getDependentFields(in).list()
		var want []string
		for _, d := range c.Get("deps").Items() {
			want = append(want, d.AsString())
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("getDependentFields(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEscapeMatchesUpstream(t *testing.T) {
	for _, c := range loadHelperVectors(t).Get("escapes").Items() {
		if got := jsEscape(c.Get("input").StrValue()); got != c.Get("output").StrValue() {
			t.Errorf("jsEscape(%q) = %q, want %q", c.Get("input").StrValue(), got, c.Get("output").StrValue())
		}
	}
}

func TestExpressionSyntaxErrors(t *testing.T) {
	for _, e := range []string{"datum.", "1 +", "(1", "a b", "'abc", "function(){}", "a = 1", "new Foo()", "this.x", "datum[", "1,2", "a ? b", "{a:}", "@"} {
		if _, err := parseExprAST(e); err == nil {
			t.Errorf("parseExprAST(%q) should fail", e)
		}
	}
	for _, e := range []string{"if(a, 1, 2)", "datum.a", "1", "/ab+c/g.test(datum.x)", "a[1][2]", "-a", "!a", "a?b:c"} {
		if _, err := parseExprAST(e); err != nil {
			t.Errorf("parseExprAST(%q): %v", e, err)
		}
	}
}

func TestJSKeyOrder(t *testing.T) {
	o := mk("b", 1, "2", 2, "a", 3, "1", 4, "10", 5, "01", 6)
	o2 := jsval.NewObject(4)
	spread(o2, jsval.Obj(o))
	got := strings.Join(o2.Keys(), ",")
	if got != "1,2,10,b,a,01" {
		t.Errorf("spread key order = %s", got)
	}
	c := deepClone(jsval.Obj(o)).ObjValue()
	if strings.Join(c.Keys(), ",") != "1,2,10,b,a,01" {
		t.Errorf("deepClone key order = %v", c.Keys())
	}
	m := newOmap[int]()
	for _, k := range []string{"x", "3", "y", "1"} {
		m.set(k, 0)
	}
	if strings.Join(m.keys, ",") != "1,3,x,y" {
		t.Errorf("omap key order = %v", m.keys)
	}
}

func TestStringValue(t *testing.T) {
	for _, c := range []struct {
		in   jsval.Value
		want string
	}{
		{jsval.Str("a\"b"), `"a\"b"`},
		{arr("a", 1, jsval.Null), `["a",1,null]`},
		{jsval.Num(0.5), "0.5"},
		{jsval.True, "true"},
		{jsval.Undefined, "undefined"},
		{mkv("k", "v"), `{"k":"v"}`},
		{jsval.Str(string(rune(0x2028))), `"` + `\` + `u2028"`},
	} {
		if got := stringValue(c.in); got != c.want {
			t.Errorf("stringValue(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestMergeConfig(t *testing.T) {
	a := mustParse(t, `{"legend": {"layout": {"a": 1, "b": 2}, "x": 1}, "style": {"s": {"p": 1}}, "axis": {"x": 1}, "signals": [{"name": "s", "value": 1}]}`)
	b := mustParse(t, `{"legend": {"layout": {"a": 9}, "y": 2}, "style": {"s": {"q": 2}}, "axis": {"y": 2}, "signals": [{"name": "s", "value": 2}, {"name": "t"}]}`)
	got := string(jsval.AppendJSON(nil, jsval.Obj(mergeConfig(a, b))))
	want := `{"legend":{"layout":{"a":9,"b":2},"x":1,"y":2},"style":{"s":{"p":1,"q":2}},"axis":{"y":2},"signals":[{"name":"s","value":2},{"name":"t"},{"name":"s","value":1}]}`
	// Signals: b's entries first, a's added when the name is not present.
	want = strings.Replace(want, `{"name":"s","value":2},{"name":"t"},{"name":"s","value":1}`, `{"name":"s","value":2},{"name":"t"}`, 1)
	want = strings.Replace(want, `"axis":{"y":2}`, `"axis":{"x":1,"y":2}`, 1)
	if got != want {
		t.Errorf("mergeConfig\n got %s\nwant %s", got, want)
	}
	// __proto__ keys are ignored.
	c := mustParse(t, `{"__proto__": {"polluted": true}, "constructor": 1, "ok": 1}`)
	if g := string(jsval.AppendJSON(nil, jsval.Obj(mergeConfig(c)))); g != `{"ok":1}` {
		t.Errorf("illegal keys not dropped: %s", g)
	}
}

func TestOptionsConfigIsMerged(t *testing.T) {
	spec := mustParse(t, `{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`)
	cfg := mustParse(t, `{"background":"red","view":{"continuousWidth":123}}`)
	out, err := Compile(spec, Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if out.Get("background").AsString() != "red" || out.Get("width").NumValue() != 123 {
		t.Errorf("config not applied: %s", jsval.AppendJSON(nil, out))
	}
	// The spec's own config wins over Options.Config.
	spec2 := mustParse(t, `{"config":{"background":"blue"},"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`)
	out2, _ := Compile(spec2, Options{Config: cfg})
	if out2.Get("background").AsString() != "blue" {
		t.Errorf("spec config should win: %s", out2.Get("background"))
	}
}

func TestCompileDoesNotMutateInput(t *testing.T) {
	spec := mustParse(t, `{"data":{"values":[{"a":1,"b":2}]},"transform":[{"filter":{"field":"a","equal":1}}],"mark":"bar","params":[{"name":"p","select":"interval"}],"encoding":{"x":{"field":"a","type":"ordinal","sort":"-y"},"y":{"field":"b","type":"quantitative","aggregate":"sum"}}}`)
	before := string(jsval.AppendJSON(nil, spec))
	if _, err := Compile(spec, Options{}); err != nil {
		t.Fatal(err)
	}
	if after := string(jsval.AppendJSON(nil, spec)); after != before {
		t.Errorf("input mutated:\n%s\n%s", before, after)
	}
}

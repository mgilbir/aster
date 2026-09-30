package jsval

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
)

type numberVector struct {
	V     string            `json:"v"`
	S     string            `json:"s"`
	Fixed map[string]string `json:"fixed"`
	Prec  map[string]string `json:"prec"`
	Exp   map[string]string `json:"exp"`
}

// TestNumberFormattingMatchesJavaScript checks the formatters against vectors
// recorded from node (testdata/gen_numbers.js).
func TestNumberFormattingMatchesJavaScript(t *testing.T) {
	raw, err := os.ReadFile("testdata/numbers.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Numbers  []numberVector    `json:"numbers"`
		ToNumber map[string]string `json:"tonumber"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	for _, vec := range data.Numbers {
		f := math.Copysign(0, -1)
		if vec.V != "-0" {
			f, err = strconv.ParseFloat(vec.V, 64)
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := JSNumberString(f); got != vec.S {
			t.Errorf("String(%s) = %q, want %q", vec.V, got, vec.S)
		}
		for k, want := range vec.Fixed {
			d, _ := strconv.Atoi(k)
			if got := ToFixed(f, d); got != want {
				t.Errorf("(%s).toFixed(%d) = %q, want %q", vec.V, d, got, want)
			}
		}
		for k, want := range vec.Prec {
			p, _ := strconv.Atoi(k)
			if got := ToPrecision(f, p); got != want {
				t.Errorf("(%s).toPrecision(%d) = %q, want %q", vec.V, p, got, want)
			}
		}
		for k, want := range vec.Exp {
			d, _ := strconv.Atoi(k)
			if got := ToExponential(f, d); got != want {
				t.Errorf("(%s).toExponential(%d) = %q, want %q", vec.V, d, got, want)
			}
		}
	}
	for s, want := range data.ToNumber {
		if got := JSNumberString(StringToNumber(s)); got != want {
			t.Errorf("Number(%q) = %s, want %s", s, got, want)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	in := `{"b":1,"a":[true,false,null,"xé😀\"\n",1e21,-0.5,{}],"b":2,"<":"&"}`
	v, err := ParseJSONString(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(AppendJSON(nil, v))
	want := `{"b":2,"a":[true,false,null,"xé😀\"\n",1e+21,-0.5,{}],"<":"&"}`
	if got != want {
		t.Errorf("round trip:\n got %s\nwant %s", got, want)
	}
	indented := string(AppendJSONIndent(nil, ArrOf(Int(1), Obj(ObjectOf("k", Str("v")))), "  "))
	if want := "[\n  1,\n  {\n    \"k\": \"v\"\n  }\n]"; indented != want {
		t.Errorf("indent:\n got %s\nwant %s", indented, want)
	}
}

func TestJSONRejectsWhatJSONParseRejects(t *testing.T) {
	for _, in := range []string{``, `{`, `[1,]`, `{"a":1,}`, `01`, `1.`, `.5`, `NaN`, `+1`, `"\x"`, "\"a\tb\"", `{a:1}`, `[1] x`, "\ufeff1"} {
		if _, err := ParseJSONString(in); err == nil {
			t.Errorf("ParseJSON(%q) succeeded, want error", in)
		}
	}
}

func TestJSONDepthLimit(t *testing.T) {
	deep := make([]byte, 0, 2*(MaxJSONDepth+1))
	for i := 0; i <= MaxJSONDepth; i++ {
		deep = append(deep, '[')
	}
	for i := 0; i <= MaxJSONDepth; i++ {
		deep = append(deep, ']')
	}
	if _, err := ParseJSON(deep); err == nil {
		t.Fatal("expected depth error")
	}
	if _, err := ParseJSON(deep[1 : len(deep)-1]); err != nil {
		t.Fatalf("depth %d should parse: %v", MaxJSONDepth, err)
	}
}

func TestFieldPath(t *testing.T) {
	cases := map[string][]string{
		"a":          {"a"},
		"a.b":        {"a", "b"},
		"a[0]":       {"a", "0"},
		"a['b.c']":   {"a", "b.c"},
		`a\.b`:       {"a.b"},
		"a[\"x\"].y": {"a", "x", "y"},
		"a[b":        {"a", "[b"},
	}
	for in, want := range cases {
		got := ParseFieldPath(in)
		if len(got) != len(want) {
			t.Errorf("%q: got %q want %q", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%q: got %q want %q", in, got, want)
			}
		}
	}
}

func BenchmarkParseJSON(b *testing.B) {
	doc := []byte(`{"data":{"values":[{"a":"A","b":28},{"a":"B","b":55},{"a":"C","b":43.5}]},"mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ParseJSON(doc); err != nil {
			b.Fatal(err)
		}
	}
}

func TestObjectDeleteKeepsIndexConsistent(t *testing.T) {
	o := NewObject(0)
	for i := 0; i < 40; i++ {
		o.Set(strconv.Itoa(i), Int(i))
	}
	for _, k := range []string{"0", "17", "39", "5"} {
		o.Delete(k)
	}
	if o.Len() != 36 {
		t.Fatalf("len %d", o.Len())
	}
	for i := 0; i < o.Len(); i++ {
		k := o.KeyAt(i)
		v, ok := o.Get(k)
		if !ok || v.AsString() != k {
			t.Fatalf("key %q -> %v %v", k, v, ok)
		}
	}
	if o.Has("17") {
		t.Fatal("deleted key still present")
	}
}

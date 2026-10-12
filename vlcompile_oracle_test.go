package aster_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/vegalite"
)

// compileAgainstNode compiles spec with both Vega-Lite versions, here and in
// node, and fails unless both succeed with byte-identical Vega (key order
// included) or both fail.
func compileAgainstNode(t *testing.T, name, spec string) {
	t.Helper()
	for _, v := range []struct{ set, version string }{{oracle.VL6, "6.4"}, {oracle.VL5, "5.8"}} {
		o := oracle.For(t, v.set)
		c, err := aster.New(aster.WithVegaLiteVersion(v.version))
		if err != nil {
			t.Fatal(err)
		}
		want, err := o.Compile([]byte(spec))
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		got, gerr := c.VegaLiteToVega([]byte(spec))
		c.Close()
		switch {
		case gerr != nil && want.Err != "":
			continue
		case gerr != nil:
			t.Errorf("%s (VL %s): engine error %v; node compiles", name, v.version, gerr)
		case want.Err != "":
			t.Errorf("%s (VL %s): node errors (%s); engine compiles", name, v.version, firstLine(want.Err))
		default:
			w, err := jsval.ParseJSON(want.Vega)
			if err != nil {
				t.Fatal(err)
			}
			if wb := jsval.AppendJSON(nil, w); string(wb) != string(got) {
				t.Errorf("%s (VL %s): compiled Vega differs from node's", name, v.version)
			}
		}
	}
}

func joinN(n int, f func(i int) string, sep string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = f(i)
	}
	return strings.Join(parts, sep)
}

// TestLongExpressionChainsCompileAsNode: the expressions a long sort array,
// a long `or` and a long sum compile to are chains, which upstream's parser
// takes at any length; the field-dependency parser once refused them past 200
// (C10).
func TestLongExpressionChainsCompileAsNode(t *testing.T) {
	data := `{"values":[{"a":"c1","b":2}]}`
	q := func(s string) string { return fmt.Sprintf("%q", s) }
	for _, n := range []int{150, 250, 1000} {
		compileAgainstNode(t, fmt.Sprintf("sort-%d", n), `{"data":`+data+`,"mark":"bar","encoding":{"x":{"field":"a","type":"nominal","sort":[`+
			joinN(n, func(i int) string { return q(fmt.Sprintf("c%d", i)) }, ",")+`]},"y":{"field":"b","type":"quantitative"}}}`)
		compileAgainstNode(t, fmt.Sprintf("or-%d", n), `{"data":`+data+`,"mark":"point","transform":[{"filter":{"or":[`+
			joinN(n, func(i int) string { return fmt.Sprintf(`{"field":"f%d","equal":%d}`, i, i) }, ",")+`]}}],"encoding":{"x":{"field":"b","type":"quantitative"}}}`)
		compileAgainstNode(t, fmt.Sprintf("sum-%d", n), `{"data":`+data+`,"mark":"point","transform":[{"calculate":`+
			q(joinN(n, func(i int) string { return fmt.Sprintf("datum.f%d", i) }, " + "))+`,"as":"s"}],"encoding":{"x":{"field":"s","type":"quantitative"}}}`)
		compileAgainstNode(t, fmt.Sprintf("ternary-%d", n), `{"data":`+data+`,"mark":"point","transform":[{"calculate":`+
			q(joinN(n, func(i int) string { return fmt.Sprintf("datum.f%d ? %d", i, i) }, " : ")+" : datum.z")+`,"as":"s"}],"encoding":{"x":{"field":"s","type":"quantitative"}}}`)
	}
}

// TestExprDepthMatchesVega: the Vega-Lite compiler's expression parser
// refuses nesting where Vega's parser does, so a specification it accepts is
// not refused later for depth by a different count.
func TestExprDepthMatchesVega(t *testing.T) {
	if vegalite.MaxExprDepth != expr.MaxDepth {
		t.Errorf("vegalite.MaxExprDepth = %d, expr.MaxDepth = %d", vegalite.MaxExprDepth, expr.MaxDepth)
	}
}

// TestInvalidStructureMatchesNode compiles specifications with values of the
// wrong type in their structural slots (composition lists, repeat, transform,
// params, field type, channel definitions, the legacy `in` predicate) with
// both Vega-Lite versions: where node throws the engine must fail too, and
// where node compiles the engine must produce the same Vega (C49, C50,
// ADR 0013).
func TestInvalidStructureMatchesNode(t *testing.T) {
	for _, spec := range []string{
		`{"data":{"values":[{"a":1}]},"layer":{}}`,
		`{"data":{"values":[{"a":1}]},"layer":"x"}`,
		`{"data":{"values":[{"a":1}]},"layer":1}`,
		`{"data":{"values":[{"a":1}]},"vconcat":"x"}`,
		`{"data":{"values":[{"a":1}]},"hconcat":{}}`,
		`{"data":{"values":[{"a":1}]},"concat":3}`,
		`{"data":{"values":[{"a":1}]},"repeat":"x","spec":{"mark":"point"}}`,
		`{"data":{"values":[{"a":1}]},"repeat":{"row":"x"},"spec":{"mark":"point"}}`,
		`{"data":{"values":[{"a":1}]},"repeat":{"row":{}},"spec":{"mark":"point"}}`,
		`{"data":{"values":[{"a":1}]},"repeat":{"layer":"x"},"spec":{"mark":"point"}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","transform":{}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","transform":"x"}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","transform":[1]}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","transform":[null]}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","params":1}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","params":{}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","params":"x"}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","params":[1]}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":0}}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"bogus"}}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":{}}}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":1}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":[]}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":"a"}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":null}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"tooltip":5}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"detail":"x"}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":1}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":[]}`,
		`{"data":{"values":[{"a":1,"b":"x"}]},"mark":"point","transform":[{"filter":{"field":"b","in":["x"]}}],"encoding":{"x":{"field":"a","type":"quantitative"}}}`,
		`{"data":{"values":[{"a":1,"b":"x"}]},"mark":"point","transform":[{"filter":{"field":"b","oneOf":["x"],"in":["y"]}}],"encoding":{"x":{"field":"a","type":"quantitative"}}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","aggregate":"avg"}}}`,
		`{"data":{"values":[{"a":1}]},"mark":"point","transform":[{"calcualte":"1","as":"b"}]}`,
		`{"data":{"values":[{"a":1}]},"layer":[1]}`,
		`{"data":{"values":[{"a":1}]},"layer":[null]}`,
		`{"data":{"values":[{"a":1}]},"hconcat":[1]}`,
		`{"data":{"values":[{"a":1}]},"facet":{"row":{"field":"a"}},"spec":"x"}`,
		`{"data":{"values":[{"a":1}]},"facet":"x","spec":{"mark":"point"}}`,
	} {
		compileAgainstNode(t, spec, spec)
	}
}

// TestSmallFidelityMatchesNode (C104): a date parse pattern that is a lone
// quote, a datetime part of null, an autosize that is not an object or a
// string, and a config whose signals are not an array, as node compiles them.
func TestSmallFidelityMatchesNode(t *testing.T) {
	d := `"data":{"values":[{"a":"2020-01-01","b":2}]`
	for _, spec := range []string{
		`{` + d + `,"format":{"parse":{"a":"date:'"}}},"mark":"point","encoding":{"x":{"field":"a","type":"temporal"}}}`,
		`{` + d + `,"format":{"parse":{"a":"utc:\""}}},"mark":"point","encoding":{"x":{"field":"a","type":"temporal"}}}`,
		`{` + d + `},"mark":"point","transform":[{"filter":{"field":"b","range":[{"year":2000,"date":null},5]}}],"encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		`{` + d + `},"mark":"point","transform":[{"filter":{"field":"b","equal":{"year":null,"month":2}}}],"encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		`{` + d + `},"mark":"point","encoding":{"x":{"field":"b","type":"quantitative","scale":{"domain":[{"year":2000,"hours":null},{"year":2001}]}}}}`,
		`{` + d + `},"autosize":5,"mark":"point","encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		`{` + d + `},"autosize":true,"mark":"point","encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		`{` + d + `},"autosize":[1],"mark":"point","encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		`{` + d + `},"autosize":null,"mark":"point","encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		`{` + d + `},"config":{"signals":"x","params":[{"name":"p","value":1}]},"mark":"point","encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		`{` + d + `},"config":{"signals":{"a":1},"params":[{"name":"p","value":1}]},"mark":"point","encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		`{` + d + `},"config":{"signals":5,"params":[{"name":"p","value":1}]},"mark":"point","encoding":{"x":{"field":"b","type":"quantitative"}}}`,
		// X1: datetime selection values JavaScript's Date reads as NaN, or
		// with fractional and huge parts.
		`{` + d + `},"mark":"point","params":[{"name":"s","select":{"type":"point","fields":["a"]},"value":[{"a":{"year":1e20}},{"a":{"year":2001.7,"month":13.5,"date":-3,"hours":1e10}},{"a":{"year":99,"utc":true}},{"a":{"year":"NaN"}}]}],"encoding":{"x":{"field":"b","type":"quantitative"}}}`,
	} {
		compileAgainstNode(t, spec, spec)
	}
}

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

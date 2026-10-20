package scale

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestInvertRangeComparesAsJavaScript is C169 (audit 2026-10-18): a
// quantize scale's invertRange compares range values with JavaScript's < and
// >, which order strings by UTF-16 code unit (U+10000 is below U+E000) and
// arrays by their text, as vega-scale does (node 24.21.0's answers).
func TestInvertRangeComparesAsJavaScript(t *testing.T) {
	s, _ := New("quantize")
	s.SetDomain([]jsval.Value{jsval.Num(0), jsval.Num(3)})
	s.SetRange([]jsval.Value{jsval.Str("a"), jsval.Str(""), jsval.Str("\U00010000")})
	for _, c := range []struct {
		lo, hi jsval.Value
		want   string
	}{
		{jsval.Str(""), jsval.Str("￿"), "[1,2]"},
		{jsval.Str("\U00010000"), jsval.Str(""), "[1,3]"},
		{jsval.Str("b"), jsval.Str(""), "[1,3]"},
		{jsval.ArrOf(jsval.Num(1)), jsval.ArrOf(jsval.Num(2)), "undefined"},
		{jsval.Str("퟿"), jsval.Str(""), "[1,3]"},
	} {
		got, ok := InvertRangeExtent(s.(*Quantize), c.lo, c.hi)
		g := "undefined"
		if ok {
			g = string(jsval.Walker{}.AppendJSON(nil, got))
		}
		if g != c.want {
			t.Errorf("invertRange([%v, %v]) = %s; vega-scale %s", c.lo, c.hi, g, c.want)
		}
	}
}

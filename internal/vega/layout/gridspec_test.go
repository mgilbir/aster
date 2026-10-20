package layout

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// A boolean columns is the number it reads as (Array(true) is no
// RangeError, `i += true` adds): only text, lists and objects take the
// concatenating path of gridLayoutExact.
func TestGridSpecColumnsKinds(t *testing.T) {
	for _, c := range []struct {
		v       jsval.Value
		raw     bool
		columns int
		invalid bool
	}{
		{jsval.True, false, 1, false},
		{jsval.Num(3), false, 3, false},
		{jsval.Num(1.5), false, 0, true},
		{jsval.Num(-2), false, 0, true},
		{jsval.Str("3"), true, 0, false},
		{jsval.Str("1.5"), true, 0, false},
		{jsval.ArrOf(jsval.Num(3)), true, 0, false},
	} {
		g := ParseGridSpec(jsval.Obj(jsval.ObjectOf("columns", c.v)))
		if raw := !g.columnsRaw.IsUndefined(); raw != c.raw || g.columns != c.columns || g.invalid != c.invalid {
			t.Errorf("columns %s: raw %v columns %d invalid %v, want %v %d %v", c.v, raw, g.columns, g.invalid, c.raw, c.columns, c.invalid)
		}
	}
}

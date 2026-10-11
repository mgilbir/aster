package vega

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// ResolveFilter's `~(_.ignore || 0)`; the expected masks are node's
// `(~(x || 0)) >>> 0`.
func TestResolveFilterIgnoreMask(t *testing.T) {
	for _, tc := range []struct {
		v    jsval.Value
		want uint32
	}{
		{jsval.Num(0), 4294967295},
		{jsval.Num(1), 4294967294},
		{jsval.Num(-1), 0},
		{jsval.Num(3), 4294967292},
		{jsval.Num(4294967297), 4294967294},
		{jsval.Num(1e20), 2632974335},
		{jsval.Num(-1e20), 1661992959},
		{jsval.Num(1<<40 + 5), 4294967290},
		{jsval.Num(math.NaN()), 4294967295},
		{jsval.Str("x"), 4294967295},
	} {
		if got := ignoreMask(tc.v); got != tc.want {
			t.Errorf("ignoreMask(%v) = %d, want %d", tc.v, got, tc.want)
		}
	}
}

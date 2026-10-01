package scene

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// A position given a word is `item.x || 0` for the renderer: printed as the
// word in a transform, and NaN once a path generator applies the unary plus.
func TestPositionGivenAWord(t *testing.T) {
	it := &Item{}
	if _, err := it.Set("x", jsval.Str("grey")); err != nil {
		t.Fatal(err)
	}
	if got := string(it.AppendPos(nil, "x")); got != "grey" {
		t.Errorf("transform text %q", got)
	}
	if !math.IsNaN(it.OrZero("x")) {
		t.Errorf("path value %v", it.OrZero("x"))
	}
	for _, v := range []jsval.Value{jsval.Str(""), jsval.Num(math.NaN()), jsval.Null, jsval.Bool(false)} {
		if _, err := it.Set("x", v); err != nil {
			t.Fatal(err)
		}
		if got := string(it.AppendPos(nil, "x")); got != "0" || it.OrZero("x") != 0 {
			t.Errorf("%v: %q %v", v, got, it.OrZero("x"))
		}
	}
	if _, err := it.Set("x", jsval.Num(12.5)); err != nil {
		t.Fatal(err)
	}
	if got := string(it.AppendPos(nil, "x")); got != "12.5" {
		t.Errorf("number: %q", got)
	}
}

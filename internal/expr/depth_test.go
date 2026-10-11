package expr

import (
	"errors"
	"runtime/debug"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// String(v) of an array nested as deep as a run-time value can be must stop
// at jsval.MaxValueDepth, not recurse until the goroutine stack overflows. V8
// throws its RangeError only some ten thousand frames deep, so the bound is
// the engine's: an error wrapping ErrLimit, which no expression catches (C43).
func TestStringOfDeepArrayThrows(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	deep := jsval.Num(1)
	for i := 0; i < 2_000_000; i++ {
		deep = jsval.ArrOf(deep, jsval.Num(2))
	}
	for _, src := range []string{"'' + datum", "datum == 'x'", "datum + 1"} {
		p, err := Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		s := NewScope(nil)
		s.Datum = deep
		if _, err := p.Eval(s); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%s: want ErrLimit, got %v", src, err)
		}
	}
	// Within the bound the string form is unchanged.
	shallow := jsval.ArrOf(jsval.ArrOf(jsval.Num(1), jsval.Num(2)), jsval.Num(3))
	p, _ := Compile("'' + datum")
	s := NewScope(nil)
	s.Datum = shallow
	if v, err := p.Eval(s); err != nil || v.StrValue() != "1,2,3" {
		t.Errorf("shallow: %v %v", v, err)
	}
}

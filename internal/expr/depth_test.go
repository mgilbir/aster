package expr

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// String(v) of an array nested as deep as a run-time value can be must throw
// the RangeError V8 throws, not recurse until the goroutine stack overflows.
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
		if _, err := p.Eval(s); err == nil || !strings.Contains(err.Error(), "call stack") {
			t.Errorf("%s: want a RangeError, got %v", src, err)
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

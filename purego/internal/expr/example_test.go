package expr_test

import (
	"fmt"

	"github.com/mgilbir/aster/purego/internal/expr"
	"github.com/mgilbir/aster/purego/internal/jsval"
)

type signals map[string]jsval.Value

func (s signals) Signal(name string) (jsval.Value, bool) { v, ok := s[name]; return v, ok }

func Example() {
	prog, err := expr.Compile("datum.value > threshold ? upper(datum.name) : 'small'")
	if err != nil {
		panic(err)
	}
	fmt.Println(prog.Deps().Signals, prog.Deps().Fields)

	scope := expr.NewScope(signals{"threshold": jsval.Num(10)})
	for _, v := range []float64{5, 50} {
		scope.Datum = jsval.Obj(jsval.ObjectOf("value", jsval.Num(v), "name", jsval.Str("big")))
		out, _ := prog.Eval(scope)
		fmt.Println(out)
	}

	// A JavaScript exception is an error, not a panic.
	bad, _ := expr.Compile("datum.a.b")
	scope.Datum = jsval.Obj(jsval.NewObject(0))
	_, err = bad.Eval(scope)
	fmt.Println(err)
	// Output:
	// [threshold] [value name]
	// "small"
	// "BIG"
	// TypeError: Cannot read properties of undefined (reading 'b')
}

package expr

import "testing"

// A string literal named __proto__ is an identifier like the other inherited
// names, but the codegen's `globals[id] = 1` sets the prototype of a plain
// object instead of recording the name: no signal is referenced, and the
// identifier reads as undefined. Every other inherited name is a signal.
func TestProtoIdentifierReferencesNoSignal(t *testing.T) {
	p, err := Compile(`'__proto__'`)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Deps().Signals; len(got) != 0 {
		t.Errorf("signals %v", got)
	}
	v, err := p.Eval(NewScope(nil))
	if err != nil || !v.IsUndefined() {
		t.Errorf("value %v, err %v", v, err)
	}
	p, err = Compile(`'toString'`)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Deps().Signals; len(got) != 1 || got[0] != "toString" {
		t.Errorf("signals %v", got)
	}
}

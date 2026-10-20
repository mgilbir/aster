package expr

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

type storeEnv map[string]jsval.Value

func (e storeEnv) Signal(string) (jsval.Value, bool)    { return jsval.Undefined, false }
func (e storeEnv) Data(name string) (jsval.Value, bool) { v, ok := e[name]; return v, ok }

// C102 (audit 2026-10-18): vlSelectionResolve's union and intersection of
// _vgsid_ ids are d3's InternSet: ids by identity, a Date as its time value,
// -0 as 0. node 24 with vega 6.4.0, on this store (two units: {a:1}, {a:1},
// [1,2] in u; [1,2], datetime(5), 5, -0 in v, each object and array its
// own), gives a union of 6 ids and an intersection of none.
func TestSelectionResolveInternsByIdentity(t *testing.T) {
	row := func(unit string, id jsval.Value) jsval.Value {
		return jsval.Obj(jsval.ObjectOf("unit", jsval.Str(unit), "_vgsid_", id))
	}
	obj := func() jsval.Value { return jsval.Obj(jsval.ObjectOf("a", jsval.Num(1))) }
	arr := func() jsval.Value { return jsval.ArrOf(jsval.Num(1), jsval.Num(2)) }
	store := jsval.ArrOf(row("u", obj()), row("u", obj()), row("u", arr()),
		row("v", arr()), row("v", jsval.Timestamp(5)), row("v", jsval.Num(5)), row("v", jsval.Num(math.Copysign(0, -1))))
	env := storeEnv{"s": store}
	if got := evalWith(t, env, "length(vlSelectionResolve('s')._vgsid_)"); got.NumValue() != 6 {
		t.Errorf("union of %d ids, want 6", int(got.NumValue()))
	}
	if got := evalWith(t, env, "length(vlSelectionResolve('s', 'intersect')._vgsid_)"); got.NumValue() != 0 {
		t.Errorf("intersection of %d ids, want 0", int(got.NumValue()))
	}
	// The same array in both units is one id, in both.
	shared := arr()
	env = storeEnv{"s": jsval.ArrOf(row("u", shared), row("v", shared), row("v", jsval.Num(1)))}
	if got := evalWith(t, env, "length(vlSelectionResolve('s', 'intersect')._vgsid_)"); got.NumValue() != 1 {
		t.Errorf("intersection of %d ids, want 1", int(got.NumValue()))
	}
}

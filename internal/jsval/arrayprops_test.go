package jsval

import "testing"

// An array a tuple's fields are written into (Fields) keeps them as named
// own properties, as a JavaScript array does: a member read finds them before
// Array.prototype's, Get reads them, and the array's length, String() and
// JSON.stringify ignore them. A copy copies them with the tuple id.
func TestArrayNamedProperties(t *testing.T) {
	a := ArrOf(Num(1), Num(2))
	if Tuple(a) != nil {
		t.Fatal("a fresh array has named properties")
	}
	f := Fields(a)
	f.Set("startAngle", Num(0.5))
	f.Set("map", Num(7))
	f.EnsureTupleID()
	if got := Member(a, "startAngle"); !got.IsNum() || got.NumValue() != 0.5 {
		t.Errorf("Member startAngle = %v", got)
	}
	if got := Member(a, "map"); !got.IsNum() || got.NumValue() != 7 {
		t.Errorf("an own field does not shadow Array.prototype.map: %v", got)
	}
	if got := Member(a, "filter"); !IsFunction(got) {
		t.Errorf("Array.prototype.filter = %v", got)
	}
	if got := a.Get("startAngle"); got.NumValue() != 0.5 {
		t.Errorf("Get = %v", got)
	}
	if got := Member(a, "length"); got.NumValue() != 2 {
		t.Errorf("length = %v", got)
	}
	if got := a.AsString(); got != "1,2" {
		t.Errorf("String = %q", got)
	}
	if got := string(AppendJSON(nil, a)); got != "[1,2]" {
		t.Errorf("JSON = %s", got)
	}
	c := CopyArray(a)
	if Tuple(c) == nil || Tuple(c) == f || Tuple(c).TupleID() != f.TupleID() || c.Get("map").NumValue() != 7 {
		t.Error("a copy does not carry a copy of the fields and the id")
	}
	if RowCost(a) <= RowCost(ArrOf(Num(1), Num(2))) {
		t.Error("the fields of an array row are not charged")
	}
}

// FieldsFor throws the TypeError of a strict-mode write into a primitive.
func TestFieldsForPrimitiveThrows(t *testing.T) {
	for _, c := range []struct {
		v    Value
		want string
	}{
		{Str("x"), "Cannot create property 'f' on string 'x'"},
		{Num(5), "Cannot create property 'f' on number '5'"},
		{True, "Cannot create property 'f' on boolean 'true'"},
		{Null, "Cannot set properties of null (setting 'f')"},
	} {
		func() {
			defer func() {
				r, _ := recover().(*Thrown)
				if r == nil || r.Msg != c.want {
					t.Errorf("FieldsFor(%v) threw %v, want %q", c.v, r, c.want)
				}
			}()
			FieldsFor(c.v, "f")
		}()
	}
}

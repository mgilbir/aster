package jsval

import "testing"

// The walks on the hot paths: the String of a label or a short array, a
// deep comparison of an item's property, a comparison in a sort, the JSON of
// a data row. The same calls as before the walks were bounded by the call
// (the detached forms), so a change of their cost shows here.

var walkSink any

func BenchmarkWalkStringNumber(b *testing.B) {
	v := Num(1234.5)
	for b.Loop() {
		walkSink = v.AsString()
	}
}

func BenchmarkWalkStringPair(b *testing.B) {
	v := ArrOf(Num(1), Str("abc"))
	for b.Loop() {
		walkSink = v.AsString()
	}
}

func BenchmarkWalkEqualNumbers(b *testing.B) {
	x, y := Num(1), Num(1)
	for b.Loop() {
		walkSink = Equal(x, y)
	}
}

func BenchmarkWalkEqualDash(b *testing.B) {
	x, y := ArrOf(Num(4), Num(2)), ArrOf(Num(4), Num(2))
	for b.Loop() {
		walkSink = Equal(x, y)
	}
}

func BenchmarkWalkRelateArrays(b *testing.B) {
	x, y := ArrOf(Num(4), Num(2)), ArrOf(Num(4), Num(3))
	for b.Loop() {
		walkSink, _ = Relate(x, y)
	}
}

func BenchmarkWalkJSONRow(b *testing.B) {
	o := NewObject(4)
	o.Set("name", Str("Alpha"))
	o.Set("value", Num(12.5))
	o.Set("date", Num(1.6e12))
	o.Set("tags", ArrOf(Str("a"), Str("b")))
	v := Obj(o)
	buf := make([]byte, 0, 256)
	for b.Loop() {
		buf = AppendJSON(buf[:0], v)
	}
	walkSink = buf
}

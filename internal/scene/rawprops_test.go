package scene

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// Every typed property keeps the value it was given when its field cannot
// hold it (see Item.Raw): a property missing from rawNumeric or rawString
// would read back, and be drawn, as its coercion.
func TestEveryTypedPropertyKeepsItsValue(t *testing.T) {
	for name := range numFields {
		if !rawNumeric(name) {
			t.Errorf("numeric property %q drops a value that is not a number", name)
		}
		it := &Item{}
		if _, err := it.Set(name, jsval.Str("w")); err != nil {
			t.Fatal(err)
		}
		if v := it.Get(name); !v.IsStr() || v.StrValue() != "w" {
			t.Errorf("%s given \"w\" reads back as %v", name, v)
		}
		if _, err := it.Set(name, jsval.Null); err != nil {
			t.Fatal(err)
		}
		if v := it.Get(name); !v.IsNull() {
			t.Errorf("%s given null reads back as %v", name, v)
		}
	}
	for name := range strFields {
		if !rawString(name) {
			t.Errorf("string property %q drops a value that is not a string", name)
		}
		it := &Item{}
		v := jsval.ArrOf(jsval.Str("top"))
		if _, err := it.Set(name, v); err != nil {
			t.Fatal(err)
		}
		if got := it.Get(name); !got.IsArr() {
			t.Errorf("%s given ['top'] reads back as %v", name, got)
		}
	}
}

// A Bounds coordinate that is a string compares as text with another string
// and concatenates under translate and expand's +=, as upstream's does.
func TestBoundsStringCoordinates(t *testing.T) {
	w := jsval.Walker{}
	b := NewBounds()
	b.SetJS(w, jsval.Num(0), jsval.Num(0), jsval.Str("020"), jsval.Num(5))
	if !b.HasStrings() || b.X2 != 20 {
		t.Fatalf("set: %+v", b)
	}
	b.TranslateJS(w, jsval.Str("5"), jsval.Num(1))
	if got := b.Coord(0).AsString(); got != "05" {
		t.Errorf("x1 after translate = %q, want \"05\"", got)
	}
	if got := b.Coord(2).AsString(); got != "0205" {
		t.Errorf("x2 after translate = %q, want \"0205\"", got)
	}
	b.Expand(0.5)
	if got := b.Coord(0); !got.IsNum() || got.NumValue() != 4.5 {
		t.Errorf("x1 after expand = %v, want 4.5 (-= is numeric)", got)
	}
	if got := b.Coord(2).AsString(); got != "02050.5" {
		t.Errorf("x2 after expand = %q, want \"02050.5\"", got)
	}
	o := NewBounds()
	o.SetJS(w, jsval.Num(0), jsval.Num(0), jsval.Str("9"), jsval.Num(0))
	b.Union(&o)
	// "9" > "02050.5" as text, though not as a number.
	if got := b.Coord(2).AsString(); got != "9" {
		t.Errorf("x2 after union = %q, want \"9\"", got)
	}
	b.Round()
	if b.HasStrings() {
		t.Error("round leaves numbers")
	}
}

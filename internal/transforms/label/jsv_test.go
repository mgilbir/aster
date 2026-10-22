package label

import "testing"

// A bounds coordinate that is text is concatenated with the offsets, as
// upstream's `+` does, and read as a number by everything else.
func TestStringBoundsConcatenate(t *testing.T) {
	y1, y2 := "-2.530", "2.530"
	l := Label{FontSize: 8, Base: &Base{
		MarkType: "symbol",
		Bounds:   Box{X1: 47.5, Y1: -2.53, X2: 52.5, Y2: 2.53},
		Strs:     &BaseTexts{Bounds: [4]*string{nil, &y1, nil, &y2}},
	}}
	var xt, yt *string
	called := false
	_, err := Layout(t.Context(), []Label{l}, Options{
		Size: [2]float64{100, 60}, Anchor: []string{"top"}, Offset: []float64{1}, NoAvoidBaseMark: true,
		PlacedText: func(_ *Label, x, y *string) { xt, yt, called = x, y, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	// top: y1 + (8*-1)/2 + 1*-1 is "-2.530" + -4 + -1; y2 + 4 is "-2.530-4-14".
	if want := "-2.530-4-14"; !called || yt == nil || *yt != want {
		t.Fatalf("y = %v, want the text %q", yt, want)
	}
	if xt != nil {
		t.Fatalf("x = %q, want a number", *xt)
	}
}

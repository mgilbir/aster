package text

import (
	"strings"
	"testing"
)

// Exact is a view of the same fonts with unrounded advances: its widths are
// those of a Measurer built with WithExactAdvances, and it shares the font
// set, so nothing is parsed again (C71).
func TestExactViewSharesTheFonts(t *testing.T) {
	ref, err := New() // the reference metric model: rounded advances
	if err != nil {
		t.Fatal(err)
	}
	exact, err := New(WithExactAdvances())
	if err != nil {
		t.Fatal(err)
	}
	v := ref.Exact()
	if v.measurerState != ref.measurerState {
		t.Fatal("Exact built a font set of its own")
	}
	if ref.Exact().metrics != v.metrics {
		t.Error("each Exact has its own width cache")
	}
	if exact.Exact() != exact {
		t.Error("Exact of an exact Measurer is not itself")
	}
	pango, err := New(WithPangoAdvances(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Hamburgefonstiv", "1,234.5", "Ünïcödé", strings.Repeat("W", 40)} {
		for _, font := range []string{"10px sans-serif", "bold 13.7px serif", "italic 9px monospace"} {
			if got, want := v.MeasureText(s, font), exact.MeasureText(s, font); got != want {
				t.Errorf("%q in %s: Exact view %v, exact Measurer %v", s, font, got, want)
			}
			if got, want := pango.Exact().MeasureText(s, font), exact.MeasureText(s, font); got != want {
				t.Errorf("%q in %s: Exact of a Pango Measurer %v, exact %v", s, font, got, want)
			}
		}
	}
	if b := v.Bounded(nil); b.metrics != v.metrics {
		t.Error("Bounded dropped the view's metrics")
	}
}

// A generic family set to one with no font fails New, as a fallback family
// with none does (C74): it silently drew in the first face.
func TestDefaultFamiliesMustExist(t *testing.T) {
	for _, opt := range []Option{WithDefaultFontFamily("Nope"), WithDefaultSerifFamily("Nope"), WithDefaultMonospaceFamily("Nope")} {
		if _, err := New(opt); err == nil || !strings.Contains(err.Error(), `"Nope" has no font`) {
			t.Errorf("err = %v", err)
		}
	}
	if _, err := New(WithDefaultFontFamily("liberation serif"), WithDefaultMonospaceFamily("Liberation Sans")); err != nil {
		t.Errorf("embedded families: %v", err)
	}
}

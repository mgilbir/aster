package text

import (
	"github.com/mgilbir/aster/internal/testutil"
	"strings"
	"testing"
)

// TestParseCSSFontCanvasGrammar holds ParseCSSFont to the CSS font shorthand
// as node-canvas parses it (C12): a numeric weight is a weight, never the
// size; the size needs its unit; a number may have an exponent; a
// line-height after the size is not the size.
func TestParseCSSFontCanvasGrammar(t *testing.T) {
	for _, tt := range []struct {
		in     string
		italic bool
		weight int
		size   float64
		family string
	}{
		{"450 12px Arial", false, 450, 12, "Arial"},
		{"1000 12px Arial", false, 1000, 12, "Arial"},
		{"normal 450 12px Arial", false, 450, 12, "Arial"},
		{"italic small-caps 650 9px A, B", true, 650, 9, "A|B"},
		{"bold 12px/1.5em Arial", false, 700, 12, "Arial"},
		{"12px/normal Arial", false, 400, 12, "Arial"},
		{"1e+21px Arial", false, 400, 1e21, "Arial"},
		{"2.5e1px Arial", false, 400, 25, "Arial"},
		{"lighter 10px x", false, 100, 10, "x"},
		{"bolder 10px x", false, 700, 10, "x"},
		{"1in x", false, 400, 96, "x"},
		// A string node-canvas rejects is the defaults, as before.
		{"12 Arial", false, 400, 11, "sans-serif"},
		{"1200 12px Arial", false, 400, 11, "sans-serif"},
		{"bold 12px/1.5 Arial", false, 400, 11, "sans-serif"},
		// A size of zero or less leaves the default size.
		{"-20px Arial", false, 400, 11, "Arial"},
		{"0px Arial", false, 400, 11, "Arial"},
	} {
		got := ParseCSSFont(tt.in)
		if got.Italic != tt.italic || got.Weight != tt.weight || got.Size != tt.size || strings.Join(got.Family, "|") != tt.family {
			t.Errorf("ParseCSSFont(%q) = %+v, want italic %v weight %d size %v family %s", tt.in, got, tt.italic, tt.weight, tt.size, tt.family)
		}
	}
}

// TestMeasureNumericWeight is C12 end to end: a weight of 450 measures as the
// 400 face at the stated size, not as text 450 px tall.
func TestMeasureNumericWeight(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	want := m.MeasureText("Hello", "12px Arial")
	for _, font := range []string{"450 12px Arial", "normal 450 12px Arial", "12px/1.5em Arial"} {
		if got := m.MeasureText("Hello", font); got != want {
			t.Errorf("MeasureText(Hello, %q) = %v, want %v (as 12px Arial)", font, got, want)
		}
	}
}

// TestShapeFontRepeatsFree: shaping label after label in one font of
// several families allocates no more than in a font of one, as the string
// path does: the candidate list of the last font is kept (building its key
// joined the families into a new string each time).
func TestShapeFontRepeatsFree(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if testutil.RaceEnabled {
		t.Skip("allocation counts are not the code's under -race (see testutil.RaceEnabled)")
	}
	allocs := func(f CSSFont) float64 {
		m.ShapeFont("Hello", f)
		return testing.AllocsPerRun(100, func() { m.ShapeFont("Hello", f) })
	}
	one := allocs(CSSFont{Weight: 400, Size: 12, Family: []string{"Arial"}})
	two := allocs(CSSFont{Weight: 400, Size: 12, Family: []string{"Helvetica Neue", "Arial", "sans-serif"}})
	if two > one {
		t.Errorf("%v allocations a run in a font of three families, %v in one", two, one)
	}
}

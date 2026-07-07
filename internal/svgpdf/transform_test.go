package svgpdf

import (
	"math"
	"testing"
)

func matricesEqual(a, b Matrix) bool {
	return almostEqual(a.A, b.A) && almostEqual(a.B, b.B) &&
		almostEqual(a.C, b.C) && almostEqual(a.D, b.D) &&
		almostEqual(a.E, b.E) && almostEqual(a.F, b.F)
}

func TestParseTransformTranslate(t *testing.T) {
	m, err := parseTransform("translate(124,22)")
	if err != nil {
		t.Fatalf("parseTransform: %v", err)
	}
	if !matricesEqual(m, Matrix{A: 1, D: 1, E: 124, F: 22}) {
		t.Errorf("got %+v", m)
	}

	m, err = parseTransform("translate(5)")
	if err != nil {
		t.Fatalf("parseTransform: %v", err)
	}
	if !matricesEqual(m, Matrix{A: 1, D: 1, E: 5}) {
		t.Errorf("single-arg translate: got %+v", m)
	}
}

func TestParseTransformRotate(t *testing.T) {
	m, err := parseTransform("rotate(-90)")
	if err != nil {
		t.Fatalf("parseTransform: %v", err)
	}
	x, y := m.Apply(1, 0)
	// SVG rotation is clockwise-positive in a y-down system; rotate(-90)
	// maps (1,0) to (0,-1).
	if !almostEqual(x, 0) || !almostEqual(y, -1) {
		t.Errorf("rotate(-90) of (1,0): got (%g,%g), want (0,-1)", x, y)
	}
}

func TestParseTransformRotateAboutCenter(t *testing.T) {
	m, err := parseTransform("rotate(90, 10, 10)")
	if err != nil {
		t.Fatalf("parseTransform: %v", err)
	}
	// The center is a fixed point.
	x, y := m.Apply(10, 10)
	if !almostEqual(x, 10) || !almostEqual(y, 10) {
		t.Errorf("center moved: got (%g,%g)", x, y)
	}
	x, y = m.Apply(11, 10)
	if !almostEqual(x, 10) || !almostEqual(y, 11) {
		t.Errorf("rotate(90,10,10) of (11,10): got (%g,%g), want (10,11)", x, y)
	}
}

// TestParseTransformSequence checks the vega rotated-label idiom:
// "translate(a,b) rotate(-90) translate(0,c)" composes left to right, with
// the rightmost transform applied to points first.
func TestParseTransformSequence(t *testing.T) {
	m, err := parseTransform("translate(-107,100) rotate(-90) translate(0,-2)")
	if err != nil {
		t.Fatalf("parseTransform: %v", err)
	}
	want := Matrix{A: 1, D: 1, E: -107, F: 100}.
		Mul(Matrix{A: math.Cos(-math.Pi / 2), B: math.Sin(-math.Pi / 2), C: -math.Sin(-math.Pi / 2), D: math.Cos(-math.Pi / 2)}).
		Mul(Matrix{A: 1, D: 1, F: -2})
	if !matricesEqual(m, want) {
		t.Errorf("got %+v, want %+v", m, want)
	}
	// Origin walks right-to-left: translate(0,-2) → (0,-2); rotate(-90)
	// → (-2,0); translate(-107,100) → (-109,100).
	x, y := m.Apply(0, 0)
	if !almostEqual(x, -109) || !almostEqual(y, 100) {
		t.Errorf("origin: got (%g,%g), want (-109,100)", x, y)
	}
}

func TestParseTransformScaleAndMatrix(t *testing.T) {
	m, err := parseTransform("scale(2)")
	if err != nil {
		t.Fatalf("parseTransform: %v", err)
	}
	if !matricesEqual(m, Matrix{A: 2, D: 2}) {
		t.Errorf("scale(2): got %+v", m)
	}
	m, err = parseTransform("matrix(1,2,3,4,5,6)")
	if err != nil {
		t.Fatalf("parseTransform: %v", err)
	}
	if !matricesEqual(m, Matrix{A: 1, B: 2, C: 3, D: 4, E: 5, F: 6}) {
		t.Errorf("matrix: got %+v", m)
	}
}

func TestParseTransformErrors(t *testing.T) {
	for _, s := range []string{
		"frobnicate(1,2)",
		"translate(1,2,3)",
		"rotate()",
		"translate(1,2) garbage",
		"matrix(1,2,3)",
	} {
		if _, err := parseTransform(s); err == nil {
			t.Errorf("%q: expected error, got none", s)
		}
	}
}

func TestMatrixMulOrder(t *testing.T) {
	// Translation then scale (M = T·S): scaling applies to points first.
	tr := Matrix{A: 1, D: 1, E: 10, F: 0}
	sc := Matrix{A: 2, D: 2}
	m := tr.Mul(sc)
	x, y := m.Apply(1, 1)
	if !almostEqual(x, 12) || !almostEqual(y, 2) {
		t.Errorf("T·S apply (1,1): got (%g,%g), want (12,2)", x, y)
	}
}

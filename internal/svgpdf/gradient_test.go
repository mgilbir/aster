package svgpdf

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
)

func gradSVG(defs, body string) string {
	return `<svg width="100" height="100"><defs>` + defs + `</defs>` + body + `</svg>`
}

const twoStops = `<stop offset="0" stop-color="white"/><stop offset="1" stop-color="darkgreen"/>`

// gradContent is the uncompressed content stream and the shadings of svg.
func gradContent(t *testing.T, svg string) (string, []*gradient, error) {
	t.Helper()
	root, err := parseSVG(context.Background(), svg, Limits{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	content, _, _, _, paints, _, _, err := render(root, nil, nil, Options{})
	return string(bytes.Join(content, nil)), paints.shadings, err
}

func TestGradientLinearBoundingBox(t *testing.T) {
	svg := gradSVG(`<linearGradient id="g" x1="1" x2="1" y1="1" y2="0">`+twoStops+`</linearGradient>`,
		`<rect x="10" y="20" width="30" height="40" fill="url(#g)"/><rect x="0" y="0" width="5" height="5" fill="url(#g)"/>`)
	got, shadings, err := gradContent(t, svg)
	if err != nil {
		t.Fatal(err)
	}
	// Clipped to the rect, the unit square mapped onto its box, then shaded.
	if !strings.Contains(got, "10 20 30 40 re\nW\nn\n30 0 0 40 10 20 cm\n/Sh0 sh") {
		t.Errorf("content:\n%s", got)
	}
	if len(shadings) != 1 || strings.Contains(got, "/Sh1") {
		t.Errorf("one gradient drawn twice is %d shadings", len(shadings))
	}
	pdf, err := Convert(svg, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/Shading", "/ShadingType 2", "/Coords [1.0 1.0 1.0 0.0]", "/Extend [true true]", "/FunctionType 2"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("the PDF has no %s", want)
		}
	}
}

func TestGradientRadialAndUserSpace(t *testing.T) {
	svg := gradSVG(`<radialGradient id="r" cx="0.5" cy="0.5" r="0.5" fx="0.25" fy="0.25">`+twoStops+`</radialGradient>`+
		`<linearGradient id="u" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="100" y2="0">`+twoStops+`</linearGradient>`,
		`<path d="M0,0L50,0L50,50Z" fill="url(#r)"/><rect width="10" height="10" fill="url(#u)"/>`)
	got, shadings, err := gradContent(t, svg)
	if err != nil {
		t.Fatal(err)
	}
	if len(shadings) != 2 || !shadings[0].radial {
		t.Fatalf("shadings %+v", shadings)
	}
	if want := []float64{0.25, 0.25, 0, 0.5, 0.5, 0.5}; !equalFloats(shadings[0].coords, want) {
		t.Errorf("radial coords %v, want %v", shadings[0].coords, want)
	}
	// User space units take no box matrix: the shading follows the rect.
	if !strings.Contains(got, "0 0 10 10 re\nW\nn\n/Sh1 sh") {
		t.Errorf("content:\n%s", got)
	}
	pdf, err := Convert(svg, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pdf, []byte("/ShadingType 3")) {
		t.Error("no radial shading")
	}
}

func TestGradientStops(t *testing.T) {
	// A hard edge (two stops at one offset) and padding before and after.
	g, err := parseGradient(&element{name: "linearGradient", attrs: []attribute{{"id", "g"}}, children: []*element{
		{name: "stop", attrs: []attribute{{"offset", "20%"}, {"stop-color", "red"}}},
		{name: "stop", attrs: []attribute{{"offset", "0.5"}, {"stop-color", "red"}}},
		{name: "stop", attrs: []attribute{{"offset", "0.4"}, {"stop-color", "blue"}}}, // clamped to 0.5
		{name: "stop", attrs: []attribute{{"offset", "0.8"}, {"stop-color", "blue"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if g.stops[2].offset != 0.5 {
		t.Errorf("a decreasing offset is %g, want it clamped to 0.5", g.stops[2].offset)
	}
	svg := gradSVG(`<linearGradient id="g"><stop offset="20%" stop-color="red"/><stop offset="0.5" stop-color="red"/>`+
		`<stop offset="0.4" stop-color="blue"/><stop offset="0.8" stop-color="blue"/></linearGradient>`, `<rect width="5" height="5" fill="url(#g)"/>`)
	pdf, err := Convert(svg, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 0..0.2 red, 0.2..0.5 red, (hard edge), 0.5..0.8 blue, 0.8..1 blue.
	if got := bytes.Count(pdf, []byte("/FunctionType 2")); got != 4 {
		t.Errorf("%d interpolating functions, want 4", got)
	}
	if !bytes.Contains(pdf, []byte("/Bounds [0.2 0.5 0.8]")) {
		t.Errorf("no /Bounds [0.2 0.5 0.8] in the stitching function")
	}

	// One stop is a solid fill.
	got, shadings, err := gradContent(t, gradSVG(`<linearGradient id="g"><stop offset="0.3" stop-color="#ff0000"/></linearGradient>`,
		`<rect width="10" height="10" fill="url(#g)"/>`))
	if err != nil || len(shadings) != 0 || !strings.Contains(got, "1 0 0 rg") {
		t.Errorf("a one-stop gradient: %v, %d shadings, content:\n%s", err, len(shadings), got)
	}
}

func TestGradientRefusals(t *testing.T) {
	grad := `<linearGradient id="g">` + twoStops + `</linearGradient>`
	for name, svg := range map[string]string{
		"translucent stop": gradSVG(`<linearGradient id="g"><stop offset="0" stop-color="red" stop-opacity="0.5"/></linearGradient>`, `<rect width="5" height="5" fill="url(#g)"/>`),
		"spread method":    gradSVG(`<linearGradient id="g" spreadMethod="reflect">`+twoStops+`</linearGradient>`, `<rect width="5" height="5" fill="url(#g)"/>`),
		"transform":        gradSVG(`<linearGradient id="g" gradientTransform="rotate(45)">`+twoStops+`</linearGradient>`, `<rect width="5" height="5" fill="url(#g)"/>`),
		"missing":          gradSVG(grad, `<rect width="5" height="5" fill="url(#nope)"/>`),
	} {
		if _, err := Convert(svg, nil, Options{}); err == nil || !strings.HasPrefix(err.Error(), "svgpdf:") {
			t.Errorf("%s: err = %v, want an svgpdf error", name, err)
		}
	}
}

func TestSegsBox(t *testing.T) {
	// A cubic bulging past its end points: its box reaches the bulge.
	segs := []PathSeg{
		{Op: OpMoveTo, P3: Point{0, 0}},
		{Op: OpCubicTo, P1: Point{0, 10}, P2: Point{10, 10}, P3: Point{10, 0}},
	}
	b, ok := segsBox(segs)
	if !ok || b.x0 != 0 || b.x1 != 10 || b.y0 != 0 || math.Abs(b.y1-7.5) > 1e-9 {
		t.Errorf("box %+v, want 0,0 to 10,7.5", b)
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-9 {
			return false
		}
	}
	return true
}

// A gradient stroke is a shading pattern as the stroke colour, placed in page
// space: the shape's box under the transform at the stroke.
func TestGradientStroke(t *testing.T) {
	svg := gradSVG(`<linearGradient id="g">`+twoStops+`</linearGradient>`,
		`<g transform="translate(5,5)"><rect x="10" y="20" width="30" height="40" fill="red" stroke="url(#g)" stroke-width="2"/></g>`)
	got, shadings, err := gradContent(t, svg)
	if err != nil {
		t.Fatal(err)
	}
	if len(shadings) != 1 || !strings.Contains(got, "/Pattern CS\n/P0 SCN") || !strings.Contains(got, "1 0 0 rg") {
		t.Errorf("shadings %d, content:\n%s", len(shadings), got)
	}
	pdf, err := Convert(svg, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Page space: the y flip of a 100 high page, the translate, then the box.
	if !bytes.Contains(pdf, []byte("/PatternType 2")) || !bytes.Contains(pdf, []byte("/Matrix [30.0 0.0 0.0 -40.0 15.0 75.0]")) {
		i := bytes.Index(pdf, []byte("/Matrix"))
		t.Errorf("pattern: %q", pdf[max(i, 0):min(len(pdf), max(i, 0)+60)])
	}
}

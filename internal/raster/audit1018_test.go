package raster

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// A stroke thinner than a device pixel is drawn as tiny-skia draws it, a
// one-pixel hairline whose paint is scaled by the stroke's coverage
// (Pixmap::stroke_path: paint.shader.apply_opacity), whatever the paint: a
// gradient or a pattern of a colour weighs what the colour does (C46).
func TestHairlineScalesEveryPaint(t *testing.T) {
	doc := func(stroke string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><defs>
<linearGradient id="g" gradientUnits="userSpaceOnUse" x1="0" x2="20"><stop offset="0" stop-color="black"/><stop offset="1" stop-color="black"/></linearGradient>
<pattern id="p" width="40" height="40" patternUnits="userSpaceOnUse"><rect width="40" height="40"/></pattern></defs>
<path d="M0 10.5 H20" stroke="` + stroke + `" stroke-width="0.2"/></svg>`
	}
	solid, err := Render([]byte(doc("black")), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := solid.NRGBAAt(10, 10)
	if want.A == 0 || want.A == 255 {
		t.Fatalf("solid hairline alpha %d: want a coverage between 0 and 255", want.A)
	}
	for _, paint := range []string{"url(#g)", "url(#p)"} {
		img, err := Render([]byte(doc(paint)), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if got := img.NRGBAAt(10, 10); got.A > want.A+1 || got.A+1 < want.A {
			t.Errorf("%s hairline %v, the solid colour's %v", paint, got, want)
		}
	}
}

// Bicubic sampling of a pattern overshoots, and a channel converted to a
// byte out of range is implementation-defined (it wraps on amd64 and
// saturates on arm64): alpha is clamped first, then the colour to it (C108).
func TestPatternBicubicOvershootClamped(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="60" height="60">
<defs><pattern id="p" width="4" height="4" patternUnits="userSpaceOnUse" patternTransform="rotate(20)"><rect x="1" width="2" height="4" fill="white"/></pattern></defs>
<rect width="60" height="60" fill="url(#p)"/></svg>`
	img, err := Render([]byte(svg), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 60 * 60 {
		if c := img.NRGBAAt(i%60, i/60); c.A > 0 && (c.R < 250 || c.G < 250 || c.B < 250) {
			t.Fatalf("pixel (%d,%d) %v: a white pattern has only white pixels", i%60, i/60, c)
		}
	}
	// At the shader: half way between transparent and white pixels.
	tile := &patternTile{w: 4, h: 1, pix: []uint8{0, 0, 0, 0, 255, 255, 255, 255, 255, 255, 255, 255, 0, 0, 0, 0}}
	s := &patternShader{tile: tile, inv: matrix{1, 0, 0, 1, 1.5, 0}, opacity: 1}
	dst := make([]uint8, 4*8)
	s.shadeRow(0, 0, dst)
	for i := 0; i < len(dst); i += 4 {
		if p := dst[i : i+4]; p[0] > p[3] || p[1] > p[3] || p[2] > p[3] || (p[3] == 255 && p[0] < 255) {
			t.Errorf("pixel %d: %v is not white premultiplied", i/4, p)
		}
	}
}

// A stop's currentColor is the stop's own color property, inherited
// through its ancestors, as usvg resolves it (paint_server.rs:
// stop.find_attribute(AId::Color), black when none): not the colour of the
// element painting with the gradient, so the gradient is the same for each
// (C109).
func TestGradientStopCurrentColorIsTheStops(t *testing.T) {
	grad := `<defs><linearGradient id="g" color="lime"><stop offset="0" stop-color="currentColor"/><stop offset="1" stop-color="currentColor"/></linearGradient>
<linearGradient id="h"><stop offset="0" stop-color="currentColor"/><stop offset="1" stop-color="currentColor"/></linearGradient></defs>`
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="10">` + grad + `
<rect x="0" width="10" height="10" fill="url(#g)" color="red"/><rect x="10" width="10" height="10" fill="url(#g)" color="blue"/>
<rect x="20" width="10" height="10" fill="url(#h)" color="red"/><rect x="30" width="10" height="10" fill="url(#h)" color="blue"/></svg>`
	img, err := Render([]byte(svg), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		x       int
		r, g, b uint8
	}{{5, 0, 255, 0}, {15, 0, 255, 0}, {25, 0, 0, 0}, {35, 0, 0, 0}} {
		if p := img.NRGBAAt(c.x, 5); p.R != c.r || p.G != c.g || p.B != c.b || p.A != 255 {
			t.Errorf("x=%d: %v, want rgb(%d,%d,%d)", c.x, p, c.r, c.g, c.b)
		}
	}
}

// A clip mask is given back when its scope ends, not when the element
// that made it does: each marker's viewport clip, each clip of a colour
// glyph (C110).
func TestMarkerClipsGivenBackPerMarker(t *testing.T) {
	var pts strings.Builder
	for i := range 200 {
		fmt.Fprintf(&pts, "%d,%d ", 10+i*4, 500+(i%2)*7)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="1000">
<marker id="m" markerWidth="4" markerHeight="4" orient="auto"><rect width="4" height="4"/></marker>
<polyline points="` + pts.String() + `" fill="none" stroke="black" marker-mid="url(#m)"/></svg>`
	if _, err := Render([]byte(svg), Options{Limits: Limits{MaxCanvasBytes: 16 << 20}}); err != nil {
		t.Errorf("200 rotated markers on a 1000x1000 canvas, MaxCanvasBytes 16 MiB: %v", err)
	}
}

// A <use> that references itself through other uses is skipped as usvg
// skips it (svgtree/parse.rs, parse_svg_use_element: a use whose element is
// its origin, or holds a use of it or of the use, is "recursive"), and a
// longer cycle, which usvg refuses as too deep, fails naming its cause
// (C111). A box computed through one says what stopped it.
func TestUseCycles(t *testing.T) {
	head := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20">`
	cases := []struct {
		name, body string
		alpha      uint8 // at (2,2)
	}{
		{"self", `<use id="a" href="#a"/><rect width="5" height="5"/>`, 255},
		{"pair", `<use id="a" href="#b"/><use id="b" href="#a"/><rect width="5" height="5"/>`, 255},
		{"pair-in-groups", `<g><use id="a" href="#b"/></g><g><use id="b" href="#a"/></g><rect width="5" height="5"/>`, 255},
		// u1 holds u2, which uses u1: u1 is skipped wherever it is, so the
		// rectangle is drawn once, by the group itself.
		{"via-group", `<g id="g"><rect width="5" height="5" fill-opacity="0.5"/><use id="u2" href="#u1"/></g><use id="u1" href="#g"/>`, 128},
		// The symbol holds a use of itself: the use of the symbol is
		// skipped, and a symbol is drawn only through a use.
		{"symbol", `<symbol id="s"><rect width="5" height="5"/><use href="#s"/></symbol><use href="#s"/>`, 0},
	}
	for _, c := range cases {
		img, err := Render([]byte(head+c.body+`</svg>`), Options{})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if a := img.NRGBAAt(2, 2).A; a != c.alpha {
			t.Errorf("%s: alpha %d at the rectangle, want %d", c.name, a, c.alpha)
		}
	}
	for name, body := range map[string]string{
		"three uses":  `<use id="a" href="#b"/><use id="b" href="#c"/><use id="c" href="#a"/>`,
		"two groups":  `<g id="a"><rect width="5" height="5"/><use href="#b"/></g><g id="b"><use href="#a"/></g>`,
		"in a box":    `<clipPath id="c" clipPathUnits="objectBoundingBox"><rect width="1" height="1"/></clipPath><g clip-path="url(#c)"><use id="a" href="#b"/><use id="b" href="#c2"/><use id="c2" href="#a"/></g>`,
		"groups, box": `<clipPath id="c" clipPathUnits="objectBoundingBox"><rect width="1" height="1"/></clipPath><g id="a" clip-path="url(#c)"><rect width="5" height="5"/><use href="#b"/></g><g id="b"><use href="#a"/></g>`,
	} {
		_, err := Render([]byte(head+body+`</svg>`), Options{})
		if !errors.Is(err, budget.ErrLimit) || !strings.Contains(err.Error(), "cycle") {
			t.Errorf("%s: err = %v, want ErrLimit naming the cycle", name, err)
		}
	}
}

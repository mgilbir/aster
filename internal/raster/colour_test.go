package raster

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/forme/shape"
)

func colourFonts(t *testing.T) []FontData {
	t.Helper()
	var fonts []FontData
	for _, name := range []string{"ColourTest", "SbixTest"} {
		data, err := os.ReadFile("../../testdata/colourfonts/" + name + ".ttf")
		if err != nil {
			t.Fatal(err)
		}
		fonts = append(fonts, FontData{Family: name, Data: data})
	}
	return fonts
}

func renderColour(t *testing.T, svg string) *image.NRGBA {
	t.Helper()
	img, err := Render([]byte(svg), Options{Fonts: colourFonts(t)})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestColourGlyphsMatchHarfBuzz draws each glyph of ColourTest.ttf as
// testdata/colourfonts/hb-view.png has HarfBuzz 14.6.0 draw them (through
// cairo):
//
//	hb-view --font-size=60 --margin=10 --foreground=00AA88 --background=EEEEEE \
//	  -O png -o hb-view.png ColourTest.ttf -u 1F534,1F7E2,1F308,1F31E,1F300,1F600,1F3A8,1F4A0,1F4A1,41
//
// The sweep gradient is left out: cairo draws the wedge of a reflected sweep
// below its start angle wrongly. TestSweepGradient checks it instead.
func TestColourGlyphsMatchHarfBuzz(t *testing.T) {
	f, err := os.Open("../../testdata/colourfonts/hb-view.png")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	got := renderColour(t, `<svg xmlns="http://www.w3.org/2000/svg" width="620" height="80"><rect width="620" height="80" fill="#eee"/>
<text x="10" y="58" font-family="ColourTest" font-size="60" fill="#0a8">&#x1F534;&#x1F7E2;&#x1F308;&#x1F31E;&#x1F300;&#x1F600;&#x1F3A8;&#x1F4A0;&#x1F4A1;A</text></svg>`)
	if got.Bounds() != ref.Bounds() {
		t.Fatalf("size %v, HarfBuzz's %v", got.Bounds(), ref.Bounds())
	}
	names := []string{"COLRv0 layers", "solid", "linear", "radial", "sweep", "foreground, rotated", "SrcIn", "Multiply", "HSL luminosity", "outline"}
	for i, name := range names {
		if name == "sweep" {
			continue
		}
		// Each glyph is 60 pixels wide; edges may differ by anti-aliasing.
		var sum, far, n int
		for y := 0; y < 80; y++ {
			for x := 10 + 60*i; x < 70+60*i; x++ {
				d := maxChannelDiff(got.At(x, y), ref.At(x, y))
				sum += d
				if d > 32 {
					far++
				}
				n++
			}
		}
		if mean := float64(sum) / float64(n); mean > 0.5 || far > 3 {
			t.Errorf("%s: mean difference %.2f, %d pixels far from HarfBuzz's", name, mean, far)
		}
	}
}

func maxChannelDiff(a, b color.Color) int {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := 0
	for _, p := range [][2]uint32{{ar, br}, {ag, bg}, {ab, bb}} {
		d = max(d, int(math.Abs(float64(p[0]>>8)-float64(p[1]>>8))))
	}
	return d
}

// near reports whether c is within 3 of each channel of want.
func near(c color.Color, want color.NRGBA) bool { return within(c, want, 3) }

func within(c color.Color, want color.NRGBA, tol float64) bool {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	return math.Abs(float64(n.R)-float64(want.R)) <= tol && math.Abs(float64(n.G)-float64(want.G)) <= tol &&
		math.Abs(float64(n.B)-float64(want.B)) <= tol && math.Abs(float64(n.A)-float64(want.A)) <= tol
}

func TestSweepGradient(t *testing.T) {
	// U+1F300 sweeps red, green, blue from 0 to 90 degrees counter-clockwise
	// about (500, 400) in font units, reflected: at 100px, a centre of
	// (50, 60) on the screen, y down.
	img := renderColour(t, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
<text x="0" y="100" font-family="ColourTest" font-size="100">&#x1F300;</text></svg>`)
	red, green, blue := color.NRGBA{230, 26, 26, 255}, color.NRGBA{26, 178, 51, 255}, color.NRGBA{26, 51, 230, 255}
	for _, c := range []struct {
		deg  float64
		want color.NRGBA
	}{
		{1, red}, {45, green}, {89, blue}, // the colour line
		{135, green}, {179, red}, // reflected
		{225, green}, {269, blue}, {315, green}, {359, red},
	} {
		s, co := math.Sincos(c.deg * math.Pi / 180)
		x, y := 50+30*co, 60-30*s
		// A degree from a stop is a ninetieth of the way to the next colour.
		if got := img.At(int(x), int(y)); !within(got, c.want, 8) {
			t.Errorf("%v degrees: %v, want %v", c.deg, got, c.want)
		}
	}
}

func TestColourGlyphs(t *testing.T) {
	img := renderColour(t, `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="100">
<text x="0" y="80" font-family="SbixTest" font-size="100">&#x1F600;</text>
<text x="100" y="80" font-family="SbixTest" font-size="10">&#x1F600;</text>
<text x="200" y="80" font-family="ColourTest" font-size="100" fill="#0a8" fill-opacity="0.5">&#x1F534;</text>
<text x="300" y="80" font-family="ColourTest" font-size="100" fill="#0a8" stroke="#000" stroke-width="20">&#x1F600;</text>
</svg>`)
	for _, c := range []struct {
		what string
		x, y int
		want color.NRGBA
	}{
		// At 100 pixels per em, the 100 ppem strike, purple; at 10, the 20
		// ppem strike, orange. The image fills the glyph's box.
		{"100 ppem strike", 50, 40, color.NRGBA{120, 40, 200, 255}},
		{"20 ppem strike", 105, 76, color.NRGBA{255, 140, 0, 255}},
		// Fill opacity applies to the glyph as a whole: the square is not seen
		// through the triangle.
		{"fill-opacity, square", 212, 20, color.NRGBA{230, 26, 26, 128}},
		{"fill-opacity, triangle", 250, 70, color.NRGBA{26, 51, 230, 128}},
		// The foreground is the fill, here at the font's 0.8 over the green
		// square; a colour glyph is not stroked, so the square's outline is
		// not drawn around it.
		{"foreground", 350, 40, color.NRGBA{5, 172, 119, 255}},
		{"no stroke", 305, 2, color.NRGBA{}},
	} {
		if got := img.At(c.x, c.y); !near(got, c.want) {
			t.Errorf("%s: %v, want %v", c.what, got, c.want)
		}
	}
}

// fakeColourFace paints a square with a hook to misbehave.
type fakeColourFace struct {
	paint func(p shape.Painter) error
}

func (f *fakeColourFace) UnitsPerEm() float64 { return 1000 }
func (f *fakeColourFace) Ascent() float64     { return 800 }
func (f *fakeColourFace) Descent() float64    { return 200 }
func (f *fakeColourFace) Outline(gid uint32, sink OutlineSink) bool {
	sink.MoveTo(0, 0)
	sink.LineTo(0, -800)
	sink.LineTo(800, -800)
	sink.LineTo(800, 0)
	sink.Close()
	return true
}
func (f *fakeColourFace) Colour(uint32, int) bool { return true }
func (f *fakeColourFace) Paint(_ uint32, _ shape.PaintOptions, p shape.Painter) error {
	return f.paint(p)
}

type fakeShaper struct{ f Face }

func (s fakeShaper) Shape(string, FontRequest, float64) []ShapedGlyph {
	return []ShapedGlyph{{Face: s.f, ID: 1, Advance: 100}}
}

func renderFake(t *testing.T, paint func(p shape.Painter) error) *image.NRGBA {
	t.Helper()
	img, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
<text x="0" y="90" font-size="100" fill="#00f">x</text></svg>`), Options{Shaper: fakeShaper{&fakeColourFace{paint}}})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestColourGlyphFallsBackToOutline(t *testing.T) {
	// A painting that fails, here having opened groups it never closes, is
	// dropped and the glyph filled from its outline instead.
	img := renderFake(t, func(p shape.Painter) error {
		p.PushGroup()
		p.PushClipRect(shape.Rect{XMax: 1000, YMax: 1000})
		p.Solid(shape.Color{R: 255, A: 255}, false)
		p.PushGroup()
		return errors.New("refused")
	})
	if got := img.At(40, 50); !near(got, color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("fallback: %v, want the outline in the fill", got)
	}
}

func TestColourGlyphDeepGroups(t *testing.T) {
	// Groups nested past the layer budget are painted into the one beneath
	// rather than failing the render.
	img := renderFake(t, func(p shape.Painter) error {
		for range 100 {
			p.PushGroup()
		}
		p.PushClipRect(shape.Rect{XMax: 1000, YMax: 1000})
		p.Solid(shape.Color{G: 255, A: 255}, false)
		p.PopClip()
		for range 100 {
			p.PopGroup(shape.CompositeSrcOver)
		}
		return nil
	})
	if got := img.At(40, 50); !near(got, color.NRGBA{0, 255, 0, 255}) {
		t.Errorf("deep groups: %v, want green", got)
	}
}

func TestComposite(t *testing.T) {
	// Half-transparent red onto opaque blue, premultiplied.
	s := [4]float64{0.5, 0, 0, 0.5}
	d := [4]float64{0, 0, 1, 1}
	for _, c := range []struct {
		mode shape.CompositeMode
		want [4]float64
	}{
		{shape.CompositeClear, [4]float64{}},
		{shape.CompositeSrc, s},
		{shape.CompositeDest, d},
		{shape.CompositeSrcOver, [4]float64{0.5, 0, 0.5, 1}},
		{shape.CompositeDestOver, d},
		{shape.CompositeSrcIn, s},
		{shape.CompositeDestIn, [4]float64{0, 0, 0.5, 0.5}},
		{shape.CompositeSrcOut, [4]float64{}},
		{shape.CompositeDestOut, [4]float64{0, 0, 0.5, 0.5}},
		{shape.CompositeSrcAtop, [4]float64{0.5, 0, 0.5, 1}},
		{shape.CompositeDestAtop, [4]float64{0, 0, 0.5, 0.5}},
		{shape.CompositeXor, [4]float64{0, 0, 0.5, 0.5}},
		{shape.CompositePlus, [4]float64{0.5, 0, 1, 1}},
		// Multiply of red and blue is black, at half the source's alpha.
		{shape.CompositeMultiply, [4]float64{0, 0, 0.5, 1}},
		// Blue at red's luminosity, 0.3, clipped back into gamut: (0.21, 0.21, 1),
		// half of it over half of the backdrop.
		{shape.CompositeHSLLuminosity, [4]float64{0.107, 0.107, 1, 1}},
	} {
		got := composite(s, d, c.mode)
		for k := range 4 {
			if math.Abs(got[k]-c.want[k]) > 0.005 {
				t.Errorf("mode %d: %v, want %v", c.mode, got, c.want)
				break
			}
		}
	}
}

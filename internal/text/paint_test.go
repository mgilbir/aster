package text

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"
)

// recorder writes down each call a glyph's painting makes, one per line.
type recorder struct{ calls []string }

func (r *recorder) add(format string, a ...any) { r.calls = append(r.calls, fmt.Sprintf(format, a...)) }

func (r *recorder) PushTransform(t shape.Transform) { r.add("PushTransform") }
func (r *recorder) PopTransform()                   { r.add("PopTransform") }
func (r *recorder) PushClipGlyph(gid int)           { r.add("PushClipGlyph") }
func (r *recorder) PushClipRect(shape.Rect)         { r.add("PushClipRect") }
func (r *recorder) PopClip()                        { r.add("PopClip") }
func (r *recorder) PushGroup()                      { r.add("PushGroup") }
func (r *recorder) PopGroup(m shape.CompositeMode)  { r.add("PopGroup %d", m) }
func (r *recorder) Solid(c shape.Color, fg bool)    { r.add("Solid %v %v", c, fg) }
func (r *recorder) LinearGradient(shape.LinearGradient) {
	r.add("LinearGradient")
}
func (r *recorder) RadialGradient(shape.RadialGradient) {
	r.add("RadialGradient")
}
func (r *recorder) SweepGradient(shape.SweepGradient) { r.add("SweepGradient") }
func (r *recorder) Image(img shape.Image) {
	format := fmt.Sprint(img.Format)
	if img.Format == shape.ImagePNG {
		format = "png"
	}
	r.add("Image %s %dx%d strike %d", format, img.Width, img.Height, img.Strike.PPEM())
}

// colourFace loads one of testdata/colourfonts and returns it with the glyph
// it maps r to.
func colourFace(t *testing.T, file string, r rune) (*Face, int) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/colourfonts/" + file)
	if err != nil {
		t.Fatal(err)
	}
	family := strings.TrimSuffix(file, ".ttf")
	m, err := New(WithFont(family, data))
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := m.ShapeText(string(r), "20px "+family)
	if len(runs) != 1 || len(runs[0].Glyphs) != 1 || runs[0].Face.Family != family {
		t.Fatalf("%U did not shape to one glyph of %s", r, family)
	}
	return runs[0].Face, runs[0].Glyphs[0].GID
}

func TestGlyphColour(t *testing.T) {
	for _, c := range []struct {
		file string
		r    rune
		want shape.GlyphColour
	}{
		{"ColourTest.ttf", 'A', shape.ColourNone},
		{"ColourTest.ttf", 0x1F534, shape.ColourLayers},
		{"ColourTest.ttf", 0x1F308, shape.ColourPaint},
		{"SbixTest.ttf", 'A', shape.ColourNone},
		{"SbixTest.ttf", 0x1F600, shape.ColourBitmap},
	} {
		f, gid := colourFace(t, c.file, c.r)
		if got := GlyphColour(f, gid, shape.PaintOptions{PPEM: 20}); got != c.want {
			t.Errorf("%s %U: GlyphColour = %d, want %d", c.file, c.r, got, c.want)
		}
	}
	if got := GlyphColour(nil, 1, shape.PaintOptions{}); got != shape.ColourNone {
		t.Errorf("nil face: GlyphColour = %d", got)
	}
}

func TestPaintGlyph(t *testing.T) {
	for _, c := range []struct {
		file string
		r    rune
		opts shape.PaintOptions
		want string
	}{
		// COLRv0: a layer is a glyph clip around a solid fill.
		{"ColourTest.ttf", 0x1F534, shape.PaintOptions{},
			"PushClipGlyph|Solid {230 26 26 255} false|PopClip|PushClipGlyph|Solid {26 51 230 255} false|PopClip"},
		// The foreground colour is the one asked for, its alpha the font's.
		{"ColourTest.ttf", 0x1F600, shape.PaintOptions{Foreground: shape.Color{R: 10, G: 20, B: 30, A: 255}},
			"PushClipGlyph|Solid {26 178 51 255} false|PopClip|PushTransform|" +
				"PushClipGlyph|Solid {10 20 30 204} true|PopClip|PopTransform"},
		{"ColourTest.ttf", 0x1F3A8, shape.PaintOptions{},
			"PushGroup|PushTransform|PushClipGlyph|Solid {26 51 230 255} false|PopClip|PopTransform|" +
				"PushGroup|PushClipGlyph|Solid {230 26 26 255} false|PopClip|PopGroup 5|PopGroup 3"},
		// The strike is the smallest at least the size asked for.
		{"SbixTest.ttf", 0x1F600, shape.PaintOptions{PPEM: 16}, "Image png 16x16 strike 20"},
		{"SbixTest.ttf", 0x1F600, shape.PaintOptions{PPEM: 40}, "Image png 80x80 strike 100"},
		// A glyph with no colour is its outline in the foreground.
		{"SbixTest.ttf", 'A', shape.PaintOptions{}, "PushClipGlyph|Solid {0 0 0 255} true|PopClip"},
	} {
		f, gid := colourFace(t, c.file, c.r)
		var rec recorder
		if err := PaintGlyph(f, gid, c.opts, &rec); err != nil {
			t.Errorf("%s %U: %v", c.file, c.r, err)
			continue
		}
		if got := strings.Join(rec.calls, "|"); got != c.want {
			t.Errorf("%s %U painted\n  %s\nwant\n  %s", c.file, c.r, got, c.want)
		}
	}
}

// panicker panics on the first call, as a painter with a bug would.
type panicker struct{ recorder }

func (p *panicker) PushClipGlyph(int) { panic("painter bug") }

func TestPaintGlyphErrors(t *testing.T) {
	f, gid := colourFace(t, "ColourTest.ttf", 0x1F534)
	if err := PaintGlyph(f, gid, shape.PaintOptions{}, &panicker{}); err == nil || !strings.Contains(err.Error(), "painter bug") {
		t.Errorf("painter panic: err = %v", err)
	}
	if err := PaintGlyph(f, 1<<20, shape.PaintOptions{}, &recorder{}); err == nil {
		t.Error("glyph outside the face: no error")
	}
	if err := PaintGlyph(nil, 1, shape.PaintOptions{}, &recorder{}); err == nil {
		t.Error("nil face: no error")
	}
}

func TestCustomFontFallsBackBeforeEmbedded(t *testing.T) {
	data, err := os.ReadFile("../../testdata/colourfonts/ColourTest.ttf")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		opts []Option
		want string // the family drawing the emoji
	}{
		{nil, "Noto Emoji"},
		// A font given with WithFont is tried before the embedded ones for
		// what the families asked for do not cover; the Latin letters stay
		// with Liberation Sans, the default family.
		{[]Option{WithFont("Colour Test", data)}, "Colour Test"},
	} {
		m, err := New(c.opts...)
		if err != nil {
			t.Fatal(err)
		}
		runs, _ := m.ShapeText("Hi \U0001F534", "20px sans-serif")
		if len(runs) != 2 || runs[0].Face.Family != "Liberation Sans" || runs[1].Face.Family != c.want {
			var got []string
			for _, r := range runs {
				got = append(got, r.Face.Family)
			}
			t.Errorf("runs from %v, want Liberation Sans then %s", got, c.want)
		}
	}
}

func TestWithFontInstance(t *testing.T) {
	data, err := os.ReadFile("../../testdata/colourfonts/VarTest.ttf")
	if err != nil {
		t.Fatal(err)
	}
	// U+1F7E0's alpha is 1 at the default weight, 400, and 0.25 at 900.
	for _, c := range []struct {
		opt  Option
		want uint8
	}{
		{WithFont("V", data), 255},
		{WithFontInstance("V", data, map[string]float64{"wght": 900}), 64},
		{WithFontInstance("V", data, map[string]float64{"wght": 650}), 159},
		{WithFontInstance("V", data, map[string]float64{"wght": 2000}), 64}, // clamped to 900
	} {
		m, err := New(c.opt)
		if err != nil {
			t.Fatal(err)
		}
		runs, _ := m.ShapeText("\U0001F7E0", "20px V")
		var rec recorder
		if err := PaintGlyph(runs[0].Face, runs[0].Glyphs[0].GID, shape.PaintOptions{}, &rec); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("Solid {230 26 26 %d} false", c.want); !strings.Contains(strings.Join(rec.calls, "|"), want) {
			t.Errorf("painted %v, want %s", rec.calls, want)
		}
	}
	if _, err := New(WithFontInstance("V", data, map[string]float64{"wdth": 75})); err == nil {
		t.Error("an axis the font does not have: no error")
	}
	// Two instances under one family: CSS font-weight chooses between them.
	m, err := New(WithFontInstance("V", data, map[string]float64{"wght": 400}), WithFontInstance("V", data, map[string]float64{"wght": 900}))
	if err != nil {
		t.Fatal(err)
	}
	for css, want := range map[string]int{"20px V": 400, "bold 20px V": 900, "900 20px V": 900} {
		runs, _ := m.ShapeText("\U0001F7E0", css)
		if got := runs[0].Face.Weight; got != want {
			t.Errorf("%q: the face of weight %d, want %d", css, got, want)
		}
	}
}

func TestWithFontPalette(t *testing.T) {
	data, err := os.ReadFile("../../testdata/colourfonts/ColourTest.ttf")
	if err != nil {
		t.Fatal(err)
	}
	// U+1F534 is a square in palette colour 0 under a triangle in 1: red and
	// blue in the first palette, yellow and black in the second.
	for _, c := range []struct {
		opts []Option
		want string
	}{
		{nil, "Solid {230 26 26 255} false|PopClip|PushClipGlyph|Solid {26 51 230 255}"},
		{[]Option{WithFontPalette("Colour Test", 1)}, "Solid {242 204 26 255} false|PopClip|PushClipGlyph|Solid {26 26 26 255}"},
		// One the font does not have is its first.
		{[]Option{WithFontPalette("colourtest", 7)}, "Solid {230 26 26 255} false|PopClip|PushClipGlyph|Solid {26 51 230 255}"},
		// Another family's palette is not this one's.
		{[]Option{WithFontPalette("Other", 1)}, "Solid {230 26 26 255} false|PopClip|PushClipGlyph|Solid {26 51 230 255}"},
	} {
		m, err := New(append([]Option{WithFont("ColourTest", data)}, c.opts...)...)
		if err != nil {
			t.Fatal(err)
		}
		runs, _ := m.ShapeText("\U0001F534", "20px ColourTest")
		var rec recorder
		if err := PaintGlyph(runs[0].Face, runs[0].Glyphs[0].GID, shape.PaintOptions{}, &rec); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(rec.calls, "|"); !strings.Contains(got, c.want) {
			t.Errorf("painted %s, want %s", got, c.want)
		}
	}
}

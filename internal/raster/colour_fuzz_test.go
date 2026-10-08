package raster

import (
	"os"
	"testing"
	"time"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/fuzzutil"
)

// FuzzColourPainter plays op strings through the PNG writer's colour glyph
// painter and the bounds pass: neither may panic, nor fail the render.
func FuzzColourPainter(f *testing.F) {
	for _, s := range fuzzutil.PaintSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		fuzzutil.Within(t, 10*time.Second, "painting", func() {
			img, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><text x="4" y="50" font-size="48" fill="#08f" fill-opacity="0.7">x</text></svg>`),
				Options{Shaper: fakeShaper{&fakeColourFace{paint: func(p shape.Painter) error {
					fuzzutil.PlayPaint(ops, p)
					return nil
				}}}})
			if err != nil || img == nil {
				t.Errorf("render failed: %v", err)
			}
			fuzzutil.PlayPaint(ops, &colourBounds{face: &fakeColourFace{}})
		})
	})
}

// FuzzColourFont renders every glyph of a mutated colour font.
func FuzzColourFont(f *testing.F) {
	for _, name := range []string{"ColourTest.ttf", "SbixTest.ttf", "SvgTest.ttf"} {
		data, err := os.ReadFile("../../testdata/colourfonts/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// A font is bounded work: one that is not is a failure to record.
		fuzzutil.Within(t, 10*time.Second, "drawing a colour font", func() {
			sh, err := NewShaper(FontData{Family: "F", Data: data})
			if err != nil {
				return
			}
			_, _ = Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="40"><text x="0" y="30" font-family="F" font-size="30" fill-opacity="0.5">&#x1F534;&#x1F7E2;&#x1F308;&#x1F31E;&#x1F300;&#x1F600;&#x1F3A8;&#x1F4A0;&#x1F4A1;&#x1F52E;&#x1F4A7;A</text></svg>`),
				Options{Shaper: sh, Limits: fuzzLimits})
		})
	})
}

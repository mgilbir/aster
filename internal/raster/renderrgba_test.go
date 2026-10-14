package raster

import (
	"image"
	"testing"

	"github.com/mgilbir/aster/internal/fuzzutil"
)

// TestRenderRGBAMatchesRender checks that RenderRGBA's canvas has Render's
// alpha at every pixel and that its ink rectangle holds every pixel that is
// not transparent, on the synthetic documents of the tests and the SVG of
// real charts.
func TestRenderRGBAMatchesRender(t *testing.T) {
	var docs []string
	for _, list := range [][]synth{synths, featureSynths, filterParity} {
		for _, s := range list {
			docs = append(docs, s.svg)
		}
	}
	docs = append(docs, fuzzutil.Files(t, "../../testdata/vl-convert/expected/v5_8/*.svg", "../../testdata/security/*.svg")...)
	if len(docs) < 100 {
		t.Fatalf("only %d documents", len(docs))
	}
	scales := []float64{1, 2.5}
	if testing.Short() {
		scales = scales[:1]
	}
	inked := 0
	for i, doc := range docs {
		for _, scale := range scales {
			want, werr := Render([]byte(doc), Options{Scale: scale})
			got, ink, gerr := RenderRGBA([]byte(doc), Options{Scale: scale})
			if (werr == nil) != (gerr == nil) {
				t.Fatalf("doc %d at %v: Render error %v, RenderRGBA error %v", i, scale, werr, gerr)
			}
			if werr != nil {
				continue
			}
			if got.Rect != want.Rect {
				t.Fatalf("doc %d at %v: bounds %v, want %v", i, scale, got.Rect, want.Rect)
			}
			if !ink.In(got.Rect) {
				t.Fatalf("doc %d at %v: ink %v outside the canvas %v", i, scale, ink, got.Rect)
			}
			if !ink.Empty() {
				inked++
			}
			for y := got.Rect.Min.Y; y < got.Rect.Max.Y; y++ {
				for x := got.Rect.Min.X; x < got.Rect.Max.X; x++ {
					p := got.PixOffset(x, y)
					if a, wa := got.Pix[p+3], want.Pix[want.PixOffset(x, y)+3]; a != wa {
						t.Fatalf("doc %d at %v: alpha %d at (%d, %d), Render's is %d", i, scale, a, x, y, wa)
					}
					if !image.Pt(x, y).In(ink) && got.Pix[p]|got.Pix[p+1]|got.Pix[p+2]|got.Pix[p+3] != 0 {
						t.Fatalf("doc %d at %v: pixel (%d, %d) is drawn outside ink %v", i, scale, x, y, ink)
					}
				}
			}
		}
	}
	if inked < len(docs)*len(scales)/2 {
		t.Fatalf("only %d of %d renders have ink", inked, len(docs)*len(scales))
	}
	t.Logf("%d documents", len(docs))
}

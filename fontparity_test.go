package aster_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/aster/internal/fonts/liberation"
	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/svgpdf"
	"github.com/mgilbir/aster/internal/text"
)

// withWeight is a copy of font with its OS/2 weight class and italic bit
// set: a face of a family at any weight, made from one font.
func withWeight(font []byte, weight int, italic bool) []byte {
	out := append([]byte(nil), font...)
	n := int(binary.BigEndian.Uint16(out[4:]))
	for i := range n {
		r := out[12+16*i:]
		if string(r[:4]) != "OS/2" {
			continue
		}
		os2 := out[binary.BigEndian.Uint32(r[8:]):]
		binary.BigEndian.PutUint16(os2[4:], uint16(weight))
		sel := binary.BigEndian.Uint16(os2[62:]) &^ (1 | 1<<6) // italic, regular
		if italic {
			sel |= 1
		}
		binary.BigEndian.PutUint16(os2[62:], sel)
	}
	return out
}

// faceRecorder records the face each text was drawn in, by its text: the
// PDF writer's TextShaper, and the rasterizer's Shaper, around the ones the
// converter gives them.
type faceRecorder struct {
	*text.Measurer
	png   raster.Shaper
	mu    sync.Mutex
	faces map[string]*text.Face
}

func (r *faceRecorder) record(s string, f *text.Face) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.faces[s] = f
}

func (r *faceRecorder) ShapeFont(s string, font text.CSSFont) ([]text.Run, float64) {
	runs, w := r.Measurer.ShapeFont(s, font)
	if len(runs) > 0 {
		r.record(s, runs[0].Face)
	}
	return runs, w
}

func (r *faceRecorder) FontData(ctx context.Context, f *text.Face) ([]byte, error) {
	return r.Measurer.FontData(ctx, f)
}

type pngRecorder struct{ *faceRecorder }

func (r pngRecorder) Shape(s string, req raster.FontRequest, size float64) []raster.ShapedGlyph {
	g := r.png.Shape(s, req, size)
	if len(g) > 0 {
		if f, ok := raster.TextFace(g[0].Face); ok {
			r.record(s, f)
		}
	}
	return g
}

// TestFontFaceParity is C31 and C112 (audit 2026-10-18): for the font Vega
// writes into the SVG of a text mark, layout (which measures the canvas
// font string Vega builds), the PNG and the PDF draw the same face, so a
// label is drawn in the font it was laid out in. The sweep covers every
// numeric weight from 1 to 1000 and the keywords, every style, and family
// lists with quotes, a quoted comma, unknown families and generic families
// in the middle, over a family with faces at weights that are not
// multiples of 100.
func TestFontFaceParity(t *testing.T) {
	src := liberation.SansRegular
	var opts []text.Option
	for _, f := range []struct {
		w      int
		italic bool
	}{{100, false}, {300, false}, {400, false}, {450, false}, {549, false}, {600, false}, {700, false}, {900, false}, {300, true}, {700, true}} {
		opts = append(opts, text.WithFont("W", withWeight(src, f.w, f.italic)))
	}
	m, err := text.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	png := raster.NewShaperFromMeasurer(m.Exact())

	var weights []string
	for w := 1; w <= 1000; w++ {
		weights = append(weights, strconv.Itoa(w))
	}
	weights = append(weights, "normal", "bold", "bolder", "lighter", "549.5", "1000.5")
	families := []string{
		`W`, `'W'`, `"W", serif`, `NoSuch, W`, `sans-serif, W`, `NoSuch, sans-serif, 'Liberation Serif'`,
		`'Liberation Serif, W'`, `W, 'Liberation Mono'`, `serif`, `monospace, W`, `system-ui, W`, `ui-serif, W`,
	}
	mismatches := 0
	for _, family := range families {
		for _, style := range []string{"", "italic", "oblique", "normal"} {
			rec := &faceRecorder{Measurer: m, png: png, faces: map[string]*text.Face{}}
			var b strings.Builder
			fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="%d">`, 12*len(weights))
			layout := map[string]*text.Face{}
			for i, w := range weights {
				label := fmt.Sprintf("t%d", i)
				// What vega-scenegraph writes for a text item (svg/style.js),
				// and the font string it measures it with (util/text.js).
				fmt.Fprintf(&b, `<text transform="translate(0,%d)" font-family=%q font-size="12px" font-weight=%q`, 12*i+10, strings.ReplaceAll(family, `"`, "&quot;"), w)
				css := w + " 12px " + family
				if style != "" {
					fmt.Fprintf(&b, ` font-style=%q`, style)
					css = style + " " + css
				}
				fmt.Fprintf(&b, ` fill="#000">%s</text>`, label)
				runs, _ := m.ShapeText(label, css)
				layout[label] = runs[0].Face
			}
			b.WriteString(`</svg>`)
			svg := b.String()
			if _, err := svgpdf.Convert(svg, rec, svgpdf.Options{Text: svgpdf.TextOutlines}); err != nil {
				t.Fatal(err)
			}
			pdfFaces := rec.faces
			rec.faces = map[string]*text.Face{}
			if _, err := raster.Render([]byte(svg), raster.Options{Shaper: pngRecorder{rec}}); err != nil {
				t.Fatal(err)
			}
			shown := 0
			for i, w := range weights {
				label := fmt.Sprintf("t%d", i)
				lf, pf, gf := layout[label], pdfFaces[label], rec.faces[label]
				if lf != pf || lf != gf {
					if mismatches++; shown < 2 {
						shown++
						t.Errorf("family %s, style %q, weight %s: layout %s, PDF %s, PNG %s", family, style, w, id(lf), id(pf), id(gf))
					}
				}
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("%d mismatches in all", mismatches)
	}
}

func id(f *text.Face) string {
	if f == nil {
		return "none"
	}
	return fmt.Sprintf("%s (%d)", f.ID, f.Weight)
}

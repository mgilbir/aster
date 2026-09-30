package wordcloud

import (
	"math"
	"strconv"
	"strings"

	"github.com/mgilbir/aster/internal/text"
)

// Shaper measures and shapes text set in a CSS font shorthand. It is satisfied
// by *text.Measurer.
type Shaper interface {
	MeasureText(text, cssFont string) float64
	ShapeText(text, cssFont string) ([]text.Run, float64)
}

// CanvasRenderer is the TextRenderer that reproduces what upstream gets from a
// node-canvas 2D context (Pango and Cairo): text is measured with the
// Shaper, and the words are drawn as outlines whose pixels are read back as
// an occupancy mask.
//
// Upstream fills and strokes every word in red and treats any pixel with a
// non-zero red channel as occupied, so all that matters is which pixels a
// glyph touches at all. node-canvas draws text as paths (its default
// textDrawingMode), so the fill is a Cairo path fill at exact, unrounded glyph
// positions and the padding a Cairo stroke of the same path; see ink.go for
// the scan conversion.
type CanvasRenderer struct {
	Shaper Shaper
	ink    inkRaster
}

// NewCanvasRenderer returns a CanvasRenderer. It keeps scratch buffers and is
// not safe for concurrent use; a layout drives it from one goroutine.
func NewCanvasRenderer(s Shaper) *CanvasRenderer { return &CanvasRenderer{Shaper: s} }

// canvasFont is what assigning `style weight Npx family` to a canvas context's
// font does: a string that is not a valid CSS font leaves the default,
// `10px sans-serif`, in place.
func canvasFont(f Font) Font {
	if f.Px > 0 && validStyle(f.Style) && validWeight(f.Weight) && strings.TrimSpace(f.Family) != "" {
		return f
	}
	return Font{Style: "normal", Weight: "normal", Family: "sans-serif", Px: 10}
}

func validStyle(s string) bool { return s == "normal" || s == "italic" || s == "oblique" }

func validWeight(w string) bool {
	switch w {
	case "normal", "bold", "bolder", "lighter":
		return true
	}
	n, err := strconv.ParseFloat(w, 64)
	return err == nil && n >= 1 && n <= 1000 && strconv.FormatFloat(n, 'f', -1, 64) == w
}

func (f Font) css() string {
	return f.Style + " " + f.Weight + " " + strconv.Itoa(f.Px) + "px " + f.Family
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Measure implements TextRenderer.
func (r *CanvasRenderer) Measure(f Font, text string) float64 {
	return r.Shaper.MeasureText(text, canvasFont(f).css())
}

// Draw implements TextRenderer.
func (r *CanvasRenderer) Draw(m *Mask, f Font, txt string, tx, ty, angle, strokeWidth float64) {
	if !finite(tx) || !finite(ty) || !(strokeWidth >= 0) || !finite(strokeWidth) {
		return
	}
	f = canvasFont(f)
	runs, total := r.Shaper.ShapeText(txt, f.css())
	if len(runs) == 0 {
		return
	}
	// node-canvas centres with `x -= logical_rect.width / 2`, both integers:
	// pango_layout_get_pixel_extents rounds the logical width up to a whole
	// pixel, and the C division truncates.
	wpx := (int(math.Round(total*1024)) + 1023) >> 10
	pen := -float64(wpx / 2)

	// A canvas ignores a rotation that is not finite.
	if math.IsNaN(angle) || math.IsInf(angle, 0) {
		angle = 0
	}
	s, c := math.Sincos(angle)
	// A glyph reaches about an em from its pen position; one that cannot
	// touch the sheet is not traced, which bounds the work for a long word.
	reach := 2*float64(f.Px) + strokeWidth
	ink := &r.ink
	ink.reset()
	for _, run := range runs {
		for _, g := range run.Glyphs {
			gx, gy := pen+g.XOffset, -g.YOffset
			pen += g.Advance
			dx, dy := tx+float64(c*gx)-float64(s*gy), ty+float64(s*gx)+float64(c*gy)
			if dx+reach < 0 || dx-reach > sheetW || dy+reach < 0 || dy-reach > sheetH {
				continue
			}
			segs, err := text.GlyphOutline(run.Face, g.GID, run.Size)
			if err != nil {
				continue
			}
			ink.addGlyph(segs, gx, gy, tx, ty, c, s)
		}
	}
	ink.fill(m)
	if strokeWidth > 0 {
		ink.stroke(m, strokeWidth)
	}
}

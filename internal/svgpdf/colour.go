package svgpdf

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"
	"unicode/utf16"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/imageref"
	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/text"
)

// colourImagePPEM is the size, in pixels per em, a colour glyph PDF cannot
// draw is drawn at as an image: four times a 64px label's.
const colourImagePPEM = 256

// colourGlyph reports whether glyph gid of face is drawn in colour: a COLR,
// sbix, CBDT or EBDT glyph. An SVG-table glyph is drawn from its outline. A
// PDF has no device size, so a bitmap glyph is drawn from its largest strike.
func colourGlyph(face *text.Face, gid int) bool {
	c := text.GlyphColour(face, gid, shape.PaintOptions{})
	return c != shape.ColourNone && c != shape.ColourSVG
}

// hasColour reports whether a run has a colour glyph.
func hasColour(run text.Run) bool {
	for _, g := range run.Glyphs {
		if colourGlyph(run.Face, g.GID) {
			return true
		}
	}
	return false
}

// drawTextRunColour draws a run that has colour glyphs: those painted, the
// rest as outlines. In the modes that write text, the run is also written as
// invisible text in its own font, as a scanned page's OCR text is, so that it
// is found, selected and copied as text; its subset keeps the outlines and
// none of the colour tables. The whole is marked with the run's source text
// as ActualText, for the readers that take it over what is drawn.
func (r *renderer) drawTextRunColour(run text.Run, penX float64, str string, textStart, textEnd int, st gstate) (float64, error) {
	actual := r.fonts != nil && textStart >= 0 && textStart < textEnd && textEnd <= len(str)
	if actual {
		r.w.beginActualText(str[textStart:textEnd])
	}
	if r.fonts != nil {
		if f := r.fonts.fontFor(run.Face); f != nil {
			r.w.textRender(3) // neither filled nor stroked
			_, err := r.drawTextRunFont(f, run, penX, str, textEnd)
			r.w.textRender(0)
			if err != nil {
				return penX, err
			}
		}
	}
	fg := st.fill.Color
	alpha := st.opacity * st.fillOpacity * st.fill.alpha()
	outlined := false
	for _, g := range run.Glyphs {
		ox, oy := penX+g.XOffset, -g.YOffset
		if colourGlyph(run.Face, g.GID) {
			if outlined {
				r.w.paint(true, false, false)
				outlined = false
			}
			if err := r.drawColourGlyph(run.Face, g.GID, run.Size, ox, oy, fg, alpha); err != nil {
				return penX, err
			}
		} else if outline, ok := r.glyphOutline(run.Face, g.GID, run.Size); ok {
			outlined = emitGlyphOutline(r.w, outline, ox, oy) || outlined
		}
		penX += g.Advance
	}
	if outlined {
		r.w.paint(true, false, false)
	}
	if actual {
		r.w.endMarkedContent()
	}
	return penX, nil
}

// drawColourGlyph draws one colour glyph with its origin at (x, y): as PDF
// paths, shadings and images where PDF can draw what it paints, and as an
// image of it painted by the PNG writer where it cannot.
func (r *renderer) drawColourGlyph(face *text.Face, gid int, size, x, y float64, fg Color, alpha float64) error {
	upem := float64(face.UnitsPerEm())
	fgc := shape.Color{R: byteOf(fg.R), G: byteOf(fg.G), B: byteOf(fg.B), A: 255}
	opts := shape.PaintOptions{Foreground: fgc}
	var check colourCheck
	if err := text.PaintGlyph(face, gid, opts, &check); err != nil {
		return nil // a glyph that cannot be painted is not drawn, as an empty one
	}
	r.w.save()
	defer r.w.restore()
	// Font units, y up, at the glyph's origin.
	r.w.concat(Matrix{A: size / upem, D: -size / upem, E: x, F: y})
	// A translucent glyph of several paints is drawn whole, as an image, so
	// that its layers are not seen through one another.
	if check.vector && (alpha >= 1 || check.paints <= 1) {
		p := &pdfPainter{r: r, face: face, alpha: alpha, upem: upem}
		if err := text.PaintGlyph(face, gid, opts, p); err != nil {
			return err
		}
		return p.err
	}
	return r.drawGlyphImage(face, gid, fgc, alpha)
}

// drawGlyphImage draws a glyph as an image the PNG writer paints, in font
// units at the glyph's origin.
func (r *renderer) drawGlyphImage(face *text.Face, gid int, fg shape.Color, alpha float64) error {
	var box shape.Rect
	key := fmt.Sprintf("glyph:%p:%d:%d,%d,%d", face, gid, fg.R, fg.G, fg.B)
	img, err := r.images.put(key, func() (*image.NRGBA, error) {
		px, b, err := raster.ColourGlyphImage(face, gid, fg, colourImagePPEM)
		box = b
		return px, err
	})
	if err != nil || img == nil {
		return err
	}
	if box == (shape.Rect{}) {
		box = r.glyphBoxes[key]
	} else {
		if r.glyphBoxes == nil {
			r.glyphBoxes = map[string]shape.Rect{}
		}
		r.glyphBoxes[key] = box
	}
	r.drawImageIn(img, box, alpha)
	return nil
}

// drawImageIn draws img filling box, in the current space with y up.
func (r *renderer) drawImageIn(img *pdfImage, box shape.Rect, alpha float64) {
	r.w.save()
	r.w.setAlpha(alpha, alpha)
	r.w.concat(Matrix{A: box.XMax - box.XMin, D: box.YMax - box.YMin, E: box.XMin, F: box.YMin})
	r.w.drawXObject(img.res(true))
	r.w.restore()
}

func byteOf(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(1, v)) * 255)) }

// colourCheck is a painter that paints nothing and finds whether PDF can
// draw a glyph's painting as it is: with no sweep gradient, gradients only
// padded and opaque, and groups only drawn source-over, which painting
// straight onto the page is. It counts the paints.
type colourCheck struct {
	notVector bool
	vector    bool
	paints    int
}

func (c *colourCheck) PushTransform(shape.Transform) {}
func (c *colourCheck) PopTransform()                 {}
func (c *colourCheck) PushClipGlyph(int)             {}
func (c *colourCheck) PushClipRect(shape.Rect)       {}
func (c *colourCheck) PopClip()                      {}
func (c *colourCheck) PushGroup()                    {}
func (c *colourCheck) PopGroup(m shape.CompositeMode) {
	c.need(m == shape.CompositeSrcOver)
}
func (c *colourCheck) Solid(shape.Color, bool) { c.need(true) }
func (c *colourCheck) LinearGradient(g shape.LinearGradient) {
	c.need(vectorLine(g.Line))
}
func (c *colourCheck) RadialGradient(g shape.RadialGradient) {
	// A start circle the colour line moves to a negative radius is not one
	// PDF can draw.
	lo := 0.0
	if s := sortedStops(g.Line); len(s) > 0 {
		lo = s[0].Offset
	}
	c.need(vectorLine(g.Line) && g.R0+lo*(g.R1-g.R0) >= 0)
}
func (c *colourCheck) SweepGradient(shape.SweepGradient) { c.need(false) }
func (c *colourCheck) Image(img shape.Image) {
	c.need(img.Format == shape.ImagePNG || img.Format == shape.ImageMask)
}

func (c *colourCheck) need(ok bool) {
	c.paints++
	c.notVector = c.notVector || !ok
	c.vector = !c.notVector
}

// vectorLine reports whether a colour line is one a PDF shading draws: padded,
// and opaque, a shading having no alpha.
func vectorLine(l shape.ColorLine) bool {
	if l.Extend != shape.ExtendPad {
		return false
	}
	for _, s := range l.Stops {
		if s.Color.A != 255 {
			return false
		}
	}
	return true
}

func sortedStops(l shape.ColorLine) []shape.ColorStop {
	st := append([]shape.ColorStop(nil), l.Stops...)
	sort.SliceStable(st, func(i, j int) bool { return st[i].Offset < st[j].Offset })
	return st
}

// pdfPainter writes a glyph's painting as PDF: a transform or clip is a q, Q
// pair around what it applies to, a fill paints the clip, a gradient is a
// shading and an image an image XObject. Groups are only ever source-over
// (see colourCheck), and painted straight onto the page.
type pdfPainter struct {
	r     *renderer
	face  *text.Face
	alpha float64
	upem  float64
	err   error
}

// big is past any glyph's painting, in font units: a fill covers it and the
// clips around it bound it.
const big = 1 << 15

func (p *pdfPainter) PushTransform(t shape.Transform) {
	p.r.w.save()
	p.r.w.concat(Matrix{A: t.XX, B: t.YX, C: t.XY, D: t.YY, E: t.X0, F: t.Y0})
}

func (p *pdfPainter) PopTransform() { p.r.w.restore() }

func (p *pdfPainter) PushClipGlyph(gid int) {
	p.r.w.save()
	outline, err := text.GlyphOutline(p.face, gid, p.upem)
	if err != nil || len(outline) == 0 {
		// Nothing is inside an empty outline.
		p.r.w.rect(0, 0, 0, 0)
		p.r.w.clip()
		return
	}
	// The outline is y down; the painting y up.
	for i := range outline {
		for j := range outline[i].P {
			outline[i].P[j].Y = -outline[i].P[j].Y
		}
	}
	emitGlyphOutline(p.r.w, outline, 0, 0)
	p.r.w.clip()
}

func (p *pdfPainter) PushClipRect(rc shape.Rect) {
	p.r.w.save()
	p.r.w.rect(rc.XMin, rc.YMin, rc.XMax-rc.XMin, rc.YMax-rc.YMin)
	p.r.w.clip()
}

func (p *pdfPainter) PopClip()                     { p.r.w.restore() }
func (p *pdfPainter) PushGroup()                   {}
func (p *pdfPainter) PopGroup(shape.CompositeMode) {}

func (p *pdfPainter) Solid(c shape.Color, _ bool) {
	p.r.w.fillColor(colourOf(c))
	a := p.alpha * float64(c.A) / 255
	p.r.w.setAlpha(a, a)
	p.r.w.rect(-big, -big, 2*big, 2*big)
	p.r.w.paint(true, false, false)
}

func colourOf(c shape.Color) Color {
	return Color{R: float64(c.R) / 255, G: float64(c.G) / 255, B: float64(c.B) / 255}
}

// shading registers a gradient's shading, its colour line sorted and moved
// to run from 0 to 1, and paints it. at gives the geometry at the colour
// line's offset t.
func (p *pdfPainter) shading(radial bool, line shape.ColorLine, at func(lo, hi float64) []float64) {
	st := sortedStops(line)
	if len(st) == 0 {
		return
	}
	lo, hi := st[0].Offset, st[len(st)-1].Offset
	if hi == lo {
		hi = lo + 1e-9
	}
	g := &gradient{radial: radial, coords: at(lo, hi)}
	for _, s := range st {
		g.stops = append(g.stops, gradStop{offset: (s.Offset - lo) / (hi - lo), color: colourOf(s.Color)})
	}
	// A glyph drawn again draws the same shading, which is written once.
	key := fmt.Sprint(g.radial, g.coords, g.stops)
	if seen, ok := p.r.glyphShadings[key]; ok {
		g = seen
	} else {
		if p.r.glyphShadings == nil {
			p.r.glyphShadings = map[string]*gradient{}
		}
		p.r.glyphShadings[key] = g
	}
	p.r.w.setAlpha(p.alpha, p.alpha)
	p.r.w.shade(p.r.shadingOf(g))
}

func (p *pdfPainter) LinearGradient(g shape.LinearGradient) {
	// From P0 to P1 projected onto the line through P0 at right angles to P0P2.
	p0, p1, p2 := g.P0, g.P1, g.P2
	nx, ny := -(p2.Y - p0.Y), p2.X-p0.X
	p3x, p3y := p1.X, p1.Y
	if n2 := nx*nx + ny*ny; n2 > 0 {
		k := ((p1.X-p0.X)*nx + (p1.Y-p0.Y)*ny) / n2
		p3x, p3y = p0.X+k*nx, p0.Y+k*ny
	}
	dx, dy := p3x-p0.X, p3y-p0.Y
	if dx == 0 && dy == 0 {
		return
	}
	p.shading(false, g.Line, func(lo, hi float64) []float64 {
		return []float64{p0.X + lo*dx, p0.Y + lo*dy, p0.X + hi*dx, p0.Y + hi*dy}
	})
}

func (p *pdfPainter) RadialGradient(g shape.RadialGradient) {
	cx, cy, cr := g.C1.X-g.C0.X, g.C1.Y-g.C0.Y, g.R1-g.R0
	p.shading(true, g.Line, func(lo, hi float64) []float64 {
		return []float64{g.C0.X + lo*cx, g.C0.Y + lo*cy, g.R0 + lo*cr, g.C0.X + hi*cx, g.C0.Y + hi*cy, g.R0 + hi*cr}
	})
}

// SweepGradient is never called: colourCheck sends such a glyph to an image.
func (p *pdfPainter) SweepGradient(shape.SweepGradient) {}

func (p *pdfPainter) Image(img shape.Image) {
	if p.err != nil {
		return
	}
	var key string
	var make func() (*image.NRGBA, error)
	switch img.Format {
	case shape.ImagePNG:
		key = fmt.Sprintf("glyphpng:%p:%d", &img.Data[0], len(img.Data))
		make = func() (*image.NRGBA, error) { return decodeNRGBA(img.Data, p.r.lim) }
	case shape.ImageMask:
		c := img.Color
		key = fmt.Sprintf("glyphmask:%p:%d:%dx%d:%d,%d,%d,%d", p.face, len(img.Data), img.Width, img.Height, c.R, c.G, c.B, c.A)
		make = func() (*image.NRGBA, error) { return maskNRGBA(img), nil }
	default:
		return
	}
	if len(img.Data) == 0 {
		return
	}
	pi, err := p.r.images.put(key, make)
	if err != nil {
		p.err = err
		return
	}
	if pi != nil {
		p.r.drawImageIn(pi, img.Box, p.alpha)
	}
}

// decodeNRGBA decodes a bitmap glyph's PNG. One that does not decode is not
// drawn; one over the limits is an error.
func decodeNRGBA(data []byte, lim Limits) (*image.NRGBA, error) {
	src, err := imageref.Decode(data, lim.imageLimits())
	if err != nil {
		if errors.Is(err, ErrLimit) {
			return nil, err
		}
		return nil, nil
	}
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	return out, nil
}

// maskNRGBA is a monochrome or greyscale strike's coverage in its colour.
func maskNRGBA(img shape.Image) *image.NRGBA {
	w, h := img.Width, img.Height
	if w <= 0 || h <= 0 || len(img.Data) < w*h {
		return nil
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	c := img.Color
	for i, cov := range img.Data[:w*h] {
		out.SetNRGBA(i%w, i/w, color.NRGBA{c.R, c.G, c.B, uint8(uint32(c.A) * uint32(cov) / 255)})
	}
	return out
}

// beginActualText starts marked content whose text, for search, selection and
// copying, is s rather than what it draws.
func (w *contentWriter) beginActualText(s string) {
	w.buf = append(w.buf, "/Span <</ActualText <FEFF"...)
	for _, u := range utf16.Encode([]rune(s)) {
		w.buf = fmt.Appendf(w.buf, "%04X", u)
	}
	w.buf = append(w.buf, ">>> BDC\n"...)
}

func (w *contentWriter) endMarkedContent() { w.buf = append(w.buf, "EMC\n"...) }

// textRender sets the text rendering mode: 0 fills glyphs, 3 draws nothing.
func (w *contentWriter) textRender(mode int) { w.op("Tr", float64(mode)) }

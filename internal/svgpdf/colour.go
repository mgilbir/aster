package svgpdf

import (
	"errors"
	"fmt"
	"hash/fnv"
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

// A colour glyph PDF cannot draw is drawn as an image of eight pixels a
// point of its size, as a 576 dpi print would, and within these.
const (
	minColourImagePPEM = 256
	maxColourImagePPEM = 1024
)

// colourImagePPEM is the size, in pixels per em, a glyph of size points is
// drawn at as an image.
func colourImagePPEM(size float64) int {
	return int(math.Max(minColourImagePPEM, math.Min(maxColourImagePPEM, math.Ceil(8*size))))
}

// maxInvisibleFontBytes bounds the colour fonts whose runs are also written
// as invisible text in their own font.
const maxInvisibleFontBytes = 64 << 20

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
	// A font too large to copy out and subset, such as Apple Color Emoji, at
	// 190 MB, keeps its ActualText only.
	if r.fonts != nil && run.Face.Size() <= maxInvisibleFontBytes {
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
	r.w.save()
	defer r.w.restore()
	// Font units, y up, at the glyph's origin.
	r.w.concat(Matrix{A: size / upem, D: -size / upem, E: x, F: y})
	ctm := r.w.cur.ctm
	key := paintedKey{face, gid, fgc, ctm.A, ctm.B, ctm.C, ctm.D}
	g, ok := r.painted[key]
	if !ok {
		g = r.paintColourGlyph(face, gid, fgc, upem)
		if r.painted == nil {
			r.painted = map[paintedKey]paintedGlyph{}
		}
		r.painted[key] = g
	}
	switch {
	case g.err != nil:
		return g.err
	case g.image:
		return r.drawGlyphImage(face, gid, fgc, alpha, colourImagePPEM(size))
	case len(g.content) == 0:
		return nil
	case alpha >= 1:
		r.w.buf = append(r.w.buf, g.content...)
		return nil
	}
	// The glyph is drawn translucent as one thing at the text's alpha, so
	// that its layers are not seen through one another.
	p := &pdfPainter{r: r, face: face, upem: upem, box: g.box, m: []Matrix{Identity()}}
	p.setOrigin()
	fm := p.pageForm(g.content, p.bbox())
	r.w.setAlpha(alpha, alpha)
	p.toForm()
	r.w.drawXObject(fm)
	return nil
}

// paintedKey is a colour glyph as it is painted: by its face, in a
// foreground, under a transform's size and turn. Where it is on the page is
// not part of it, as nothing of a glyph's painting depends on that.
type paintedKey struct {
	face       *text.Face
	gid        int
	fg         shape.Color
	a, b, c, d float64
}

// paintedGlyph is how a colour glyph is drawn: as an image, or as its
// content, the operators that paint it at the current transform, empty for
// a glyph that paints nothing.
type paintedGlyph struct {
	image   bool
	content []byte
	box     shape.Rect
	err     error
}

// paintColourGlyph paints a glyph once, at the current transform: whether
// PDF can draw it, and if so what draws it. A glyph drawn again at the same
// size and turn is drawn from it, as it paints the same.
func (r *renderer) paintColourGlyph(face *text.Face, gid int, fg shape.Color, upem float64) paintedGlyph {
	opts := shape.PaintOptions{Foreground: fg}
	var check colourCheck
	if err := text.PaintGlyph(face, gid, opts, &check); err != nil {
		return paintedGlyph{} // a glyph that cannot be painted is not drawn, as an empty one
	}
	if !check.vector() {
		return paintedGlyph{image: true}
	}
	box, _ := raster.ColourGlyphBounds(face, gid, opts)
	p := &pdfPainter{r: r, face: face, upem: upem, box: box, m: []Matrix{Identity()}}
	p.setOrigin()
	// The glyph is a group of its own: the backdrop of its composites.
	p.beginGroup()
	err := text.PaintGlyph(face, gid, opts, p)
	p.unwind()
	content := p.endGroup()
	if err != nil || p.err != nil {
		return paintedGlyph{err: errors.Join(err, p.err)}
	}
	return paintedGlyph{content: content, box: box}
}

// setOrigin places the glyph's page space at the corner of its box on the
// page (see pageForm).
func (p *pdfPainter) setOrigin() {
	ctm, b := p.r.w.cur.ctm, p.box
	p.origin = [2]float64{math.Inf(1), math.Inf(1)}
	for _, c := range [][2]float64{{b.XMin, b.YMin}, {b.XMax, b.YMin}, {b.XMin, b.YMax}, {b.XMax, b.YMax}} {
		x, y := ctm.Apply(c[0], c[1])
		p.origin = [2]float64{math.Min(p.origin[0], x), math.Min(p.origin[1], y)}
	}
}

// drawGlyphImage draws a glyph as an image the PNG writer paints, in font
// units at the glyph's origin.
func (r *renderer) drawGlyphImage(face *text.Face, gid int, fg shape.Color, alpha float64, ppem int) error {
	var box shape.Rect
	key := fmt.Sprintf("glyph:%p:%d:%d,%d,%d:%d", face, gid, fg.R, fg.G, fg.B, ppem)
	img, err := r.images.put(key, func() (*image.NRGBA, error) {
		px, b, err := raster.ColourGlyphImage(face, gid, fg, ppem)
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
// draw a glyph's painting as it is: with no Xor or Plus, which PDF has no way
// to draw, no padded radial gradient starting at a negative radius, and each
// Porter-Duff operator's backdrop whole, with no clip or transform opened
// in its group before the group composited onto it.
type colourCheck struct {
	notVector bool
	depth     int   // clips and transforms open
	groups    []int // depth at each open group's start, the glyph's first
}

func (c *colourCheck) vector() bool { return !c.notVector }

func (c *colourCheck) PushTransform(shape.Transform) { c.depth++ }
func (c *colourCheck) PopTransform()                 { c.depth-- }
func (c *colourCheck) PushClipGlyph(int)             { c.depth++ }
func (c *colourCheck) PushClipRect(shape.Rect)       { c.depth++ }
func (c *colourCheck) PopClip()                      { c.depth-- }
func (c *colourCheck) PushGroup()                    { c.groups = append(c.groups, c.depth) }
func (c *colourCheck) PopGroup(m shape.CompositeMode) {
	n := len(c.groups)
	if n == 0 {
		c.need(false)
		return
	}
	c.groups = c.groups[:n-1]
	switch m {
	case shape.CompositeXor, shape.CompositePlus:
		c.need(false)
	default:
		// The enclosing group's start: the glyph's, at depth 0, for the
		// outermost.
		start := 0
		if n > 1 {
			start = c.groups[n-2]
		}
		c.need(!porterDuff(m) || c.depth == start)
	}
}
func (c *colourCheck) Solid(shape.Color, bool)             { c.need(true) }
func (c *colourCheck) LinearGradient(shape.LinearGradient) { c.need(true) }
func (c *colourCheck) SweepGradient(shape.SweepGradient)   { c.need(true) }
func (c *colourCheck) RadialGradient(g shape.RadialGradient) {
	// A padded gradient whose colour line moves its start circle to a
	// negative radius is not one PDF can draw; a repeated one is drawn from
	// where the radius is zero.
	lo := 0.0
	if s := sortedStops(g.Line); len(s) > 0 {
		lo = s[0].Offset
	}
	c.need(g.Line.Extend != shape.ExtendPad || g.R0+lo*(g.R1-g.R0) >= 0)
}
func (c *colourCheck) Image(img shape.Image) {
	c.need(img.Format == shape.ImagePNG || img.Format == shape.ImageMask)
}

func (c *colourCheck) need(ok bool) { c.notVector = c.notVector || !ok }

func sortedStops(l shape.ColorLine) []shape.ColorStop {
	st := append([]shape.ColorStop(nil), l.Stops...)
	sort.SliceStable(st, func(i, j int) bool { return st[i].Offset < st[j].Offset })
	return st
}

// pdfPainter writes a glyph's painting as PDF: a transform or clip is a q, Q
// pair around what it applies to, a fill paints the clip, a gradient is a
// shading, an image an image XObject, and a group is composited as PopGroup
// says.
type pdfPainter struct {
	r      *renderer
	face   *text.Face
	upem   float64
	box    shape.Rect // the glyph's bounds, in its font units
	m      []Matrix   // the current user space to the glyph's font units; innermost last
	depth  int        // clips and transforms open
	open   []bool     // which of them, innermost last, are transforms
	origin [2]float64 // where the glyph's page space starts on the page (see pageForm)
	groups []pdfGroup
	err    error
}

// top maps the current user space into the glyph's font units.
func (p *pdfPainter) top() Matrix { return p.m[len(p.m)-1] }

// big is past any glyph's painting, in font units: a fill covers it and the
// clips around it bound it.
const big = 1 << 15

func (p *pdfPainter) PushTransform(t shape.Transform) {
	m := Matrix{A: t.XX, B: t.YX, C: t.XY, D: t.YY, E: t.X0, F: t.Y0}
	p.push(true)
	p.r.w.concat(m)
	p.m = append(p.m, p.top().Mul(m))
}

func (p *pdfPainter) PopTransform() { p.pop() }

// push opens a q for a transform or a clip.
func (p *pdfPainter) push(transform bool) {
	p.r.w.save()
	p.open = append(p.open, transform)
	p.depth++
}

// pop closes the innermost transform or clip with its Q. One the current
// group did not open is not closed: forme balances its pushes and pops, and
// a stray Q would corrupt the page.
func (p *pdfPainter) pop() {
	start := 0
	if n := len(p.groups); n > 0 {
		start = p.groups[n-1].depth
	}
	if p.depth <= start {
		return
	}
	p.r.w.restore()
	if p.open[len(p.open)-1] && len(p.m) > 1 {
		p.m = p.m[:len(p.m)-1]
	}
	p.open = p.open[:len(p.open)-1]
	p.depth--
}

// closeTo closes the transforms and clips opened since depth.
func (p *pdfPainter) closeTo(depth int) {
	for p.depth > depth {
		p.r.w.restore()
		if p.open[len(p.open)-1] && len(p.m) > 1 {
			p.m = p.m[:len(p.m)-1]
		}
		p.open = p.open[:len(p.open)-1]
		p.depth--
	}
}

// unwind closes everything a painting left open, down to its own group: what
// forme never does, but what would otherwise leave the page unbalanced.
func (p *pdfPainter) unwind() {
	for len(p.groups) > 1 {
		p.PopGroup(shape.CompositeSrcOver)
	}
	if len(p.groups) == 1 {
		p.closeTo(p.groups[0].depth)
	}
}

func (p *pdfPainter) PushClipGlyph(gid int) {
	p.push(false)
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
	p.push(false)
	p.r.w.rect(rc.XMin, rc.YMin, rc.XMax-rc.XMin, rc.YMax-rc.YMin)
	p.r.w.clip()
}

func (p *pdfPainter) PopClip() { p.pop() }

func (p *pdfPainter) Solid(c shape.Color, _ bool) {
	p.r.w.fillColor(colourOf(c))
	a := float64(c.A) / 255
	p.r.w.setAlpha(a, a)
	// The glyph's bounds, which the clips around a fill are within: a fill
	// of the plane trips readers inside a soft mask's group.
	b := p.bbox()
	p.r.w.rect(b[0], b[1], b[2]-b[0], b[3]-b[1])
	p.r.w.paint(true, false, false)
}

func colourOf(c shape.Color) Color {
	return Color{R: float64(c.R) / 255, G: float64(c.G) / 255, B: float64(c.B) / 255}
}

// colourLine is a gradient's colour line sorted and moved to run from 0 to
// 1, as its colour and its alpha, and the offsets its 0 and 1 were. alpha is
// nil when every stop is opaque. ok is false when there are no stops.
func colourLine(line shape.ColorLine) (colour, alpha []gradStop, lo, hi float64, ok bool) {
	st := sortedStops(line)
	if len(st) == 0 {
		return nil, nil, 0, 0, false
	}
	lo, hi = st[0].Offset, st[len(st)-1].Offset
	if hi == lo {
		hi = lo + 1e-9
	}
	translucent := false
	for _, s := range st {
		t := (s.Offset - lo) / (hi - lo)
		colour = append(colour, gradStop{offset: t, color: colourOf(s.Color)})
		a := float64(s.Color.A) / 255
		alpha = append(alpha, gradStop{offset: t, color: Color{R: a, G: a, B: a}})
		translucent = translucent || s.Color.A != 255
	}
	if !translucent {
		alpha = nil
	}
	return colour, alpha, lo, hi, true
}

// paint paints a gradient, g in colour and, when its stops are translucent,
// a, the same in grey, as a soft mask of its alpha.
func (p *pdfPainter) paint(g, a *gradient) {
	w := p.r.w
	if a == nil {
		w.setAlpha(1, 1)
		w.shade(p.r.shadingOf(p.shared(g)))
		return
	}
	mask := p.pageForm([]byte("/"+p.r.shadingOf(p.shared(a))+" sh\n"), p.bbox())
	// The mask is set in the glyph's page space, then the gradient painted
	// back in the current one.
	k := p.formMatrix()
	w.save()
	p.toForm()
	w.stateGS("lum:"+mask, gsEntry{mask: mask, maskLuminosity: true})
	w.concat(k)
	w.setAlpha(1, 1)
	w.shade(p.r.shadingOf(p.shared(g)))
	w.restore()
}

// shared is g, or the gradient drawn before that is the same: a glyph drawn
// again draws the same shading, which is written once.
func (p *pdfPainter) shared(g *gradient) *gradient {
	key := fmt.Sprint(g.radial, g.gray, g.coords, g.stops, g.domain, g.periods, len(g.mesh))
	if g.mesh != nil {
		key += fmt.Sprint(g.mesh)
	}
	if seen, ok := p.r.glyphShadings[key]; ok {
		return seen
	}
	if p.r.glyphShadings == nil {
		p.r.glyphShadings = map[string]*gradient{}
	}
	p.r.glyphShadings[key] = g
	return g
}

// gradients makes the colour and alpha gradients of a linear or radial
// gradient: at gives the coords for a span of the colour line's parameter,
// and span the span the glyph needs of a repeated one.
func (p *pdfPainter) gradients(radial bool, line shape.ColorLine, at func(t0, t1 float64) []float64, span func() (float64, float64, bool)) {
	colour, alpha, _, _, ok := colourLine(line)
	if !ok {
		return
	}
	g := &gradient{radial: radial, stops: colour}
	switch line.Extend {
	case shape.ExtendRepeat, shape.ExtendReflect:
		t0, t1, ok := span()
		if !ok {
			return
		}
		if t1-t0 > maxColourPeriods {
			t1 = t0 + maxColourPeriods
		}
		g.domain = [2]float64{t0, t1}
		g.periods = periodsRepeat
		if line.Extend == shape.ExtendReflect {
			g.periods = periodsReflect
		}
		g.coords = at(t0, t1)
	default:
		g.coords = at(0, 1)
	}
	var a *gradient
	if alpha != nil {
		a = &gradient{radial: radial, gray: true, stops: alpha, coords: g.coords, domain: g.domain, periods: g.periods}
	}
	p.paint(g, a)
}

// corners samples the glyph's bounds in the current space: a grid over it,
// which a repeated radial gradient's span is found from.
func (p *pdfPainter) corners(n int) [][2]float64 {
	b := p.bbox()
	var pts [][2]float64
	for i := 0; i <= n; i++ {
		for j := 0; j <= n; j++ {
			pts = append(pts, [2]float64{b[0] + (b[2]-b[0])*float64(i)/float64(n), b[1] + (b[3]-b[1])*float64(j)/float64(n)})
		}
	}
	return pts
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
	_, _, lo, hi, ok := colourLine(g.Line)
	if !ok {
		return
	}
	// The normalised line: from a at 0 to b at 1.
	dx, dy := p3x-p0.X, p3y-p0.Y
	ax, ay := p0.X+lo*dx, p0.Y+lo*dy
	ux, uy := (hi-lo)*dx, (hi-lo)*dy
	l2 := ux*ux + uy*uy
	if l2 == 0 {
		return
	}
	p.gradients(false, g.Line, func(t0, t1 float64) []float64 {
		return []float64{ax + t0*ux, ay + t0*uy, ax + t1*ux, ay + t1*uy}
	}, func() (float64, float64, bool) {
		t0, t1 := math.Inf(1), math.Inf(-1)
		for _, c := range p.corners(1) {
			t := ((c[0]-ax)*ux + (c[1]-ay)*uy) / l2
			t0, t1 = math.Min(t0, t), math.Max(t1, t)
		}
		return math.Floor(t0), math.Ceil(t1), true
	})
}

func (p *pdfPainter) RadialGradient(g shape.RadialGradient) {
	_, _, lo, hi, ok := colourLine(g.Line)
	if !ok {
		return
	}
	cx, cy, cr := g.C1.X-g.C0.X, g.C1.Y-g.C0.Y, g.R1-g.R0
	// The normalised circles: from (x0, y0, r0) at 0, moving by (dx, dy, dr)
	// to 1.
	x0, y0, r0 := g.C0.X+lo*cx, g.C0.Y+lo*cy, g.R0+lo*cr
	dx, dy, dr := (hi-lo)*cx, (hi-lo)*cy, (hi-lo)*cr
	p.gradients(true, g.Line, func(t0, t1 float64) []float64 {
		return []float64{x0 + t0*dx, y0 + t0*dy, r0 + t0*dr, x0 + t1*dx, y0 + t1*dy, r0 + t1*dr}
	}, func() (float64, float64, bool) {
		// The parameters the glyph's points take, sampled, and a margin.
		t0, t1 := math.Inf(1), math.Inf(-1)
		for _, c := range p.corners(16) {
			if t, ok := radialT(c[0]-x0, c[1]-y0, dx, dy, r0, dr); ok {
				t0, t1 = math.Min(t0, t), math.Max(t1, t)
			}
		}
		if t0 > t1 {
			return 0, 0, false
		}
		t0, t1 = math.Floor(t0)-1, math.Ceil(t1)+1
		// No circle of a negative radius.
		if dr > 0 {
			t0 = math.Max(t0, -r0/dr)
		} else if dr < 0 {
			t1 = math.Min(t1, -r0/dr)
		}
		return t0, t1, t1 > t0
	})
}

// radialT is the largest t whose circle, from radius r0 moving by
// (dx, dy, dr), passes through (px, py) relative to its start, with a radius
// of at least zero.
func radialT(px, py, dx, dy, r0, dr float64) (float64, bool) {
	a := dx*dx + dy*dy - dr*dr
	b := px*dx + py*dy + r0*dr
	c := px*px + py*py - r0*r0
	if math.Abs(a) < 1e-9 {
		if b == 0 {
			return 0, false
		}
		t := c / (2 * b)
		return t, r0+t*dr >= 0
	}
	disc := b*b - a*c
	if disc < 0 {
		return 0, false
	}
	sq := math.Sqrt(disc)
	t1, t2 := (b+sq)/a, (b-sq)/a
	if t1 < t2 {
		t1, t2 = t2, t1
	}
	if r0+t1*dr >= 0 {
		return t1, true
	}
	return t2, r0+t2*dr >= 0
}

// sweepWedges is how many triangles a sweep gradient's mesh has, a half
// degree each.
const sweepWedges = 720

// SweepGradient paints a sweep as a mesh of thin triangles about its centre,
// reaching past the glyph's bounds, each corner coloured from the colour line
// at its angle.
func (p *pdfPainter) SweepGradient(g shape.SweepGradient) {
	colour, alpha, lo, hi, ok := colourLine(g.Line)
	if !ok {
		return
	}
	span := g.EndAngle - g.StartAngle
	a0, da := g.StartAngle+lo*span, (hi-lo)*span
	if da == 0 {
		return
	}
	cx, cy := g.Center.X, g.Center.Y
	r := 0.0
	for _, c := range p.corners(1) {
		r = math.Max(r, math.Hypot(c[0]-cx, c[1]-cy))
	}
	r = r*1.01 + 1
	at := func(stops []gradStop, a float64) Color {
		return colourAt(stops, spread(g.Line.Extend, (a-a0)/da))
	}
	mesh := func(stops []gradStop) []meshVertex {
		var m []meshVertex
		for i := range sweepWedges {
			b0 := 2 * math.Pi * float64(i) / sweepWedges
			b1 := 2 * math.Pi * float64(i+1) / sweepWedges
			c0, c1 := at(stops, b0), at(stops, b1)
			s0, k0 := math.Sincos(b0)
			s1, k1 := math.Sincos(b1)
			m = append(m, meshVertex{cx, cy, at(stops, (b0+b1)/2)},
				meshVertex{cx + r*k0, cy + r*s0, c0}, meshVertex{cx + r*k1, cy + r*s1, c1})
		}
		return m
	}
	sg := &gradient{mesh: mesh(colour)}
	var sa *gradient
	if alpha != nil {
		sa = &gradient{gray: true, mesh: mesh(alpha)}
	}
	p.paint(sg, sa)
}

// spread maps a gradient's parameter into 0..1 as its extend mode does.
func spread(e shape.Extend, t float64) float64 {
	switch e {
	case shape.ExtendRepeat:
		return t - math.Floor(t)
	case shape.ExtendReflect:
		t = math.Mod(t, 2)
		if t < 0 {
			t += 2
		}
		if t > 1 {
			t = 2 - t
		}
		return t
	}
	return math.Max(0, math.Min(1, t))
}

// colourAt is the colour of a colour line, from 0 to 1, at t.
func colourAt(stops []gradStop, t float64) Color {
	if t <= stops[0].offset {
		return stops[0].color
	}
	for i := 1; i < len(stops); i++ {
		a, b := stops[i-1], stops[i]
		if t <= b.offset {
			f := 0.0
			if b.offset > a.offset {
				f = (t - a.offset) / (b.offset - a.offset)
			}
			return Color{R: a.color.R + (b.color.R-a.color.R)*f, G: a.color.G + (b.color.G-a.color.G)*f, B: a.color.B + (b.color.B-a.color.B)*f}
		}
	}
	return stops[len(stops)-1].color
}

func (p *pdfPainter) Image(img shape.Image) {
	if p.err != nil {
		return
	}
	if len(img.Data) == 0 {
		return
	}
	var key string
	var make func() (*image.NRGBA, error)
	switch img.Format {
	case shape.ImagePNG:
		// The font's own bytes, which forme hands out without copying.
		key = fmt.Sprintf("glyphpng:%p:%d", &img.Data[0], len(img.Data))
		make = func() (*image.NRGBA, error) { return decodeNRGBA(img.Data, p.r.lim) }
	case shape.ImageMask:
		// Made for the call, so known by what it holds.
		c := img.Color
		h := fnv.New64a()
		h.Write(img.Data)
		key = fmt.Sprintf("glyphmask:%x:%dx%d:%d,%d,%d,%d", h.Sum64(), img.Width, img.Height, c.R, c.G, c.B, c.A)
		make = func() (*image.NRGBA, error) { return maskNRGBA(img), nil }
	default:
		return
	}
	pi, err := p.r.images.put(key, make)
	if err != nil {
		p.err = err
		return
	}
	if pi != nil {
		p.r.drawImageIn(pi, img.Box, 1)
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

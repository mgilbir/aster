package raster

import (
	"math"
	"sort"

	"github.com/mgilbir/forme/shape"
)

// ColourFace is a Face some of whose glyphs are painted in colour, as COLR,
// sbix, CBDT or EBDT glyphs are, rather than filled from their outlines.
type ColourFace interface {
	Face
	// Colour reports whether glyph gid is painted through Paint at ppem
	// pixels per em; a glyph it is false for is filled from its outline.
	Colour(gid uint32, ppem int) bool
	// Paint paints glyph gid through p, in font units with y pointing up.
	Paint(gid uint32, opts shape.PaintOptions, p shape.Painter) error
}

// maxColourImages bounds the decoded bitmap glyphs a render keeps.
const maxColourImages = 256

// colourImageKey is a bitmap glyph's image by the font bytes it is decoded
// from, which forme hands out without copying.
type colourImageKey struct {
	p *byte
	n int
}

// colourPPEM is the size, in device pixels per em, that a glyph of size user
// units is drawn at under ctm: what picks a bitmap glyph's strike.
func colourPPEM(ctm matrix, size float64) int {
	k := size * math.Sqrt(math.Abs(ctm.det()))
	if !(k > 0) || k > 1<<16 {
		return 0
	}
	return int(math.Ceil(k))
}

// colourGlyph reports whether g is a colour glyph at the size it is drawn
// at under ctm, which it returns.
func colourGlyph(g *glyphPos, ctm matrix) (ColourFace, int, bool) {
	cf, ok := g.g.Face.(ColourFace)
	if !ok {
		return nil, 0, false
	}
	ppem := colourPPEM(ctm, g.size)
	return cf, ppem, cf.Colour(g.g.ID, ppem)
}

// glyphForeground is the colour a colour glyph's foreground paints take: the
// text's fill when it is a colour, its opacity left to the layer the glyph is
// painted into, and black for a fill a colour glyph cannot take (a gradient
// or pattern).
func glyphForeground(sp *state) shape.Color {
	c := black
	switch sp.fill.kind {
	case pColor:
		c = sp.fill.c
	case pCurrent:
		c = sp.color
	case pURL:
		switch sp.fill.fbKind {
		case pColor:
			c = sp.fill.fb
		case pCurrent:
			c = sp.color
		}
	}
	return shape.Color{R: c.r, G: c.g, B: c.b, A: 255}
}

// drawColourGlyph paints one colour glyph into a layer of its own, which is
// then drawn at opacity: a glyph's composite modes reach no further than its
// own paints. It reports false, having drawn nothing, when the face refuses
// to paint the glyph, which is then filled from its outline instead.
func (r *renderer) drawColourGlyph(f ColourFace, g *glyphPos, sp *state, ctm matrix, ppem int, opacity float64) bool {
	// Font units, y up, to device space.
	m := ctm.mul(g.matrix()).mul(scaleM(1, -1))
	return r.paintColour(f, g.g.ID, m, sp, glyphForeground(sp), ppem, opacity)
}

// paintColour paints glyph gid of f under m, from font units (y up) to
// device space, as drawColourGlyph describes.
func (r *renderer) paintColour(f ColourFace, gid uint32, m matrix, sp *state, fg shape.Color, ppem int, opacity float64) bool {
	if r.depth >= r.lim.MaxLayerDepth {
		return false
	}
	prev := r.pushLayer()
	if prev == nil {
		return true // the render has failed
	}
	cs := *sp
	p := &colourPainter{r: r, face: f, st: &cs}
	p.m = []matrix{m}
	p.clips = []*mask{sp.clip}
	opts := shape.PaintOptions{Foreground: fg, PPEM: ppem}
	err := f.Paint(gid, opts, p)
	// Groups a refused or failed painting left open are dropped.
	for len(p.groups) > 0 {
		p.discardGroup()
	}
	if err != nil {
		layer := r.cv
		r.cv, r.depth = prev, r.depth-1
		layer.clearDirty()
		r.pool = append(r.pool, layer)
		return r.err != nil
	}
	r.popLayer(prev, opacity, blendNormal)
	return true
}

// colourPainter draws a glyph's painting onto the renderer's canvas.
type colourPainter struct {
	r    *renderer
	face Face
	st   *state // the text's state, its clip the painting's current one
	fl   flat   // the renderer's own is collecting the text's outlines

	m      []matrix // font units (y up) to device, innermost last
	clips  []*mask  // the clip in effect, innermost last; nil is none
	groups []*canvas
	// flat is the groups that were painted straight into the canvas under
	// them because the layer budget had none to spare, by depth.
	flat []bool
}

func (p *colourPainter) top() matrix { return p.m[len(p.m)-1] }

func (p *colourPainter) PushTransform(t shape.Transform) {
	p.m = append(p.m, p.top().mul(matrix{t.XX, t.YX, t.XY, t.YY, t.X0, t.Y0}))
}

func (p *colourPainter) PopTransform() {
	if len(p.m) > 1 {
		p.m = p.m[:len(p.m)-1]
	}
}

// region is the device area painting may touch: the canvas within the clip.
func (p *colourPainter) region() irect {
	reg := p.r.cv.bounds()
	if c := p.st.clip; c != nil {
		reg = reg.intersect(c.r)
	}
	return reg
}

func (p *colourPainter) pushClip(m *mask) {
	p.clips = append(p.clips, m)
	p.st.clip = m
}

func (p *colourPainter) PushClipGlyph(gid int) {
	o := p.r.glyphOutline(p.face, uint32(gid))
	if o == nil {
		p.pushClip(&mask{r: irect{}})
		return
	}
	// The outline is y down; the painting y up.
	m := p.top().mul(scaleM(1, -1))
	reg := p.region().intersect(deviceBounds(o, m))
	mk := p.r.pathsMask([]*path{o}, []matrix{m}, []bool{false}, reg)
	p.pushClip(intersectMasks(mk, p.st.clip))
}

func (p *colourPainter) PushClipRect(rc shape.Rect) {
	p.pushClip(p.r.rectClip(p.st, p.top(), rect{rc.XMin, rc.YMin, rc.XMax, rc.YMax}))
}

func (p *colourPainter) PopClip() {
	if len(p.clips) > 1 {
		p.clips = p.clips[:len(p.clips)-1]
		p.st.clip = p.clips[len(p.clips)-1]
	}
}

// deviceBounds is the pixel box covering path o under m.
func deviceBounds(o *path, m matrix) irect {
	if len(o.pts) == 0 {
		return irect{}
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, pt := range o.pts {
		q := m.apply(pt)
		minX, maxX = math.Min(minX, q.x), math.Max(maxX, q.x)
		minY, maxY = math.Min(minY, q.y), math.Max(maxY, q.y)
	}
	if !(maxX >= minX) || !(maxY >= minY) {
		return irect{}
	}
	return irect{clampInt(math.Floor(minX)), clampInt(math.Floor(minY)), clampInt(math.Ceil(maxX)), clampInt(math.Ceil(maxY))}
}

func (p *colourPainter) PushGroup() {
	r := p.r
	if r.depth >= r.lim.MaxLayerDepth {
		p.flat = append(p.flat, true)
		return
	}
	prev := r.pushLayer()
	if prev == nil {
		p.flat = append(p.flat, true)
		return
	}
	p.flat = append(p.flat, false)
	p.groups = append(p.groups, prev)
}

func (p *colourPainter) PopGroup(mode shape.CompositeMode) {
	n := len(p.flat)
	if n == 0 {
		return
	}
	flat := p.flat[n-1]
	p.flat = p.flat[:n-1]
	if flat {
		return
	}
	r := p.r
	layer := r.cv
	prev := p.groups[len(p.groups)-1]
	p.groups = p.groups[:len(p.groups)-1]
	r.cv = prev
	r.depth--
	// Where neither the group nor what is beneath has been painted every
	// mode leaves nothing, so the two dirty areas are all there is to do.
	reg := layer.dirty.intersect(layer.bounds())
	if mode != shape.CompositeSrcOver {
		reg = unionRect(reg, prev.dirty.intersect(prev.bounds()))
	}
	if c := p.st.clip; c != nil {
		reg = reg.intersect(c.r)
	}
	if !reg.empty() && r.chargeOps(reg.w()*reg.h()) {
		compositeMode(prev, layer, reg, p.st.clip, mode)
	}
	layer.clearDirty()
	r.pool = append(r.pool, layer)
}

// discardGroup drops the innermost open group unpainted.
func (p *colourPainter) discardGroup() {
	r := p.r
	layer := r.cv
	r.cv = p.groups[len(p.groups)-1]
	p.groups = p.groups[:len(p.groups)-1]
	r.depth--
	layer.clearDirty()
	r.pool = append(r.pool, layer)
}

func unionRect(a, b irect) irect {
	switch {
	case a.empty():
		return b
	case b.empty():
		return a
	}
	return irect{min(a.x0, b.x0), min(a.y0, b.y0), max(a.x1, b.x1), max(a.y1, b.y1)}
}

// paintRegion fills the current clip with ps.
func (p *colourPainter) paintRegion(ps paintSrc) {
	reg := p.region()
	if reg.empty() {
		return
	}
	var o path
	o.addRect(float64(reg.x0), float64(reg.y0), float64(reg.w()), float64(reg.h()))
	p.fl.flatten(&o, identity, shapeTol)
	p.r.fillPolys(&p.fl, false, ps, p.st)
}

func (p *colourPainter) Solid(c shape.Color, _ bool) {
	p.paintRegion(solidPaint(shapeRGBA(c), 1))
}

func shapeRGBA(c shape.Color) rgba {
	return rgba{c.R, c.G, c.B, float32(c.A) / 255}
}

func (p *colourPainter) LinearGradient(g shape.LinearGradient) {
	// COLR's gradient runs from P0 to P1 projected onto the line through P0
	// at right angles to P0P2.
	p0, p1, p2 := g.P0, g.P1, g.P2
	nx, ny := -(p2.Y - p0.Y), p2.X-p0.X
	p3x, p3y := p1.X, p1.Y
	if n2 := nx*nx + ny*ny; n2 > 0 {
		k := ((p1.X-p0.X)*nx + (p1.Y-p0.Y)*ny) / n2
		p3x, p3y = p0.X+k*nx, p0.Y+k*ny
	}
	cg := &colrGradient{kind: colrLinear}
	lo, hi, ok := cg.stops(g.Line)
	if !ok {
		return
	}
	dx, dy := p3x-p0.X, p3y-p0.Y
	cg.x0, cg.y0 = p0.X+lo*dx, p0.Y+lo*dy
	cg.dx, cg.dy = (hi-lo)*dx, (hi-lo)*dy
	if l2 := cg.dx*cg.dx + cg.dy*cg.dy; l2 > 0 {
		cg.dx, cg.dy = cg.dx/l2, cg.dy/l2
	} else {
		return
	}
	p.paintGradient(cg)
}

func (p *colourPainter) RadialGradient(g shape.RadialGradient) {
	cg := &colrGradient{kind: colrRadial}
	lo, hi, ok := cg.stops(g.Line)
	if !ok {
		return
	}
	cx, cy, cr := g.C1.X-g.C0.X, g.C1.Y-g.C0.Y, g.R1-g.R0
	cg.x0, cg.y0, cg.r0 = g.C0.X+lo*cx, g.C0.Y+lo*cy, g.R0+lo*cr
	cg.dx, cg.dy, cg.dr = (hi-lo)*cx, (hi-lo)*cy, (hi-lo)*cr
	p.paintGradient(cg)
}

func (p *colourPainter) SweepGradient(g shape.SweepGradient) {
	cg := &colrGradient{kind: colrSweep}
	lo, hi, ok := cg.stops(g.Line)
	if !ok {
		return
	}
	span := g.EndAngle - g.StartAngle
	cg.x0, cg.y0 = g.Center.X, g.Center.Y
	cg.a0, cg.da = g.StartAngle+lo*span, (hi-lo)*span
	if cg.da == 0 {
		return
	}
	p.paintGradient(cg)
}

func (p *colourPainter) paintGradient(cg *colrGradient) {
	inv, ok := p.top().invert()
	if !ok {
		return
	}
	cg.inv = inv
	p.paintRegion(paintSrc{sh: cg})
}

func (p *colourPainter) Image(img shape.Image) {
	var ri *rasterImage
	switch img.Format {
	case shape.ImagePNG:
		ri = p.r.colourImage(img.Data)
	case shape.ImageMask:
		ri = maskImage(img)
	}
	if ri == nil || ri.w <= 0 || ri.h <= 0 {
		return
	}
	b := img.Box
	// Image pixels, y down from the top of the box, to font units.
	toFont := matrix{(b.XMax - b.XMin) / float64(ri.w), 0, 0, -(b.YMax - b.YMin) / float64(ri.h), b.XMin, b.YMax}
	m := p.top().mul(toFont)
	inv, ok := m.invert()
	if !ok {
		return
	}
	lvl, linv, area := sampledLevel(ri, inv)
	clip := p.r.rectClip(p.st, p.top(), rect{b.XMin, b.YMin, b.XMax, b.YMax})
	if clip.r.empty() {
		return
	}
	saved := p.st.clip
	p.st.clip = clip
	p.paintRegion(paintSrc{sh: &imageShader{img: lvl, inv: linv, area: area, opacity: 255}})
	p.st.clip = saved
}

// colourImage decodes a bitmap glyph's PNG, once per render.
func (r *renderer) colourImage(data []byte) *rasterImage {
	if len(data) == 0 {
		return nil
	}
	k := colourImageKey{&data[0], len(data)}
	if img, ok := r.colourImgs[k]; ok {
		return img
	}
	img, err := decodeImage(data, r.lim)
	if err != nil {
		img = nil
	}
	if r.colourImgs == nil || len(r.colourImgs) >= maxColourImages {
		r.colourImgs = map[colourImageKey]*rasterImage{}
	}
	r.colourImgs[k] = img
	return img
}

// maskImage is a monochrome or greyscale strike's coverage in its colour.
func maskImage(img shape.Image) *rasterImage {
	w, h := img.Width, img.Height
	if w <= 0 || h <= 0 || len(img.Data) < w*h {
		return nil
	}
	c := img.Color
	pix := make([]uint8, w*h*4)
	for i, cov := range img.Data[:w*h] {
		a := mul255(uint32(c.A), uint32(cov))
		pix[i*4] = uint8(mul255(uint32(c.R), a))
		pix[i*4+1] = uint8(mul255(uint32(c.G), a))
		pix[i*4+2] = uint8(mul255(uint32(c.B), a))
		pix[i*4+3] = uint8(a)
	}
	return &rasterImage{w: w, h: h, pix: pix}
}

type colrKind uint8

const (
	colrLinear colrKind = iota
	colrRadial
	colrSweep
)

// colrGradient is a COLR gradient shader. Its colour line is normalised to
// run from 0 to 1 and its geometry moved to match, as HarfBuzz does, so that
// stops out of order or past either end are drawn as the font states them.
type colrGradient struct {
	kind   colrKind
	inv    matrix // device to font units
	extend shape.Extend
	// linear: from (x0,y0), t = (p - p0) . (dx,dy), (dx,dy) scaled by 1/len².
	// radial: circles from (x0,y0,r0) moving by (dx,dy,dr) over t in [0,1].
	// sweep: about (x0,y0), from angle a0 by da over t in [0,1].
	x0, y0, dx, dy float64
	r0, dr         float64
	a0, da         float64
	lut            [lutSize]uint32 // premultiplied RGBA
}

// stops sorts and normalises a colour line into the lookup table, returning
// the offsets the table's 0 and 1 are. ok is false when there is nothing to
// paint.
func (g *colrGradient) stops(line shape.ColorLine) (lo, hi float64, ok bool) {
	if len(line.Stops) == 0 {
		return 0, 0, false
	}
	st := append([]shape.ColorStop(nil), line.Stops...)
	sort.SliceStable(st, func(i, j int) bool { return st[i].Offset < st[j].Offset })
	lo, hi = st[0].Offset, st[len(st)-1].Offset
	if math.IsNaN(lo) || math.IsNaN(hi) || math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return 0, 0, false
	}
	g.extend = line.Extend
	if hi == lo {
		// One offset: under pad, a step from the first colour to the last
		// there; repeated or reflected, the last colour everywhere.
		if g.extend != shape.ExtendPad {
			g.extend = shape.ExtendPad
			st = st[len(st)-1:]
		}
		hi = lo + 1e-9
	}
	premul := func(c shape.Color) [4]float64 {
		a := float64(c.A) / 255
		return [4]float64{float64(c.R) * a, float64(c.G) * a, float64(c.B) * a, a * 255}
	}
	j := 0
	for i := range lutSize {
		t := lo + (hi-lo)*float64(i)/(lutSize-1)
		for j < len(st)-1 && st[j+1].Offset < t {
			j++
		}
		var c [4]float64
		switch {
		case t <= st[0].Offset:
			c = premul(st[0].Color)
		case j >= len(st)-1:
			c = premul(st[len(st)-1].Color)
		default:
			a, b := st[j], st[j+1]
			ca, cb := premul(a.Color), premul(b.Color)
			f := 0.0
			if b.Offset > a.Offset {
				f = (t - a.Offset) / (b.Offset - a.Offset)
			}
			for k := range c {
				c[k] = ca[k] + (cb[k]-ca[k])*f
			}
		}
		g.lut[i] = uint32(clampByte(c[0])) | uint32(clampByte(c[1]))<<8 | uint32(clampByte(c[2]))<<16 | uint32(clampByte(c[3]))<<24
	}
	return lo, hi, true
}

func (g *colrGradient) spread(t float64) float64 {
	switch g.extend {
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

// radialT is the largest t whose circle passes through (x, y) with a radius
// of at least zero: the circle drawn last, as the gradient's circles are
// drawn in order of t.
func (g *colrGradient) radialT(x, y float64) (float64, bool) {
	px, py := x-g.x0, y-g.y0
	a := g.dx*g.dx + g.dy*g.dy - g.dr*g.dr
	b := px*g.dx + py*g.dy + g.r0*g.dr
	c := px*px + py*py - g.r0*g.r0
	if math.Abs(a) < 1e-9 {
		if b == 0 {
			return 0, false
		}
		t := c / (2 * b)
		return t, g.r0+t*g.dr >= 0
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
	if g.r0+t1*g.dr >= 0 {
		return t1, true
	}
	return t2, g.r0+t2*g.dr >= 0
}

func (g *colrGradient) shadeRow(y, x0 int, dst []uint8) {
	n := len(dst) / 4
	py := float64(y) + 0.5
	for i := range n {
		px := float64(x0+i) + 0.5
		gx := g.inv.a*px + g.inv.c*py + g.inv.e
		gy := g.inv.b*px + g.inv.d*py + g.inv.f
		var t float64
		ok := true
		switch g.kind {
		case colrLinear:
			t = (gx-g.x0)*g.dx + (gy-g.y0)*g.dy
		case colrRadial:
			t, ok = g.radialT(gx, gy)
		case colrSweep:
			a := math.Atan2(gy-g.y0, gx-g.x0)
			if a < 0 {
				a += 2 * math.Pi
			}
			t = (a - g.a0) / g.da
		}
		var v uint32
		if ok && !math.IsNaN(t) {
			v = g.lut[int(g.spread(t)*(lutSize-1)+0.5)]
		}
		dst[i*4] = uint8(v)
		dst[i*4+1] = uint8(v >> 8)
		dst[i*4+2] = uint8(v >> 16)
		dst[i*4+3] = uint8(v >> 24)
	}
}

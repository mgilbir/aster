package raster

import "math"

// mul255 returns round(a*b/255).
func mul255(a, b uint32) uint32 {
	t := a*b + 128
	return (t + t>>8) >> 8
}

// canvas is a premultiplied RGBA8 pixel buffer.
type canvas struct {
	w, h  int
	pix   []uint8
	dirty irect // bounding box of pixels possibly non-transparent
}

func newCanvas(w, h int) *canvas {
	return &canvas{w: w, h: h, pix: make([]uint8, w*h*4)}
}

func (c *canvas) bounds() irect { return irect{0, 0, c.w, c.h} }

func (c *canvas) markDirty(r irect) { c.dirty = c.dirty.union(r) }

// clearDirty zeroes the dirty region so the buffer can be reused as a layer.
func (c *canvas) clearDirty() {
	d := c.dirty.intersect(c.bounds())
	if d.empty() {
		c.dirty = irect{}
		return
	}
	if d.x0 == 0 && d.x1 == c.w {
		clear(c.pix[d.y0*c.w*4 : d.y1*c.w*4])
	} else {
		for y := d.y0; y < d.y1; y++ {
			clear(c.pix[(y*c.w+d.x0)*4 : (y*c.w+d.x1)*4])
		}
	}
	c.dirty = irect{}
}

// mask is a coverage map over region r. a==nil means fully covered.
type mask struct {
	r irect
	a []uint8 // r.w()*r.h(), row-major
}

func (m *mask) at(x, y int) uint8 {
	if m.a == nil {
		return 255
	}
	return m.a[(y-m.r.y0)*m.r.w()+(x-m.r.x0)]
}

// shader produces premultiplied RGBA for a pixel span.
type shader interface {
	shadeRow(y, x0 int, dst []uint8)
}

// paintSrc is either a solid premultiplied colour or a shader.
type paintSrc struct {
	solid bool
	col   [4]uint8 // premultiplied
	sh    shader
}

func solidPaint(c rgba, opacity float64) paintSrc {
	a := float64(c.a) * opacity
	if a < 0 {
		a = 0
	} else if a > 1 {
		a = 1
	}
	a8 := uint32(math.Round(a * 255))
	return paintSrc{solid: true, col: [4]uint8{
		uint8(mul255(uint32(c.r), a8)), uint8(mul255(uint32(c.g), a8)), uint8(mul255(uint32(c.b), a8)), uint8(a8),
	}}
}

// blitter composites coverage rows onto a canvas with source-over.
type blitter struct {
	cv    *canvas
	paint paintSrc
	mask  *mask
	tmp   []uint8
}

func (b *blitter) blitRow(y, x0 int, cov []uint8) {
	cv := b.cv
	if y < 0 || y >= cv.h {
		return
	}
	// Clip horizontally to the canvas (the rasterizer clip normally ensures it).
	if x0 < 0 {
		if -x0 >= len(cov) {
			return
		}
		cov = cov[-x0:]
		x0 = 0
	}
	if x0+len(cov) > cv.w {
		if x0 >= cv.w {
			return
		}
		cov = cov[:cv.w-x0]
	}
	n := len(cov)
	dst := cv.pix[(y*cv.w+x0)*4 : (y*cv.w+x0+n)*4]
	var mrow []uint8
	if m := b.mask; m != nil {
		if y < m.r.y0 || y >= m.r.y1 {
			return
		}
		// Restrict to the mask's horizontal extent.
		lo, hi := x0, x0+n
		if lo < m.r.x0 {
			lo = m.r.x0
		}
		if hi > m.r.x1 {
			hi = m.r.x1
		}
		if lo >= hi {
			return
		}
		if lo != x0 || hi != x0+n {
			cov = cov[lo-x0 : hi-x0]
			dst = dst[(lo-x0)*4 : (hi-x0)*4]
			x0 = lo
			n = hi - lo
		}
		if m.a != nil {
			w := m.r.w()
			mrow = m.a[(y-m.r.y0)*w+(x0-m.r.x0):][:n]
		}
	}
	if b.paint.solid {
		b.blitSolid(dst, cov, mrow)
		return
	}
	if cap(b.tmp) < n*4 {
		b.tmp = make([]uint8, n*4)
	}
	src := b.tmp[:n*4]
	b.paint.sh.shadeRow(y, x0, src)
	for i := 0; i < n; i++ {
		c := uint32(cov[i])
		if mrow != nil {
			c = mul255(c, uint32(mrow[i]))
		}
		if c == 0 {
			continue
		}
		sr, sg, sb, sa := uint32(src[i*4]), uint32(src[i*4+1]), uint32(src[i*4+2]), uint32(src[i*4+3])
		if c != 255 {
			sr, sg, sb, sa = mul255(sr, c), mul255(sg, c), mul255(sb, c), mul255(sa, c)
		}
		if sa == 0 {
			continue
		}
		d := dst[i*4 : i*4+4 : i*4+4]
		if sa == 255 {
			d[0], d[1], d[2], d[3] = uint8(sr), uint8(sg), uint8(sb), 255
			continue
		}
		ia := 255 - sa
		d[0] = uint8(sr + mul255(uint32(d[0]), ia))
		d[1] = uint8(sg + mul255(uint32(d[1]), ia))
		d[2] = uint8(sb + mul255(uint32(d[2]), ia))
		d[3] = uint8(sa + mul255(uint32(d[3]), ia))
	}
}

func (b *blitter) blitSolid(dst, cov, mrow []uint8) {
	cr, cg, cb, ca := uint32(b.paint.col[0]), uint32(b.paint.col[1]), uint32(b.paint.col[2]), uint32(b.paint.col[3])
	if ca == 0 {
		return
	}
	for i, cv := range cov {
		c := uint32(cv)
		if mrow != nil {
			c = mul255(c, uint32(mrow[i]))
		}
		if c == 0 {
			continue
		}
		d := dst[i*4 : i*4+4 : i*4+4]
		if c == 255 && ca == 255 {
			d[0], d[1], d[2], d[3] = uint8(cr), uint8(cg), uint8(cb), 255
			continue
		}
		sr, sg, sb, sa := cr, cg, cb, ca
		if c != 255 {
			sr, sg, sb, sa = mul255(cr, c), mul255(cg, c), mul255(cb, c), mul255(ca, c)
		}
		ia := 255 - sa
		d[0] = uint8(sr + mul255(uint32(d[0]), ia))
		d[1] = uint8(sg + mul255(uint32(d[1]), ia))
		d[2] = uint8(sb + mul255(uint32(d[2]), ia))
		d[3] = uint8(sa + mul255(uint32(d[3]), ia))
	}
}

// maskSink accumulates a union of shapes into a mask (source-over on alpha).
type maskSink struct{ m *mask }

func (s maskSink) blitRow(y, x0 int, cov []uint8) {
	m := s.m
	if y < m.r.y0 || y >= m.r.y1 {
		return
	}
	w := m.r.w()
	row := m.a[(y-m.r.y0)*w:][:w]
	for i, c := range cov {
		x := x0 + i - m.r.x0
		if x < 0 || x >= w || c == 0 {
			continue
		}
		o := uint32(row[x])
		row[x] = uint8(o + uint32(c) - mul255(o, uint32(c)))
	}
}

// blendMode is a CSS mix-blend-mode.
type blendMode uint8

const (
	blendNormal blendMode = iota
	blendMultiply
	blendScreen
	blendOverlay
	blendDarken
	blendLighten
	blendColorDodge
	blendColorBurn
	blendHardLight
	blendSoftLight
	blendDifference
	blendExclusion
)

var blendNames = map[string]blendMode{
	"normal": blendNormal, "multiply": blendMultiply, "screen": blendScreen,
	"overlay": blendOverlay, "darken": blendDarken, "lighten": blendLighten,
	"color-dodge": blendColorDodge, "color-burn": blendColorBurn,
	"hard-light": blendHardLight, "soft-light": blendSoftLight,
	"difference": blendDifference, "exclusion": blendExclusion,
}

func blendChannel(mode blendMode, cb, cs float64) float64 {
	switch mode {
	case blendMultiply:
		return cb * cs
	case blendScreen:
		return cb + cs - cb*cs
	case blendOverlay:
		return blendChannel(blendHardLight, cs, cb)
	case blendDarken:
		return math.Min(cb, cs)
	case blendLighten:
		return math.Max(cb, cs)
	case blendColorDodge:
		if cb == 0 {
			return 0
		}
		if cs >= 1 {
			return 1
		}
		return math.Min(1, cb/(1-cs))
	case blendColorBurn:
		if cb >= 1 {
			return 1
		}
		if cs <= 0 {
			return 0
		}
		return 1 - math.Min(1, (1-cb)/cs)
	case blendHardLight:
		if cs <= 0.5 {
			return cb * 2 * cs
		}
		return cb + (2*cs - 1) - cb*(2*cs-1)
	case blendSoftLight:
		if cs <= 0.5 {
			return cb - (1-2*cs)*cb*(1-cb)
		}
		var d float64
		if cb <= 0.25 {
			d = ((16*cb-12)*cb + 4) * cb
		} else {
			d = math.Sqrt(cb)
		}
		return cb + (2*cs-1)*(d-cb)
	case blendDifference:
		return math.Abs(cb - cs)
	case blendExclusion:
		return cb + cs - 2*cb*cs
	}
	return cs
}

// compositeLayer draws layer onto dst within region r, scaled by opacity, the
// optional clip mask and blend mode.
func compositeLayer(dst, layer *canvas, r irect, opacity float64, m *mask, mode blendMode) {
	r = r.intersect(layer.dirty).intersect(dst.bounds())
	if m != nil {
		r = r.intersect(m.r)
	}
	if r.empty() {
		return
	}
	dst.markDirty(r)
	op := uint32(math.Round(math.Max(0, math.Min(1, opacity)) * 255))
	for y := r.y0; y < r.y1; y++ {
		for x := r.x0; x < r.x1; x++ {
			s := layer.pix[(y*layer.w+x)*4 : (y*layer.w+x)*4+4 : (y*layer.w+x)*4+4]
			if s[3] == 0 {
				continue
			}
			compositePixel(dst.pix[(y*dst.w+x)*4:(y*dst.w+x)*4+4:(y*dst.w+x)*4+4], s, op, m, x, y, mode)
		}
	}
}

// compositeLayerAt draws layer onto dst with its origin at (ox, oy).
func compositeLayerAt(dst, layer *canvas, ox, oy int, opacity float64, m *mask, mode blendMode) {
	r := irect{ox, oy, ox + layer.w, oy + layer.h}.intersect(dst.bounds())
	if m != nil {
		r = r.intersect(m.r)
	}
	if r.empty() {
		return
	}
	dst.markDirty(r)
	op := uint32(math.Round(math.Max(0, math.Min(1, opacity)) * 255))
	for y := r.y0; y < r.y1; y++ {
		for x := r.x0; x < r.x1; x++ {
			li := ((y-oy)*layer.w + (x - ox)) * 4
			s := layer.pix[li : li+4 : li+4]
			if s[3] == 0 {
				continue
			}
			compositePixel(dst.pix[(y*dst.w+x)*4:(y*dst.w+x)*4+4:(y*dst.w+x)*4+4], s, op, m, x, y, mode)
		}
	}
}

func compositePixel(d, s []uint8, op uint32, m *mask, x, y int, mode blendMode) {
	c := op
	if m != nil {
		c = mul255(c, uint32(m.at(x, y)))
	}
	if c == 0 {
		return
	}
	sr, sg, sb, sa := uint32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3])
	if c != 255 {
		sr, sg, sb, sa = mul255(sr, c), mul255(sg, c), mul255(sb, c), mul255(sa, c)
	}
	if mode != blendNormal && sa > 0 {
		blendPixel(d, sr, sg, sb, sa, mode)
		return
	}
	ia := 255 - sa
	d[0] = uint8(sr + mul255(uint32(d[0]), ia))
	d[1] = uint8(sg + mul255(uint32(d[1]), ia))
	d[2] = uint8(sb + mul255(uint32(d[2]), ia))
	d[3] = uint8(sa + mul255(uint32(d[3]), ia))
}

// blendPixel applies a separable blend mode with premultiplied inputs (W3C
// compositing spec: Cr = (1-ab)*Cs + ab*B(Cb,Cs), then source-over).
func blendPixel(d []uint8, sr, sg, sb, sa uint32, mode blendMode) {
	as := float64(sa) / 255
	ab := float64(d[3]) / 255
	src := [3]float64{float64(sr) / 255, float64(sg) / 255, float64(sb) / 255}
	var out [3]float64
	for i := 0; i < 3; i++ {
		cs := 0.0
		if as > 0 {
			cs = src[i] / as
		}
		cb := 0.0
		if ab > 0 {
			cb = float64(d[i]) / 255 / ab
		}
		mixed := (1-ab)*cs + ab*blendChannel(mode, cb, cs)
		out[i] = as*mixed + (1-as)*float64(d[i])/255
	}
	for i := 0; i < 3; i++ {
		d[i] = clampByte(out[i] * 255)
	}
	d[3] = clampByte((as + ab - as*ab) * 255)
}

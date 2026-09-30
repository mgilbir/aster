package raster

import (
	"math"
	"strings"
)

const (
	tfX uint8 = 1 << iota
	tfY
	tfDX
	tfDY
)

// tchar is one character of a text element after whitespace processing.
type tchar struct {
	r      rune
	sp     *state
	flags  uint8
	x, y   float64
	dx, dy float64
}

// glyphPos is a glyph placed in user space (before the element transform).
type glyphPos struct {
	g      ShapedGlyph
	x, y   float64
	size   float64
	sp     *state
	chunk  int
	isDeco bool
}

const maxTextChars = 1 << 20

type textCollector struct {
	r        *renderer
	chars    []tchar
	prevSpc  bool
	preserve bool
}

func parseNumberList(st *state, s string, axis int) []float64 {
	var out []float64
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
		v, u, ok := parseLength(f)
		if !ok {
			v = 0
			u = ""
		}
		out = append(out, st.toPx(v, u, axis))
		if len(out) > maxTextChars {
			break
		}
	}
	return out
}

func (tc *textCollector) addText(s string, sp *state) {
	for _, c := range s {
		if len(tc.chars) >= maxTextChars {
			return
		}
		if tc.preserve {
			if c == '\n' || c == '\r' || c == '\t' {
				c = ' '
			}
			tc.chars = append(tc.chars, tchar{r: c, sp: sp})
			continue
		}
		switch c {
		case '\n', '\r':
			continue
		case '\t':
			c = ' '
		}
		if c == ' ' {
			if tc.prevSpc {
				continue
			}
			tc.prevSpc = true
		} else {
			tc.prevSpc = false
		}
		tc.chars = append(tc.chars, tchar{r: c, sp: sp})
	}
}

func (tc *textCollector) walk(n *node, st *state) {
	base := len(tc.chars)
	savedPreserve := tc.preserve
	if v, ok := n.get(aSpace); ok {
		tc.preserve = strings.TrimSpace(v) == "preserve"
	}
	for _, k := range n.kids {
		switch k.tag {
		case tagChars:
			tc.addText(k.text, st)
		case tagTspan:
			if isDisplayNone(k) {
				continue
			}
			ks := new(state)
			*ks = *st
			ks.applyProps(k)
			tc.walk(k, ks)
		}
	}
	tc.preserve = savedPreserve
	end := len(tc.chars)
	// Apply positioning lists: inner elements were applied first, so only fill
	// characters they left unset.
	apply := func(id attrID, axis int, flag uint8, set func(c *tchar, v float64)) {
		v, ok := n.get(id)
		if !ok {
			return
		}
		list := parseNumberList(st, v, axis)
		for j, val := range list {
			if base+j >= end {
				break
			}
			c := &tc.chars[base+j]
			if c.flags&flag == 0 {
				c.flags |= flag
				set(c, val)
			}
		}
	}
	apply(aX, 0, tfX, func(c *tchar, v float64) { c.x = v })
	apply(aY, 1, tfY, func(c *tchar, v float64) { c.y = v })
	apply(aDx, 0, tfDX, func(c *tchar, v float64) { c.dx = v })
	apply(aDy, 1, tfDY, func(c *tchar, v float64) { c.dy = v })
}

func (r *renderer) renderText(n *node, st *state, opacity float64, blend blendMode) {
	tc := &textCollector{r: r, prevSpc: true}
	root := new(state)
	*root = *st
	tc.walk(n, root)
	if len(tc.chars) == 0 {
		return
	}
	if !tc.preserve && tc.chars[len(tc.chars)-1].r == ' ' {
		tc.chars = tc.chars[:len(tc.chars)-1]
	}
	if len(tc.chars) == 0 {
		return
	}
	glyphs := r.layoutText(tc.chars)
	if len(glyphs) == 0 {
		return
	}
	// Draw consecutive glyphs sharing a style together.
	fillAny, strokeAny := false, false
	for _, g := range glyphs {
		if g.sp.visible && g.sp.hasFill() {
			fillAny = true
		}
		if g.sp.visible && g.sp.hasStroke() {
			strokeAny = true
		}
	}
	if !fillAny && !strokeAny {
		return
	}
	layered := blend != blendNormal || (opacity < 1 && fillAny && strokeAny)
	var prev *canvas
	if layered {
		if prev = r.pushLayer(); prev == nil {
			return
		}
	}
	for i := 0; i < len(glyphs); {
		j := i
		for j < len(glyphs) && glyphs[j].sp == glyphs[i].sp {
			j++
		}
		r.drawGlyphs(glyphs[i:j], glyphs[i].sp, opacity, layered)
		i = j
	}
	if layered {
		r.popLayer(prev, opacity, blend)
	}
}

// layoutText places glyphs in text-chunk order and applies text-anchor.
func (r *renderer) layoutText(chars []tchar) []glyphPos {
	var out []glyphPos
	cx, cy := 0.0, 0.0
	chunk := 0
	chunkStart := 0 // index into out where the current chunk begins
	chunkX0 := 0.0
	var chunkAnchor uint8
	finishChunk := func() {
		if len(out) == chunkStart {
			return
		}
		width := cx - chunkX0
		var shift float64
		switch chunkAnchor {
		case anchorMiddle:
			shift = -width / 2
		case anchorEnd:
			shift = -width
		}
		if shift != 0 {
			for i := chunkStart; i < len(out); i++ {
				out[i].x += shift
			}
		}
	}
	i := 0
	for i < len(chars) {
		c := chars[i]
		// A run extends while the style is unchanged and no character carries
		// positioning attributes.
		j := i + 1
		for j < len(chars) && chars[j].sp == c.sp && chars[j].flags == 0 {
			j++
		}
		if c.flags&(tfX|tfY) != 0 || i == 0 {
			finishChunk()
			chunk++
			chunkStart = len(out)
			if c.flags&tfX != 0 {
				cx = c.x
			}
			if c.flags&tfY != 0 {
				cy = c.y
			}
			chunkX0 = cx
			chunkAnchor = c.sp.anchor
		}
		if c.flags&tfDX != 0 {
			cx += c.dx
		}
		if c.flags&tfDY != 0 {
			cy += c.dy
		}
		var sb strings.Builder
		for _, ch := range chars[i:j] {
			sb.WriteRune(ch.r)
		}
		text := sb.String()
		sp := c.sp
		req := FontRequest{Families: sp.families, Weight: sp.fontWeight, Italic: sp.italic}
		shaped := r.shaper.Shape(text, req, sp.fontSize)
		by := cy + r.baselineOffset(sp, shaped)
		runes := []rune(text)
		for gi, g := range shaped {
			out = append(out, glyphPos{g: g, x: cx + g.XOffset, y: by - g.YOffset, size: sp.fontSize, sp: sp, chunk: chunk})
			cx += g.Advance + sp.letterSpacing
			// Word spacing applies to spaces; with one glyph per rune (the common
			// case) index the rune directly.
			if sp.wordSpacing != 0 && len(shaped) == len(runes) && runes[gi] == ' ' {
				cx += sp.wordSpacing
			}
		}
		i = j
	}
	finishChunk()
	return out
}

// baselineOffset returns the downward shift implied by dominant-baseline and
// baseline-shift for a run.
func (r *renderer) baselineOffset(sp *state, shaped []ShapedGlyph) float64 {
	if (sp.baseline == "" || sp.baseline == "auto" || sp.baseline == "alphabetic") && (sp.baselineShift == "" || sp.baselineShift == "baseline") {
		return 0
	}
	var asc, desc, k float64 = 0.8, 0.2, sp.fontSize
	if len(shaped) > 0 && shaped[0].Face != nil {
		f := shaped[0].Face
		asc, desc = f.Ascent()/f.UnitsPerEm(), f.Descent()/f.UnitsPerEm()
	}
	off := 0.0
	switch sp.baseline {
	case "middle":
		off = 0.26 * k // roughly half the x-height
	case "central":
		off = (asc - desc) / 2 * k
	case "hanging":
		off = asc * 0.8 * k
	case "text-before-edge", "text-top", "before-edge":
		off = asc * k
	case "text-after-edge", "text-bottom", "after-edge":
		off = -desc * k
	case "mathematical":
		off = 0.4 * k
	}
	switch sp.baselineShift {
	case "sub":
		off += 0.2 * k
	case "super":
		off -= 0.4 * k
	case "", "baseline":
	default:
		if v, u, ok := parseLength(sp.baselineShift); ok {
			if u == "%" {
				off -= v / 100 * sp.fontSize
			} else {
				off -= sp.toPx(v, u, 1)
			}
		}
	}
	return off
}

type outlineKey struct {
	f   Face
	gid uint32
}

func (r *renderer) glyphOutline(f Face, gid uint32) *path {
	k := outlineKey{f, gid}
	if p, ok := r.outlines[k]; ok {
		if p == emptyPath {
			return nil
		}
		return p
	}
	p := &path{}
	if !f.Outline(gid, pathSink{p}) {
		p = emptyPath
	} else {
		p = closeContours(p)
	}
	if r.outlines == nil {
		r.outlines = map[outlineKey]*path{}
	}
	if len(r.outlines) > 1<<14 {
		clear(r.outlines)
	}
	r.outlines[k] = p
	if p == emptyPath {
		return nil
	}
	return p
}

type pathSink struct{ p *path }

func (s pathSink) MoveTo(x, y float64)                  { s.p.moveTo(x, y) }
func (s pathSink) LineTo(x, y float64)                  { s.p.lineTo(x, y) }
func (s pathSink) QuadTo(x1, y1, x, y float64)          { s.p.quadTo(x1, y1, x, y) }
func (s pathSink) CubicTo(x1, y1, x2, y2, x, y float64) { s.p.cubicTo(x1, y1, x2, y2, x, y) }
func (s pathSink) Close()                               { s.p.close() }

func (r *renderer) drawGlyphs(gs []glyphPos, sp *state, opacity float64, layered bool) {
	if !sp.visible {
		return
	}
	fill := sp.hasFill()
	stroke := sp.hasStroke()
	if !fill && !stroke {
		return
	}
	fo, so := sp.fillOpacity, sp.strokeOpacity
	if !layered && opacity < 1 {
		fo *= opacity
		so *= opacity
	}
	// Use the element's transform (the tspan states carry the same ctm).
	ctm := sp.ctm
	// Bounding box of the glyph cells for objectBoundingBox paint servers.
	var cellBox rect
	haveBox := false
	bboxFn := func() (rect, bool) {
		if haveBox {
			return cellBox, true
		}
		var b bbox
		for _, g := range gs {
			f := g.g.Face
			if f == nil {
				continue
			}
			asc, desc := f.Ascent()/f.UnitsPerEm()*g.size, f.Descent()/f.UnitsPerEm()*g.size
			b.add(point{g.x, g.y - asc})
			b.add(point{g.x + g.g.Advance, g.y + desc})
		}
		cellBox, haveBox = b.r, b.ok
		return cellBox, b.ok
	}
	if fill {
		if ps, ok := r.resolvePaint(sp.fill, fo, sp, bboxFn); ok {
			r.fl.reset()
			for _, g := range gs {
				if g.g.Face == nil {
					continue
				}
				p := r.glyphOutline(g.g.Face, g.g.ID)
				if p == nil {
					continue
				}
				k := g.size / g.g.Face.UnitsPerEm()
				m := ctm.mul(matrix{k, 0, 0, k, g.x, g.y})
				r.fl.appendFlatten(p, m, shapeTol)
			}
			r.fillPolys(&r.fl, false, ps, sp)
		}
	}
	if stroke {
		if ps, ok := r.resolvePaint(sp.stroke, so, sp, bboxFn); ok {
			var gp path
			for _, g := range gs {
				if g.g.Face == nil {
					continue
				}
				p := r.glyphOutline(g.g.Face, g.g.ID)
				if p == nil {
					continue
				}
				k := g.size / g.g.Face.UnitsPerEm()
				gp.verbs = append(gp.verbs, p.verbs...)
				for _, pt := range p.pts {
					gp.pts = append(gp.pts, point{pt.x*k + g.x, pt.y*k + g.y})
				}
			}
			if len(gp.verbs) > 0 {
				r.strokePath(&gp, sp, ps)
			}
		}
	}
	if sp.decoration != 0 && fill {
		r.drawDecorations(gs, sp, fo)
	}
}

// drawDecorations draws underline, overline and line-through as thin rects per
// contiguous run of glyphs on one baseline.
func (r *renderer) drawDecorations(gs []glyphPos, sp *state, fo float64) {
	ps, ok := r.resolvePaint(sp.fill, fo, sp, func() (rect, bool) { return rect{}, false })
	if !ok || len(gs) == 0 {
		return
	}
	size := sp.fontSize
	thick := math.Max(size/16, 0.5)
	i := 0
	for i < len(gs) {
		j := i + 1
		for j < len(gs) && gs[j].y == gs[i].y && gs[j].chunk == gs[i].chunk {
			j++
		}
		x0 := gs[i].x
		x1 := gs[j-1].x + gs[j-1].g.Advance
		by := gs[i].y
		draw := func(top float64) {
			var p path
			p.addRect(x0, top, x1-x0, thick)
			r.fl.flatten(&p, sp.ctm, shapeTol)
			r.fillPolys(&r.fl, false, ps, sp)
		}
		if sp.decoration&1 != 0 {
			draw(by + size*0.1)
		}
		if sp.decoration&2 != 0 {
			draw(by - size*0.85)
		}
		if sp.decoration&4 != 0 {
			draw(by - size*0.3)
		}
		i = j
	}
}

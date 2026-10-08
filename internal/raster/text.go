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

// spanInfo describes the textLength of one run of character data (usvg builds
// one span per text node, taking textLength from its parent element).
type spanInfo struct {
	target float64
	glyphs bool // lengthAdjust="spacingAndGlyphs"
}

// tchar is one character of a text element after whitespace processing.
type tchar struct {
	r      rune
	sp     *state
	flags  uint8
	x, y   float64
	dx, dy float64
	rot    float64 // rotate attribute, degrees
	rotSet bool
	span   *spanInfo
}

// glyphPos is a glyph placed in user space (before the element transform).
type glyphPos struct {
	g       ShapedGlyph
	x, y    float64
	size    float64
	sp      *state
	chunk   int
	rot     float64 // degrees, about the glyph origin
	xs, ox  float64 // horizontal stretch (spacingAndGlyphs) about x = ox; 0 means none
	shifted bool    // dx/dy/rotate breaks a text decoration here
	span    *spanInfo
}

// matrix maps the glyph's outline (font units, y down) into user space.
func (g *glyphPos) matrix() matrix {
	k := g.size / g.g.Face.UnitsPerEm()
	m := translate(g.x, g.y)
	if g.xs != 0 && g.xs != 1 {
		m = translate(g.ox, 0).mul(scaleM(g.xs, 1)).mul(translate(g.x-g.ox, g.y))
	}
	if g.rot != 0 {
		sn, cs := math.Sincos(g.rot * math.Pi / 180)
		m = m.mul(matrix{cs, sn, -sn, cs, 0, 0})
	}
	return m.mul(scaleM(k, k))
}

const maxTextChars = 1 << 20

type textCollector struct {
	r        *renderer
	chars    []tchar
	prevSpc  bool
	preserve bool
	adjust   string // lengthAdjust in effect
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

func (tc *textCollector) addText(s string, sp *state, span *spanInfo) {
	for _, c := range s {
		if len(tc.chars) >= maxTextChars {
			return
		}
		if tc.preserve {
			if c == '\n' || c == '\r' || c == '\t' {
				c = ' '
			}
			tc.chars = append(tc.chars, tchar{r: c, sp: sp, span: span})
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
		tc.chars = append(tc.chars, tchar{r: c, sp: sp, span: span})
	}
}

func (tc *textCollector) walk(n *node, st *state) {
	base := len(tc.chars)
	savedPreserve, savedAdjust := tc.preserve, tc.adjust
	if v, ok := n.get(aSpace); ok {
		tc.preserve = strings.TrimSpace(v) == "preserve"
	}
	if v, ok := n.get(aLengthAdjust); ok {
		tc.adjust = strings.TrimSpace(v)
	}
	var span *spanInfo
	if v, ok := n.get(aTextLength); ok {
		if l, u, good := parseLength(v); good {
			if t := st.toPx(l, u, 0); t >= 0 && !math.IsInf(t, 0) {
				span = &spanInfo{target: t, glyphs: tc.adjust == "spacingAndGlyphs"}
			}
		}
	}
	for _, k := range n.kids {
		switch k.tag {
		case tagChars:
			tc.addText(k.text, st, span)
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
	tc.preserve, tc.adjust = savedPreserve, savedAdjust
	end := len(tc.chars)
	// rotate: one angle per character; characters past the list repeat its
	// last value. Inner elements were applied first and win.
	if v, ok := n.get(aRotate); ok {
		if list, good := parseNumList(v); good && len(list) > 0 {
			for j := 0; base+j < end; j++ {
				c := &tc.chars[base+j]
				if c.rotSet {
					continue
				}
				a := list[len(list)-1]
				if j < len(list) {
					a = list[j]
				}
				c.rot, c.rotSet = a, true
			}
		}
	}
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

// layoutTextNode collects and lays out the characters of a <text> element.
func (r *renderer) layoutTextNode(n *node, st *state) []glyphPos {
	tc := &textCollector{r: r, prevSpc: true}
	root := new(state)
	*root = *st
	tc.walk(n, root)
	if len(tc.chars) == 0 {
		return nil
	}
	if !tc.preserve && tc.chars[len(tc.chars)-1].r == ' ' {
		tc.chars = tc.chars[:len(tc.chars)-1]
	}
	if len(tc.chars) == 0 {
		return nil
	}
	return r.layoutText(tc.chars)
}

func (r *renderer) renderText(n *node, st *state, opacity float64, blend blendMode) {
	glyphs := r.layoutTextNode(n, st)
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
	out := make([]glyphPos, 0, len(chars)) // one glyph per character is the common case
	cx, cy := 0.0, 0.0
	chunk := 0
	chunkStart := 0 // index into out where the current chunk begins
	chunkX0 := 0.0
	var chunkAnchor uint8
	finishChunk := func() {
		if len(out) == chunkStart {
			return
		}
		gs := out[chunkStart:]
		// textLength with lengthAdjust=spacing changes the advances before
		// anchoring, like usvg's apply_length_adjust.
		cx += adjustSpacing(gs, cx)
		width := cx - chunkX0
		var shift float64
		switch chunkAnchor {
		case anchorMiddle:
			shift = -width / 2
		case anchorEnd:
			shift = -width
		}
		if shift != 0 {
			for i := range gs {
				gs[i].x += shift
			}
		}
		stretchGlyphs(gs, chunkX0)
	}
	i := 0
	for i < len(chars) {
		c := chars[i]
		// A run extends while the style and text-length span are unchanged and
		// no character carries positioning attributes or a rotation.
		j := i + 1
		if c.rot == 0 {
			for j < len(chars) && chars[j].sp == c.sp && chars[j].flags == 0 && chars[j].span == c.span && chars[j].rot == 0 {
				j++
			}
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
		shifted := (c.flags&(tfDX|tfDY) != 0 && (c.dx != 0 || c.dy != 0)) || c.rot != 0
		var sb strings.Builder
		sb.Grow(j - i)
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
			out = append(out, glyphPos{g: g, x: cx + g.XOffset, y: by - g.YOffset, size: sp.fontSize, sp: sp, chunk: chunk, rot: c.rot, shifted: shifted && gi == 0, span: c.span})
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

// adjustSpacing applies textLength with lengthAdjust=spacing to the glyphs of
// one chunk: every glyph of a span advances by its own width plus a common
// increment so the span is target wide. It shifts later glyphs accordingly and
// returns the change of the chunk's total advance.
func adjustSpacing(gs []glyphPos, end float64) float64 {
	any := false
	for i := range gs {
		if gs[i].span != nil && !gs[i].span.glyphs {
			any = true
			break
		}
	}
	if !any {
		return 0
	}
	delta := 0.0
	for i := 0; i < len(gs); {
		sp := gs[i].span
		if sp == nil || sp.glyphs {
			gs[i].x += delta
			i++
			continue
		}
		j := i
		width := 0.0
		for j < len(gs) && gs[j].span == sp {
			width += gs[j].g.Advance
			j++
		}
		n := j - i
		factor := 0.0
		if n > 1 {
			factor = (sp.target - width) / float64(n-1)
		}
		oldEnd := end
		if j < len(gs) {
			oldEnd = gs[j].x
		}
		oldTotal := oldEnd - gs[i].x
		x := gs[i].x - gs[i].g.XOffset + delta
		for k := i; k < j; k++ {
			off := gs[k].g.XOffset
			gs[k].x = x + off
			x += gs[k].g.Advance + factor
		}
		delta += (width + float64(n)*factor) - oldTotal
		i = j
	}
	return delta
}

// stretchGlyphs applies textLength with lengthAdjust=spacingAndGlyphs: the
// glyphs of a span (positions included, measured from the chunk origin) are
// scaled horizontally by target/width.
func stretchGlyphs(gs []glyphPos, x0 float64) {
	for i := 0; i < len(gs); {
		sp := gs[i].span
		if sp == nil || !sp.glyphs {
			i++
			continue
		}
		j := i
		width := 0.0
		for j < len(gs) && gs[j].span == sp {
			width += gs[j].g.Advance
			j++
		}
		if width > 0 {
			if f := sp.target / width; f >= 0.001 {
				for k := i; k < j; k++ {
					gs[k].xs, gs[k].ox = f, x0
				}
			}
		}
		i = j
	}
}

// baselineOffset returns the downward shift implied by dominant-baseline and
// baseline-shift for a run, from the font's own metrics like usvg's
// ResolvedFont (hard-coded fractions when the face reports none).
func (r *renderer) baselineOffset(sp *state, shaped []ShapedGlyph) float64 {
	if (sp.baseline == "" || sp.baseline == "auto" || sp.baseline == "alphabetic") && (sp.baselineShift == "" || sp.baselineShift == "baseline") {
		return 0
	}
	k := sp.fontSize
	fm := FaceMetrics{UnitsPerEm: 1, Ascender: 0.8, Descender: -0.2, XHeight: 0.5, SubscriptOffset: 0.2, SuperscriptOffset: 0.4}
	if len(shaped) > 0 && shaped[0].Face != nil {
		fm, _ = faceMetrics(shaped[0].Face)
	}
	sc := k / fm.UnitsPerEm
	asc, desc := fm.Ascender*sc, fm.Descender*sc
	off := 0.0
	switch sp.baseline {
	case "middle":
		off = fm.XHeight * sc * 0.5
	case "central":
		off = asc - (asc-desc)*0.5
	case "hanging":
		off = asc * 0.8
	case "text-before-edge", "text-top", "before-edge":
		off = asc
	case "text-after-edge", "text-bottom", "after-edge", "ideographic":
		off = desc
	case "mathematical":
		off = asc * 0.5
	}
	switch sp.baselineShift {
	case "sub":
		off += fm.SubscriptOffset * sc
	case "super":
		off -= fm.SuperscriptOffset * sc
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
	r.inText = true
	defer func() { r.inText = false }()
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
		// Overline and underline are painted below the glyphs, line-through above.
		if sp.decoration&2 != 0 {
			r.drawDecorations(gs, sp, fo, 2)
		}
		if sp.decoration&1 != 0 {
			r.drawDecorations(gs, sp, fo, 1)
		}
	}
	doFill := func() {
		if !fill {
			return
		}
		if ps, ok := r.resolvePaint(sp.fill, fo, sp, bboxFn); ok {
			r.fl.reset()
			for i := range gs {
				g := &gs[i]
				if g.g.Face == nil {
					continue
				}
				if cf, ppem, ok := colourGlyph(g, ctm); ok && r.drawColourGlyph(cf, g, sp, ctm, ppem, fo) {
					continue
				}
				p := r.glyphOutline(g.g.Face, g.g.ID)
				if p == nil {
					continue
				}
				r.fl.appendFlatten(p, ctm.mul(g.matrix()), shapeTol)
			}
			r.fillPolys(&r.fl, false, ps, sp)
		}
	}
	doStroke := func() {
		if !stroke {
			return
		}
		if ps, ok := r.resolvePaint(sp.stroke, so, sp, bboxFn); ok {
			var gp path
			for i := range gs {
				g := &gs[i]
				if g.g.Face == nil {
					continue
				}
				if _, _, ok := colourGlyph(g, ctm); ok {
					continue // a colour glyph's outline is not what is drawn
				}
				p := r.glyphOutline(g.g.Face, g.g.ID)
				if p == nil {
					continue
				}
				gm := g.matrix()
				gp.verbs = append(gp.verbs, p.verbs...)
				for _, pt := range p.pts {
					gp.pts = append(gp.pts, gm.apply(pt))
				}
			}
			if len(gp.verbs) > 0 {
				r.strokePath(&gp, sp, ps)
			}
		}
	}
	if sp.strokeFirst() {
		doStroke()
		doFill()
	} else {
		doFill()
		doStroke()
	}
	if sp.decoration&4 != 0 && fill {
		r.drawDecorations(gs, sp, fo, 4)
	}
}

// drawDecorations draws one kind of text decoration (1 underline, 2 overline,
// 4 line-through) as a thin rectangle per run of glyphs on one baseline, at
// the position and thickness the font specifies (usvg's convert_decoration).
func (r *renderer) drawDecorations(gs []glyphPos, sp *state, fo float64, kind uint8) {
	ps, ok := r.resolvePaint(sp.fill, fo, sp, func() (rect, bool) { return rect{}, false })
	if !ok || len(gs) == 0 {
		return
	}
	i := 0
	for i < len(gs) {
		j := i + 1
		for j < len(gs) && gs[j].y == gs[i].y && gs[j].chunk == gs[i].chunk && !gs[j].shifted {
			j++
		}
		g0 := gs[i]
		if g0.g.Face == nil {
			i = j
			continue
		}
		fm, has := faceMetrics(g0.g.Face)
		size := g0.size
		k := size / fm.UnitsPerEm
		thick := fm.UnderlineThickness * k
		var off float64
		switch kind {
		case 1:
			off = -fm.UnderlinePosition * k
		case 2:
			off = -fm.Ascender * k
		case 4:
			off = -fm.StrikePosition * k
		}
		if !has {
			thick = size / 12
			switch kind {
			case 1:
				off = size / 9
			case 2:
				off = -size * 0.9
			case 4:
				off = -size * 0.25
			}
		}
		x0 := g0.x - g0.g.XOffset
		w := gs[j-1].x - gs[j-1].g.XOffset + gs[j-1].g.Advance - x0
		if thick > 0 && w > 0 {
			m := translate(x0, g0.y)
			if g0.xs != 0 && g0.xs != 1 {
				m = translate(g0.ox, 0).mul(scaleM(g0.xs, 1)).mul(translate(x0-g0.ox, g0.y))
			}
			if g0.rot != 0 {
				sn, cs := math.Sincos(g0.rot * math.Pi / 180)
				m = m.mul(matrix{cs, sn, -sn, cs, 0, 0})
			}
			var p path
			p.addRect(0, off-thick/2, w, thick)
			r.fl.flatten(&p, sp.ctm.mul(m), shapeTol)
			r.fillPolys(&r.fl, false, ps, sp)
		}
		i = j
	}
}

// faceMetrics returns the face's font metrics, or fixed fractions of the em
// when the face cannot report them (has is false then).
func faceMetrics(f Face) (fm FaceMetrics, has bool) {
	if mf, ok := f.(MetricsFace); ok {
		if m, ok := mf.Metrics(); ok {
			return m, true
		}
	}
	return FaceMetrics{UnitsPerEm: f.UnitsPerEm(), Ascender: f.Ascent(), Descender: -f.Descent()}, false
}

// textOutlinePath returns the glyph outlines of a <text> element as one path in
// the element's local coordinates (used for clipPath children).
func (r *renderer) textOutlinePath(n *node, st *state) *path {
	gs := r.layoutTextNode(n, st)
	if len(gs) == 0 {
		return nil
	}
	p := &path{}
	for _, g := range gs {
		if g.g.Face == nil || !g.sp.visible {
			continue
		}
		o := r.glyphOutline(g.g.Face, g.g.ID)
		if o == nil {
			continue
		}
		gm := g.matrix()
		p.verbs = append(p.verbs, o.verbs...)
		for _, pt := range o.pts {
			p.pts = append(p.pts, gm.apply(pt))
		}
	}
	return p
}

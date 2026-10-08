package raster

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
)

type gradKey struct {
	n       *node
	opacity float64
}

// renderer draws a parsed document onto canvases.
type renderer struct {
	doc    *document
	lim    Limits
	shaper Shaper

	cv    *canvas
	rast  *rasterizer
	bl    blitter
	fl    flat // fill polygons (device space)
	sf    flat // stroke source (local space)
	so    flat // stroke outline
	pth   path
	stk   stroker
	pool  []*canvas
	depth int // active layers
	cw    int
	ch    int

	visited int
	nest    int
	err     error

	imgs       map[*node]*rasterImage
	fetched    map[string][]byte // image bytes by href, from Options.Images
	outlines   map[outlineKey]*path
	colourImgs map[colourImageKey]*rasterImage // decoded bitmap glyphs
	grads      map[gradKey]*gradient

	patterns     map[patternKey]*patternTile
	patternBytes int
	rootState    state
	docStates    map[*node]*state
	active       map[*node]bool // paint servers, masks, filters, markers being expanded
	useLoops     map[*node]bool // clip-path <use> elements whose chain of uses loops (see useLoops)
	inText       bool           // drawing glyphs (text-rendering selects anti-aliasing)
	effectPx     int            // pixels charged to filters and pattern tiles so far

	ctx         context.Context // nil = never cancelled
	pixelOps    int             // pixel work charged so far (see Limits.MaxPixelOps)
	canvasBytes int             // pixel memory of live canvases (see Limits.MaxCanvasBytes)
	ticks       int
}

var errLimit = fmt.Errorf("raster: %w", budget.ErrLimit)

// chargePixels accounts for n pixels of filter or pattern-tile work and fails
// the render when the budget is spent.
func (r *renderer) chargePixels(n int) bool {
	r.effectPx += n
	if r.effectPx > r.lim.MaxEffectPixels || r.effectPx < 0 {
		r.fail(fmt.Errorf("raster: filter and pattern work exceeds %d pixels: %w", r.lim.MaxEffectPixels, errLimit))
		return false
	}
	return r.chargeOps(n)
}

// chargeOps accounts for n pixels of work against Limits.MaxPixelOps and
// polls the context. It reports whether rendering may continue.
func (r *renderer) chargeOps(n int) bool {
	if r.err != nil {
		return false
	}
	r.pixelOps += n
	if r.pixelOps > r.lim.MaxPixelOps || r.pixelOps < 0 {
		r.fail(fmt.Errorf("raster: render work exceeds %d pixel operations (output %dx%d; raise Limits.MaxPixelOps to allow it): %w",
			r.lim.MaxPixelOps, r.cw, r.ch, errLimit))
		return false
	}
	return r.poll(n >= 1<<14)
}

// poll checks the context, every call when force is set and otherwise every
// few calls.
func (r *renderer) poll(force bool) bool {
	if r.ctx == nil {
		return r.err == nil
	}
	if r.ticks++; force || r.ticks&15 == 0 {
		if err := r.ctx.Err(); err != nil {
			r.fail(err)
		}
	}
	return r.err == nil
}

// allocCanvas allocates a canvas, failing the render when the live canvas
// memory would exceed Limits.MaxCanvasBytes.
func (r *renderer) allocCanvas(w, h int) *canvas {
	if r.err != nil {
		return nil
	}
	n := w * h * 4
	if n < 0 || n > r.lim.MaxCanvasBytes-r.canvasBytes {
		r.fail(fmt.Errorf("raster: offscreen layers need more than %d MiB of pixel memory at once (raise Limits.MaxCanvasBytes or reduce nested opacity/filter/mask groups): %w",
			r.lim.MaxCanvasBytes>>20, errLimit))
		return nil
	}
	r.canvasBytes += n
	return newCanvas(w, h)
}

// releaseCanvas returns c's memory to the budget (c must not be used again).
func (r *renderer) releaseCanvas(c *canvas) {
	if c != nil {
		r.canvasBytes -= len(c.pix)
	}
}

// releasePool releases every pooled layer (a pool being discarded).
func (r *renderer) releasePool(pool []*canvas) {
	for _, c := range pool {
		r.releaseCanvas(c)
	}
}

// fillRast runs the rasterizer's fill, charging the pixels it processes and
// stopping early when the budget runs out or the context is done.
func (r *renderer) fillRast(evenOdd bool, sink spanSink) {
	if r.err != nil {
		return
	}
	rem := r.lim.MaxPixelOps - r.pixelOps
	r.rast.work, r.rast.workCap = 0, rem+1
	r.rast.fill(evenOdd, sink)
	w := r.rast.work
	r.rast.workCap = 0
	if r.rast.stopped && r.ctx != nil {
		if err := r.ctx.Err(); err != nil {
			r.fail(err)
			return
		}
	}
	r.chargeOps(w + 16) // a small constant so empty shapes are not free
}

func (r *renderer) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *renderer) newLayer() *canvas {
	for n := len(r.pool); n > 0; n = len(r.pool) {
		c := r.pool[n-1]
		r.pool = r.pool[:n-1]
		if c.w == r.cw && c.h == r.ch {
			return c
		}
		r.releaseCanvas(c)
	}
	return r.allocCanvas(r.cw, r.ch)
}

func (r *renderer) pushLayer() *canvas {
	if r.depth >= r.lim.MaxLayerDepth {
		r.fail(fmt.Errorf("%w: more than %d nested opacity/blend layers", errLimit, r.lim.MaxLayerDepth))
		return nil
	}
	layer := r.newLayer()
	if layer == nil {
		return nil
	}
	r.depth++
	prev := r.cv
	r.cv = layer
	return prev
}

func (r *renderer) popLayer(prev *canvas, opacity float64, mode blendMode) {
	layer := r.cv
	r.cv = prev
	r.depth--
	if d := layer.dirty.intersect(layer.bounds()); !d.empty() && r.chargeOps(d.w()*d.h()) {
		compositeLayer(prev, layer, layer.dirty, opacity, nil, mode)
	}
	layer.clearDirty()
	r.pool = append(r.pool, layer)
}

func (r *renderer) renderChildren(n *node, st *state) {
	for _, k := range n.kids {
		if r.err != nil {
			return
		}
		r.renderElement(k, st)
	}
}

func (r *renderer) budget() bool {
	if !r.poll(false) {
		return false
	}
	r.visited++
	if r.visited > r.lim.MaxRenderNodes {
		r.fail(fmt.Errorf("raster: more than %d elements rendered (possible <use> expansion attack): %w", r.lim.MaxRenderNodes, errLimit))
		return false
	}
	return r.err == nil
}

func isDisplayNone(n *node) bool {
	v, ok := n.get(aDisplay)
	return ok && strings.TrimSpace(v) == "none"
}

// prepare computes the element's state from its parent's. ok is false when the
// element must not be drawn (display none, degenerate transform, empty clip).
func (r *renderer) prepare(n *node, parent *state, st *state) bool {
	if isDisplayNone(n) {
		return false
	}
	*st = *parent
	st.applyProps(n)
	if tf, ok := n.get(aTransform); ok {
		if m, valid := parseTransform(tf); valid {
			st.ctm = st.ctm.mul(m)
		}
	}
	if !st.ctm.isFinite() || !st.ctm.invertible() {
		return false
	}
	if cp, ok := n.get(aClipPath); ok {
		if id, isURL := parseURLRef(cp); isURL {
			if cn := r.doc.ids[id]; cn != nil {
				if cn.tag != tagClipPath {
					return false // a clip-path must reference a clipPath element
				}
				m := r.buildClipFor(cn, n, st)
				if m == nil || m.r.empty() {
					return false
				}
				st.clip = m
			}
		}
	}
	return true
}

// buildClipFor builds the clip mask of clipPath cn for element n (whose state
// is st); clipPathUnits=objectBoundingBox needs n's bounding box.
func (r *renderer) buildClipFor(cn, n *node, st *state) *mask {
	var bb *rect
	if r.clipNeedsBBox(cn, 0) {
		b, ok := nonZero(r.contentBBox(n, st))
		if !ok {
			return nil // clipping of zero-sized shapes is not allowed
		}
		bb = &b
	}
	return r.buildClip(cn, st, 0, bb)
}

// clipNeedsBBox reports whether cn (or a clipPath it chains to) uses
// objectBoundingBox units.
func (r *renderer) clipNeedsBBox(cn *node, depth int) bool {
	for ; cn != nil && depth < maxClipDepth; depth++ {
		if strings.TrimSpace(cn.str(aClipPathUnits)) == "objectBoundingBox" {
			return true
		}
		id, isURL := parseURLRef(cn.str(aClipPath))
		if !isURL {
			return false
		}
		next := r.doc.ids[id]
		if next == nil || next == cn || next.tag != tagClipPath {
			return false
		}
		cn = next
	}
	return false
}

func (r *renderer) renderElement(n *node, parent *state) {
	switch n.tag {
	case tagSVG, tagG, tagSwitch, tagUse, tagPath, tagRect, tagCircle, tagEllipse,
		tagLine, tagPolyline, tagPolygon, tagText, tagImage:
	default:
		return
	}
	if !r.budget() {
		return
	}
	r.nest++
	defer func() { r.nest-- }()
	if r.nest > r.lim.MaxDepth*2 {
		r.fail(fmt.Errorf("raster: element nesting too deep: %w", errLimit))
		return
	}
	var st state
	if !r.prepare(n, parent, &st) {
		return
	}
	opacity, _ := parseOpacity(n.str(aOpacity))
	blend := blendNormal
	if v, ok := n.get(aMixBlendMode); ok {
		blend = blendNames[strings.TrimSpace(v)]
	}
	mk, fls, ok := r.effectRefs(n)
	if !ok {
		return
	}
	if mk != nil || len(fls) > 0 {
		r.renderEffects(n, &st, opacity, blend, mk, fls)
		return
	}
	r.drawContent(n, &st, opacity, blend)
}

// drawContent draws the element's own content (children, shape, text or
// image) with opacity and blend applied as a group when they need a layer.
func (r *renderer) drawContent(n *node, st *state, opacity float64, blend blendMode) {
	switch n.tag {
	case tagSVG, tagG, tagSwitch, tagUse:
		layered := opacity < 1 || blend != blendNormal
		var prev *canvas
		if layered {
			if prev = r.pushLayer(); prev == nil {
				return
			}
		}
		switch n.tag {
		case tagG:
			r.renderChildren(n, st)
		case tagSwitch:
			for _, k := range n.kids {
				if k.tag == tagChars || !switchAccepts(k) {
					continue
				}
				r.renderElement(k, st)
				break
			}
		case tagSVG:
			r.renderNestedSVG(n, st)
		case tagUse:
			r.renderUse(n, st)
		}
		if layered {
			r.popLayer(prev, opacity, blend)
		}
	case tagText:
		r.renderText(n, st, opacity, blend)
	case tagImage:
		r.renderImage(n, st, opacity, blend)
	default:
		r.renderShape(n, st, opacity, blend)
	}
}

func switchAccepts(n *node) bool {
	v, ok := n.get(aSystemLanguage)
	if !ok {
		return true
	}
	for _, l := range strings.Split(v, ",") {
		l = strings.TrimSpace(l)
		if l == "en" || strings.HasPrefix(l, "en-") {
			return true
		}
	}
	return false
}

// viewBox parses "minx miny w h".
func parseViewBox(s string) (rect, bool) {
	sc := numScanner{s: s}
	var v [4]float64
	for i := 0; i < 4; i++ {
		if i > 0 {
			sc.skipSep()
		}
		x, ok := sc.number()
		if !ok {
			return rect{}, false
		}
		v[i] = x
	}
	if !(v[2] > 0 && v[3] > 0) {
		return rect{}, false
	}
	return rect{v[0], v[1], v[0] + v[2], v[1] + v[3]}, true
}

// viewBoxTransform maps a viewBox onto a w x h viewport at the origin
// following preserveAspectRatio.
func viewBoxTransform(vb rect, w, h float64, par string) matrix {
	fields := strings.Fields(par)
	if len(fields) > 0 && fields[0] == "defer" {
		fields = fields[1:]
	}
	align := "xMidYMid"
	slice := false
	if len(fields) > 0 {
		align = fields[0]
	}
	if len(fields) > 1 && fields[1] == "slice" {
		slice = true
	}
	sx, sy := w/vb.w(), h/vb.h()
	if align == "none" {
		return matrix{sx, 0, 0, sy, -vb.x0 * sx, -vb.y0 * sy}
	}
	s := math.Min(sx, sy)
	if slice {
		s = math.Max(sx, sy)
	}
	tx := -vb.x0 * s
	ty := -vb.y0 * s
	dx := w - vb.w()*s
	dy := h - vb.h()*s
	switch {
	case strings.HasPrefix(align, "xMid"):
		tx += dx / 2
	case strings.HasPrefix(align, "xMax"):
		tx += dx
	}
	switch {
	case strings.HasSuffix(align, "YMid"):
		ty += dy / 2
	case strings.HasSuffix(align, "YMax"):
		ty += dy
	}
	return matrix{s, 0, 0, s, tx, ty}
}

func (r *renderer) renderNestedSVG(n *node, st *state) {
	x := st.length(n.str(aX), 0, 0)
	y := st.length(n.str(aY), 1, 0)
	w := st.length(n.str(aWidth), 0, st.vw)
	h := st.length(n.str(aHeight), 1, st.vh)
	r.renderViewport(n, st, x, y, w, h, true)
}

// renderViewport establishes the viewport (and viewBox) of an svg or symbol
// element, clips to it unless overflow is visible, and renders the children.
func (r *renderer) renderViewport(n *node, st *state, x, y, w, h float64, clipDefault bool) {
	if !(w > 0 && h > 0) {
		return
	}
	ov := strings.TrimSpace(n.str(aOverflow))
	if clipDefault && ov != "visible" && ov != "auto" {
		st.clip = r.rectClip(st, st.ctm, rect{x, y, x + w, y + h})
		if st.clip.r.empty() {
			return
		}
	}
	st.ctm = st.ctm.mul(translate(x, y))
	if vb, ok := parseViewBox(n.str(aViewBox)); ok {
		st.ctm = st.ctm.mul(viewBoxTransform(vb, w, h, n.str(aPreserveAspectRatio)))
		st.vw, st.vh = vb.w(), vb.h()
	} else {
		st.vw, st.vh = w, h
	}
	if !st.ctm.isFinite() || !st.ctm.invertible() {
		return
	}
	r.renderChildren(n, st)
}

func (r *renderer) renderUse(n *node, st *state) {
	href := n.str(aHref)
	if !strings.HasPrefix(href, "#") {
		return // external references are never fetched
	}
	target := r.doc.ids[href[1:]]
	if target == nil {
		return
	}
	// A use must not reference its own ancestor (or itself).
	for p := n; p != nil; p = p.parent {
		if p == target {
			return
		}
	}
	x := st.length(n.str(aX), 0, 0)
	y := st.length(n.str(aY), 1, 0)
	st.ctm = st.ctm.mul(translate(x, y))
	switch target.tag {
	case tagSymbol:
		s2 := *st
		s2.applyProps(target)
		w := s2.length(n.str(aWidth), 0, st.vw)
		h := s2.length(n.str(aHeight), 1, st.vh)
		r.renderViewport(target, &s2, 0, 0, w, h, true)
	case tagSVG:
		// width/height on the use override those of the svg.
		var s2 state
		if !r.prepare(target, st, &s2) {
			return
		}
		ux := s2.length(target.str(aX), 0, 0)
		uy := s2.length(target.str(aY), 1, 0)
		w := s2.length(target.str(aWidth), 0, s2.vw)
		h := s2.length(target.str(aHeight), 1, s2.vh)
		if v := n.str(aWidth); v != "" {
			w = s2.length(v, 0, w)
		}
		if v := n.str(aHeight); v != "" {
			h = s2.length(v, 1, h)
		}
		r.renderViewport(target, &s2, ux, uy, w, h, true)
	default:
		r.renderElement(target, st)
	}
}

// ---- clipping --------------------------------------------------------------

// rectClip returns st.clip intersected with the device-space region covered by
// rc transformed by m. Axis-aligned, pixel-aligned rectangles avoid a coverage
// map entirely.
func (r *renderer) rectClip(st *state, m matrix, rc rect) *mask {
	region := r.cv.bounds()
	if st.clip != nil {
		region = region.intersect(st.clip.r)
	}
	if m.b == 0 && m.c == 0 && m.a != 0 && m.d != 0 {
		x0, y0 := m.a*rc.x0+m.e, m.d*rc.y0+m.f
		x1, y1 := m.a*rc.x1+m.e, m.d*rc.y1+m.f
		if x0 > x1 {
			x0, x1 = x1, x0
		}
		if y0 > y1 {
			y0, y1 = y1, y0
		}
		if isNearInt(x0) && isNearInt(y0) && isNearInt(x1) && isNearInt(y1) {
			ir := irect{clampInt(x0), clampInt(y0), clampInt(x1), clampInt(y1)}
			ir = ir.intersect(region)
			if st.clip != nil && st.clip.a != nil {
				return intersectMasks(&mask{r: ir}, st.clip)
			}
			return &mask{r: ir}
		}
	}
	var p path
	p.addRect(rc.x0, rc.y0, rc.w(), rc.h())
	mk := r.pathsMask([]*path{&p}, []matrix{m}, []bool{false}, region)
	return intersectMasks(mk, st.clip)
}

func isNearInt(v float64) bool { return math.Abs(v-math.Round(v)) < 1e-6 && math.Abs(v) < 1e9 }

func clampInt(v float64) int {
	v = math.Round(v)
	if v > 1e9 {
		return 1e9
	}
	if v < -1e9 {
		return -1e9
	}
	return int(v)
}

// intersectMasks multiplies two coverage maps (nil means unrestricted).
func intersectMasks(a, b *mask) *mask {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	reg := a.r.intersect(b.r)
	if reg.empty() {
		return &mask{r: irect{}}
	}
	if a.a == nil && b.a == nil {
		return &mask{r: reg}
	}
	out := &mask{r: reg, a: make([]uint8, reg.w()*reg.h())}
	w := reg.w()
	for y := reg.y0; y < reg.y1; y++ {
		row := out.a[(y-reg.y0)*w:][:w]
		for x := 0; x < w; x++ {
			v := uint32(a.at(reg.x0+x, y))
			if v != 0 {
				v = mul255(v, uint32(b.at(reg.x0+x, y)))
			}
			row[x] = uint8(v)
		}
	}
	return out
}

// pathsMask rasterizes the union of the paths (each under its own matrix and
// fill rule) into a new mask over region.
func (r *renderer) pathsMask(ps []*path, ms []matrix, evenOdd []bool, region irect) *mask {
	if region.empty() {
		return &mask{r: irect{}}
	}
	if !r.chargeOps(region.w()*region.h()/8 + 1) { // allocating and clearing the coverage map
		return &mask{r: irect{}}
	}
	mk := &mask{r: region, a: make([]uint8, region.w()*region.h())}
	var f flat
	for i, p := range ps {
		f.flatten(p, ms[i], 0.1)
		r.rast.begin(region)
		r.rast.addPolys(&f)
		r.fillRast(evenOdd[i], maskSink{mk})
	}
	return mk
}

const maxClipDepth = 8

// useLoop reports whether the chain of <use> elements starting at u, each
// referencing the next, comes back on itself: such a chain draws nothing.
// Each use references one element, so the chains form a functional graph and
// a walk stops at the first use already decided; deciding every use of a
// document is linear in their number however the chains share their tails.
func (r *renderer) useLoop(u *node) bool {
	if r.useLoops == nil {
		r.useLoops = map[*node]bool{}
	}
	var chain []*node
	onChain := map[*node]bool{}
	loops := false
	for n := u; ; {
		if v, done := r.useLoops[n]; done {
			loops = v
			break
		}
		if onChain[n] {
			loops = true
			break
		}
		chain = append(chain, n)
		onChain[n] = true
		href := n.str(aHref)
		if !strings.HasPrefix(href, "#") {
			break
		}
		t := r.doc.ids[href[1:]]
		if t == nil || t.tag != tagUse {
			break
		}
		n = t
	}
	for _, n := range chain {
		r.useLoops[n] = loops
	}
	return loops
}

// buildClip resolves a clipPath element for an element whose state is st.
// Returns nil when the clip is unusable (the element is then not drawn).
func (r *renderer) buildClip(cn *node, st *state, depth int, bb *rect) *mask {
	if depth > maxClipDepth {
		return nil
	}
	region := r.cv.bounds()
	if st.clip != nil {
		region = region.intersect(st.clip.r)
	}
	if region.empty() {
		return &mask{r: irect{}}
	}
	base := st.ctm
	if tf, ok := cn.get(aTransform); ok {
		m, valid := parseTransform(tf)
		if !valid || !m.invertible() {
			return nil // an invalid clipPath transform disables the whole clip
		}
		base = base.mul(m)
	}
	if strings.TrimSpace(cn.str(aClipPathUnits)) == "objectBoundingBox" {
		if bb == nil {
			return nil
		}
		base = base.mul(matrix{bb.w(), 0, 0, bb.h(), bb.x0, bb.y0})
	}
	cs := *r.inheritedState(cn)
	cs.ctm = st.ctm
	cs.clip = st.clip
	cs.vw, cs.vh = st.vw, st.vh
	var ps []*path
	var ms []matrix
	var eo []bool
	var single *node
	var collect func(parent *node, pm matrix, ps0 *state, top bool, hops int)
	collect = func(parent *node, pm matrix, ps0 *state, top bool, hops int) {
		// A <use> in a clipPath may reference another <use>, even itself
		// through a cycle: bound the chain by the element nesting limit.
		if hops > r.lim.MaxDepth {
			return
		}
		for _, k := range parent.kids {
			if isDisplayNone(k) || !r.budget() {
				continue
			}
			kst := *ps0
			kst.applyProps(k)
			m := pm
			if tf, ok := k.get(aTransform); ok {
				if t, valid := parseTransform(tf); valid {
					m = m.mul(t)
				}
			}
			kst.ctm = m
			if !kst.visible {
				continue
			}
			switch k.tag {
			case tagUse:
				href := k.str(aHref)
				if strings.HasPrefix(href, "#") {
					if t := r.doc.ids[href[1:]]; t != nil && t != k && !r.useLoop(k) {
						x := kst.length(k.str(aX), 0, 0)
						y := kst.length(k.str(aY), 1, 0)
						fake := &node{kids: []*node{t}}
						collect(fake, m.mul(translate(x, y)), &kst, false, hops+1)
					}
				}
			case tagPath, tagRect, tagCircle, tagEllipse, tagLine, tagPolyline, tagPolygon:
				p := &path{}
				if r.buildShape(k, &kst, p) && !p.empty() {
					ps = append(ps, p)
					ms = append(ms, m)
					eo = append(eo, kst.clipEvenOdd)
					if len(ps) == 1 && top && k.tag == tagRect {
						single = k
					}
				}
			case tagText:
				if p := r.textOutlinePath(k, &kst); p != nil && !p.empty() {
					ps = append(ps, p)
					ms = append(ms, m)
					eo = append(eo, kst.clipEvenOdd)
				}
			}
		}
	}
	collect(cn, base, &cs, true, 0)
	var result *mask
	if len(ps) == 0 {
		result = &mask{r: irect{}}
	} else if len(ps) == 1 && single != nil && ps[0].pts != nil && len(ps[0].pts) == 4 {
		// One rectangle: use the cheap rectangular mask when aligned.
		b, _ := ps[0].bounds()
		result = r.rectClip(&state{clip: st.clip}, ms[0], b)
	} else {
		result = intersectMasks(r.pathsMask(ps, ms, eo, region), st.clip)
	}
	// A clipPath may itself be clipped.
	if cp, ok := cn.get(aClipPath); ok {
		if id, isURL := parseURLRef(cp); isURL {
			if c2 := r.doc.ids[id]; c2 != nil && c2.tag == tagClipPath && c2 != cn {
				inner := *st
				inner.clip = nil
				m2 := r.buildClip(c2, &inner, depth+1, bb)
				if m2 == nil {
					return nil
				}
				result = intersectMasks(result, m2)
			}
		}
	}
	return result
}

// ---- shapes ----------------------------------------------------------------

// buildShape builds the local-space path of a basic shape or path element.
func (r *renderer) buildShape(n *node, st *state, p *path) bool {
	switch n.tag {
	case tagPath:
		d, ok := n.get(aD)
		if !ok {
			return false
		}
		parsePathData(d, p)
		return !p.empty()
	case tagRect:
		w := st.length(n.str(aWidth), 0, 0)
		h := st.length(n.str(aHeight), 1, 0)
		if !(w > 0 && h > 0) {
			return false
		}
		x := st.length(n.str(aX), 0, 0)
		y := st.length(n.str(aY), 1, 0)
		rxs, rys := n.str(aRx), n.str(aRy)
		rx := st.length(rxs, 0, -1)
		ry := st.length(rys, 1, -1)
		if rx < 0 && ry < 0 {
			rx, ry = 0, 0
		} else if rx < 0 {
			rx = ry
		} else if ry < 0 {
			ry = rx
		}
		rx = math.Min(rx, w/2)
		ry = math.Min(ry, h/2)
		p.addRoundRect(x, y, w, h, rx, ry)
		return true
	case tagCircle:
		rad := st.length(n.str(aR), 2, 0)
		if !(rad > 0) {
			return false
		}
		p.addEllipse(st.length(n.str(aCx), 0, 0), st.length(n.str(aCy), 1, 0), rad, rad)
		return true
	case tagEllipse:
		rx := st.length(n.str(aRx), 0, -1)
		ry := st.length(n.str(aRy), 1, -1)
		if rx < 0 && ry < 0 {
			return false
		} else if rx < 0 {
			rx = ry
		} else if ry < 0 {
			ry = rx
		}
		if !(rx > 0 && ry > 0) {
			return false
		}
		p.addEllipse(st.length(n.str(aCx), 0, 0), st.length(n.str(aCy), 1, 0), rx, ry)
		return true
	case tagLine:
		p.moveTo(st.length(n.str(aX1), 0, 0), st.length(n.str(aY1), 1, 0))
		p.lineTo(st.length(n.str(aX2), 0, 0), st.length(n.str(aY2), 1, 0))
		return true
	case tagPolyline, tagPolygon:
		sc := numScanner{s: n.str(aPoints)}
		cnt := 0
		for !sc.atEnd() {
			x, ok := sc.number()
			if !ok {
				break
			}
			sc.skipSep()
			y, ok := sc.number()
			if !ok {
				break
			}
			sc.skipSep()
			if cnt == 0 {
				p.moveTo(x, y)
			} else {
				p.lineTo(x, y)
			}
			cnt++
		}
		if cnt < 2 {
			return false
		}
		if n.tag == tagPolygon {
			p.close()
		}
		return true
	}
	return false
}

// shapeTol is the flattening tolerance in device pixels.
const shapeTol = 0.1

func (r *renderer) renderShape(n *node, st *state, opacity float64, blend blendMode) {
	r.pth.reset()
	if !r.buildShape(n, st, &r.pth) {
		return
	}
	if r.hasMarkers(n, st) {
		// The path and its markers form one group for opacity and blending.
		p := &path{verbs: append([]uint8(nil), r.pth.verbs...), pts: append([]point(nil), r.pth.pts...)}
		layered := opacity < 1 || blend != blendNormal
		var prev *canvas
		gop, gblend := opacity, blend
		if layered {
			if prev = r.pushLayer(); prev == nil {
				return
			}
			opacity, blend = 1, blendNormal
		}
		po := st.paintOrder
		switch {
		case po[0] == 2:
			r.drawMarkers(p, st)
			r.drawPath(p, st, opacity, blend)
		case po[1] == 2:
			// Markers between the two paints: draw them separately.
			for _, k := range [2]uint8{po[0], po[2]} {
				sub := *st
				if k == 0 {
					sub.stroke = paint{kind: pNone}
				} else {
					sub.fill = paint{kind: pNone}
				}
				r.drawPath(p, &sub, opacity, blend)
				if k == po[0] {
					r.drawMarkers(p, st)
				}
			}
		default:
			r.drawPath(p, st, opacity, blend)
			r.drawMarkers(p, st)
		}
		if layered {
			r.popLayer(prev, gop, gblend)
		}
		return
	}
	if st.fill.kind == pURL || st.stroke.kind == pURL {
		// A pattern fill renders nested shapes, which reuse r.pth.
		p := &path{verbs: append([]uint8(nil), r.pth.verbs...), pts: append([]point(nil), r.pth.pts...)}
		r.drawPath(p, st, opacity, blend)
		return
	}
	r.drawPath(&r.pth, st, opacity, blend)
}

func (st *state) hasFill() bool {
	return st.fill.kind != pNone && st.fillOpacity > 0
}

func (st *state) hasStroke() bool {
	return st.stroke.kind != pNone && st.strokeOpacity > 0 && st.strokeWidth > 0
}

// drawPath fills and strokes p (local space) according to st.
func (r *renderer) drawPath(p *path, st *state, opacity float64, blend blendMode) {
	if !st.visible {
		return
	}
	fill := st.hasFill()
	stroke := st.hasStroke()
	if !fill && !stroke {
		return
	}
	layered := blend != blendNormal || (opacity < 1 && fill && stroke)
	fo, so := st.fillOpacity, st.strokeOpacity
	if !layered && opacity < 1 {
		fo *= opacity
		so *= opacity
	}
	var prev *canvas
	if layered {
		if prev = r.pushLayer(); prev == nil {
			return
		}
	}
	var bboxLocal rect
	bboxGood, bboxDone := false, false
	needBBox := func() (rect, bool) {
		if !bboxDone {
			bboxLocal, bboxGood = tightBounds(p)
			bboxDone = true
		}
		return bboxLocal, bboxGood
	}
	doFill := func() {
		if fill {
			if ps, ok := r.resolvePaint(st.fill, fo, st, needBBox); ok {
				r.fl.flatten(p, st.ctm, shapeTol)
				r.fillPolys(&r.fl, st.evenOdd, ps, st)
			}
		}
	}
	doStroke := func() {
		if stroke {
			if ps, ok := r.resolvePaint(st.stroke, so, st, needBBox); ok {
				r.strokePath(p, st, ps)
			}
		}
	}
	if st.strokeFirst() {
		doStroke()
		doFill()
	} else {
		doFill()
		doStroke()
	}
	if layered {
		r.popLayer(prev, opacity, blend)
	}
}

func (r *renderer) strokePath(p *path, st *state, ps paintSrc) {
	ms := st.ctm.maxScale()
	if ms <= 0 {
		return
	}
	r.sf.flatten(p, identity, shapeTol/ms)
	if len(r.sf.subs) == 0 {
		return
	}
	width := st.strokeWidth
	// Strokes thinner than a device pixel are drawn by tiny-skia as 1px-wide
	// hairlines whose opacity is scaled by the real width; do the same.
	l1 := math.Hypot(st.ctm.a, st.ctm.b) * width
	l2 := math.Hypot(st.ctm.c, st.ctm.d) * width
	hair := l1 <= 1 && l2 <= 1
	if hair {
		cov := (l1 + l2) / 2
		if cov <= 0 {
			return
		}
		width /= cov
		if ps.solid {
			for i := range ps.col {
				ps.col[i] = uint8(mul255(uint32(ps.col[i]), uint32(math.Round(cov*255))))
			}
		}
	}
	ss := strokeStyle{width: width, cap: st.cap, join: st.join, miterLimit: st.miter, dash: st.dash, dashOffset: st.dashOffset, hairline: hair, lin: st.ctm}
	r.stk.stroke(&r.sf, ss, ms, &r.so)
	for i, pt := range r.so.pts {
		r.so.pts[i] = st.ctm.apply(pt)
	}
	r.fillPolys(&r.so, false, ps, st)
}

// fillPolys rasterizes device-space polygons with the given paint.
func (r *renderer) fillPolys(f *flat, evenOdd bool, ps paintSrc, st *state) {
	if len(f.pts) == 0 {
		return
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, pt := range f.pts {
		if pt.x < minX {
			minX = pt.x
		}
		if pt.x > maxX {
			maxX = pt.x
		}
		if pt.y < minY {
			minY = pt.y
		}
		if pt.y > maxY {
			maxY = pt.y
		}
	}
	if !(maxX >= minX) || !(maxY >= minY) {
		return
	}
	clip := r.cv.bounds()
	if st.clip != nil {
		clip = clip.intersect(st.clip.r)
	}
	bb := irect{clampInt(math.Floor(minX)), clampInt(math.Floor(minY)), clampInt(math.Ceil(maxX)), clampInt(math.Ceil(maxY))}
	clip = clip.intersect(bb)
	if clip.empty() {
		return
	}
	crisp := st.crisp
	if r.inText {
		crisp = st.textCrisp
	}
	if crisp {
		r.rast.setAA(false)
	}
	r.rast.begin(clip)
	r.rast.addPolys(f)
	r.bl.cv = r.cv
	r.bl.paint = ps
	r.bl.mask = st.clip
	r.fillRast(evenOdd, &r.bl)
	if crisp {
		r.rast.setAA(true)
	}
	r.cv.markDirty(clip)
}

// ---- paint servers ---------------------------------------------------------

func (r *renderer) resolvePaint(p paint, opacity float64, st *state, bbox func() (rect, bool)) (paintSrc, bool) {
	kind, col := p.kind, p.c
	if kind == pURL {
		if src, ok, handled := r.serverPaint(p, opacity, st, bbox); handled {
			return src, ok
		}
		kind, col = p.fbKind, p.fb
	}
	switch kind {
	case pCurrent:
		return solidPaint(st.color, opacity), true
	case pColor:
		return solidPaint(col, opacity), true
	}
	return paintSrc{}, false
}

func (r *renderer) hrefTarget(n *node) *node {
	href := n.str(aHref)
	if strings.HasPrefix(href, "#") {
		return r.doc.ids[href[1:]]
	}
	return nil
}

func isGradient(n *node) bool {
	return n != nil && (n.tag == tagLinearGradient || n.tag == tagRadialGradient)
}

// gradAttr looks an attribute up along the gradient's href chain.
func (r *renderer) gradAttr(n *node, id attrID) (string, bool) {
	for i := 0; n != nil && i < 8; i++ {
		if !isGradient(n) {
			return "", false
		}
		if v, ok := n.get(id); ok {
			return v, true
		}
		n = r.hrefTarget(n)
	}
	return "", false
}

func (r *renderer) gradStops(n *node) []*node {
	for i := 0; n != nil && i < 8; i++ {
		if !isGradient(n) {
			return nil
		}
		var stops []*node
		for _, k := range n.kids {
			if k.tag == tagStop {
				stops = append(stops, k)
			}
		}
		if len(stops) > 0 {
			return stops
		}
		n = r.hrefTarget(n)
	}
	return nil
}

// serverPaint resolves url(#id). handled is false when the reference is broken,
// in which case the fallback colour applies.
func (r *renderer) serverPaint(p paint, opacity float64, st *state, bbox func() (rect, bool)) (src paintSrc, ok, handled bool) {
	n := r.doc.ids[p.id]
	if n != nil && n.tag == tagPattern {
		return r.patternPaint(n, opacity, st, bbox)
	}
	if !isGradient(n) {
		return paintSrc{}, false, false
	}
	stopNodes := r.gradStops(n)
	if len(stopNodes) == 0 {
		return paintSrc{}, false, true
	}
	if len(stopNodes) > 4096 {
		stopNodes = stopNodes[:4096]
	}
	stops := make([]gstop, 0, len(stopNodes))
	prev := 0.0
	for _, sn := range stopNodes {
		off := 0.0
		if v := sn.str(aOffset); v != "" {
			if f, u, good := parseLength(v); good {
				if u == "%" {
					f /= 100
				}
				off = f
			}
		}
		off = math.Max(0, math.Min(1, off))
		if off < prev {
			off = prev
		}
		prev = off
		var sst state
		sst.color = st.color
		c := black
		if v, has := sn.get(aStopColor); has {
			v = strings.TrimSpace(v)
			if v == "currentColor" {
				c = st.color
			} else if cc, good := parseColor(v); good {
				c = cc
			}
		}
		if v, has := sn.get(aStopOpacity); has {
			if o, good := parseOpacity(v); good {
				c.a *= float32(o)
			}
		}
		stops = append(stops, gstop{off, c})
	}
	if len(stops) == 1 {
		return solidPaint(stops[0].c, opacity), true, true
	}

	unitsBBox := true
	if v, has := r.gradAttr(n, aGradientUnits); has && strings.TrimSpace(v) == "userSpaceOnUse" {
		unitsBBox = false
	}
	m := st.ctm
	var bb rect
	if unitsBBox {
		b, good := bbox()
		if !good || b.w() <= 0 || b.h() <= 0 {
			return paintSrc{}, false, true
		}
		bb = b
		m = m.mul(matrix{b.w(), 0, 0, b.h(), b.x0, b.y0})
	}
	if v, has := r.gradAttr(n, aGradientTransform); has {
		if t, good := parseTransform(v); good {
			m = m.mul(t)
		}
	}
	inv, good := m.invert()
	if !good || !m.isFinite() {
		return paintSrc{}, false, true
	}
	coord := func(id attrID, def string, axis int) float64 {
		v, has := r.gradAttr(n, id)
		if !has {
			v = def
		}
		f, u, okk := parseLength(v)
		if !okk {
			f, u, _ = parseLength(def)
		}
		if unitsBBox {
			if u == "%" {
				return f / 100
			}
			return f
		}
		return st.toPx(f, u, axis)
	}
	key := gradKey{n, opacity}
	base := r.grads[key]
	if base == nil {
		base = &gradient{}
		base.build(stops, opacity)
		if v, has := r.gradAttr(n, aSpreadMethod); has {
			switch strings.TrimSpace(v) {
			case "reflect":
				base.spread = spreadReflect
			case "repeat":
				base.spread = spreadRepeat
			}
		}
		if r.grads == nil {
			r.grads = map[gradKey]*gradient{}
		}
		r.grads[key] = base
	}
	g := new(gradient)
	*g = *base
	g.inv = inv
	if n.tag == tagLinearGradient {
		g.x1 = coord(aX1, "0%", 0)
		g.y1 = coord(aY1, "0%", 1)
		x2 := coord(aX2, "100%", 0)
		y2 := coord(aY2, "0%", 1)
		g.dx, g.dy = x2-g.x1, y2-g.y1
		l2 := g.dx*g.dx + g.dy*g.dy
		if l2 == 0 {
			// Degenerate vector: the last stop colour fills everything.
			return solidPaint(stops[len(stops)-1].c, opacity), true, true
		}
		g.invLen2 = 1 / l2
	} else {
		g.radial = true
		g.cx = coord(aCx, "50%", 0)
		g.cy = coord(aCy, "50%", 1)
		g.r = coord(aR, "50%", 2)
		g.fx, g.fy = g.cx, g.cy
		if _, has := r.gradAttr(n, aFx); has {
			g.fx = coord(aFx, "50%", 0)
		}
		if _, has := r.gradAttr(n, aFy); has {
			g.fy = coord(aFy, "50%", 1)
		}
		if !(g.r > 0) {
			return solidPaint(stops[len(stops)-1].c, opacity), true, true
		}
		// A focal point outside the circle is pulled onto it (SVG 2).
		if d := math.Hypot(g.fx-g.cx, g.fy-g.cy); d > g.r*0.9999 {
			k := g.r * 0.9999 / d
			g.fx = g.cx + (g.fx-g.cx)*k
			g.fy = g.cy + (g.fy-g.cy)*k
		}
	}
	_ = bb
	return paintSrc{sh: g}, true, true
}

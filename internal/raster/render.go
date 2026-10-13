package raster

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
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

	imgs        map[string]*rasterImage      // decoded images by href, nil for one that failed
	imageBytes  int64                        // what imgs is charged to the ledger
	fetched     map[string][]byte            // image bytes by href, from Options.Images
	imageErr    func(href string, err error) // Options.ImageError
	outlines    renderCache[outlineKey, *path]
	colourImgs  map[colourImageKey]*rasterImage        // decoded bitmap glyphs
	colourBytes int64                                  // what colourImgs is charged to the ledger
	svgGlyphs   renderCache[colourImageKey, *document] // parsed SVG glyph documents
	inSVGGlyph  bool                                   // drawing one, whose text draws no other
	grads       renderCache[gradKey, *gradient]

	patterns     map[patternKey]*patternTile
	patternBytes int
	rootState    state
	docStates    map[*node]*state
	active       map[*node]bool // paint servers, masks, filters, markers being expanded
	useLoops     map[*node]bool // clip-path <use> elements whose chain of uses loops (see useLoops)
	inText       bool           // drawing glyphs (text-rendering selects anti-aliasing)
	effectPx     int            // pixels charged to filters and pattern tiles so far

	ctx         context.Context // nil = never cancelled
	ledger      *budget.Ledger  // the call's, from ctx; holds the pattern tiles kept
	pixelOps    int             // pixel work charged so far (see Limits.MaxPixelOps)
	canvasBytes int             // pixel memory of live canvases (see Limits.MaxCanvasBytes)
	ticks       int
	// livePts is the path geometry held for the elements being drawn, in
	// points (see holdPoints and Limits.MaxPathSegments).
	livePts int
	// host maps the host coordinate space (the root viewport's, before its
	// viewBox) to the device: the output scale.
	host matrix
	// maskBytes is the clip coverage held for the elements being drawn,
	// part of canvasBytes (see newMaskCoverage).
	maskBytes int
}

// errLimit is wrapped by every error a limit causes. Its own text is
// budget.ErrLimit's, so a message that names the package and the bound
// ("raster: style sheets of more than 4 bytes: limit exceeded") does not
// name the package twice; errRasterLimit is the bare error, prefixed.
var (
	errLimit       = fmt.Errorf("%w", budget.ErrLimit)
	errRasterLimit = fmt.Errorf("raster: %w", errLimit)
)

// chargePixels accounts for n pixels of filter or pattern-tile work and fails
// the render when the budget is spent.
func (r *renderer) chargePixels(n int) bool {
	return r.chargeEffect(n) && r.chargeOps(n)
}

// chargeEffect accounts for n pixels of filter work against
// Limits.MaxEffectPixels alone: the filter charges its work to MaxPixelOps
// as it does it, a band at a time (filterRun.work).
func (r *renderer) chargeEffect(n int) bool {
	if n < 0 || n > r.lim.MaxEffectPixels-r.effectPx {
		r.fail(fmt.Errorf("raster: filter and pattern work exceeds %d pixels: %w", r.lim.MaxEffectPixels, errLimit))
		return false
	}
	r.effectPx += n
	return r.err == nil
}

// bands makes one pass over reg, f a band of rows (about
// imageref.BandPixels pixels) at a time, each band's pixels charged to
// MaxPixelOps first (chargeOps, which polls the context): a pass over a
// layer as large as the canvas, compositing it or applying a mask, reaches
// the deadline as the canvas's own passes do. It reports whether the pass
// ran to its end; once it stops, no further band runs and the render has
// failed.
func (r *renderer) bands(reg irect, f func(band irect)) bool {
	if reg.empty() {
		return r.err == nil
	}
	step := imageref.BandRows(reg.w())
	for y := reg.y0; y < reg.y1; y += step {
		band := irect{reg.x0, y, reg.x1, min(y+step, reg.y1)}
		if !r.chargeOps(band.w() * band.h()) {
			return false
		}
		f(band)
	}
	return true
}

// chargeOps accounts for n pixels of work against Limits.MaxPixelOps and
// polls the context. It reports whether rendering may continue.
func (r *renderer) chargeOps(n int) bool {
	if !r.affordOps(n) {
		return false
	}
	r.pixelOps += n
	return r.poll(n >= 1<<14)
}

// affordOps reports whether n pixel operations fit what is left of
// Limits.MaxPixelOps, failing the render when they do not, without charging
// them: work known to be at least n fails before it starts.
func (r *renderer) affordOps(n int) bool {
	if r.err != nil {
		return false
	}
	if n < 0 || n > r.lim.MaxPixelOps-r.pixelOps {
		r.fail(fmt.Errorf("raster: render work exceeds %d pixel operations (output %dx%d; raise Limits.MaxPixelOps to allow it): %w",
			r.lim.MaxPixelOps, r.cw, r.ch, errLimit))
		return false
	}
	return true
}

// holdPoints accounts for n points of path geometry the element being drawn
// is about to hold: parsed path data, flattened polylines, stroke outlines
// and the scan converter's edges (an edge as edgePoints points, its size).
// An element gives back what it held when it is done (renderElement), its
// buffers then being reused or dropped, so livePts is what the elements on
// the stack hold at once, and MaxPathSegments bounds it. Past the limit the
// render stops with budget.Stop, which Render turns into its error:
// geometry is built deep in flattening and stroking, which have no error to
// return.
func (r *renderer) holdPoints(n int) {
	if n <= 0 {
		return
	}
	if n > r.lim.MaxPathSegments-r.livePts {
		r.fail(fmt.Errorf("raster: a path's geometry exceeds %d points (raise Limits.MaxPathSegments or WithMemoryLimit): %w", r.lim.MaxPathSegments, errLimit))
		panic(&budget.Stop{Err: r.err})
	}
	r.livePts += n
}

// edgePoints is what an edge of the scan converter counts as in holdPoints:
// an edge, its index and its active copy take about five points' bytes.
const edgePoints = 5

// flatCharge charges flattening for kept points (holdPoints) and for work,
// the points it computed, culled or not, to MaxPixelOps; it stops the
// render as holdPoints does.
func (r *renderer) flatCharge(kept, work int) {
	r.holdPoints(kept)
	if !r.chargeOps(work) {
		panic(&budget.Stop{Err: r.err})
	}
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
	n := budget.MulInt(budget.MulInt(w, h), 4)
	if n > r.lim.MaxCanvasBytes-r.canvasBytes {
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
	r.rast.work, r.rast.workCap = 0, min(rem, math.MaxInt-1)+1
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
		r.fail(fmt.Errorf("raster: more than %d nested opacity/blend layers: %w", r.lim.MaxLayerDepth, errLimit))
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
	if d := layer.dirty.intersect(layer.bounds()); !d.empty() {
		r.bands(d, func(band irect) { compositeLayer(prev, layer, band, opacity, nil, mode) })
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
	held, masks := r.livePts, r.maskBytes
	defer func() {
		// What the element built for itself is dropped with it.
		r.nest--
		r.livePts = held
		r.canvasBytes -= r.maskBytes - masks
		r.maskBytes = masks
	}()
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
				return r.intersectMasks(&mask{r: ir}, st.clip)
			}
			return &mask{r: ir}
		}
	}
	// The rectangle from its corners: x0 + (x1 - x0) is not x1 where the
	// corners are far apart, 1e38 and -1e5 say, which moved the far edge.
	var p path
	p.moveTo(rc.x0, rc.y0)
	p.lineTo(rc.x1, rc.y0)
	p.lineTo(rc.x1, rc.y1)
	p.lineTo(rc.x0, rc.y1)
	p.close()
	mk := r.pathsMask([]*path{&p}, []matrix{m}, []bool{false}, region)
	return r.intersectMasks(mk, st.clip)
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

// newMaskCoverage returns a coverage map for region, charged to the canvas
// budget (C103): a clip lives as long as the element it clips, and nested
// clips, one per level, each as large as the canvas, would otherwise add up
// unbounded. The element that made it gives it back when it is done
// (renderElement), as everything charged with maskBytes since it began.
func (r *renderer) newMaskCoverage(region irect) []uint8 {
	n := region.w() * region.h()
	if n > r.lim.MaxCanvasBytes-r.canvasBytes {
		r.fail(fmt.Errorf("raster: clip and mask coverage needs more than %d MiB of pixel memory at once (raise Limits.MaxCanvasBytes or WithMemoryLimit): %w",
			r.lim.MaxCanvasBytes>>20, errLimit))
		panic(&budget.Stop{Err: r.err})
	}
	r.canvasBytes += n
	r.maskBytes += n
	return make([]uint8, n)
}

// intersectMasks multiplies two coverage maps (nil means unrestricted).
func (r *renderer) intersectMasks(a, b *mask) *mask {
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
	out := &mask{r: reg, a: r.newMaskCoverage(reg)}
	w := reg.w()
	r.bands(reg, func(band irect) {
		for y := band.y0; y < band.y1; y++ {
			row := out.a[(y-reg.y0)*w:][:w]
			for x := 0; x < w; x++ {
				v := uint32(a.at(reg.x0+x, y))
				if v != 0 {
					v = mul255(v, uint32(b.at(reg.x0+x, y)))
				}
				row[x] = uint8(v)
			}
		}
	})
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
	mk := &mask{r: region, a: r.newMaskCoverage(region)}
	f := flat{r: r}
	f.setCull(region)
	for i, p := range ps {
		if !r.mayCover(p, ms[i], region, 0) {
			continue // beyond the region: it adds no coverage
		}
		f.flatten(p, ms[i], 0.1)
		r.holdPoints(edgePoints * len(f.pts))
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
		result = r.intersectMasks(r.pathsMask(ps, ms, eo, region), st.clip)
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
				result = r.intersectMasks(result, m2)
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
		start := len(p.pts)
		if !parsePathDataMax(d, p, start+r.lim.MaxPathSegments-r.livePts) {
			r.holdPoints(r.lim.MaxPathSegments + 1) // fails the render
		}
		r.holdPoints(len(p.pts) - start)
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
		pts := n.str(aPoints)
		// Two numbers a point, each a character at least and most a
		// separator too: at most a point per two characters.
		r.holdPoints(len(pts)/2 + 1)
		sc := numScanner{s: pts}
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
	if v, ok := n.get(aVectorEffect); ok {
		st.nonScaling = strings.TrimSpace(v) == "non-scaling-stroke"
	}
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
	// A paint whose geometry lies wholly beyond the clip is neither resolved
	// nor flattened: it would draw nothing (see mayCover).
	region := r.clipRegion(st)
	// The control points' box, one pass for the fill and the stroke both.
	var cb rect
	cbOK, cbDone := false, false
	covers := func(reach float64) bool {
		if !cbDone {
			cb, cbOK = p.bounds()
			cbDone = true
			r.geomWork(len(p.pts))
		}
		return cbOK && mayCoverBox(cb, st.ctm, region, reach)
	}
	doFill := func() {
		if fill && covers(0) {
			if ps, ok := r.resolvePaint(st.fill, fo, st, needBBox); ok {
				r.fl.setCull(region)
				r.fl.flatten(p, st.ctm, shapeTol)
				r.fl.setCull(irect{})
				r.fillPolys(&r.fl, st.evenOdd, ps, st)
			}
		}
	}
	doStroke := func() {
		sp, sst := p, st
		if st.nonScaling {
			sp, sst = r.hostStroke(p, st)
			if sp == nil {
				return
			}
		}
		may := false
		if stroke {
			if sp == p {
				may = covers(strokeReach(sst))
			} else {
				may = r.mayCover(sp, sst.ctm, region, strokeReach(sst))
			}
		}
		if may {
			// The paint is the element's, in its user space.
			if ps, ok := r.resolvePaint(st.stroke, so, st, needBBox); ok {
				r.strokePath(sp, sst, ps)
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
	r.sf.r = r
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
	r.stk.r = r
	r.stk.stroke(&r.sf, ss, ms, &r.so)
	for i, pt := range r.so.pts {
		r.so.pts[i] = st.ctm.apply(pt)
	}
	r.fillPolys(&r.so, false, ps, st)
}

// hostStroke is what a non-scaling stroke is built from (SVG 2, "vector-effect"):
// the path in the host coordinate space, the root viewport's, where the
// stroke's width, dashes, joins and caps are taken unscaled, and a state
// whose transform maps that space to the device (the output scale only). As
// a browser draws it, a path mark Vega scales keeps its stroke width. nil
// when the element's transform cannot be taken out of the host's.
func (r *renderer) hostStroke(p *path, st *state) (*path, *state) {
	inv, ok := r.host.invert()
	if !ok {
		return nil, nil
	}
	l := inv.mul(st.ctm) // user space to host space
	if !l.isFinite() {
		return nil, nil
	}
	r.holdPoints(len(p.pts))
	hp := &path{verbs: append([]uint8(nil), p.verbs...), pts: make([]point, len(p.pts))}
	for i, pt := range p.pts {
		hp.pts[i] = l.apply(pt)
	}
	hst := *st
	hst.ctm = r.host
	return hp, &hst
}

// clipRegion is the device area st may paint: the canvas within its clip.
func (r *renderer) clipRegion(st *state) irect {
	region := r.cv.bounds()
	if st.clip != nil {
		region = region.intersect(st.clip.r)
	}
	return region
}

// strokeReach is how far, in st's user units, a stroke's outline reaches
// from the path: half the width times the longest a join or cap sticks out
// (a miter up to the miter limit, a square cap the half diagonal), and the
// width of a hairline, which is drawn a device pixel wide whatever its own.
func strokeReach(st *state) float64 {
	k := math.Sqrt2
	if (st.join == joinMiter || st.join == joinMiterClip) && st.miter > k {
		k = st.miter
	}
	reach := st.strokeWidth / 2 * k
	if ms := st.ctm.maxScale(); ms > 0 && reach*ms < 2 {
		reach = 2 / ms
	}
	return reach
}

// mayCover reports whether what p draws under m, reaching up to reach (user
// units) beyond its outline, may touch region: false only when the box of
// its control points, so grown and transformed, misses it by a pixel or
// more, so that skipping it changes no pixel (the curve and every point
// flattening takes lie within the control points' hull). It costs a pass
// over the control points, against flattening and scan converting them.
func (r *renderer) mayCover(p *path, m matrix, region irect, reach float64) bool {
	if region.empty() {
		return false
	}
	b, ok := p.bounds()
	if !ok {
		return false
	}
	r.geomWork(len(p.pts))
	return mayCoverBox(b, m, region, reach)
}

// mayCoverBox is mayCover for geometry within the box b of local units.
func mayCoverBox(b rect, m matrix, region irect, reach float64) bool {
	if region.empty() {
		return false
	}
	b = rect{b.x0 - reach, b.y0 - reach, b.x1 + reach, b.y1 + reach}
	d := transformRect(b, m)
	if !(d.x1 >= d.x0) || !(d.y1 >= d.y0) {
		return true // NaN: leave it to the scan converter
	}
	// The corners and the points are transformed by different sums, each
	// rounded: a margin of a pixel and of what rounding can move a point
	// by where the terms are large.
	lin := math.Abs(m.a) + math.Abs(m.b) + math.Abs(m.c) + math.Abs(m.d)
	mag := lin*max(math.Abs(b.x0), math.Abs(b.x1), math.Abs(b.y0), math.Abs(b.y1)) + math.Abs(m.e) + math.Abs(m.f)
	e := 1 + mag*0x1p-40
	return d.x1+e >= float64(region.x0) && d.x0-e <= float64(region.x1) &&
		d.y1+e >= float64(region.y0) && d.y0-e <= float64(region.y1)
}

// geomWork charges n units of geometry work to MaxPixelOps, stopping the
// render as holdPoints does past it.
func (r *renderer) geomWork(n int) {
	if !r.chargeOps(n) {
		panic(&budget.Stop{Err: r.err})
	}
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
	r.holdPoints(edgePoints * len(f.pts))
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
	if unitsBBox {
		b, good := bbox()
		if !good || b.w() <= 0 || b.h() <= 0 {
			return paintSrc{}, false, true
		}
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
	base, _ := r.grads.get(key)
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
		r.grads.put(r.ledger, key, base, gradientBytes)
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
		// tiny-skia's LinearGradient::new: a vector whose length overflows
		// paints nothing, and one too short to draw is one colour, the last
		// stop's when padding and the average of the colour line when
		// repeating or reflecting (it is repeated infinitely often).
		switch length := f32Length(g.x1, g.y1, x2, y2); {
		case math.IsInf(float64(length), 0) || length != length:
			return paintSrc{}, false, true
		case length <= degenerateThreshold:
			if base.spread == spreadPad {
				return solidPaint(stops[len(stops)-1].c, opacity), true, true
			}
			return averageStopColour(stops, opacity), true, true
		}
		l2 := g.dx*g.dx + g.dy*g.dy
		g.invLen2 = 1 / l2
		if math.IsInf(g.invLen2, 0) || g.invLen2 != g.invLen2 {
			// Short in float32 terms but not in float64 ones cannot happen
			// past the threshold above; kept so no NaN reaches the table.
			return paintSrc{}, false, true
		}
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
		// tiny-skia's RadialGradient::new: a radius it rounds to nothing
		// paints nothing, nor does a focal point at an infinite distance.
		if float32(g.r) <= nearlyZero {
			return paintSrc{}, false, true
		}
		if l := f32Length(g.fx, g.fy, g.cx, g.cy); math.IsInf(float64(l), 0) || l != l {
			return paintSrc{}, false, true
		}
		// A focal point outside the circle is pulled onto it (SVG 2).
		if d := math.Hypot(g.fx-g.cx, g.fy-g.cy); d > g.r*0.9999 {
			k := g.r * 0.9999 / d
			g.fx = g.cx + (g.fx-g.cx)*k
			g.fy = g.cy + (g.fy-g.cy)*k
		}
	}
	return paintSrc{sh: g}, true, true
}

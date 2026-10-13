package svgpdf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strings"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/text"
)

// gstate carries the inherited graphics state down the element tree.
// Presentation attributes (fill, stroke, stroke-width, font-*, ...) inherit
// per the SVG spec; opacity is not inherited but multiplies down the tree,
// which matches how Vega uses it (per-leaf alpha, no overlapping translucent
// groups).
type gstate struct {
	// fill and stroke are the paints as resolved where the state is;
	// fillSpec and strokeSpec as stated, which for currentColor is resolved
	// again under each element's own color (see resolvePaints).
	fill, stroke         Paint
	fillSpec, strokeSpec Paint
	color                Color // the color property, what currentColor is
	colorAlpha           float64
	strokeWidth          float64
	dashPattern          []float64
	dashOffset           float64
	lineCap              int // PDF J: 0 butt, 1 round, 2 square
	lineJoin             int // PDF j: 0 miter, 1 round, 2 bevel
	miterLimit           float64
	opacity              float64 // multiplied product of ancestor opacity attrs
	fillOpacity          float64
	strokeOpacity        float64
	evenOdd              bool // fill-rule: evenodd
	smooth               bool // image-rendering: not pixelated

	fontFamily []string
	fontSize   float64
	fontWeight int
	italic     bool
	textAnchor string

	// vw and vh are the viewport's size, what a percentage length is of.
	vw, vh float64
}

func rootState(vw, vh float64) gstate {
	black := Paint{Color: Color{0, 0, 0}, Alpha: 1} // SVG default fill is black
	return gstate{
		fill:          black,
		fillSpec:      black,
		stroke:        Paint{None: true}, // SVG default stroke is none
		strokeSpec:    Paint{None: true},
		colorAlpha:    1,
		strokeWidth:   1,
		miterLimit:    4, // SVG default (PDF's is 10, so it is always written)
		opacity:       1,
		fillOpacity:   1,
		strokeOpacity: 1,
		smooth:        true,
		fontFamily:    []string{"sans-serif"},
		fontSize:      16, // SVG default; Vega always sets it explicitly
		fontWeight:    400,
		textAnchor:    "start",
		vw:            vw,
		vh:            vh,
	}
}

// diag is what a percentage of a length that is neither horizontal nor
// vertical is of (SVG's normalized diagonal).
func (st *gstate) diag() float64 { return math.Sqrt((st.vw*st.vw + st.vh*st.vh) / 2) }

// lengths are the contexts lengths are read in: horizontal, vertical, and
// other (a stroke width, a dash).
func (st *gstate) lenX() lengthCtx { return lengthCtx{st.fontSize, st.vw} }
func (st *gstate) lenY() lengthCtx { return lengthCtx{st.fontSize, st.vh} }
func (st *gstate) lenD() lengthCtx { return lengthCtx{st.fontSize, st.diag()} }

// ignorableAttrs are attributes with no rendering effect that Vega emits.
var ignorableAttrs = map[string]bool{
	"class":                true,
	"role":                 true,
	"pointer-events":       true,
	"version":              true,
	"id":                   true,
	"aria-label":           true,
	"aria-hidden":          true,
	"aria-roledescription": true,
}

func isIgnorableAttr(name string) bool {
	return ignorableAttrs[name] || strings.HasPrefix(name, "aria-") || strings.HasPrefix(name, "data-")
}

// geometryAttrs lists the element-specific attributes the renderer consumes.
var geometryAttrs = map[string]map[string]bool{
	"svg":      {"width": true, "height": true, "viewBox": true},
	"g":        {},
	"a":        {"href": true, "target": true, "rel": true},
	"rect":     {"x": true, "y": true, "width": true, "height": true},
	"path":     {"d": true, "vector-effect": true},
	"line":     {"x1": true, "y1": true, "x2": true, "y2": true},
	"text":     {"x": true, "y": true, "dx": true, "dy": true},
	"tspan":    {"x": true, "y": true, "dx": true, "dy": true},
	"image":    {"x": true, "y": true, "width": true, "height": true, "href": true, "preserveAspectRatio": true},
	"defs":     {},
	"clipPath": {},
}

// checkAttrs errors on any attribute the renderer would otherwise silently
// drop, so unsupported SVG features surface as errors instead of misrendered
// output (the caller then falls back to raster). An element drawn through
// applyPresentation has its attributes checked there (declarations).
func checkAttrs(e *element) error {
	geo := geometryAttrs[e.name]
	for _, a := range e.attrs {
		if p := props[a.name]; p != propNone && !styleOnly[p] {
			continue
		}
		if err := checkAttr(e, geo, a.name); err != nil {
			return err
		}
	}
	return nil
}

// checkAttr errors on an attribute that is not a presentation property
// unless it is one of e's own (geo) or one with no rendering effect.
func checkAttr(e *element, geo map[string]bool, name string) error {
	if geo[name] || name == "transform" || name == "style" || isIgnorableAttr(name) {
		return nil
	}
	return fmt.Errorf("svgpdf: unsupported attribute %s on <%s>", quoted(name), e.name)
}

// renderer walks the element tree and emits content stream operators.
type renderer struct {
	w      *contentWriter
	clips  map[string]*element
	shaper TextShaper
	fonts  *fontCatalog // nil in TextOutlines mode: all text drawn as paths
	images *imageCatalog

	gradients map[string]*gradient // by id, and Vega's patterns by theirs
	// sliced are the ids of Vega's patterns around radial gradients, and
	// badPatterns those of other patterns (see collectPatterns).
	sliced, badPatterns map[string]bool
	shadings            []*gradient  // the gradients drawn, in first-use order
	patterns            []patternUse // the gradient strokes, in order

	// glyphs memoizes scaled outlines per (Face, glyph, size) for the
	// lifetime of one render: axis labels repeat digits, so the same glyph
	// is drawn many times. It holds at most maxGlyphCacheBytes of them.
	glyphs      map[glyphKey][]text.Segment
	glyphsBytes int64
	// glyphBoxes are where the colour glyphs drawn as images go, in font
	// units, by their image's key.
	glyphBoxes map[string]shape.Rect
	// glyphShadings are the colour glyphs' gradients, each written once.
	glyphShadings map[string]*gradient
	// forms are the form XObjects drawn, in first-use order, each content
	// once (see form), by a digest of the content.
	forms     []*formDef
	formIndex map[[sha256.Size]byte]*formDef
	// links are the link annotations of the page, in document order.
	links []linkAnnot
	// pageW and pageH are the page's size, which an inverted soft mask's
	// BBox covers (see invertedMaskForm); vw and vh the viewport's, in user
	// units, what a percentage is of.
	pageW, pageH float64
	vw, vh       float64
	// painted are the colour glyphs painted so far (see paintColourGlyph),
	// at most maxPaintedBytes of them.
	painted      map[paintedKey]paintedGlyph
	paintedBytes int64

	ctx context.Context
	// ledger is the call's (nil for none): what the render keeps is charged
	// to it before it is kept (ADR 0010).
	ledger    *budget.Ledger
	lim       Limits
	visited   int       // elements rendered (a <clipPath> is re-applied per reference)
	segsTotal int       // path segments parsed so far
	segBuf    []PathSeg // reused by parsePath
	declBuf   []decl    // reused by applyPresentation
	// famSrc is the font-family value read last, and fam its families.
	famSrc string
	fam    []string

	// Reused by drawTextRunFont and mapRun.
	gidBuf     []uint16
	tjBuf      []tjItem
	clusterBuf []int
	textTotal  int // text bytes shaped so far
}

// The per-render caches of glyph outlines and painted colour glyphs keep
// what a chart repeats; past these they stop keeping, and what they would
// have kept is made again where it is drawn (X4).
const (
	maxGlyphCacheBytes = 8 << 20
	maxPaintedBytes    = 16 << 20
)

// alloc charges n bytes the render is about to keep to the call's ledger,
// panicking with a *budget.Stop past its bound (recovered by
// ConvertWithUsage).
func (r *renderer) alloc(n int64) { r.ledger.MustAlloc(n) }

// parsePath parses path data, charging its segments to the render budget.
// Path data with an error is drawn up to it (see parsePathDataInto).
func (r *renderer) parsePath(d string) ([]PathSeg, error) {
	segs, err := parsePathDataInto(r.segBuf, d, r.lim.MaxPathSegments-r.segsTotal)
	if err != nil {
		return nil, err
	}
	r.segBuf = segs // the caller draws the segments before the next parse
	r.segsTotal += len(segs)
	return segs, nil
}

// render translates the parsed SVG root into a content stream plus page
// dimensions in points (1 SVG px = 1 pt), along with the font catalog of the
// text drawn (nil in TextOutlines mode).
func render(root *element, shaper TextShaper, fetched map[string][]byte, opts Options) (content [][]byte, gsList []gsEntry, fonts *fontCatalog, images *imageCatalog, paints paintDefs, width, height float64, err error) {
	if err := checkAttrs(root); err != nil {
		return nil, nil, nil, nil, paintDefs{}, 0, 0, err
	}
	// The page's size: a length in absolute units (no percentage, which
	// would be of a viewport there is none of).
	noPercent := lengthCtx{16, math.NaN()}
	w, okW := parseLength(root.attrVal("width"), noPercent)
	h, okH := parseLength(root.attrVal("height"), noPercent)
	if !okW || !okH || w <= 0 || h <= 0 {
		return nil, nil, nil, nil, paintDefs{}, 0, 0, fmt.Errorf("svgpdf: <svg> must declare positive width and height")
	}
	width, height = w, h

	clips, err := collectClipPaths(root)
	if err != nil {
		return nil, nil, nil, nil, paintDefs{}, 0, 0, err
	}

	gradients, patterns, err := collectGradients(root)
	if err != nil {
		return nil, nil, nil, nil, paintDefs{}, 0, 0, err
	}

	r := &renderer{clips: clips, gradients: gradients, shaper: shaper, ctx: opts.Context, lim: opts.Limits.withDefaults()}
	r.sliced, r.badPatterns = collectPatterns(patterns, gradients)
	if opts.Context != nil {
		r.ledger = budget.LedgerFrom(opts.Context)
	}
	r.w = newContentWriter(r.ledger)
	r.images = newImageCatalog(fetched, r.lim, r.ledger)
	r.images.onErr = opts.ImageError
	r.images.ctx = opts.Context
	if opts.Text != TextOutlines && shaper != nil {
		r.fonts = newFontCatalog(opts.Context, opts.Text, shaper)
	}

	// SVG's y axis points down, PDF's points up. A global flip mapping
	// (x, y) → (x, height − y) lets every SVG coordinate pass through
	// unchanged below this point. Glyph outlines are pre-flipped in
	// drawText, so text is not mirrored by this.
	r.w.concat(Matrix{A: 1, B: 0, C: 0, D: -1, E: 0, F: height})
	r.pageW, r.pageH = width, height

	// viewBox, when present and valid, maps user units onto the
	// width×height viewport; one that does not parse, or has a negative
	// size, is ignored, and one of size zero draws nothing (SVG 1.1, 7.7).
	vw, vh := width, height
	if vb, ok := root.attr("viewBox"); ok {
		if nums, ok := parseNumberList(vb); ok && len(nums) == 4 && nums[2] >= 0 && nums[3] >= 0 {
			if nums[2] == 0 || nums[3] == 0 {
				return r.finish(width, height)
			}
			m := viewBoxMatrix(nums, width, height)
			if !m.IsIdentity() {
				r.w.concat(m)
			}
			vw, vh = nums[2], nums[3]
		}
	}
	r.vw, r.vh = vw, vh

	if err := r.children(root, rootState(vw, vh)); err != nil {
		return nil, nil, nil, nil, paintDefs{}, 0, 0, err
	}
	return r.finish(width, height)
}

// finish is what render returns once the tree is drawn.
func (r *renderer) finish(width, height float64) (content [][]byte, gsList []gsEntry, fonts *fontCatalog, images *imageCatalog, paints paintDefs, w, h float64, err error) {
	if err := r.w.err; err != nil {
		return nil, nil, nil, nil, paintDefs{}, 0, 0, err
	}
	return r.w.stream(), r.w.gsNames, r.fonts, r.images, paintDefs{r.shadings, r.patterns, r.forms, r.links}, width, height, nil
}

// viewBoxMatrix maps a viewBox (min-x, min-y, width, height, the sizes
// positive) onto the width×height viewport, stretched (Vega writes no
// preserveAspectRatio, and its viewBox is the viewport's own size).
func viewBoxMatrix(vb []float64, width, height float64) Matrix {
	minX, minY, vbW, vbH := vb[0], vb[1], vb[2], vb[3]
	sx, sy := width/vbW, height/vbH
	return Matrix{A: sx, D: sy, E: -minX * sx, F: -minY * sy}
}

func (r *renderer) children(e *element, st gstate) error {
	for _, c := range e.children {
		if err := r.element(c, st); err != nil {
			return err
		}
	}
	return nil
}

// elementProps are an element's properties that do not inherit.
type elementProps struct {
	hidden bool   // display: none
	clip   string // the id clip-path refers to, or ""
	blend  string // mix-blend-mode as a PDF blend mode, or "" for normal
}

func (r *renderer) element(e *element, st gstate) error {
	defer r.w.spill()
	if r.visited++; r.visited&63 == 0 {
		if err := ctxErr(r.ctx); err != nil {
			return err
		}
	}
	switch e.name {
	case "defs", "clipPath", "linearGradient", "radialGradient", "pattern":
		return nil // definitions are referenced, not drawn
	}
	st, ep, err := r.applyPresentation(e, st)
	if err != nil {
		return err
	}
	if ep.hidden {
		return nil
	}

	// A transform that does not parse is ignored: the element is drawn
	// untransformed, as SVG draws it.
	transform := Identity()
	if v, ok := e.attr("transform"); ok {
		if m, ok := parseTransform(v); ok {
			transform = m
		}
	}
	if ep.blend != "" {
		return r.blended(ep.blend, func() error { return r.place(e, st, transform, ep.clip) })
	}
	return r.place(e, st, transform, ep.clip)
}

// place draws an element under its transform and clip.
func (r *renderer) place(e *element, st gstate, transform Matrix, clip string) error {
	needsWrap := !transform.IsIdentity() || clip != ""
	if needsWrap {
		r.w.save()
		if !transform.IsIdentity() {
			r.w.concat(transform)
		}
		if clip != "" {
			if err := r.applyClip(clip); err != nil {
				return err
			}
		}
	}

	var err error
	switch e.name {
	case "g":
		err = r.children(e, st)
	case "a":
		err = r.link(e, st)
	case "rect":
		err = r.drawRect(e, st)
	case "path":
		err = r.drawPath(e, st)
	case "line":
		err = r.drawLine(e, st)
	case "text":
		err = r.drawText(e, st)
	case "image":
		err = r.drawImage(e, st)
	default:
		err = fmt.Errorf("svgpdf: unsupported element <%s>", e.name)
	}
	if err != nil {
		return err
	}

	if needsWrap {
		r.w.restore()
	}
	return nil
}

// blended draws an element whose mix-blend-mode is bm: as a group, a form
// XObject of what draw writes, painted under the blend mode, as CSS
// composites an element with a blend mode as a group of its own onto what
// is behind it. The group covers the page, mapped into the current user
// space; in a space that collapses nothing shows.
func (r *renderer) blended(bm string, draw func() error) error {
	inv, ok := invert(r.w.cur.ctm)
	if !ok {
		return nil
	}
	saved := r.w.beginCapture()
	err := draw()
	content := r.w.endCapture(saved)
	if err != nil || len(content) == 0 {
		return err
	}
	res := r.form(content, mapBox(inv, [4]float64{0, 0, r.pageW, r.pageH}))
	r.w.save()
	r.w.stateGS("bm:"+bm, gsEntry{bm: bm})
	r.w.drawXObject(res)
	r.w.restore()
	return nil
}

// applyPresentation folds an element's presentation properties into the
// inherited state, and returns the ones that do not inherit. A value that
// does not parse is ignored (see attrs.go); the only error is a style
// property the renderer does not know.
func (r *renderer) applyPresentation(e *element, st gstate) (gstate, elementProps, error) {
	var ep elementProps
	ds, err := declarations(e, r.declBuf)
	if err != nil {
		return st, ep, err
	}
	r.declBuf = ds
	if len(ds) == 0 {
		return st, ep, nil
	}
	// font-size first: an em length of the element is of its own size.
	parentSize := st.fontSize
	for _, d := range ds {
		if d.p == propFontSize {
			st.applyFontSize(d.value, parentSize)
		}
	}
	for _, d := range ds {
		switch d.p {
		case propFontSize:
		case propFontFamily:
			// A chart's labels name one family list each: the last one read
			// is kept, and shared (the list is never written to).
			if d.value != r.famSrc || r.fam == nil {
				f, ok := parseFontFamilies(strings.TrimSpace(d.value))
				if !ok {
					continue
				}
				r.famSrc, r.fam = d.value, f
			}
			st.fontFamily = r.fam
		default:
			st.apply(d, &ep)
		}
	}
	st.fill, st.stroke = st.resolve(st.fillSpec), st.resolve(st.strokeSpec)
	return st, ep, nil
}

// resolve is a paint with currentColor resolved against the state's color.
func (st *gstate) resolve(p Paint) Paint {
	if p.Current {
		return colorPaint(st.color, st.colorAlpha)
	}
	if p.Fallback != nil && p.Fallback.Current {
		fb := colorPaint(st.color, st.colorAlpha)
		p.Fallback = &fb
	}
	return p
}

// applyFontSize applies a font-size, its relative values relative to the
// parent's.
func (st *gstate) applyFontSize(v string, parent float64) {
	v = strings.TrimSpace(v)
	if kw, ok := fontSizeKeywords[v]; ok {
		st.fontSize = kw
		return
	}
	switch v {
	case "larger":
		st.fontSize = parent * 1.2
		return
	case "smaller":
		st.fontSize = parent / 1.2
		return
	}
	// A negative size is invalid; zero is a size, at which nothing shows.
	if px, ok := parseLength(v, lengthCtx{parent, parent}); ok && px >= 0 {
		st.fontSize = px
	}
}

// apply applies one presentation property (not font-size).
func (st *gstate) apply(d decl, ep *elementProps) {
	v := strings.TrimSpace(d.value)
	if v == "inherit" {
		return // the inherited value, which the state holds
	}
	switch d.p {
	case propFill:
		if p, ok := parsePaint(v); ok {
			st.fillSpec = p
		}
	case propStroke:
		if p, ok := parsePaint(v); ok {
			st.strokeSpec = p
		}
	case propColor:
		if c, a, ok := parseColor(v); ok {
			st.color, st.colorAlpha = c, a
		}
	case propStrokeWidth:
		if w, ok := parseLength(v, st.lenD()); ok && w >= 0 {
			st.strokeWidth = w
		}
	case propMiterLimit:
		if m, ok := parseNumber(v); ok && m >= 1 {
			st.miterLimit = m
		}
	case propLineCap:
		switch v {
		case "butt":
			st.lineCap = 0
		case "round":
			st.lineCap = 1
		case "square":
			st.lineCap = 2
		}
	case propLineJoin:
		switch v {
		case "miter", "miter-clip", "arcs":
			st.lineJoin = 0
		case "round":
			st.lineJoin = 1
		case "bevel":
			st.lineJoin = 2
		}
	case propDashArray:
		if dash, ok := parseDashArray(v, st.lenD()); ok {
			st.dashPattern = dash
		}
	case propDashOffset:
		if o, ok := parseLength(v, st.lenD()); ok {
			st.dashOffset = o
		}
	case propOpacity:
		if o, ok := parseOpacity(v); ok {
			st.opacity *= o // multiplies down the tree, see gstate doc
		}
	case propFillOpacity:
		if o, ok := parseOpacity(v); ok {
			st.fillOpacity = o
		}
	case propStrokeOpacity:
		if o, ok := parseOpacity(v); ok {
			st.strokeOpacity = o
		}
	case propFillRule:
		switch v {
		case "nonzero":
			st.evenOdd = false
		case "evenodd":
			st.evenOdd = true
		}
	case propClipPath:
		if v == "none" {
			ep.clip = ""
		} else if id, ok := urlRef(v); ok {
			ep.clip = id
		}
	case propDisplay:
		ep.hidden = v == "none"
	case propFontWeight:
		if w, ok := parseFontWeight(v, st.fontWeight); ok {
			st.fontWeight = w
		}
	case propFontStyle:
		switch {
		case v == "normal":
			st.italic = false
		case v == "italic", v == "oblique", strings.HasPrefix(v, "oblique "):
			st.italic = true
		}
	case propFontVariant:
		// Drawn as normal, as the PNG writer draws it: small capitals are
		// synthesised by no renderer here.
	case propTextAnchor:
		switch v {
		case "start", "middle", "end":
			st.textAnchor = v
		}
	case propBlend:
		if v == "normal" {
			ep.blend = ""
		} else if bm, ok := pdfBlendModes[v]; ok {
			ep.blend = bm
		}
	case propImageRendering:
		switch v {
		case "pixelated", "optimizeSpeed", "crisp-edges":
			st.smooth = false
		case "auto", "optimizeQuality", "smooth", "high-quality":
			st.smooth = true
		}
	}
}

// urlRef is the id of a url(#id) reference.
func urlRef(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "url(") || !strings.HasSuffix(s, ")") {
		return "", false
	}
	ref := strings.Trim(strings.TrimSpace(s[4:len(s)-1]), `"'`)
	if len(ref) < 2 || ref[0] != '#' {
		return "", false
	}
	return ref[1:], true
}

// applyClip intersects the current clip region with the geometry of the
// <clipPath> of the id.
func (r *renderer) applyClip(id string) error {
	clip, ok := r.clips[id]
	if !ok {
		return fmt.Errorf("svgpdf: clip-path references unknown id %s", quoted(id))
	}
	// A clip is no drawing: it grows no link's box.
	links := r.w.links
	r.w.links = nil
	defer func() { r.w.links = links }()
	// Every reference re-emits the clip geometry; charge it to the path budget.
	if r.segsTotal += len(clip.children); r.segsTotal > r.lim.MaxPathSegments {
		return limitErr("clip paths emitted more than %d segments", r.lim.MaxPathSegments)
	}
	st := rootState(r.vw, r.vh)
	for _, c := range clip.children {
		if _, hasT := c.attr("transform"); hasT {
			return fmt.Errorf("svgpdf: transform on <clipPath> children is not supported")
		}
		switch c.name {
		case "rect":
			x, y, w, h := rectGeometry(c, &st)
			r.w.rect(x, y, w, h)
		case "path":
			segs, err := r.parsePath(c.attrVal("d"))
			if err != nil {
				return err
			}
			r.w.pathSegs(segs)
		default:
			return fmt.Errorf("svgpdf: unsupported element <%s> inside <clipPath>", c.name)
		}
	}
	r.w.clip()
	return nil
}

// setPaintState writes color/opacity/stroke parameters for a leaf element
// and reports whether it should be filled and/or stroked.
func (r *renderer) setPaintState(st gstate) (fill, stroke bool) {
	fill = !st.fill.None
	stroke = !st.stroke.None
	if fill {
		r.w.fillColor(st.fill.Color)
	}
	if stroke {
		r.w.strokeColor(st.stroke.Color)
		r.w.lineWidth(st.strokeWidth)
		// The reconcile setters emit each parameter only when it differs from
		// the stream's current state, INCLUDING resets to the default (0 J,
		// 0 j, [] 0 d). This is what stops a dashed/round-capped leaf from
		// leaking that state into a following bare sibling that shares the same
		// graphics context (leaves are wrapped in q/Q only for transform/clip).
		r.w.setMiterLimit(st.miterLimit)
		r.w.setLineCap(st.lineCap)
		r.w.setLineJoin(st.lineJoin)
		r.w.setDash(st.dashPattern, st.dashOffset)
	}
	fillAlpha := st.opacity * st.fillOpacity * st.fill.alpha()
	strokeAlpha := st.opacity * st.strokeOpacity * st.stroke.alpha()
	// setAlpha reconciles against the current alpha and resets to opaque when
	// needed, so a translucent leaf cannot leak its alpha forward.
	r.w.setAlpha(fillAlpha, strokeAlpha)
	return fill, stroke
}

// coord is a geometry attribute of an element, a length: 0 when absent or
// when it does not parse (its initial value, as SVG takes an invalid one).
func coord(e *element, name string, c lengthCtx) float64 {
	v, ok := e.attr(name)
	if !ok {
		return 0
	}
	f, ok := parseLength(v, c)
	if !ok {
		return 0
	}
	return f
}

func rectGeometry(e *element, st *gstate) (x, y, w, h float64) {
	return coord(e, "x", st.lenX()), coord(e, "y", st.lenY()), coord(e, "width", st.lenX()), coord(e, "height", st.lenY())
}

func (r *renderer) drawRect(e *element, st gstate) error {
	x, y, w, h := rectGeometry(e, &st)
	if w <= 0 || h <= 0 {
		return nil // zero-area rects render nothing per the SVG spec
	}
	if st.fill.Gradient != "" || st.stroke.Gradient != "" {
		return r.paintShape(st, func() { r.w.rect(x, y, w, h) }, rect{x, y, x + w, y + h}, true)
	}
	fill, stroke := r.setPaintState(st)
	if !fill && !stroke {
		return nil
	}
	r.w.rect(x, y, w, h)
	r.w.paint(fill, stroke, st.evenOdd)
	return nil
}

func (r *renderer) drawPath(e *element, st gstate) error {
	d := e.attrVal("d")
	if strings.TrimSpace(d) == "" {
		return nil // Vega emits empty d for placeholder foreground paths
	}
	segs, err := r.parsePath(d)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return nil
	}
	if st.fill.Gradient != "" || st.stroke.Gradient != "" {
		box, ok := segsBox(segs)
		return r.paintShape(st, func() { r.w.pathSegs(segs) }, box, ok)
	}
	if strings.TrimSpace(e.attrVal("vector-effect")) == "non-scaling-stroke" && !st.stroke.None {
		return r.drawNonScaling(segs, st)
	}
	fill, stroke := r.setPaintState(st)
	if !fill && !stroke {
		return nil
	}
	r.w.pathSegs(segs)
	r.w.paint(fill, stroke, st.evenOdd)
	return nil
}

// drawNonScaling draws a path with vector-effect non-scaling-stroke, which
// Vega writes for a path mark with a scaleX or scaleY: its stroke (width and
// dashes) is in the page's space, not the path's. A PDF stroke is in the
// space current when it is painted, so the path is mapped to the page here
// and drawn there.
func (r *renderer) drawNonScaling(segs []PathSeg, st gstate) error {
	m := r.w.cur.ctm
	inv, ok := invert(m)
	if !ok {
		return nil // a space that collapses shows nothing
	}
	r.w.save()
	r.w.concat(inv)
	fill, stroke := r.setPaintState(st)
	if fill || stroke {
		for _, s := range segs {
			r.w.spill()
			p := func(q Point) Point { x, y := m.Apply(q.X, q.Y); return Point{x, y} }
			switch s.Op {
			case OpMoveTo:
				r.w.moveTo(p(s.P3))
			case OpLineTo:
				r.w.lineTo(p(s.P3))
			case OpCubicTo:
				r.w.cubicTo(p(s.P1), p(s.P2), p(s.P3))
			case OpClose:
				r.w.closePath()
			}
		}
		r.w.paint(fill, stroke, st.evenOdd)
	}
	r.w.restore()
	return nil
}

func (r *renderer) drawLine(e *element, st gstate) error {
	x1, y1 := coord(e, "x1", st.lenX()), coord(e, "y1", st.lenY())
	x2, y2 := coord(e, "x2", st.lenX()), coord(e, "y2", st.lenY())
	// Lines are stroke-only geometry.
	if st.stroke.None {
		return nil
	}
	if st.stroke.Gradient != "" {
		st.fill = Paint{None: true}
		return r.paintShape(st, func() {
			r.w.moveTo(Point{x1, y1})
			r.w.lineTo(Point{x2, y2})
		}, rect{math.Min(x1, x2), math.Min(y1, y2), math.Max(x1, x2), math.Max(y1, y2)}, true)
	}
	_, stroke := r.setPaintState(st)
	if !stroke {
		return nil
	}
	r.w.moveTo(Point{x1, y1})
	r.w.lineTo(Point{x2, y2})
	r.w.paint(false, true, false)
	return nil
}

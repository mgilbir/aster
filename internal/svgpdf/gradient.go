package svgpdf

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	pdf0 "github.com/mgilbir/pdf0"
)

// gradient is a <linearGradient> or <radialGradient>, as Vega writes them for
// gradient fills and continuous colour legends: in objectBoundingBox units
// unless gradientUnits says userSpaceOnUse, padded past its ends. Its stops'
// colours are its shading; translucent stops are drawn under a soft mask of
// their alpha. It is drawn as a PDF shading (axial or radial) painted with
// sh inside the shape, under the shape's bounding box when its units are the
// box's.
type gradient struct {
	radial bool
	user   bool      // gradientUnits="userSpaceOnUse"
	coords []float64 // linear: x1 y1 x2 y2; radial: fx fy fr cx cy r
	stops  []gradStop
	res    string // the shading's resource name, once drawn
	// alphas are the stops' alphas, nil when every stop is opaque, and alpha
	// the gradient of them a soft mask draws (see alphaGradient).
	alphas []float64
	alpha  *gradient

	// The rest serve colour glyphs only. gray makes the shading one of
	// DeviceGray, from each stop's colour's R: a soft mask's alpha.
	gray bool
	// domain is the span of the gradient's parameter its coords run over
	// (zero: 0 to 1), and periods how its colour line repeats across it:
	// not at all, repeated, or reflected every other period.
	domain  [2]float64
	periods colourPeriods
	// noExtend leaves a colour glyph's shading unextended past its start or
	// end: a radial one whose circles' radius reaches zero there.
	noExtend [2]bool
	// mesh, when set, makes the shading a free-form triangle mesh, a sweep
	// gradient's, and the rest of the geometry unused.
	mesh []meshVertex
}

type colourPeriods uint8

const (
	periodsNone colourPeriods = iota
	periodsRepeat
	periodsReflect
)

// meshVertex is a corner of a triangle of a mesh shading.
type meshVertex struct {
	x, y float64
	c    Color
}

// maxColourPeriods bounds the periods of a repeated gradient's function.
const maxColourPeriods = 512

type gradStop struct {
	offset float64
	color  Color
}

var gradientAttrs = map[string]map[string]bool{
	"linearGradient": {"id": true, "x1": true, "y1": true, "x2": true, "y2": true, "gradientUnits": true, "spreadMethod": true},
	"radialGradient": {"id": true, "cx": true, "cy": true, "r": true, "fx": true, "fy": true, "fr": true, "gradientUnits": true, "spreadMethod": true},
	"stop":           {"offset": true, "stop-color": true, "stop-opacity": true},
}

// collectGradients gathers the gradients of the document by id.
// The <pattern>s found on the way are returned for collectPatterns.
func collectGradients(root *element) (map[string]*gradient, []*element, error) {
	grads := map[string]*gradient{}
	var patterns []*element
	stack := []*element{root}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e.name == "pattern" {
			patterns = append(patterns, e)
		}
		if e.name != "linearGradient" && e.name != "radialGradient" {
			stack = append(stack, e.children...)
			continue
		}
		id, ok := e.attr("id")
		if !ok {
			return nil, nil, fmt.Errorf("svgpdf: <%s> without id", e.name)
		}
		g, err := parseGradient(e)
		if err != nil {
			return nil, nil, err
		}
		grads[id] = g
	}
	return grads, patterns, nil
}

// collectPatterns finds the <pattern>s Vega writes around each radial
// gradient (vega-scenegraph's SVG renderers: "We wrap the radial gradient in
// a pattern element, allowing us to maintain a circular gradient"):
//
//	<pattern id="p_ID" viewBox="0,0,1,1" width="100%" height="100%"
//	  preserveAspectRatio="xMidYMid slice"><rect width="1" height="1"
//	  fill="url(#ID)"/></pattern>
//
// A paint that refers to one paints the gradient over the square whose side
// is the larger of the shape's box, centred on it, as the pattern's viewBox
// sliced into the box does. They are added to grads under the pattern's id,
// and named in sliced. Any other pattern is unsupported, an error where a
// paint refers to it (in unsupported).
func collectPatterns(patterns []*element, grads map[string]*gradient) (sliced map[string]bool, unsupported map[string]bool) {
	for _, e := range patterns {
		id, ok := e.attr("id")
		if !ok {
			continue
		}
		if g := vegaPatternGradient(e, grads); g != nil {
			if sliced == nil {
				sliced = map[string]bool{}
			}
			grads[id], sliced[id] = g, true
		} else {
			if unsupported == nil {
				unsupported = map[string]bool{}
			}
			unsupported[id] = true
		}
	}
	return sliced, unsupported
}

// vegaPatternGradient is the gradient of a pattern of Vega's form, or nil.
func vegaPatternGradient(e *element, grads map[string]*gradient) *gradient {
	for _, a := range e.attrs {
		v := strings.TrimSpace(a.value)
		switch a.name {
		case "id":
		case "viewBox":
			if nums, ok := parseNumberList(v); !ok || len(nums) != 4 || nums[0] != 0 || nums[1] != 0 || nums[2] != 1 || nums[3] != 1 {
				return nil
			}
		case "width", "height":
			if v != "100%" {
				return nil
			}
		case "preserveAspectRatio":
			if strings.Join(strings.Fields(v), " ") != "xMidYMid slice" {
				return nil
			}
		case "patternUnits":
			if v != "objectBoundingBox" {
				return nil
			}
		default:
			if !isIgnorableAttr(a.name) {
				return nil
			}
		}
	}
	if len(e.children) != 1 || len(e.attrVal("viewBox")) == 0 {
		return nil
	}
	r := e.children[0]
	if r.name != "rect" || len(r.children) > 0 {
		return nil
	}
	for _, a := range r.attrs {
		v := strings.TrimSpace(a.value)
		switch a.name {
		case "width", "height":
			if n, ok := parseNumber(v); !ok || n != 1 {
				return nil
			}
		case "x", "y":
			if n, ok := parseNumber(v); !ok || n != 0 {
				return nil
			}
		case "fill":
		default:
			if !isIgnorableAttr(a.name) {
				return nil
			}
		}
	}
	id, ok := urlRef(r.attrVal("fill"))
	if !ok {
		return nil
	}
	return grads[id]
}

// sliceBox is the box a gradient of a Vega pattern is painted over: the
// square of the larger side of box, centred on it.
func sliceBox(box rect) rect {
	s := math.Max(box.w(), box.h())
	x0 := box.x0 + (box.w()-s)/2
	y0 := box.y0 + (box.h()-s)/2
	return rect{x0, y0, x0 + s, y0 + s}
}

// gradNumber reads a gradient coordinate or a stop offset: a number, or a
// percentage of the box (or of 1).
func gradNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if p, ok := strings.CutSuffix(s, "%"); ok {
		v, ok := parseNumber(p)
		return v / 100, ok
	}
	return parseNumber(s)
}

// parseGradient reads a gradient element. A value that does not parse is
// ignored, as for any attribute (see attrs.go): a coordinate takes its
// initial value, a stop's offset 0, its colour black and its opacity 1.
// What the renderer does not draw (a spread method, a percentage in user
// space) is an error.
func parseGradient(e *element) (*gradient, error) {
	for _, a := range e.attrs {
		if !gradientAttrs[e.name][a.name] && !isIgnorableAttr(a.name) {
			return nil, fmt.Errorf("svgpdf: unsupported attribute %s on <%s>", quoted(a.name), e.name)
		}
	}
	g := &gradient{radial: e.name == "radialGradient"}
	if strings.TrimSpace(e.attrVal("gradientUnits")) == "userSpaceOnUse" {
		g.user = true
	}
	switch strings.TrimSpace(e.attrVal("spreadMethod")) {
	case "reflect", "repeat":
		return nil, fmt.Errorf("svgpdf: a gradient spreadMethod other than pad is not supported")
	}
	num := func(name string, def float64) (float64, error) {
		v, ok := e.attr(name)
		if !ok {
			return def, nil
		}
		if g.user && strings.HasSuffix(strings.TrimSpace(v), "%") {
			return 0, fmt.Errorf("svgpdf: a percentage %s in user space units is not supported", name)
		}
		f, ok := gradNumber(v)
		if !ok {
			return def, nil
		}
		return f, nil
	}
	var names []string
	var defs []float64
	if g.radial {
		names, defs = []string{"cx", "cy", "r", "fr"}, []float64{0.5, 0.5, 0.5, 0}
	} else {
		names, defs = []string{"x1", "y1", "x2", "y2"}, []float64{0, 0, 1, 0}
	}
	if g.user && (len(e.attrs) == 0 || !hasAll(e, names[:3])) {
		return nil, fmt.Errorf("svgpdf: <%s> in user space units needs its coordinates", e.name)
	}
	v := make([]float64, 4)
	for i, n := range names {
		f, err := num(n, defs[i])
		if err != nil {
			return nil, err
		}
		v[i] = f
	}
	if g.radial {
		cx, cy, r, fr := v[0], v[1], v[2], v[3]
		fx, err := num("fx", cx)
		if err != nil {
			return nil, err
		}
		fy, err := num("fy", cy)
		if err != nil {
			return nil, err
		}
		if r < 0 || fr < 0 {
			return nil, fmt.Errorf("svgpdf: <radialGradient> with a negative radius")
		}
		g.coords = []float64{fx, fy, fr, cx, cy, r}
	} else {
		g.coords = v
	}

	prev := 0.0
	translucent := false
	for _, s := range e.children {
		if s.name != "stop" {
			return nil, fmt.Errorf("svgpdf: unsupported element <%s> in <%s>", s.name, e.name)
		}
		for _, a := range s.attrs {
			if !gradientAttrs["stop"][a.name] && !isIgnorableAttr(a.name) {
				return nil, fmt.Errorf("svgpdf: unsupported attribute %s on <stop>", quoted(a.name))
			}
		}
		off, _ := gradNumber(s.attrVal("offset"))
		// Offsets are clamped to 0..1 and to at least the one before.
		off = math.Max(prev, clamp01(off))
		prev = off
		col, alpha := Color{}, 1.0
		if v, ok := s.attr("stop-color"); ok {
			if c, a, ok := parseColor(v); ok {
				col, alpha = c, a
			}
		}
		if v, ok := s.attr("stop-opacity"); ok {
			if o, ok := parseOpacity(v); ok {
				alpha *= o
			}
		}
		translucent = translucent || alpha < 1
		g.stops = append(g.stops, gradStop{off, col})
		g.alphas = append(g.alphas, alpha)
	}
	if !translucent {
		g.alphas = nil
	}
	return g, nil
}

// alphaGradient is g's alpha as a gradient of grey, the same geometry: a
// soft mask's (see paintShape). It is nil for an opaque gradient.
func (g *gradient) alphaGradient() *gradient {
	if g.alphas == nil {
		return nil
	}
	if g.alpha == nil {
		a := &gradient{radial: g.radial, user: g.user, coords: g.coords, gray: true}
		for i, s := range g.stops {
			v := g.alphas[i]
			a.stops = append(a.stops, gradStop{s.offset, Color{v, v, v}})
		}
		g.alpha = a
	}
	return g.alpha
}

func hasAll(e *element, names []string) bool {
	for _, n := range names {
		if _, ok := e.attr(n); !ok {
			return false
		}
	}
	return true
}

// shading is the PDF shading of g: a dictionary, or a stream for a mesh. Its
// colour function runs over the gradient's 0..1 and is the colour of the
// first stop before it and of the last after it, as SVG's pad; repeated, it
// runs over the domain, a copy of it, or its mirror, each period.
func (g *gradient) shading() pdf0.Object {
	if g.mesh != nil {
		return g.meshShading()
	}
	type point struct {
		t float64
		c Color
	}
	var pts []point
	if s := g.stops; s[0].offset > 0 {
		pts = append(pts, point{0, s[0].color})
	}
	for _, s := range g.stops {
		pts = append(pts, point{s.offset, s.color})
	}
	if last := g.stops[len(g.stops)-1]; last.offset < 1 {
		pts = append(pts, point{1, last.color})
	}
	rgb := func(c Color) pdf0.Array {
		if g.gray {
			return pdf0.Array{pdf0.Real(c.R)}
		}
		return pdf0.Array{pdf0.Real(c.R), pdf0.Real(c.G), pdf0.Real(c.B)}
	}
	interp := func(a, b Color) *pdf0.Dictionary {
		f := &pdf0.Dictionary{}
		f.Set("FunctionType", pdf0.Integer(2))
		f.Set("Domain", pdf0.Array{pdf0.Integer(0), pdf0.Integer(1)})
		f.Set("C0", rgb(a))
		f.Set("C1", rgb(b))
		f.Set("N", pdf0.Integer(1))
		return f
	}
	// One function a span between stops; a span of no width is a hard edge
	// between the colours either side, and takes no function of its own.
	var fns, bounds, encode pdf0.Array
	for i := 1; i < len(pts); i++ {
		if pts[i].t <= pts[i-1].t && len(pts) > 2 {
			continue
		}
		if len(fns) > 0 {
			bounds = append(bounds, pdf0.Real(pts[i-1].t))
		}
		fns = append(fns, interp(pts[i-1].c, pts[i].c))
		encode = append(encode, pdf0.Integer(0), pdf0.Integer(1))
	}
	var fn pdf0.Object
	if len(fns) == 1 {
		fn = fns[0]
	} else {
		st := &pdf0.Dictionary{}
		st.Set("FunctionType", pdf0.Integer(3))
		st.Set("Domain", pdf0.Array{pdf0.Integer(0), pdf0.Integer(1)})
		st.Set("Functions", fns)
		st.Set("Bounds", bounds)
		st.Set("Encode", encode)
		fn = st
	}
	if g.periods != periodsNone {
		fn = g.repeat(fn)
	}
	sh := &pdf0.Dictionary{}
	coords := make(pdf0.Array, len(g.coords))
	for i, v := range g.coords {
		coords[i] = pdf0.Real(v)
	}
	if g.radial {
		sh.Set("ShadingType", pdf0.Integer(3))
	} else {
		sh.Set("ShadingType", pdf0.Integer(2))
	}
	sh.Set("ColorSpace", g.space())
	sh.Set("Coords", coords)
	if g.domain != ([2]float64{}) {
		sh.Set("Domain", pdf0.Array{pdf0.Real(g.domain[0]), pdf0.Real(g.domain[1])})
	}
	sh.Set("Function", fn)
	sh.Set("Extend", pdf0.Array{pdf0.Boolean(!g.noExtend[0]), pdf0.Boolean(!g.noExtend[1])})
	return sh
}

// paintDefs are what the content stream names besides fonts and alphas: the
// shadings of the gradients drawn and the patterns of gradient strokes.
type paintDefs struct {
	shadings []*gradient
	patterns []patternUse
	forms    []*formDef
	links    []linkAnnot // the page's link annotations
	// actualText is set when the content has ActualText marked content,
	// a PDF 1.5 feature (see buildPDF).
	actualText bool
}

// patternUse is a gradient stroke: the shading pattern of a gradient placed
// for one shape, in page space.
type patternUse struct {
	res    string // the pattern's resource name (P0, P1, ...)
	g      *gradient
	matrix Matrix
}

// shadingOf registers g's shading, once, and returns its resource name.
func (r *renderer) shadingOf(g *gradient) string {
	if g.res == "" {
		g.res = fmt.Sprintf("Sh%d", len(r.shadings))
		r.shadings = append(r.shadings, g)
	}
	return g.res
}

// resolveGradients is st with a paint that refers to a gradient there is
// none of replaced by its fallback ("url(#g) red"); one without a fallback
// is an error.
func (r *renderer) resolveGradients(st gstate) (gstate, error) {
	for _, p := range []*Paint{&st.fill, &st.stroke} {
		if p.Gradient == "" {
			continue
		}
		if _, ok := r.gradients[p.Gradient]; ok {
			continue
		}
		if r.badPatterns[p.Gradient] {
			return st, fmt.Errorf("svgpdf: paint refers to a <pattern> other than Vega's radial gradient, which is not supported")
		}
		if p.Fallback == nil {
			return st, fmt.Errorf("svgpdf: paint refers to a missing gradient %s", quoted(p.Gradient))
		}
		*p = *p.Fallback
	}
	return st, nil
}

// softMask sets, as the soft mask, the luminosity of the grey gradient a
// (a gradient's alpha), painted as the gradient is: over the box when its
// units are the box's. The mask is a form drawn in the current user space,
// covering the page there. key is the mask's, for paintObject; "" when none
// is set, in a space that collapses.
func (r *renderer) softMask(a *gradient, boxM Matrix) (key string) {
	inv, ok := invert(r.w.cur.ctm)
	if !ok {
		return ""
	}
	var content []byte
	if !a.user {
		content = r.w.appendMatrix(content, boxM)
		content = append(content, " cm\n"...)
	}
	content = formShading(content, r.shadingOf(a))
	fm := r.form(content, mapBox(inv, [4]float64{0, 0, r.pageW, r.pageH}))
	key = "lum:" + fm
	r.w.stateGS(key, gsEntry{mask: fm, maskLuminosity: true})
	return key
}

// paintShape paints the path that addPath draws, of bounding box box, with
// the fill and stroke of st, either or both of which is a gradient. A gradient
// fill is a shading painted inside the path; a gradient stroke is a shading
// pattern as the stroke colour, placed in page space. A gradient in
// objectBoundingBox units stretches over the box, and paints nothing on a box
// with no width or height, as in SVG. A gradient with translucent stops is
// painted under a soft mask of their alpha (C33).
func (r *renderer) paintShape(st gstate, addPath func(), box rect, haveBox bool) error {
	st, err := r.resolveGradients(st)
	if err != nil {
		return err
	}
	boxed := haveBox && box.w() > 0 && box.h() > 0
	// boxOf maps the unit square onto the box a paint's gradient covers.
	boxOf := func(p Paint) Matrix {
		b := box
		if r.sliced[p.Gradient] {
			b = sliceBox(box)
		}
		return Matrix{A: b.w(), D: b.h(), E: b.x0, F: b.y0}
	}

	// Fill.
	if p := st.fill; !p.None {
		boxM := boxOf(p)
		if p.Gradient == "" {
			solid := st
			solid.stroke = Paint{None: true}
			r.setPaintState(solid)
			addPath()
			r.w.paint(true, false, st.evenOdd)
		} else {
			g := r.gradients[p.Gradient]
			switch {
			case len(g.stops) == 0:
			case len(g.stops) == 1:
				solid := st
				solid.fill = colorPaint(g.stops[0].color, alphaOf(g, 0))
				solid.stroke = Paint{None: true}
				if !solid.fill.None {
					r.setPaintState(solid)
					addPath()
					r.w.paint(true, false, st.evenOdd)
				}
			case g.user || boxed:
				res := r.shadingOf(g)
				r.w.save()
				alpha := st.opacity * st.fillOpacity
				r.w.setAlpha(alpha, r.w.cur.strokeAlpha)
				mask := ""
				if a := g.alphaGradient(); a != nil {
					mask = r.softMask(a, boxM)
				}
				addPath()
				r.w.clipRule(st.evenOdd)
				if !g.user {
					r.w.concat(boxM)
				}
				r.w.paintShading(res, composite{alpha: alpha, strokeAlpha: r.w.cur.strokeAlpha, mask: mask})
				r.w.restore()
			}
		}
	}

	// Stroke.
	p := st.stroke
	if p.None {
		return nil
	}
	strokeOnly := st
	strokeOnly.fill = Paint{None: true}
	if p.Gradient == "" {
		if _, stroke := r.setPaintState(strokeOnly); stroke {
			addPath()
			r.w.paint(false, true, false)
		}
		return nil
	}
	g := r.gradients[p.Gradient]
	boxM := boxOf(p)
	masked := false
	switch {
	case len(g.stops) == 0:
		return nil
	case len(g.stops) == 1:
		strokeOnly.stroke = colorPaint(g.stops[0].color, alphaOf(g, 0))
		if strokeOnly.stroke.None {
			return nil
		}
		r.setPaintState(strokeOnly)
	case g.user || boxed:
		m := r.w.cur.ctm
		if !g.user {
			m = m.Mul(boxM)
		}
		r.shadingOf(g)
		use := patternUse{res: fmt.Sprintf("P%d", len(r.patterns)), g: g, matrix: m}
		r.patterns = append(r.patterns, use)
		if a := g.alphaGradient(); a != nil {
			r.w.save()
			r.softMask(a, boxM)
			masked = true
		}
		// The line state as for a solid stroke, then the pattern as its
		// colour in place of the colour setPaintState sets.
		strokeOnly.stroke = Paint{Alpha: 1}
		r.setPaintState(strokeOnly)
		r.w.strokePattern(use.res)
	default:
		return nil
	}
	addPath()
	r.w.paint(false, true, false)
	if masked {
		r.w.restore()
	}
	return nil
}

// alphaOf is the alpha of stop i of g.
func alphaOf(g *gradient, i int) float64 {
	if g.alphas == nil {
		return 1
	}
	return g.alphas[i]
}

// rect is a box: x0 <= x1, y0 <= y1.
type rect struct{ x0, y0, x1, y1 float64 }

func (b rect) w() float64 { return b.x1 - b.x0 }
func (b rect) h() float64 { return b.y1 - b.y0 }

// segsBox is the bounding box of the path's geometry, a cubic's extremes
// included, as SVG's objectBoundingBox is.
func segsBox(segs []PathSeg) (rect, bool) {
	b := rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	add := func(p Point) {
		b.x0, b.y0 = math.Min(b.x0, p.X), math.Min(b.y0, p.Y)
		b.x1, b.y1 = math.Max(b.x1, p.X), math.Max(b.y1, p.Y)
	}
	var cur Point
	for _, s := range segs {
		switch s.Op {
		case OpMoveTo, OpLineTo:
			add(s.P3)
			cur = s.P3
		case OpCubicTo:
			add(s.P3)
			for _, t := range cubicExtrema(cur.X, s.P1.X, s.P2.X, s.P3.X) {
				add(cubicAt(cur, s.P1, s.P2, s.P3, t))
			}
			for _, t := range cubicExtrema(cur.Y, s.P1.Y, s.P2.Y, s.P3.Y) {
				add(cubicAt(cur, s.P1, s.P2, s.P3, t))
			}
			cur = s.P3
		}
	}
	return b, b.x0 <= b.x1
}

func cubicAt(p0, p1, p2, p3 Point, t float64) Point {
	u := 1 - t
	a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return Point{a*p0.X + b*p1.X + c*p2.X + d*p3.X, a*p0.Y + b*p1.Y + c*p2.Y + d*p3.Y}
}

// cubicExtrema are the t in (0, 1) where one coordinate of a cubic Bézier
// turns: the roots of its derivative.
func cubicExtrema(p0, p1, p2, p3 float64) []float64 {
	a := -p0 + 3*p1 - 3*p2 + p3
	b := 2 * (p0 - 2*p1 + p2)
	c := p1 - p0
	var ts []float64
	keep := func(t float64) {
		if t > 0 && t < 1 {
			ts = append(ts, t)
		}
	}
	if math.Abs(a) < 1e-12 {
		if math.Abs(b) > 1e-12 {
			keep(-c / b)
		}
		return ts
	}
	disc := b*b - 4*a*c
	if disc < 0 {
		return ts
	}
	sq := math.Sqrt(disc)
	keep((-b + sq) / (2 * a))
	keep((-b - sq) / (2 * a))
	return ts
}

func (g *gradient) space() pdf0.Name {
	if g.gray {
		return "DeviceGray"
	}
	return "DeviceRGB"
}

// repeat is fn, a function over 0..1, once each period of the domain: a
// stitching function whose bounds are the whole numbers in the domain, each
// period encoded onto fn's 0..1, or onto 1..0 for every other one reflected.
func (g *gradient) repeat(fn pdf0.Object) pdf0.Object {
	d0, d1 := g.domain[0], g.domain[1]
	if !(d1 > d0) {
		return fn
	}
	var fns, bounds, encode pdf0.Array
	for k := math.Floor(d0); k < d1 && len(fns) < maxColourPeriods; k++ {
		lo, hi := math.Max(d0, k), math.Min(d1, k+1)
		if len(fns) > 0 {
			bounds = append(bounds, pdf0.Real(lo))
		}
		e0, e1 := lo-k, hi-k
		// The parity of k as a float: int64 of a k past its range, which
		// a far-out parameter makes, is not defined in Go (X1).
		if g.periods == periodsReflect && math.Mod(k, 2) != 0 {
			e0, e1 = 1-e0, 1-e1
		}
		fns = append(fns, fn)
		encode = append(encode, pdf0.Real(e0), pdf0.Real(e1))
	}
	st := &pdf0.Dictionary{}
	st.Set("FunctionType", pdf0.Integer(3))
	st.Set("Domain", pdf0.Array{pdf0.Real(d0), pdf0.Real(d1)})
	st.Set("Functions", fns)
	st.Set("Bounds", bounds)
	st.Set("Encode", encode)
	return st
}

// meshShading is a free-form triangle mesh shading (type 4) of g.mesh, each
// three vertices a triangle, its coordinates in 32 bits over their range.
func (g *gradient) meshShading() pdf0.Object {
	x0, y0 := math.Inf(1), math.Inf(1)
	x1, y1 := math.Inf(-1), math.Inf(-1)
	for _, v := range g.mesh {
		x0, y0 = math.Min(x0, v.x), math.Min(y0, v.y)
		x1, y1 = math.Max(x1, v.x), math.Max(y1, v.y)
	}
	if !(x1 > x0) || !(y1 > y0) {
		x1, y1 = x0+1, y0+1
	}
	comps := 3
	if g.gray {
		comps = 1
	}
	q := func(v, lo, hi float64) uint32 {
		return uint32(math.Round(unit((v-lo)/(hi-lo)) * math.MaxUint32))
	}
	data := make([]byte, 0, len(g.mesh)*(9+comps))
	for _, v := range g.mesh {
		data = append(data, 0) // a new triangle's vertex
		data = binary.BigEndian.AppendUint32(data, q(v.x, x0, x1))
		data = binary.BigEndian.AppendUint32(data, q(v.y, y0, y1))
		for _, c := range []float64{v.c.R, v.c.G, v.c.B}[:comps] {
			data = append(data, uint8(math.Round(unit(c)*255)))
		}
	}
	// Compressing into memory cannot fail.
	z, _ := compress(data)
	decode := pdf0.Array{pdf0.Real(x0), pdf0.Real(x1), pdf0.Real(y0), pdf0.Real(y1)}
	for range comps {
		decode = append(decode, pdf0.Integer(0), pdf0.Integer(1))
	}
	sh := &pdf0.Stream{Data: z}
	sh.Dict.Set("ShadingType", pdf0.Integer(4))
	sh.Dict.Set("ColorSpace", g.space())
	sh.Dict.Set("BitsPerCoordinate", pdf0.Integer(32))
	sh.Dict.Set("BitsPerComponent", pdf0.Integer(8))
	sh.Dict.Set("BitsPerFlag", pdf0.Integer(8))
	sh.Dict.Set("Decode", decode)
	sh.Dict.Set("Filter", pdf0.Name("FlateDecode"))
	sh.Dict.Set("Length", pdf0.Integer(len(z)))
	return sh
}

// key is a colour glyph gradient's identity: a digest of everything its
// shading is written from, so that two that would write the same shading
// share it. A sweep's mesh is thousands of vertices, which formatting as
// text cost more than drawing.
func (g *gradient) key() string {
	h := sha256.New()
	var b []byte
	f := func(v float64) { b = binary.LittleEndian.AppendUint64(b, math.Float64bits(v)) }
	flag := func(v bool) {
		if v {
			b = append(b, 1)
		} else {
			b = append(b, 0)
		}
	}
	flag(g.radial)
	flag(g.gray)
	flag(g.noExtend[0])
	flag(g.noExtend[1])
	b = append(b, byte(g.periods))
	f(g.domain[0])
	f(g.domain[1])
	b = binary.LittleEndian.AppendUint64(b, uint64(len(g.coords)))
	for _, v := range g.coords {
		f(v)
	}
	b = binary.LittleEndian.AppendUint64(b, uint64(len(g.stops)))
	for _, s := range g.stops {
		f(s.offset)
		f(s.color.R)
		f(s.color.G)
		f(s.color.B)
	}
	b = binary.LittleEndian.AppendUint64(b, uint64(len(g.mesh)))
	h.Write(b)
	for _, v := range g.mesh {
		b = b[:0]
		f(v.x)
		f(v.y)
		f(v.c.R)
		f(v.c.G)
		f(v.c.B)
		h.Write(b)
	}
	return string(h.Sum(nil))
}

// unit is v within 0..1, and 0 for NaN, so that converting a multiple of it
// to an integer is defined (X1): math.Max and Min pass NaN through.
func unit(v float64) float64 {
	switch {
	case v > 1:
		return 1
	case v > 0:
		return v
	}
	return 0
}

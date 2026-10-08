package svgpdf

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	pdf0 "github.com/mgilbir/pdf0"
)

// gradient is a <linearGradient> or <radialGradient>, as Vega writes them for
// gradient fills and continuous colour legends: in objectBoundingBox units
// unless gradientUnits says userSpaceOnUse, padded past its ends, with
// opaque stops. It is drawn as a PDF shading (axial or radial) painted with
// sh inside the shape, under the shape's bounding box when its units are the
// box's.
type gradient struct {
	radial bool
	user   bool      // gradientUnits="userSpaceOnUse"
	coords []float64 // linear: x1 y1 x2 y2; radial: fx fy fr cx cy r
	stops  []gradStop
	res    string // the shading's resource name, once drawn
}

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
func collectGradients(root *element) (map[string]*gradient, error) {
	grads := map[string]*gradient{}
	stack := []*element{root}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e.name != "linearGradient" && e.name != "radialGradient" {
			stack = append(stack, e.children...)
			continue
		}
		id, ok := e.attr("id")
		if !ok {
			return nil, fmt.Errorf("svgpdf: <%s> without id", e.name)
		}
		g, err := parseGradient(e)
		if err != nil {
			return nil, err
		}
		grads[id] = g
	}
	return grads, nil
}

// gradNumber reads a gradient coordinate or a stop offset: a number, or a
// percentage of the box (or of 1).
func gradNumber(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if p, ok := strings.CutSuffix(s, "%"); ok {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		return v / 100, err
	}
	return strconv.ParseFloat(s, 64)
}

func parseGradient(e *element) (*gradient, error) {
	for _, a := range e.attrs {
		if !gradientAttrs[e.name][a.name] && !isIgnorableAttr(a.name) {
			return nil, fmt.Errorf("svgpdf: unsupported attribute %q on <%s>", a.name, e.name)
		}
	}
	g := &gradient{radial: e.name == "radialGradient"}
	switch u := e.attrVal("gradientUnits"); u {
	case "", "objectBoundingBox":
	case "userSpaceOnUse":
		g.user = true
	default:
		return nil, fmt.Errorf("svgpdf: unsupported gradientUnits %q", u)
	}
	if s := e.attrVal("spreadMethod"); s != "" && s != "pad" {
		return nil, fmt.Errorf("svgpdf: unsupported spreadMethod %q", s)
	}
	num := func(name string, def float64) (float64, error) {
		v, ok := e.attr(name)
		if !ok {
			return def, nil
		}
		if g.user && strings.HasSuffix(strings.TrimSpace(v), "%") {
			return 0, fmt.Errorf("svgpdf: a percentage %s in user space units is not supported", name)
		}
		f, err := gradNumber(v)
		if err != nil {
			return 0, fmt.Errorf("svgpdf: <%s> %s: %w", e.name, name, err)
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
	for _, s := range e.children {
		if s.name != "stop" {
			return nil, fmt.Errorf("svgpdf: unsupported element <%s> in <%s>", s.name, e.name)
		}
		for _, a := range s.attrs {
			if !gradientAttrs["stop"][a.name] && !isIgnorableAttr(a.name) {
				return nil, fmt.Errorf("svgpdf: unsupported attribute %q on <stop>", a.name)
			}
		}
		off, err := gradNumber(s.attrVal("offset"))
		if err != nil && strings.TrimSpace(s.attrVal("offset")) != "" {
			return nil, fmt.Errorf("svgpdf: <stop> offset: %w", err)
		}
		// Offsets are clamped to 0..1 and to at least the one before.
		off = math.Max(prev, clamp01(off))
		prev = off
		col, alpha := Color{}, 1.0
		if v, ok := s.attr("stop-color"); ok {
			if col, alpha, err = parseColor(v); err != nil {
				return nil, err
			}
		}
		if v, ok := s.attr("stop-opacity"); ok {
			o, err := gradNumber(v)
			if err != nil {
				return nil, fmt.Errorf("svgpdf: <stop> stop-opacity: %w", err)
			}
			alpha *= clamp01(o)
		}
		if alpha < 1 {
			return nil, fmt.Errorf("svgpdf: a translucent gradient stop is not supported")
		}
		g.stops = append(g.stops, gradStop{off, col})
	}
	return g, nil
}

func hasAll(e *element, names []string) bool {
	for _, n := range names {
		if _, ok := e.attr(n); !ok {
			return false
		}
	}
	return true
}

// shading is the PDF shading dictionary of g. Its colour function runs over
// the gradient's 0..1 and is the colour of the first stop before it and of the
// last after it, as SVG's pad.
func (g *gradient) shading() *pdf0.Dictionary {
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
	rgb := func(c Color) pdf0.Array { return pdf0.Array{pdf0.Real(c.R), pdf0.Real(c.G), pdf0.Real(c.B)} }
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
	sh.Set("ColorSpace", pdf0.Name("DeviceRGB"))
	sh.Set("Coords", coords)
	sh.Set("Function", fn)
	sh.Set("Extend", pdf0.Array{pdf0.Boolean(true), pdf0.Boolean(true)})
	return sh
}

// paintDefs are what the content stream names besides fonts and alphas: the
// shadings of the gradients drawn and the patterns of gradient strokes.
type paintDefs struct {
	shadings []*gradient
	patterns []patternUse
	forms    []*formDef
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

// paintShape paints the path that addPath draws, of bounding box box, with
// the fill and stroke of st, either or both of which is a gradient. A gradient
// fill is a shading painted inside the path; a gradient stroke is a shading
// pattern as the stroke colour, placed in page space. A gradient in
// objectBoundingBox units stretches over the box, and paints nothing on a box
// with no width or height, as in SVG.
func (r *renderer) paintShape(st gstate, addPath func(), box rect, haveBox bool) error {
	lookup := func(p Paint) (*gradient, error) {
		g, ok := r.gradients[p.Gradient]
		if !ok {
			return nil, fmt.Errorf("svgpdf: paint refers to a missing gradient %q", p.Gradient)
		}
		return g, nil
	}
	boxed := haveBox && box.w() > 0 && box.h() > 0
	boxM := Matrix{A: box.w(), D: box.h(), E: box.x0, F: box.y0}

	// Fill.
	if p := st.fill; !p.None {
		if p.Gradient == "" {
			solid := st
			solid.stroke = Paint{None: true}
			r.setPaintState(solid)
			addPath()
			r.w.paint(true, false, st.evenOdd)
		} else {
			g, err := lookup(p)
			if err != nil {
				return err
			}
			switch {
			case len(g.stops) == 0:
			case len(g.stops) == 1:
				solid := st
				solid.fill = Paint{Color: g.stops[0].color, Alpha: 1}
				solid.stroke = Paint{None: true}
				r.setPaintState(solid)
				addPath()
				r.w.paint(true, false, st.evenOdd)
			case g.user || boxed:
				res := r.shadingOf(g)
				r.w.save()
				r.w.setAlpha(st.opacity*st.fillOpacity, r.w.cur.strokeAlpha)
				addPath()
				r.w.clipRule(st.evenOdd)
				if !g.user {
					r.w.concat(boxM)
				}
				r.w.shade(res)
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
	g, err := lookup(p)
	if err != nil {
		return err
	}
	switch {
	case len(g.stops) == 0:
		return nil
	case len(g.stops) == 1:
		strokeOnly.stroke = Paint{Color: g.stops[0].color, Alpha: 1}
		r.setPaintState(strokeOnly)
	case g.user || boxed:
		m := r.w.cur.ctm
		if !g.user {
			m = m.Mul(boxM)
		}
		r.shadingOf(g)
		use := patternUse{res: fmt.Sprintf("P%d", len(r.patterns)), g: g, matrix: m}
		r.patterns = append(r.patterns, use)
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
	return nil
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

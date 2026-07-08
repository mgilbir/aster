package svgpdf

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-text/typesetting/font"
)

// gstate carries the inherited graphics state down the element tree.
// Presentation attributes (fill, stroke, stroke-width, font-*, ...) inherit
// per the SVG spec; opacity is not inherited but multiplies down the tree,
// which matches how Vega uses it (per-leaf alpha, no overlapping translucent
// groups).
type gstate struct {
	fill          Paint
	stroke        Paint
	strokeWidth   float64
	dashPattern   []float64
	dashOffset    float64
	lineCap       int // PDF J: 0 butt, 1 round, 2 square
	lineJoin      int // PDF j: 0 miter, 1 round, 2 bevel
	miterLimit    float64
	opacity       float64 // multiplied product of ancestor opacity attrs
	fillOpacity   float64
	strokeOpacity float64
	evenOdd       bool // fill-rule: evenodd

	fontFamily string
	fontSize   float64
	fontWeight string
	fontStyle  string
	textAnchor string
}

func rootState() gstate {
	return gstate{
		fill:          Paint{Color: Color{0, 0, 0}}, // SVG default fill is black
		stroke:        Paint{None: true},            // SVG default stroke is none
		strokeWidth:   1,
		miterLimit:    4, // SVG default (PDF's is 10, so it is always written)
		opacity:       1,
		fillOpacity:   1,
		strokeOpacity: 1,
		fontFamily:    "sans-serif",
		fontSize:      16, // SVG default; Vega always sets it explicitly
		textAnchor:    "start",
	}
}

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

// presentationAttrs are handled by applyPresentation for every element.
var presentationAttrs = map[string]bool{
	"fill":              true,
	"stroke":            true,
	"stroke-width":      true,
	"stroke-miterlimit": true,
	"stroke-linecap":    true,
	"stroke-linejoin":   true,
	"stroke-dasharray":  true,
	"stroke-dashoffset": true,
	"opacity":           true,
	"fill-opacity":      true,
	"stroke-opacity":    true,
	"fill-rule":         true,
	"transform":         true,
	"clip-path":         true,
	"display":           true,
	"font-family":       true,
	"font-size":         true,
	"font-weight":       true,
	"font-style":        true,
	"text-anchor":       true,
}

// geometryAttrs lists the element-specific attributes the renderer consumes.
var geometryAttrs = map[string]map[string]bool{
	"svg":      {"width": true, "height": true, "viewBox": true},
	"g":        {},
	"rect":     {"x": true, "y": true, "width": true, "height": true},
	"path":     {"d": true},
	"line":     {"x1": true, "y1": true, "x2": true, "y2": true},
	"text":     {},
	"defs":     {},
	"clipPath": {},
}

// checkAttrs errors on any attribute the renderer would otherwise silently
// drop, so unsupported SVG features surface as errors instead of misrendered
// output (the caller then falls back to raster).
func checkAttrs(e *element) error {
	geo := geometryAttrs[e.name]
	for name := range e.attrs {
		if isIgnorableAttr(name) || presentationAttrs[name] || geo[name] {
			continue
		}
		return fmt.Errorf("svgpdf: unsupported attribute %q on <%s>", name, e.name)
	}
	return nil
}

// renderer walks the element tree and emits content stream operators.
type renderer struct {
	w      *contentWriter
	clips  map[string]*element
	shaper TextShaper
	fonts  *fontCatalog // nil in TextOutlines mode: all text drawn as paths

	// glyphs memoizes outline extraction per (Face, GlyphID) for the lifetime
	// of one render: axis labels repeat digits, so the same glyph is drawn
	// many times, and Face.GlyphData re-parses the outline on each call.
	glyphs map[glyphKey]font.GlyphOutline
}

// render translates the parsed SVG root into a content stream plus page
// dimensions in points (1 SVG px = 1 pt), along with the font catalog of the
// text drawn (nil in TextOutlines mode).
func render(root *element, shaper TextShaper, opts Options) (content []byte, gsList []gsEntry, fonts *fontCatalog, width, height float64, err error) {
	if err := checkAttrs(root); err != nil {
		return nil, nil, nil, 0, 0, err
	}
	width, err = parseLength(root.attrs["width"])
	if err != nil {
		return nil, nil, nil, 0, 0, fmt.Errorf("svgpdf: <svg> width: %w", err)
	}
	height, err = parseLength(root.attrs["height"])
	if err != nil {
		return nil, nil, nil, 0, 0, fmt.Errorf("svgpdf: <svg> height: %w", err)
	}
	if width <= 0 || height <= 0 {
		return nil, nil, nil, 0, 0, fmt.Errorf("svgpdf: <svg> must declare positive width and height")
	}

	clips, err := collectClipPaths(root)
	if err != nil {
		return nil, nil, nil, 0, 0, err
	}

	r := &renderer{w: newContentWriter(), clips: clips, shaper: shaper}
	if opts.Text != TextOutlines && shaper != nil {
		r.fonts = newFontCatalog(opts.Text, shaper)
	}

	// SVG's y axis points down, PDF's points up. A global flip mapping
	// (x, y) → (x, height − y) lets every SVG coordinate pass through
	// unchanged below this point. Glyph outlines are pre-flipped in
	// drawText, so text is not mirrored by this.
	r.w.concat(Matrix{A: 1, B: 0, C: 0, D: -1, E: 0, F: height})

	// viewBox, when present, maps user units onto the width×height viewport.
	if vb, ok := root.attr("viewBox"); ok {
		m, err := viewBoxMatrix(vb, width, height)
		if err != nil {
			return nil, nil, nil, 0, 0, err
		}
		if !m.IsIdentity() {
			r.w.concat(m)
		}
	}

	if err := r.children(root, rootState()); err != nil {
		return nil, nil, nil, 0, 0, err
	}
	return r.w.bytes(), r.w.gsNames, r.fonts, width, height, nil
}

func viewBoxMatrix(vb string, width, height float64) (Matrix, error) {
	nums, err := parseNumberList(vb)
	if err != nil || len(nums) != 4 {
		return Matrix{}, fmt.Errorf("svgpdf: invalid viewBox %q", vb)
	}
	minX, minY, vbW, vbH := nums[0], nums[1], nums[2], nums[3]
	if vbW <= 0 || vbH <= 0 {
		return Matrix{}, fmt.Errorf("svgpdf: invalid viewBox %q", vb)
	}
	sx, sy := width/vbW, height/vbH
	return Matrix{A: sx, D: sy, E: -minX * sx, F: -minY * sy}, nil
}

func (r *renderer) children(e *element, st gstate) error {
	for _, c := range e.children {
		if err := r.element(c, st); err != nil {
			return err
		}
	}
	return nil
}

func (r *renderer) element(e *element, st gstate) error {
	if e.name == "defs" || e.name == "clipPath" {
		return nil // definitions are referenced, not drawn
	}
	if err := checkAttrs(e); err != nil {
		return err
	}
	if v, ok := e.attr("display"); ok && strings.TrimSpace(v) == "none" {
		return nil
	}

	st, err := applyPresentation(e, st)
	if err != nil {
		return err
	}

	transform := Identity()
	if v, ok := e.attr("transform"); ok {
		transform, err = parseTransform(v)
		if err != nil {
			return err
		}
	}
	clipRef, hasClip := e.attr("clip-path")

	needsWrap := !transform.IsIdentity() || hasClip
	if needsWrap {
		r.w.save()
		if !transform.IsIdentity() {
			r.w.concat(transform)
		}
		if hasClip {
			if err := r.applyClip(clipRef); err != nil {
				return err
			}
		}
	}

	switch e.name {
	case "g":
		err = r.children(e, st)
	case "rect":
		err = r.drawRect(e, st)
	case "path":
		err = r.drawPath(e, st)
	case "line":
		err = r.drawLine(e, st)
	case "text":
		err = r.drawText(e, st)
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

// applyPresentation folds an element's presentation attributes into the
// inherited state.
func applyPresentation(e *element, st gstate) (gstate, error) {
	var err error
	if v, ok := e.attr("fill"); ok {
		if st.fill, err = parsePaint(v); err != nil {
			return st, err
		}
	}
	if v, ok := e.attr("stroke"); ok {
		if st.stroke, err = parsePaint(v); err != nil {
			return st, err
		}
	}
	if v, ok := e.attr("stroke-width"); ok {
		if st.strokeWidth, err = parseLength(v); err != nil {
			return st, fmt.Errorf("svgpdf: stroke-width: %w", err)
		}
	}
	if v, ok := e.attr("stroke-miterlimit"); ok {
		if st.miterLimit, err = parseLength(v); err != nil {
			return st, fmt.Errorf("svgpdf: stroke-miterlimit: %w", err)
		}
	}
	if v, ok := e.attr("stroke-linecap"); ok {
		switch strings.TrimSpace(v) {
		case "butt":
			st.lineCap = 0
		case "round":
			st.lineCap = 1
		case "square":
			st.lineCap = 2
		default:
			return st, fmt.Errorf("svgpdf: unsupported stroke-linecap %q", v)
		}
	}
	if v, ok := e.attr("stroke-linejoin"); ok {
		switch strings.TrimSpace(v) {
		case "miter":
			st.lineJoin = 0
		case "round":
			st.lineJoin = 1
		case "bevel":
			st.lineJoin = 2
		default:
			return st, fmt.Errorf("svgpdf: unsupported stroke-linejoin %q", v)
		}
	}
	if v, ok := e.attr("stroke-dasharray"); ok {
		v = strings.TrimSpace(v)
		if v == "none" {
			st.dashPattern = nil
		} else {
			if st.dashPattern, err = parseNumberList(v); err != nil {
				return st, fmt.Errorf("svgpdf: stroke-dasharray: %w", err)
			}
		}
	}
	if v, ok := e.attr("stroke-dashoffset"); ok {
		if st.dashOffset, err = parseLength(v); err != nil {
			return st, fmt.Errorf("svgpdf: stroke-dashoffset: %w", err)
		}
	}
	if v, ok := e.attr("opacity"); ok {
		o, err := parseOpacity(v)
		if err != nil {
			return st, err
		}
		st.opacity *= o // multiplies down the tree, see gstate doc
	}
	if v, ok := e.attr("fill-opacity"); ok {
		if st.fillOpacity, err = parseOpacity(v); err != nil {
			return st, err
		}
	}
	if v, ok := e.attr("stroke-opacity"); ok {
		if st.strokeOpacity, err = parseOpacity(v); err != nil {
			return st, err
		}
	}
	if v, ok := e.attr("fill-rule"); ok {
		switch strings.TrimSpace(v) {
		case "nonzero":
			st.evenOdd = false
		case "evenodd":
			st.evenOdd = true
		default:
			return st, fmt.Errorf("svgpdf: unsupported fill-rule %q", v)
		}
	}
	if v, ok := e.attr("font-family"); ok {
		st.fontFamily = v
	}
	if v, ok := e.attr("font-size"); ok {
		if st.fontSize, err = parseLength(v); err != nil {
			return st, fmt.Errorf("svgpdf: font-size: %w", err)
		}
	}
	if v, ok := e.attr("font-weight"); ok {
		st.fontWeight = strings.TrimSpace(v)
	}
	if v, ok := e.attr("font-style"); ok {
		st.fontStyle = strings.TrimSpace(v)
	}
	if v, ok := e.attr("text-anchor"); ok {
		switch strings.TrimSpace(v) {
		case "start", "middle", "end":
			st.textAnchor = strings.TrimSpace(v)
		default:
			return st, fmt.Errorf("svgpdf: unsupported text-anchor %q", v)
		}
	}
	return st, nil
}

// applyClip resolves a clip-path reference and intersects the current clip
// region with the referenced geometry.
func (r *renderer) applyClip(ref string) error {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "url(#") || !strings.HasSuffix(ref, ")") {
		return fmt.Errorf("svgpdf: unsupported clip-path value %q", ref)
	}
	id := ref[len("url(#") : len(ref)-1]
	clip, ok := r.clips[id]
	if !ok {
		return fmt.Errorf("svgpdf: clip-path references unknown id %q", id)
	}
	for _, c := range clip.children {
		if _, hasT := c.attr("transform"); hasT {
			return fmt.Errorf("svgpdf: transform on <clipPath> children is not supported")
		}
		switch c.name {
		case "rect":
			x, y, w, h, err := rectGeometry(c)
			if err != nil {
				return err
			}
			r.w.rect(x, y, w, h)
		case "path":
			segs, err := parsePathData(c.attrs["d"])
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
	fillAlpha := st.opacity * st.fillOpacity
	strokeAlpha := st.opacity * st.strokeOpacity
	// setAlpha reconciles against the current alpha and resets to opaque when
	// needed, so a translucent leaf cannot leak its alpha forward.
	r.w.setAlpha(fillAlpha, strokeAlpha)
	return fill, stroke
}

func rectGeometry(e *element) (x, y, w, h float64, err error) {
	get := func(name string) (float64, error) {
		v, ok := e.attr(name)
		if !ok {
			return 0, nil
		}
		return parseLength(v)
	}
	if x, err = get("x"); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("svgpdf: <rect> x: %w", err)
	}
	if y, err = get("y"); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("svgpdf: <rect> y: %w", err)
	}
	if w, err = get("width"); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("svgpdf: <rect> width: %w", err)
	}
	if h, err = get("height"); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("svgpdf: <rect> height: %w", err)
	}
	return x, y, w, h, nil
}

func (r *renderer) drawRect(e *element, st gstate) error {
	x, y, w, h, err := rectGeometry(e)
	if err != nil {
		return err
	}
	if w <= 0 || h <= 0 {
		return nil // zero-area rects render nothing per the SVG spec
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
	d := e.attrs["d"]
	if strings.TrimSpace(d) == "" {
		return nil // Vega emits empty d for placeholder foreground paths
	}
	segs, err := parsePathData(d)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return nil
	}
	fill, stroke := r.setPaintState(st)
	if !fill && !stroke {
		return nil
	}
	r.w.pathSegs(segs)
	r.w.paint(fill, stroke, st.evenOdd)
	return nil
}

func (r *renderer) drawLine(e *element, st gstate) error {
	get := func(name string) (float64, error) {
		v, ok := e.attr(name)
		if !ok {
			return 0, nil
		}
		return parseLength(v)
	}
	x1, err := get("x1")
	if err != nil {
		return fmt.Errorf("svgpdf: <line> x1: %w", err)
	}
	y1, err := get("y1")
	if err != nil {
		return fmt.Errorf("svgpdf: <line> y1: %w", err)
	}
	x2, err := get("x2")
	if err != nil {
		return fmt.Errorf("svgpdf: <line> x2: %w", err)
	}
	y2, err := get("y2")
	if err != nil {
		return fmt.Errorf("svgpdf: <line> y2: %w", err)
	}
	// Lines are stroke-only geometry.
	if st.stroke.None {
		return nil
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

// parseLength parses a numeric attribute, accepting a px/pt suffix
// (treated as user units, matching the 1px = 1pt output mapping).
func parseLength(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "px")
	s = strings.TrimSuffix(s, "pt")
	if s == "" {
		return 0, fmt.Errorf("empty length")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid length %q", s)
	}
	return v, nil
}

func parseOpacity(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("svgpdf: invalid opacity %q", s)
	}
	return clamp01(v), nil
}

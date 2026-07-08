package svgpdf

import (
	"fmt"
	"strings"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/mgilbir/aster/internal/textmeasure"
)

// TextShaper shapes a text string with a CSS font specification into
// positioned glyph runs. *textmeasure.Measurer implements it.
type TextShaper interface {
	ShapeText(text, cssFont string) ([]textmeasure.ShapedRun, float64)
}

// drawText converts a <text> element to filled glyph outlines. No fonts are
// embedded in the PDF: each glyph becomes a path, which keeps the embedder
// trivial and the output self-contained at the cost of unselectable text.
//
// Vega positions text via its transform attribute: the local origin (0, 0)
// is the alphabetic-baseline anchor point (baseline offsets are baked into
// the translate by Vega's SVG renderer), so glyphs are drawn along y = 0.
func (r *renderer) drawText(e *element, st gstate) error {
	text := e.text
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if len(e.children) > 0 {
		return fmt.Errorf("svgpdf: <text> with child elements (e.g. <tspan>) is not supported")
	}
	if r.shaper == nil {
		return fmt.Errorf("svgpdf: text rendering requires a shaper (text measurer)")
	}
	// Text is painted with the fill color only; Vega does not stroke text.
	if st.fill.None {
		return nil
	}

	runs, advance := r.shaper.ShapeText(text, cssFontString(st))
	if len(runs) == 0 {
		return nil
	}

	// text-anchor shifts the whole string relative to the origin using the
	// shaped advance width.
	var penX float64
	switch st.textAnchor {
	case "middle":
		penX = -advance / 2
	case "end":
		penX = -advance
	}

	r.w.fillColor(st.fill.Color)
	// Reconcile the alpha (text is fill-only, so fill and stroke alpha match).
	// setAlpha resets to opaque when a previous sibling left the stream
	// translucent, which the old conditional "if fillAlpha != 1" could not do.
	fillAlpha := st.opacity * st.fillOpacity
	r.w.setAlpha(fillAlpha, fillAlpha)

	emitted := false
	for _, run := range runs {
		upem := float64(run.Face.Upem())
		// Font units → local (px) units at the shaped size.
		scale := (float64(run.Size) / 64.0) / upem
		for _, g := range run.Glyphs {
			outline, ok := r.glyphOutline(run.Face, g.GlyphID)
			if !ok {
				return fmt.Errorf("svgpdf: glyph %d has non-outline data; bitmap/SVG fonts are not supported", g.GlyphID)
			}
			ox := penX + float64(g.XOffset)/64.0
			oy := -float64(g.YOffset) / 64.0
			if emitGlyphOutline(r.w, outline, ox, oy, scale) {
				emitted = true
			}
			penX += float64(g.Advance) / 64.0
		}
	}
	if emitted {
		// Glyph contours use the nonzero winding rule (TrueType/CFF
		// convention: counters wind opposite to outer contours).
		r.w.paint(true, false, false)
	}
	return nil
}

// glyphKey identifies a glyph outline by its font face and glyph id, for the
// per-render memoization cache.
type glyphKey struct {
	face *font.Face
	gid  font.GID
}

// glyphOutline returns the outline for a glyph, extracting it via GlyphData on
// first use and caching it for the rest of the render. It reports false when
// the glyph carries non-outline data (bitmap/SVG/color fonts).
func (r *renderer) glyphOutline(face *font.Face, gid font.GID) (font.GlyphOutline, bool) {
	key := glyphKey{face: face, gid: gid}
	if outline, ok := r.glyphs[key]; ok {
		return outline, true
	}
	outline, ok := face.GlyphData(gid).(font.GlyphOutline)
	if !ok {
		return font.GlyphOutline{}, false
	}
	if r.glyphs == nil {
		r.glyphs = make(map[glyphKey]font.GlyphOutline)
	}
	r.glyphs[key] = outline
	return outline, true
}

// emitGlyphOutline writes one glyph's outline as path operators and reports
// whether anything was emitted (whitespace glyphs have empty outlines).
//
// Outline coordinates are font units with y pointing up; the content stream
// is under the global y-flip, where SVG/local y points down. Negating y here
// (oy - fontY*scale) pre-flips the glyph so it comes out upright.
func emitGlyphOutline(w *contentWriter, outline font.GlyphOutline, ox, oy, scale float64) bool {
	if len(outline.Segments) == 0 {
		return false
	}
	pt := func(p opentype.SegmentPoint) Point {
		return Point{X: ox + float64(p.X)*scale, Y: oy - float64(p.Y)*scale}
	}
	var cur Point
	for _, seg := range outline.Segments {
		switch seg.Op {
		case opentype.SegmentOpMoveTo:
			cur = pt(seg.Args[0])
			w.moveTo(cur)
		case opentype.SegmentOpLineTo:
			cur = pt(seg.Args[0])
			w.lineTo(cur)
		case opentype.SegmentOpQuadTo:
			// PDF has no quadratic operator; elevate to the exact cubic.
			q, end := pt(seg.Args[0]), pt(seg.Args[1])
			c1, c2 := quadToCubic(cur, q, end)
			w.cubicTo(c1, c2, end)
			cur = end
		case opentype.SegmentOpCubeTo:
			c1, c2, end := pt(seg.Args[0]), pt(seg.Args[1]), pt(seg.Args[2])
			w.cubicTo(c1, c2, end)
			cur = end
		}
	}
	return true
}

// cssFontString rebuilds the CSS font shorthand that textmeasure parses,
// from the inherited font state: "[style] [weight] <size>px <family>".
func cssFontString(st gstate) string {
	var b strings.Builder
	if st.fontStyle == "italic" || st.fontStyle == "oblique" {
		b.WriteString(st.fontStyle)
		b.WriteByte(' ')
	}
	if st.fontWeight != "" && st.fontWeight != "normal" {
		b.WriteString(st.fontWeight)
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "%gpx ", st.fontSize)
	b.WriteString(st.fontFamily)
	return b.String()
}

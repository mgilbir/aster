package svgpdf

import (
	"fmt"
	"math"
	"strings"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/mgilbir/aster/internal/textmeasure"
)

// TextShaper shapes a text string with a CSS font specification into
// positioned glyph runs, and can recover the raw font bytes behind a shaped
// face (nil when unavailable). *textmeasure.Measurer implements it.
type TextShaper interface {
	ShapeText(text, cssFont string) ([]textmeasure.ShapedRun, float64)
	FontData(face *font.Face) []byte
}

// drawText renders a <text> element. Depending on the text mode, glyphs are
// emitted as real PDF text referencing a (subset) font resource, or as filled
// path outlines. Runs whose face cannot back a PDF font (unrecoverable bytes,
// CFF outlines under TextEmbed) fall back to outlines individually, so mixed
// content still renders.
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
	fillAlpha := st.opacity * st.fillOpacity
	r.w.setAlpha(fillAlpha, fillAlpha)

	runes := []rune(text)
	for _, run := range runs {
		var f *pdfFont
		if r.fonts != nil {
			f = r.fonts.fontFor(run.Face)
		}
		var err error
		if f != nil {
			penX, err = r.drawTextRunFont(f, run, penX, runes)
		} else {
			penX, err = r.drawTextRunOutline(run, penX)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// drawTextRunFont emits one shaped run as a PDF text object: a TJ array of
// glyph IDs with pen adjustments wherever the shaped position differs from
// the font's natural advance (kerning, mark positioning).
//
// The content stream operates under the global y-flip, so the text matrix
// negates y again (D = -1) to keep glyphs upright; text-space x then
// coincides with local x, letting shaped advances map 1:1.
func (r *renderer) drawTextRunFont(f *pdfFont, run textmeasure.ShapedRun, penX float64, runes []rune) (float64, error) {
	size := float64(run.Size) / 64.0
	if size <= 0 {
		return penX, fmt.Errorf("svgpdf: non-positive font size in shaped run")
	}
	upem := float64(f.parsed.UnitsPerEm())
	penXStart := penX

	r.w.beginText()
	r.w.setTextFont(f.res, size)
	r.w.textMatrix(Matrix{A: 1, B: 0, C: 0, D: -1, E: penXStart, F: 0})

	var items []tjItem
	var cur []uint16
	flush := func() {
		if len(cur) > 0 {
			items = append(items, tjItem{glyphs: cur})
			cur = nil
		}
	}
	show := func() {
		flush()
		if len(items) > 0 {
			r.w.showGlyphs(items)
			items = nil
		}
	}

	penText := 0.0 // viewer pen position in text space (== local px)
	rise := 0.0
	for i, g := range run.Glyphs {
		gid := uint16(g.GlyphID)
		f.used[gid] = true
		r.recordToUnicode(f, run, i, runes)

		// Vertical offset (mark positioning): PDF text rise. Shaping y is
		// up; under the doubly-flipped text matrix a positive rise moves the
		// glyph up as well. Rise changes force a TJ break.
		wantRise := float64(g.YOffset) / 64.0
		if wantRise != rise {
			show()
			r.w.textRise(wantRise)
			rise = wantRise
		}

		// Horizontal correction: where the shaped glyph should draw versus
		// where the viewer pen sits after the previous glyph's font advance.
		relX := (penX - penXStart) + float64(g.XOffset)/64.0
		if num := (penText - relX) * 1000 / size; math.Abs(num) >= 0.005 {
			flush()
			items = append(items, tjItem{adj: num, isAdj: true})
			penText -= num * size / 1000
		}

		cur = append(cur, gid)
		penText += float64(f.parsed.Advance(gid)) / upem * size
		penX += float64(g.Advance) / 64.0
	}
	show()
	if rise != 0 {
		r.w.textRise(0)
	}
	r.w.endText()
	return penX, nil
}

// recordToUnicode maps a glyph to the source text of its cluster, for the
// font's ToUnicode CMap (text extraction). The first mapping wins.
func (r *renderer) recordToUnicode(f *pdfFont, run textmeasure.ShapedRun, i int, runes []rune) {
	gid := uint16(run.Glyphs[i].GlyphID)
	if _, ok := f.toUni[gid]; ok {
		return
	}
	start := run.Glyphs[i].TextIndex()
	if start < 0 || start >= len(runes) {
		return
	}
	end := len(runes)
	for _, g := range run.Glyphs[i+1:] {
		if g.TextIndex() != start {
			end = g.TextIndex()
			break
		}
	}
	if end <= start {
		return
	}
	f.toUni[gid] = string(runes[start:end])
}

// drawTextRunOutline emits one shaped run as filled glyph outlines (the
// font-free representation).
func (r *renderer) drawTextRunOutline(run textmeasure.ShapedRun, penX float64) (float64, error) {
	upem := float64(run.Face.Upem())
	// Font units → local (px) units at the shaped size.
	scale := (float64(run.Size) / 64.0) / upem
	emitted := false
	for _, g := range run.Glyphs {
		outline, ok := r.glyphOutline(run.Face, g.GlyphID)
		if !ok {
			return penX, fmt.Errorf("svgpdf: glyph %d has non-outline data; bitmap/SVG fonts are not supported", g.GlyphID)
		}
		ox := penX + float64(g.XOffset)/64.0
		oy := -float64(g.YOffset) / 64.0
		if emitGlyphOutline(r.w, outline, ox, oy, scale) {
			emitted = true
		}
		penX += float64(g.Advance) / 64.0
	}
	if emitted {
		// Glyph contours use the nonzero winding rule (TrueType/CFF
		// convention: counters wind opposite to outer contours).
		r.w.paint(true, false, false)
	}
	return penX, nil
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

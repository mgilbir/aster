package svgpdf

import (
	"fmt"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/text"
)

// TextShaper shapes a text string with a CSS font specification into
// positioned glyph runs, and can recover the raw font bytes behind a shaped
// face (nil when unavailable). *text.Measurer implements it.
type TextShaper interface {
	ShapeText(s, cssFont string) ([]text.Run, float64)
	FontData(face *text.Face) []byte
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
//
// Multi-line text is one <tspan> a line: the first at the origin, each next
// with x="0" and a dy of the line height. A tspan's x and y set the position,
// its dx and dy move it, and its text is anchored on its own (each x starts a
// new chunk); a tspan without x continues where the text before it ended.
func (r *renderer) drawText(e *element, st gstate) error {
	if len(e.children) == 0 {
		_, err := r.drawTextLine(e.text, st, 0, 0)
		return err
	}
	if strings.TrimSpace(e.text) != "" {
		return fmt.Errorf("svgpdf: <text> mixing its own text with <tspan> is not supported")
	}
	var x, y float64
	for _, c := range e.children {
		if c.name != "tspan" || len(c.children) > 0 {
			return fmt.Errorf("svgpdf: <text> may hold only <tspan> elements of text, got <%s>", c.name)
		}
		if err := checkAttrs(c); err != nil {
			return err
		}
		cst, err := applyPresentation(c, st)
		if err != nil {
			return err
		}
		for _, a := range []struct {
			name string
			set  func(float64)
		}{
			{"x", func(v float64) { x = v }}, {"y", func(v float64) { y = v }},
			{"dx", func(v float64) { x += v }}, {"dy", func(v float64) { y += v }},
		} {
			if v, ok := c.attr(a.name); ok {
				f, err := parseLength(v)
				if err != nil {
					return fmt.Errorf("svgpdf: <tspan> %s: %w", a.name, err)
				}
				a.set(f)
			}
		}
		end, err := r.drawTextLine(c.text, cst, x, y)
		if err != nil {
			return err
		}
		x = end
	}
	return nil
}

// drawTextLine draws one line of text with its anchor at (x, y), and returns
// the x where the text after it begins.
func (r *renderer) drawTextLine(str string, st gstate, x, y float64) (float64, error) {
	if strings.TrimSpace(str) == "" {
		return x, nil
	}
	if r.shaper == nil {
		return x, fmt.Errorf("svgpdf: text rendering requires a shaper (text measurer)")
	}
	// Text is painted with the fill color only; Vega does not stroke text.
	if st.fill.None {
		return x, nil
	}

	if r.textTotal += len(str); r.textTotal > r.lim.MaxTextBytes {
		return x, limitErr("text content exceeds %d bytes", r.lim.MaxTextBytes)
	}
	if err := ctxErr(r.ctx); err != nil {
		return x, err
	}

	runs, advance := r.shaper.ShapeText(str, r.cssFont(st))
	if len(runs) == 0 {
		return x, nil
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
	end := x + penX + advance
	if x != 0 || y != 0 {
		r.w.save()
		r.w.concat(Matrix{A: 1, D: 1, E: x, F: y})
		defer r.w.restore()
	}

	r.w.fillColor(st.fill.Color)
	// Reconcile the alpha (text is fill-only, so fill and stroke alpha match).
	fillAlpha := st.opacity * st.fillOpacity * st.fill.alpha()
	r.w.setAlpha(fillAlpha, fillAlpha)

	for k, run := range runs {
		var f *pdfFont
		if r.fonts != nil {
			f = r.fonts.fontFor(run.Face)
		}
		var err error
		if f != nil {
			// The source text a run's last cluster covers ends where the
			// next run's text begins (or at the end of the string).
			end := len(str)
			if k+1 < len(runs) && len(runs[k+1].Glyphs) > 0 {
				end = runs[k+1].Glyphs[0].Cluster
			}
			penX, err = r.drawTextRunFont(f, run, penX, str, end)
		} else {
			penX, err = r.drawTextRunOutline(run, penX)
		}
		if err != nil {
			return x, err
		}
	}
	return end, nil
}

// drawTextRunFont emits one shaped run as a PDF text object: a TJ array of
// glyph IDs with pen adjustments wherever the shaped position differs from
// the font's natural advance (kerning, mark positioning).
//
// The content stream operates under the global y-flip, so the text matrix
// negates y again (D = -1) to keep glyphs upright; text-space x then
// coincides with local x, letting shaped advances map 1:1.
func (r *renderer) drawTextRunFont(f *pdfFont, run text.Run, penX float64, str string, textEnd int) (float64, error) {
	size := run.Size
	if size <= 0 {
		return penX, fmt.Errorf("svgpdf: non-positive font size in shaped run")
	}
	upem := float64(f.parsed.UnitsPerEm())
	penXStart := penX

	r.w.beginText()
	r.w.setTextFont(f.res, size)
	r.w.textMatrix(Matrix{A: 1, B: 0, C: 0, D: -1, E: penXStart, F: 0})

	// The glyph IDs of the run go to one reused buffer; each TJ item is a
	// window onto it, and showGlyphs consumes them before the next run.
	items := r.tjBuf[:0]
	r.gidBuf = r.gidBuf[:0]
	curStart := 0
	flush := func() {
		if len(r.gidBuf) > curStart {
			items = append(items, tjItem{glyphs: r.gidBuf[curStart:len(r.gidBuf):len(r.gidBuf)]})
			curStart = len(r.gidBuf)
		}
	}
	show := func() {
		flush()
		if len(items) > 0 {
			r.w.showGlyphs(items)
			items = items[:0]
		}
	}

	penText := 0.0 // viewer pen position in text space (== local px)
	rise := 0.0
	for i, g := range run.Glyphs {
		gid := uint16(g.GID)
		f.used[gid] = true
		r.recordToUnicode(f, run, i, str, textEnd)

		// Vertical offset (mark positioning): PDF text rise. Shaping y is
		// up; under the doubly-flipped text matrix a positive rise moves the
		// glyph up as well. Rise changes force a TJ break.
		wantRise := g.YOffset
		if wantRise != rise {
			show()
			r.w.textRise(wantRise)
			rise = wantRise
		}

		// Horizontal correction: where the shaped glyph should draw versus
		// where the viewer pen sits after the previous glyph's font advance.
		relX := (penX - penXStart) + g.XOffset
		if num := (penText - relX) * 1000 / size; math.Abs(num) >= 0.005 {
			flush()
			items = append(items, tjItem{adj: num, isAdj: true})
			penText -= num * size / 1000
		}

		r.gidBuf = append(r.gidBuf, gid)
		penText += float64(f.parsed.Advance(gid)) / upem * size
		penX += g.Advance
	}
	show()
	r.tjBuf = items[:0]
	if rise != 0 {
		r.w.textRise(0)
	}
	r.w.endText()
	return penX, nil
}

// recordToUnicode maps a glyph to the source text of its cluster, for the
// font's ToUnicode CMap (text extraction). The first mapping wins.
func (r *renderer) recordToUnicode(f *pdfFont, run text.Run, i int, str string, textEnd int) {
	gid := uint16(run.Glyphs[i].GID)
	if _, ok := f.toUni[gid]; ok {
		return
	}
	start := run.Glyphs[i].Cluster // byte offset in str
	if start < 0 || start >= len(str) {
		return
	}
	end := textEnd
	for _, g := range run.Glyphs[i+1:] {
		if g.Cluster != start {
			end = g.Cluster
			break
		}
	}
	if end <= start || end > len(str) {
		return
	}
	f.toUni[gid] = str[start:end]
}

// drawTextRunOutline emits one shaped run as filled glyph outlines (the
// font-free representation).
func (r *renderer) drawTextRunOutline(run text.Run, penX float64) (float64, error) {
	emitted := false
	for _, g := range run.Glyphs {
		outline, ok := r.glyphOutline(run.Face, g.GID, run.Size)
		if !ok {
			return penX, fmt.Errorf("svgpdf: glyph %d has non-outline data; bitmap/SVG fonts are not supported", g.GID)
		}
		ox := penX + g.XOffset
		oy := -g.YOffset
		if emitGlyphOutline(r.w, outline, ox, oy) {
			emitted = true
		}
		penX += g.Advance
	}
	if emitted {
		// Glyph contours use the nonzero winding rule (TrueType/CFF
		// convention: counters wind opposite to outer contours).
		r.w.paint(true, false, false)
	}
	return penX, nil
}

// glyphKey identifies a scaled glyph outline by its font face, glyph id and
// size, for the per-render memoization cache.
type glyphKey struct {
	face *text.Face
	gid  int
	size float64
}

// glyphOutline returns the outline of a glyph scaled to size (y down, origin
// on the baseline), extracting it on first use and caching it for the rest
// of the render. It reports false when the glyph has no vector outline
// (bitmap/colour fonts).
func (r *renderer) glyphOutline(face *text.Face, gid int, size float64) ([]text.Segment, bool) {
	key := glyphKey{face: face, gid: gid, size: size}
	if outline, ok := r.glyphs[key]; ok {
		return outline, true
	}
	outline, err := text.GlyphOutline(face, gid, size)
	if err != nil {
		return nil, false
	}
	if r.glyphs == nil {
		r.glyphs = make(map[glyphKey][]text.Segment)
	}
	r.glyphs[key] = outline
	return outline, true
}

// emitGlyphOutline writes one glyph's outline as path operators and reports
// whether anything was emitted (whitespace glyphs have empty outlines).
//
// The outline is already scaled to the font size with y pointing down, the
// orientation of the content stream's local space under the global y-flip,
// so the glyph only needs translating to its pen position (ox, oy).
func emitGlyphOutline(w *contentWriter, outline []text.Segment, ox, oy float64) bool {
	if len(outline) == 0 {
		return false
	}
	pt := func(p text.Point) Point { return Point{X: ox + p.X, Y: oy + p.Y} }
	var cur Point
	for _, seg := range outline {
		switch seg.Kind {
		case text.MoveTo:
			cur = pt(seg.P[0])
			w.moveTo(cur)
		case text.LineTo:
			cur = pt(seg.P[0])
			w.lineTo(cur)
		case text.QuadTo:
			// PDF has no quadratic operator; elevate to the exact cubic.
			q, end := pt(seg.P[0]), pt(seg.P[1])
			c1, c2 := quadToCubic(cur, q, end)
			w.cubicTo(c1, c2, end)
			cur = end
		case text.CubicTo:
			c1, c2, end := pt(seg.P[0]), pt(seg.P[1]), pt(seg.P[2])
			w.cubicTo(c1, c2, end)
			cur = end
		case text.Close:
			// Filling closes open subpaths implicitly.
		}
	}
	return true
}

// cssFont is cssFontString with the last result remembered: a chart's labels
// share a handful of fonts, and formatting the size dominates a short label.
func (r *renderer) cssFont(st gstate) string {
	key := fontKey{st.fontStyle, st.fontWeight, st.fontSize, st.fontFamily}
	if r.cssFontKey != key || r.cssFontStr == "" {
		r.cssFontKey, r.cssFontStr = key, cssFontString(st)
	}
	return r.cssFontStr
}

// fontKey is the part of the graphics state cssFontString reads.
type fontKey struct {
	style, weight string
	size          float64
	family        string
}

// cssFontString rebuilds the CSS font shorthand that the text package parses,
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

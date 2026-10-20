package svgpdf

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"unsafe"

	"github.com/mgilbir/aster/internal/text"
)

// TextShaper shapes text in a font given by its parts into positioned glyph
// runs, and recovers the font program behind a shaped face for embedding
// (nil when there is none to embed). *text.Measurer implements it.
type TextShaper interface {
	ShapeFont(s string, font text.CSSFont) ([]text.Run, float64)
	FontData(ctx context.Context, face *text.Face) ([]byte, error)
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
//
// Its text is drawn after SVG's whitespace processing (xml:space default,
// as the PNG writer does it): newlines are dropped, a tab is a space, spaces
// collapse to one across the element, and the element's leading and trailing
// space go (C59). No control character reaches the shaper, to be drawn as a
// .notdef box.
func (r *renderer) drawText(e *element, st gstate) error {
	// The <text>'s own position, where its first line starts.
	var x, y float64
	if err := textPosition(e, &st, &x, &y); err != nil {
		return err
	}
	if len(e.children) == 0 {
		str, _ := collapseSpace(e.text, true)
		_, err := r.drawTextLine(strings.TrimSuffix(str, " "), st, x, y)
		return err
	}
	if strings.TrimSpace(e.text) != "" {
		return fmt.Errorf("svgpdf: <text> mixing its own text with <tspan> is not supported")
	}
	// The tspans' text after whitespace processing, which runs across them,
	// then each drawn where its position says.
	type line struct {
		e   *element
		st  gstate
		str string
	}
	lines := make([]line, 0, len(e.children))
	prevSpace, last := true, -1
	for _, c := range e.children {
		if c.name != "tspan" || len(c.children) > 0 {
			return fmt.Errorf("svgpdf: <text> may hold only <tspan> elements of text, got <%s>", c.name)
		}
		cst, ep, err := r.applyPresentation(c, st)
		if err != nil {
			return err
		}
		if ep.hidden {
			continue
		}
		var str string
		str, prevSpace = collapseSpace(c.text, prevSpace)
		if str != "" {
			last = len(lines)
		}
		lines = append(lines, line{c, cst, str})
	}
	if last >= 0 {
		lines[last].str = strings.TrimSuffix(lines[last].str, " ")
	}
	for _, l := range lines {
		if err := textPosition(l.e, &l.st, &x, &y); err != nil {
			return err
		}
		end, err := r.drawTextLine(l.str, l.st, x, y)
		if err != nil {
			return err
		}
		x = end
	}
	return nil
}

// textPosition applies the x, y, dx and dy of a <text> or <tspan> to the
// current text position: x and y set it, dx and dy move it. A value that does
// not parse is ignored; a list of them, a position per character, is not
// supported.
func textPosition(e *element, st *gstate, x, y *float64) error {
	if len(e.attrs) == 0 {
		return nil
	}
	for i, name := range [4]string{"x", "y", "dx", "dy"} {
		v, ok := e.attr(name)
		if !ok {
			continue
		}
		c := st.lenX()
		if i%2 == 1 {
			c = st.lenY()
		}
		f, ok := parseLength(v, c)
		if !ok {
			if _, ok := parseNumberList(v); ok {
				return fmt.Errorf("svgpdf: <%s> %s: a position per character is not supported", e.name, name)
			}
			continue // a value that does not parse is ignored
		}
		switch i {
		case 0:
			*x = f
		case 1:
			*y = f
		case 2:
			*x += f
		case 3:
			*y += f
		}
	}
	return nil
}

// collapseSpace applies SVG's default whitespace rule to s: newlines are
// dropped, a tab is a space, and a run of spaces is one, none at all after
// a space before s (prevSpace). It reports whether s ends with a space, for
// the text after it. s is returned as it is when there is nothing to do.
func collapseSpace(s string, prevSpace bool) (string, bool) {
	clean := true
	ps := prevSpace
	for i := 0; i < len(s) && clean; i++ {
		switch c := s[i]; c {
		case '\n', '\r', '\t':
			clean = false
		case ' ':
			clean = !ps
			ps = true
		default:
			ps = false
		}
	}
	if clean {
		return s, ps
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\n', '\r':
			continue
		case '\t':
			c = ' '
		}
		if c == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		b = append(b, c)
	}
	return string(b), prevSpace
}

// font is the font of the state, as its parts.
func (st *gstate) font() text.CSSFont {
	return text.CSSFont{Italic: st.italic, Weight: st.fontWeight, Size: st.fontSize, Family: st.fontFamily}
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
	// Text is filled, then stroked, as vega-scenegraph's text mark does
	// (fillText, then strokeText) and a browser draws SVG text (C42).
	fill := !st.fill.None
	stroke := !st.stroke.None && st.strokeWidth > 0
	if !fill && !stroke {
		return x, nil
	}
	if fill && st.fill.Gradient != "" || stroke && st.stroke.Gradient != "" {
		return x, fmt.Errorf("svgpdf: a gradient paint on text is not supported")
	}

	if r.textTotal += len(str); r.textTotal > r.lim.MaxTextBytes {
		return x, limitErr("%s: text content exceeds %d bytes", r.lim.Input, r.lim.MaxTextBytes)
	}
	if err := ctxErr(r.ctx); err != nil {
		return x, err
	}

	// The font as the element states it, never through a font string to be
	// read back (C12). A size of zero draws nothing.
	runs, advance := r.shaper.ShapeFont(str, st.font())
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

	// A link around the text covers its em boxes: from an ascent of the
	// size above the baseline to a quarter of it below. What the glyphs
	// draw inside does not grow it (a colour glyph's clips reach far).
	if len(r.w.links) > 0 {
		r.w.markBox(penX, -st.fontSize, penX+advance, st.fontSize/4)
		links := r.w.links
		r.w.links = nil
		defer func() { r.w.links = links }()
	}

	fillAlpha := st.opacity * st.fillOpacity * st.fill.alpha()
	strokeAlpha := st.opacity * st.strokeOpacity * st.stroke.alpha()
	if !stroke {
		strokeAlpha = fillAlpha
	} else if !fill {
		fillAlpha = strokeAlpha
	}
	if fill {
		r.w.fillColor(st.fill.Color)
	}
	if stroke {
		r.setStroke(st)
	}
	r.w.setAlpha(fillAlpha, strokeAlpha)

	// A run's text is written once, for extraction: as the text the fill
	// draws, or, with no fill, as the text the stroke draws (text
	// rendering mode 1) where the run is in a font, and invisible (3)
	// around its stroked outlines where it is not.
	start := penX
	for _, run := range runs {
		var f *pdfFont
		if r.fonts != nil {
			var err error
			if f, err = r.fonts.fontFor(run.Face); err != nil {
				return x, err
			}
			f = r.fonts.forRun(f, run)
		}
		var err error
		switch {
		case f != nil && f.outlined:
			penX, err = r.drawTextRunHidden(f, run, penX, str, st, fill)
		case !fill:
			penX, err = r.strokeTextRun(f, run, penX, str)
		case hasColour(run):
			penX, err = r.drawTextRunColour(run, f, penX, str, st)
		case f != nil:
			penX, err = r.drawTextRunFont(f, run, penX, str)
		default:
			penX, err = r.drawTextRunUnwritten(run, penX, str, true, false)
		}
		if err != nil {
			return x, err
		}
	}
	if fill && stroke {
		// The stroke over every fill of the line, as one strokeText over
		// one fillText: as outlines, the text being written already.
		penX = start
		for _, run := range runs {
			penX, _ = r.drawTextRunOutline(run, penX, false, true)
		}
	}
	return end, nil
}

// setStroke writes the stroke state of a stroked shape: its colour, width,
// joins, caps and dashes, each only where it differs from the stream's.
func (r *renderer) setStroke(st gstate) {
	r.w.strokeColor(st.stroke.Color)
	r.w.lineWidth(st.strokeWidth)
	r.w.setMiterLimit(st.miterLimit)
	r.w.setLineCap(st.lineCap)
	r.w.setLineJoin(st.lineJoin)
	r.w.setDash(st.dashPattern, st.dashOffset)
}

// strokeTextRun draws a run of text that is stroked and not filled: in its
// font, in text rendering mode 1, which strokes the glyphs; otherwise as
// stroked outlines, and the run as invisible text in its font where it has
// one (a colour run's), so that it is found and copied as text. Colour
// glyphs are not stroked: their outline is not what they draw.
func (r *renderer) strokeTextRun(f *pdfFont, run text.Run, penX float64, str string) (float64, error) {
	if f == nil {
		return r.drawTextRunUnwritten(run, penX, str, false, true)
	}
	if !hasColour(run) {
		r.w.textRender(1)
		end, err := r.drawTextRunFont(f, run, penX, str)
		r.w.textRender(0)
		return end, err
	}
	return r.drawTextRunHidden(f, run, penX, str, gstate{}, false)
}

// drawTextRunHidden draws a run as outlines, filled or (with fill false)
// stroked, and writes it as invisible text (rendering mode 3) in font f,
// so that it is found and copied as text: a run f cannot draw (see
// fontFor), or a colour run's text. A colour run, filled, is drawn with its
// colour glyphs, which writes the invisible text itself.
func (r *renderer) drawTextRunHidden(f *pdfFont, run text.Run, penX float64, str string, st gstate, fill bool) (float64, error) {
	if fill && hasColour(run) {
		return r.drawTextRunColour(run, f, penX, str, st)
	}
	r.w.textRender(3)
	_, err := r.drawTextRunFont(f, run, penX, str)
	r.w.textRender(0)
	if err != nil {
		return penX, err
	}
	return r.drawTextRunOutline(run, penX, fill, !fill)
}

// drawTextRunUnwritten draws a run in a face that has no font to write it in
// (no font program can be had of it, or it does not parse) as outlines. In
// the modes that write text, its source text is the ActualText of what it
// draws, as a colour run's is when it has no font (C43's class): a reader
// that reads ActualText around paths finds it; poppler and PDFium do not.
func (r *renderer) drawTextRunUnwritten(run text.Run, penX float64, str string, fill, stroke bool) (float64, error) {
	actual := r.fonts != nil && run.Start >= 0 && run.Start < run.End && run.End <= len(str)
	if actual {
		r.w.beginActualText(str[run.Start:run.End])
	}
	end, err := r.drawTextRunOutline(run, penX, fill, stroke)
	if actual {
		r.w.endMarkedContent()
	}
	return end, err
}

// drawTextRunFont emits one shaped run as a PDF text object: a TJ array of
// glyph IDs with pen adjustments wherever the shaped position differs from
// the font's natural advance (kerning, mark positioning).
//
// The content stream operates under the global y-flip, so the text matrix
// negates y again (D = -1) to keep glyphs upright; text-space x then
// coincides with local x, letting shaped advances map 1:1.
func (r *renderer) drawTextRunFont(f *pdfFont, run text.Run, penX float64, str string) (float64, error) {
	size := run.Size
	upem := float64(f.parsed.UnitsPerEm())
	penXStart := penX

	// The run's text, for extraction: through the font's ToUnicode map, or,
	// where a glyph of the run already maps to other text there, as the
	// ActualText of the run (C58).
	actual := r.mapRun(f, run, str) && run.Start >= 0 && run.Start < run.End && run.End <= len(str)
	if actual {
		r.w.beginActualText(str[run.Start:run.End])
	}

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
	for _, g := range run.Glyphs {
		gid := uint16(g.GID)
		f.used[gid] = true
		code := f.code(gid)

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

		r.gidBuf = append(r.gidBuf, code)
		penText += float64(f.parsed.Advance(gid)) / upem * size
		penX += g.Advance
	}
	show()
	r.tjBuf = items[:0]
	if rise != 0 {
		r.w.textRise(0)
	}
	r.w.endText()
	if actual {
		r.w.endMarkedContent()
	}
	return penX, nil
}

// mapRun records, for the font's ToUnicode map, the text each glyph of the
// run stands for, and reports whether a glyph of it already stands for other
// text there, which a ToUnicode map, one text per glyph, cannot say.
//
// A cluster's text is the source text from its start to the next cluster's
// start in logical order (or the run's end); it is mapped by the cluster's
// first glyph in logical order, and its other glyphs (marks, the parts of a
// decomposition) stand for nothing. Runs are in visual order: a right-to-left
// run's clusters descend.
func (r *renderer) mapRun(f *pdfFont, run text.Run, str string) (conflict bool) {
	n := len(run.Glyphs)
	if n == 0 || run.Start < 0 || run.End > len(str) || run.Start >= run.End {
		return false
	}
	starts := r.clusterBuf[:0]
	for _, g := range run.Glyphs {
		starts = append(starts, g.Cluster)
	}
	slices.Sort(starts)
	starts = slices.Compact(starts)
	r.clusterBuf = starts
	endOf := func(c int) int {
		i, _ := slices.BinarySearch(starts, c+1)
		if i < len(starts) {
			return starts[i]
		}
		return run.End
	}
	rtl := n > 1 && run.Glyphs[0].Cluster > run.Glyphs[n-1].Cluster
	prev := -1
	for k := range n {
		i := k
		if rtl {
			i = n - 1 - k
		}
		g := run.Glyphs[i]
		want := ""
		if c := g.Cluster; c != prev {
			prev = c
			if e := endOf(c); c >= run.Start && e <= run.End && c < e {
				want = str[c:e]
			}
		}
		gid := uint16(g.GID)
		if have, ok := f.toUni[gid]; ok {
			conflict = conflict || have != want
			continue
		}
		f.toUni[gid] = want
	}
	return conflict
}

// drawTextRunOutline emits one shaped run as glyph outlines (the font-free
// representation), filled or stroked. Stroked, a colour glyph is left out,
// as its outline is not what it draws.
func (r *renderer) drawTextRunOutline(run text.Run, penX float64, fill, stroke bool) (float64, error) {
	emitted := false
	for _, g := range run.Glyphs {
		if stroke && colourGlyph(run.Face, g.GID) {
			penX += g.Advance
			continue
		}
		outline, ok := r.glyphOutline(run.Face, g.GID, run.Size)
		if !ok {
			// A glyph whose outline cannot be read (malformed, or a format
			// forme does not draw) draws nothing, as in the PNG; colour
			// glyphs never come here (drawTextRunColour). This was an error
			// that blamed bitmap fonts whatever the cause (C102).
			penX += g.Advance
			continue
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
		r.w.paint(fill, stroke, false)
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
	// Kept within maxGlyphCacheBytes, each charged to the call's ledger; a
	// chart with more distinct glyphs and sizes than that has the rest made
	// again where they repeat.
	if n := int64(len(outline))*segmentBytes + glyphKeyBytes; r.glyphsBytes+n <= maxGlyphCacheBytes {
		r.alloc(n)
		r.glyphsBytes += n
		if r.glyphs == nil {
			r.glyphs = make(map[glyphKey][]text.Segment)
		}
		r.glyphs[key] = outline
	}
	return outline, true
}

// segmentBytes is what one segment of an outline holds, and glyphKeyBytes
// about what a cache entry holds besides.
const (
	segmentBytes  = int64(unsafe.Sizeof(text.Segment{}))
	glyphKeyBytes = int64(unsafe.Sizeof(glyphKey{})) + 48
)

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

package svgpdf

import (
	"fmt"
	"strconv"
	"strings"
)

// contentWriter builds a PDF content stream (plain-text graphics operators)
// and tracks the ExtGState resources the stream references.
type contentWriter struct {
	buf strings.Builder

	// ExtGState registry: opacity values are applied through /GSn gs.
	// gsNames preserves first-use order so output is deterministic.
	gsIndex map[alphaPair]string
	gsNames []gsEntry

	// cur mirrors the graphics-state parameters this writer emits
	// conditionally; stack snapshots it across q/Q. See streamState.
	cur   streamState
	stack []streamState
}

// streamState mirrors the subset of PDF graphics state that svgpdf emits
// conditionally: line cap/join, miter limit, dash pattern and the constant
// fill/stroke alpha applied via an ExtGState.
//
// Leaf elements are wrapped in q/Q only when they carry a transform or clip,
// so most leaves emit directly into the shared state. PDF graphics state
// persists until reset, so without tracking what the stream currently holds a
// dashed/translucent/round-capped leaf would silently leak that state into the
// following siblings. The reconcile setters (setLineCap, setDash, setAlpha,
// ...) emit an operator only when the desired value differs from cur, which
// both fixes the leak — including back-to-default transitions like "0 J" or
// "[] 0 d" — and drops redundant re-emission of unchanged state.
type streamState struct {
	lineCap     int
	lineJoin    int
	miterLimit  float64
	dash        []float64
	dashOffset  float64
	fillAlpha   float64
	strokeAlpha float64
}

// clone deep-copies the dash slice so a snapshot on the q/Q stack is not
// aliased by later mutations of cur.
func (s streamState) clone() streamState {
	if s.dash != nil {
		s.dash = append([]float64(nil), s.dash...)
	}
	return s
}

// defaultStreamState is PDF's initial graphics state: butt cap, miter join,
// miter limit 10, solid line, fully opaque. cur starts here so the first leaf
// reconciles against the real stream defaults.
func defaultStreamState() streamState {
	return streamState{miterLimit: 10, fillAlpha: 1, strokeAlpha: 1}
}

// alphaPair is a (fill, stroke) constant-alpha combination.
type alphaPair struct {
	fill, stroke float64
}

type gsEntry struct {
	name  string
	alpha alphaPair
}

func newContentWriter() *contentWriter {
	return &contentWriter{gsIndex: make(map[alphaPair]string), cur: defaultStreamState()}
}

// fmtNum formats a coordinate for a content stream: fixed notation (PDF has
// no exponent syntax), at most 4 decimals, trailing zeros trimmed.
func fmtNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', 4, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

func (w *contentWriter) op(operator string, args ...float64) {
	for _, a := range args {
		w.buf.WriteString(fmtNum(a))
		w.buf.WriteByte(' ')
	}
	w.buf.WriteString(operator)
	w.buf.WriteByte('\n')
}

func (w *contentWriter) save() {
	w.op("q")
	// q pushes PDF's graphics state; snapshot the cached view so the matching
	// Q can restore it exactly.
	w.stack = append(w.stack, w.cur.clone())
}

func (w *contentWriter) restore() {
	w.op("Q")
	// Q reverts every graphics-state parameter to the value it held at the
	// matching q. Restore the cached view to match so the next leaf reconciles
	// against the true post-Q stream state rather than state set inside the
	// wrapper.
	if n := len(w.stack); n > 0 {
		w.cur = w.stack[n-1]
		w.stack = w.stack[:n-1]
	}
}

func (w *contentWriter) concat(m Matrix) {
	w.op("cm", m.A, m.B, m.C, m.D, m.E, m.F)
}

func (w *contentWriter) fillColor(c Color)   { w.op("rg", c.R, c.G, c.B) }
func (w *contentWriter) strokeColor(c Color) { w.op("RG", c.R, c.G, c.B) }
func (w *contentWriter) lineWidth(v float64) { w.op("w", v) }

// setMiterLimit emits "v M" only when the miter limit differs from the value
// the stream currently holds.
func (w *contentWriter) setMiterLimit(v float64) {
	if w.cur.miterLimit == v {
		return
	}
	w.op("M", v)
	w.cur.miterLimit = v
}

// setLineCap emits "v J" only when the line cap differs from the current one
// (including resetting to the 0/butt default).
func (w *contentWriter) setLineCap(v int) {
	if w.cur.lineCap == v {
		return
	}
	w.op("J", float64(v))
	w.cur.lineCap = v
}

// setLineJoin emits "v j" only when the line join differs from the current one
// (including resetting to the 0/miter default).
func (w *contentWriter) setLineJoin(v int) {
	if w.cur.lineJoin == v {
		return
	}
	w.op("j", float64(v))
	w.cur.lineJoin = v
}

// setDash emits a dash pattern only when it differs from the current one. A
// nil/empty pattern resets to a solid line ("[] 0 d"), which is how a bare leaf
// clears a dash left set by a previous sibling.
func (w *contentWriter) setDash(pattern []float64, phase float64) {
	if equalDash(w.cur.dash, pattern) && w.cur.dashOffset == phase {
		return
	}
	w.buf.WriteByte('[')
	for i, d := range pattern {
		if i > 0 {
			w.buf.WriteByte(' ')
		}
		w.buf.WriteString(fmtNum(d))
	}
	w.buf.WriteString("] ")
	w.buf.WriteString(fmtNum(phase))
	w.buf.WriteString(" d\n")
	w.cur.dash = append([]float64(nil), pattern...)
	w.cur.dashOffset = phase
}

func equalDash(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// setAlpha reconciles the constant fill/stroke alpha, emitting "/GSn gs" only
// when it differs from the current alpha. Resetting to opaque emits the gs for
// the (1,1) pair — registered once via opacity — so a translucent leaf does not
// leak its alpha into the next sibling.
func (w *contentWriter) setAlpha(fillAlpha, strokeAlpha float64) {
	if w.cur.fillAlpha == fillAlpha && w.cur.strokeAlpha == strokeAlpha {
		return
	}
	w.opacity(fillAlpha, strokeAlpha)
	w.cur.fillAlpha = fillAlpha
	w.cur.strokeAlpha = strokeAlpha
}

// opacity applies constant alphas for fill (ca) and stroke (CA) via an
// ExtGState resource, registering the pair on first use.
func (w *contentWriter) opacity(fillAlpha, strokeAlpha float64) {
	key := alphaPair{fill: fillAlpha, stroke: strokeAlpha}
	name, ok := w.gsIndex[key]
	if !ok {
		name = fmt.Sprintf("GS%d", len(w.gsNames))
		w.gsIndex[key] = name
		w.gsNames = append(w.gsNames, gsEntry{name: name, alpha: key})
	}
	w.buf.WriteByte('/')
	w.buf.WriteString(name)
	w.buf.WriteString(" gs\n")
}

// --- text object operators ---

func (w *contentWriter) beginText() { w.op("BT") }
func (w *contentWriter) endText()   { w.op("ET") }

func (w *contentWriter) setTextFont(res string, size float64) {
	w.buf.WriteByte('/')
	w.buf.WriteString(res)
	w.buf.WriteByte(' ')
	w.buf.WriteString(fmtNum(size))
	w.buf.WriteString(" Tf\n")
}

func (w *contentWriter) textMatrix(m Matrix) {
	w.op("Tm", m.A, m.B, m.C, m.D, m.E, m.F)
}

func (w *contentWriter) textRise(v float64) { w.op("Ts", v) }

// tjItem is one element of a TJ array: either a run of glyph IDs or a pen
// adjustment (thousandths of text space, subtracted from the pen).
type tjItem struct {
	glyphs []uint16
	adj    float64
	isAdj  bool
}

// showGlyphs emits one TJ array.
func (w *contentWriter) showGlyphs(items []tjItem) {
	w.buf.WriteByte('[')
	for _, it := range items {
		if it.isAdj {
			w.buf.WriteString(fmtNum(it.adj))
			continue
		}
		w.buf.WriteByte('<')
		for _, g := range it.glyphs {
			fmt.Fprintf(&w.buf, "%04X", g)
		}
		w.buf.WriteByte('>')
	}
	w.buf.WriteString("] TJ\n")
}

func (w *contentWriter) moveTo(p Point) { w.op("m", p.X, p.Y) }
func (w *contentWriter) lineTo(p Point) { w.op("l", p.X, p.Y) }
func (w *contentWriter) closePath()     { w.op("h") }
func (w *contentWriter) rect(x, y, width, height float64) {
	w.op("re", x, y, width, height)
}

func (w *contentWriter) cubicTo(c1, c2, end Point) {
	w.op("c", c1.X, c1.Y, c2.X, c2.Y, end.X, end.Y)
}

// pathSegs emits normalized path segments as PDF path construction operators.
func (w *contentWriter) pathSegs(segs []PathSeg) {
	for _, s := range segs {
		switch s.Op {
		case OpMoveTo:
			w.moveTo(s.P3)
		case OpLineTo:
			w.lineTo(s.P3)
		case OpCubicTo:
			w.cubicTo(s.P1, s.P2, s.P3)
		case OpClose:
			w.closePath()
		}
	}
}

// paint emits the painting operator for the given fill/stroke combination.
// A path with neither is consumed with the no-op "n".
func (w *contentWriter) paint(fill, stroke, evenOdd bool) {
	switch {
	case fill && stroke:
		if evenOdd {
			w.op("B*")
		} else {
			w.op("B")
		}
	case fill:
		if evenOdd {
			w.op("f*")
		} else {
			w.op("f")
		}
	case stroke:
		w.op("S")
	default:
		w.op("n")
	}
}

// clip marks the current path as the clipping region (nonzero winding) and
// consumes it without painting.
func (w *contentWriter) clip() {
	w.op("W")
	w.op("n")
}

func (w *contentWriter) bytes() []byte {
	return []byte(w.buf.String())
}

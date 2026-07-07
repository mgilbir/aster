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
	return &contentWriter{gsIndex: make(map[alphaPair]string)}
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

func (w *contentWriter) save()    { w.op("q") }
func (w *contentWriter) restore() { w.op("Q") }

func (w *contentWriter) concat(m Matrix) {
	w.op("cm", m.A, m.B, m.C, m.D, m.E, m.F)
}

func (w *contentWriter) fillColor(c Color)   { w.op("rg", c.R, c.G, c.B) }
func (w *contentWriter) strokeColor(c Color) { w.op("RG", c.R, c.G, c.B) }
func (w *contentWriter) lineWidth(v float64) { w.op("w", v) }
func (w *contentWriter) miterLimit(v float64) {
	w.op("M", v)
}
func (w *contentWriter) lineCap(v int)  { w.op("J", float64(v)) }
func (w *contentWriter) lineJoin(v int) { w.op("j", float64(v)) }

func (w *contentWriter) dash(pattern []float64, phase float64) {
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

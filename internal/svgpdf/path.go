package svgpdf

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// PathOp identifies a normalized path segment operation.
type PathOp uint8

const (
	OpMoveTo PathOp = iota
	OpLineTo
	OpCubicTo
	OpClose
)

// PathSeg is a normalized path segment in absolute coordinates. All curve
// forms (quadratic, smooth, arcs) are lowered to cubics, so consumers only
// deal with move/line/cubic/close — a 1:1 match for the PDF m/l/c/h operators.
type PathSeg struct {
	Op PathOp
	// Points: MoveTo/LineTo use P3 as the endpoint; CubicTo uses P1, P2 as
	// control points and P3 as the endpoint; Close uses none.
	P1, P2, P3 Point
}

// Point is a 2D point.
type Point struct {
	X, Y float64
}

// parsePathData parses SVG path data (the d attribute) into normalized
// absolute segments. Supported commands: M/m L/l H/h V/v C/c S/s Q/q T/t
// A/a Z/z. Unknown commands are an error.
func parsePathData(d string) ([]PathSeg, error) {
	p := &pathParser{data: d}
	return p.parse()
}

type pathParser struct {
	data string
	pos  int

	segs []PathSeg

	cur      Point // current point
	start    Point // start of current subpath (for Z)
	lastCtrl Point // last control point (for S/T reflection)
	lastCmd  byte  // previous command letter (normalized to lowercase)
}

func (p *pathParser) parse() ([]PathSeg, error) {
	for {
		p.skipSep()
		if p.pos >= len(p.data) {
			return p.segs, nil
		}
		cmd := p.data[p.pos]
		if !isPathCmd(cmd) {
			return nil, fmt.Errorf("svgpdf: path data: expected command at %q", p.data[p.pos:min(p.pos+12, len(p.data))])
		}
		p.pos++
		if err := p.runCommand(cmd); err != nil {
			return nil, err
		}
	}
}

// runCommand consumes coordinate groups for cmd, honoring SVG's implicit
// repetition (e.g. "L 1,2 3,4" is two line segments; repeated "M" pairs
// become implicit "L").
func (p *pathParser) runCommand(cmd byte) error {
	rel := cmd >= 'a' && cmd <= 'z'
	lower := cmd | 0x20 // ASCII lowercase

	first := true
	for {
		p.skipSep()
		if !first && (p.pos >= len(p.data) || isPathCmd(p.data[p.pos])) {
			return nil
		}
		if lower == 'z' {
			p.segs = append(p.segs, PathSeg{Op: OpClose})
			p.cur = p.start
			p.lastCmd = 'z'
			return nil
		}

		var err error
		switch lower {
		case 'm':
			err = p.moveTo(rel, first)
		case 'l':
			err = p.lineTo(rel)
		case 'h':
			err = p.hLineTo(rel)
		case 'v':
			err = p.vLineTo(rel)
		case 'c':
			err = p.cubicTo(rel)
		case 's':
			err = p.smoothCubicTo(rel)
		case 'q':
			err = p.quadTo(rel)
		case 't':
			err = p.smoothQuadTo(rel)
		case 'a':
			err = p.arcTo(rel)
		default:
			return fmt.Errorf("svgpdf: path data: unsupported command %q", string(cmd))
		}
		if err != nil {
			return err
		}
		first = false
	}
}

func (p *pathParser) moveTo(rel, first bool) error {
	pt, err := p.point(rel)
	if err != nil {
		return err
	}
	if first {
		p.segs = append(p.segs, PathSeg{Op: OpMoveTo, P3: pt})
		p.start = pt
		p.lastCmd = 'm'
	} else {
		// Additional coordinate pairs after a moveto are implicit linetos.
		p.segs = append(p.segs, PathSeg{Op: OpLineTo, P3: pt})
		p.lastCmd = 'l'
	}
	p.cur = pt
	return nil
}

func (p *pathParser) lineTo(rel bool) error {
	pt, err := p.point(rel)
	if err != nil {
		return err
	}
	p.emitLine(pt)
	return nil
}

func (p *pathParser) hLineTo(rel bool) error {
	x, err := p.number()
	if err != nil {
		return err
	}
	if rel {
		x += p.cur.X
	}
	p.emitLine(Point{x, p.cur.Y})
	return nil
}

func (p *pathParser) vLineTo(rel bool) error {
	y, err := p.number()
	if err != nil {
		return err
	}
	if rel {
		y += p.cur.Y
	}
	p.emitLine(Point{p.cur.X, y})
	return nil
}

func (p *pathParser) emitLine(pt Point) {
	p.segs = append(p.segs, PathSeg{Op: OpLineTo, P3: pt})
	p.cur = pt
	p.lastCmd = 'l'
}

func (p *pathParser) cubicTo(rel bool) error {
	c1, err := p.point(rel)
	if err != nil {
		return err
	}
	c2, err := p.point(rel)
	if err != nil {
		return err
	}
	end, err := p.point(rel)
	if err != nil {
		return err
	}
	p.emitCubic(c1, c2, end)
	return nil
}

func (p *pathParser) smoothCubicTo(rel bool) error {
	c2, err := p.point(rel)
	if err != nil {
		return err
	}
	end, err := p.point(rel)
	if err != nil {
		return err
	}
	c1 := p.cur
	if p.lastCmd == 'c' {
		// Reflect the previous control point about the current point.
		c1 = Point{2*p.cur.X - p.lastCtrl.X, 2*p.cur.Y - p.lastCtrl.Y}
	}
	p.emitCubic(c1, c2, end)
	return nil
}

func (p *pathParser) emitCubic(c1, c2, end Point) {
	p.segs = append(p.segs, PathSeg{Op: OpCubicTo, P1: c1, P2: c2, P3: end})
	p.cur = end
	p.lastCtrl = c2
	p.lastCmd = 'c'
}

func (p *pathParser) quadTo(rel bool) error {
	q, err := p.point(rel)
	if err != nil {
		return err
	}
	end, err := p.point(rel)
	if err != nil {
		return err
	}
	p.emitQuad(q, end)
	return nil
}

func (p *pathParser) smoothQuadTo(rel bool) error {
	end, err := p.point(rel)
	if err != nil {
		return err
	}
	q := p.cur
	if p.lastCmd == 'q' {
		q = Point{2*p.cur.X - p.lastCtrl.X, 2*p.cur.Y - p.lastCtrl.Y}
	}
	p.emitQuad(q, end)
	return nil
}

// emitQuad lowers a quadratic Bézier to the equivalent cubic. The exact
// degree elevation places the cubic control points 2/3 of the way from each
// endpoint to the quadratic control point:
//
//	c1 = p0 + 2/3 (q - p0),  c2 = p3 + 2/3 (q - p3)
func (p *pathParser) emitQuad(q, end Point) {
	c1, c2 := quadToCubic(p.cur, q, end)
	p.segs = append(p.segs, PathSeg{Op: OpCubicTo, P1: c1, P2: c2, P3: end})
	p.cur = end
	p.lastCtrl = q // T reflects the quadratic control point
	p.lastCmd = 'q'
}

// quadToCubic returns the cubic control points equivalent to the quadratic
// Bézier (p0, q, p3).
func quadToCubic(p0, q, p3 Point) (c1, c2 Point) {
	c1 = Point{p0.X + 2.0/3.0*(q.X-p0.X), p0.Y + 2.0/3.0*(q.Y-p0.Y)}
	c2 = Point{p3.X + 2.0/3.0*(q.X-p3.X), p3.Y + 2.0/3.0*(q.Y-p3.Y)}
	return c1, c2
}

func (p *pathParser) arcTo(rel bool) error {
	rx, err := p.number()
	if err != nil {
		return err
	}
	ry, err := p.number()
	if err != nil {
		return err
	}
	rot, err := p.number()
	if err != nil {
		return err
	}
	largeArc, err := p.flag()
	if err != nil {
		return err
	}
	sweep, err := p.flag()
	if err != nil {
		return err
	}
	end, err := p.point(rel)
	if err != nil {
		return err
	}

	p.segs = append(p.segs, arcToCubics(p.cur, end, rx, ry, rot, largeArc, sweep)...)
	p.cur = end
	p.lastCmd = 'a'
	return nil
}

// arcToCubics converts an SVG elliptical arc to cubic Bézier segments using
// the endpoint-to-center parameterization of SVG 1.1 appendix F.6, splitting
// the sweep into arcs of at most 90° each.
func arcToCubics(from, to Point, rx, ry, xRotDeg float64, largeArc, sweep bool) []PathSeg {
	if from == to {
		return nil
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		// Zero radii degrade to a straight line per the SVG spec.
		return []PathSeg{{Op: OpLineTo, P3: to}}
	}

	phi := xRotDeg * math.Pi / 180
	cosPhi, sinPhi := math.Cos(phi), math.Sin(phi)

	// F.6.5 step 1: transform midpoint to the ellipse-aligned frame.
	dx2, dy2 := (from.X-to.X)/2, (from.Y-to.Y)/2
	x1p := cosPhi*dx2 + sinPhi*dy2
	y1p := -sinPhi*dx2 + cosPhi*dy2

	// F.6.6: scale radii up if they cannot span the endpoints.
	lambda := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry)
	if lambda > 1 {
		s := math.Sqrt(lambda)
		rx *= s
		ry *= s
	}

	// F.6.5 step 2: center in the ellipse-aligned frame.
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	radicand := num / den
	if radicand < 0 {
		radicand = 0 // clamp floating point noise
	}
	coef := math.Sqrt(radicand)
	if largeArc == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1p / ry
	cyp := -coef * ry * x1p / rx

	// F.6.5 step 3: center in the original frame.
	cx := cosPhi*cxp - sinPhi*cyp + (from.X+to.X)/2
	cy := sinPhi*cxp + cosPhi*cyp + (from.Y+to.Y)/2

	// F.6.5 step 4: start angle and sweep extent.
	theta1 := math.Atan2((y1p-cyp)/ry, (x1p-cxp)/rx)
	theta2 := math.Atan2((-y1p-cyp)/ry, (-x1p-cxp)/rx)
	dTheta := theta2 - theta1
	if !sweep && dTheta > 0 {
		dTheta -= 2 * math.Pi
	} else if sweep && dTheta < 0 {
		dTheta += 2 * math.Pi
	}

	// Split into <= 90° pieces, each approximated by one cubic with the
	// standard tangent-length factor k = 4/3 tan(δ/4).
	n := int(math.Ceil(math.Abs(dTheta) / (math.Pi / 2)))
	if n == 0 {
		return nil
	}
	delta := dTheta / float64(n)
	k := 4.0 / 3.0 * math.Tan(delta/4)

	pointAt := func(theta float64) (pt, deriv Point) {
		cosT, sinT := math.Cos(theta), math.Sin(theta)
		pt = Point{
			X: cx + rx*cosT*cosPhi - ry*sinT*sinPhi,
			Y: cy + rx*cosT*sinPhi + ry*sinT*cosPhi,
		}
		deriv = Point{
			X: -rx*sinT*cosPhi - ry*cosT*sinPhi,
			Y: -rx*sinT*sinPhi + ry*cosT*cosPhi,
		}
		return pt, deriv
	}

	segs := make([]PathSeg, 0, n)
	t := theta1
	p0, d0 := pointAt(t)
	for i := 0; i < n; i++ {
		t2 := t + delta
		p1, d1 := pointAt(t2)
		end := p1
		if i == n-1 {
			end = to // land exactly on the endpoint
		}
		segs = append(segs, PathSeg{
			Op: OpCubicTo,
			P1: Point{p0.X + k*d0.X, p0.Y + k*d0.Y},
			P2: Point{end.X - k*d1.X, end.Y - k*d1.Y},
			P3: end,
		})
		t = t2
		p0, d0 = p1, d1
	}
	return segs
}

// --- scanning helpers ---

func isPathCmd(b byte) bool {
	switch b | 0x20 {
	case 'm', 'l', 'h', 'v', 'c', 's', 'q', 't', 'a', 'z':
		return true
	}
	return false
}

func (p *pathParser) skipSep() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r', ',':
			p.pos++
		default:
			return
		}
	}
}

func (p *pathParser) point(rel bool) (Point, error) {
	x, err := p.number()
	if err != nil {
		return Point{}, err
	}
	y, err := p.number()
	if err != nil {
		return Point{}, err
	}
	if rel {
		x += p.cur.X
		y += p.cur.Y
	}
	return Point{x, y}, nil
}

// flag reads an arc flag (0 or 1). Flags may be packed without separators
// ("A 1 1 0 1140,20"), so exactly one character is consumed.
func (p *pathParser) flag() (bool, error) {
	p.skipSep()
	if p.pos >= len(p.data) {
		return false, fmt.Errorf("svgpdf: path data: expected arc flag at end of input")
	}
	switch p.data[p.pos] {
	case '0':
		p.pos++
		return false, nil
	case '1':
		p.pos++
		return true, nil
	}
	return false, fmt.Errorf("svgpdf: path data: invalid arc flag %q", string(p.data[p.pos]))
}

func (p *pathParser) number() (float64, error) {
	p.skipSep()
	start := p.pos
	if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
		p.pos++
	}
	digits := func() {
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
	}
	digits()
	if p.pos < len(p.data) && p.data[p.pos] == '.' {
		p.pos++
		digits()
	}
	if p.pos < len(p.data) && (p.data[p.pos] == 'e' || p.data[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
			p.pos++
		}
		digits()
	}
	if p.pos == start {
		return 0, fmt.Errorf("svgpdf: path data: expected number at %q", p.data[start:min(start+12, len(p.data))])
	}
	v, err := strconv.ParseFloat(p.data[start:p.pos], 64)
	if err != nil {
		return 0, fmt.Errorf("svgpdf: path data: invalid number %q", p.data[start:p.pos])
	}
	return v, nil
}

// parseNumberList parses a whitespace/comma separated list of numbers
// (transform arguments, stroke-dasharray values).
func parseNumberList(s string) ([]float64, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]float64, 0, len(fields))
	for _, f := range fields {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", f)
		}
		out = append(out, v)
	}
	return out, nil
}

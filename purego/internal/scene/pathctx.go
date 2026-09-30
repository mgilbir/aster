package scene

import (
	"errors"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

// PathContext receives path construction calls, like the canvas 2D context that
// d3-shape and vega's path renderer draw into. Implementations here are
// StringPath (SVG path data, d3-path compatible) and the bounds accumulator.
type PathContext interface {
	MoveTo(x, y float64)
	LineTo(x, y float64)
	QuadraticCurveTo(x1, y1, x, y float64)
	BezierCurveTo(x1, y1, x2, y2, x, y float64)
	// Arc draws a circular arc; angles in radians, ccw flips the direction.
	Arc(x, y, r, a0, a1 float64, ccw bool)
	Rect(x, y, w, h float64)
	ClosePath()
}

// errNegativeRadius mirrors d3-path throwing on arcs with a negative radius.
var errNegativeRadius = errors.New("scene: negative radius")

type errContext interface{ Err() error }

// ctxErr returns a builder's recorded error, if the context keeps one.
func ctxErr(ctx PathContext) error {
	if e, ok := ctx.(errContext); ok {
		return e.Err()
	}
	return nil
}

// The angle constants are variables, not constants, on purpose: an untyped Go
// constant such as math.Pi / 180 is evaluated exactly and rounded once, whereas
// JavaScript rounds Math.PI first and then divides. Variables get the same
// double arithmetic as V8.
var (
	pi         = math.Pi
	tau        = 2 * pi
	halfPi     = pi / 2
	degToRad   = pi / 180
	halfSqrt3  = math.Sqrt(3) / 2
	tauEpsilon = tau - pathEps
)

const pathEps = 1e-6

// StringPath builds SVG path data exactly as d3-path's Path does: commands
// M L Q C A Z h v with numbers printed by JavaScript's Number toString, comma
// separated, no rounding.
//
// The zero value is ready to use. Errors that d3-path raises (negative arc
// radius) are recorded and reported by Err instead of panicking.
type StringPath struct {
	buf     []byte
	x0, y0  float64
	x1, y1  float64
	started bool    // d3's `_x1 !== null`
	has0    bool    // d3's `_x0 !== null`; Arc never sets _x0
	k       float64 // rounding factor 10^digits, 0 for none
	err     error
}

// Reset clears the path for reuse, keeping the allocation.
func (p *StringPath) Reset() {
	p.buf = p.buf[:0]
	p.started, p.has0 = false, false
	p.err = nil
}

// String returns the path data.
func (p *StringPath) String() string { return string(p.buf) }

// Bytes returns the path data without copying; it is valid until the next
// call that modifies the path.
func (p *StringPath) Bytes() []byte { return p.buf }

// Len is the number of bytes of path data so far.
func (p *StringPath) Len() int { return len(p.buf) }

// Err is the first error recorded, if any.
func (p *StringPath) Err() error { return p.err }

// SetDigits makes the path round every number to the given number of decimals
// with `Math.round(x * 10^d) / 10^d`, as d3-path's pathRound does. d3-shape's
// generators (arc, area, line, symbol) build their paths with 3 digits when no
// context is supplied, whereas vega's own rectangle and trail generators do not
// round. A negative d, or one above 15, disables rounding.
func (p *StringPath) SetDigits(d int) {
	if d < 0 || d > 15 {
		p.k = 0
		return
	}
	p.k = math.Pow10(d)
}

func (p *StringPath) num(f float64) {
	if p.k != 0 {
		f = jsRound(float64(f*p.k)) / p.k
	}
	p.buf = AppendNumber(p.buf, f)
}

// jsRound is JavaScript's Math.round: halves round toward +Infinity.
func jsRound(x float64) float64 {
	x = float64(x) // round a caller's product first (no FMA)
	if math.Abs(x) >= 1<<52 || x != x {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

func (p *StringPath) pt(c byte, x, y float64) {
	p.buf = append(p.buf, c)
	p.num(x)
	p.buf = append(p.buf, ',')
	p.num(y)
}

// MoveTo starts a subpath.
func (p *StringPath) MoveTo(x, y float64) {
	p.x0, p.x1, p.y0, p.y1 = x, x, y, y
	p.started, p.has0 = true, true
	p.pt('M', x, y)
}

// ClosePath closes the current subpath (a no-op on an empty path).
func (p *StringPath) ClosePath() {
	if p.started {
		p.x1, p.y1 = p.x0, p.y0
		p.started = p.has0
		p.buf = append(p.buf, 'Z')
	}
}

// LineTo draws a line.
func (p *StringPath) LineTo(x, y float64) {
	p.x1, p.y1 = x, y
	p.started = true
	p.pt('L', x, y)
}

// QuadraticCurveTo draws a quadratic Bézier.
func (p *StringPath) QuadraticCurveTo(x1, y1, x, y float64) {
	p.pt('Q', x1, y1)
	p.buf = append(p.buf, ',')
	p.num(x)
	p.buf = append(p.buf, ',')
	p.num(y)
	p.x1, p.y1 = x, y
	p.started = true
}

// BezierCurveTo draws a cubic Bézier.
func (p *StringPath) BezierCurveTo(x1, y1, x2, y2, x, y float64) {
	p.pt('C', x1, y1)
	p.buf = append(p.buf, ',')
	p.num(x2)
	p.buf = append(p.buf, ',')
	p.num(y2)
	p.buf = append(p.buf, ',')
	p.num(x)
	p.buf = append(p.buf, ',')
	p.num(y)
	p.x1, p.y1 = x, y
	p.started = true
}

// Rect adds a closed rectangle as `M x,y h w v h h -w Z`.
func (p *StringPath) Rect(x, y, w, h float64) {
	p.x0, p.x1, p.y0, p.y1 = x, x, y, y
	p.started, p.has0 = true, true
	p.pt('M', x, y)
	p.buf = append(p.buf, 'h')
	p.num(w)
	p.buf = append(p.buf, 'v')
	p.num(h)
	p.buf = append(p.buf, 'h')
	p.num(-w)
	p.buf = append(p.buf, 'Z')
}

// Arc draws a circular arc following d3-path: it first joins the current point
// to the arc start with a line, splits full circles into two SVG arcs, and
// flips angles that run the wrong way.
func (p *StringPath) Arc(x, y, r, a0, a1 float64, ccw bool) {
	if r < 0 {
		if p.err == nil {
			p.err = errNegativeRadius
		}
		return
	}
	dx, dy := float64(r*jsmath.Cos(a0)), float64(r*jsmath.Sin(a0))
	x0, y0 := x+dx, y+dy
	cw := 1.0
	da := a1 - a0
	if ccw {
		cw = 0
		da = a0 - a1
	}

	switch {
	case !p.started:
		p.pt('M', x0, y0)
	case math.Abs(p.x1-x0) > pathEps || math.Abs(p.y1-y0) > pathEps:
		p.pt('L', x0, y0)
	}

	if r == 0 || r != r { // JavaScript's `!r`
		return
	}
	if da < 0 {
		da = math.Mod(da, tau) + tau
	}
	switch {
	case da > tauEpsilon:
		p.arcTo(r, 1, cw, x-dx, y-dy)
		p.arcTo(r, 1, cw, x0, y0)
		p.x1, p.y1 = x0, y0
		p.started = true
	case da > pathEps:
		large := 0.0
		if da >= pi {
			large = 1
		}
		p.x1, p.y1 = x+float64(r*jsmath.Cos(a1)), y+float64(r*jsmath.Sin(a1))
		p.started = true
		p.arcTo(r, large, cw, p.x1, p.y1)
	}
}

func (p *StringPath) arcTo(r, large, sweep, x, y float64) {
	p.buf = append(p.buf, 'A')
	p.num(r)
	p.buf = append(p.buf, ',')
	p.num(r)
	p.buf = append(p.buf, ",0,"...)
	p.num(large)
	p.buf = append(p.buf, ',')
	p.num(sweep)
	p.buf = append(p.buf, ',')
	p.num(x)
	p.buf = append(p.buf, ',')
	p.num(y)
}

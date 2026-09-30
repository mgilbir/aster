package scene

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

// curve is d3-shape's curve interface: the line/area generators feed points and
// the curve emits path commands into its context.
type curve interface {
	areaStart()
	areaEnd()
	lineStart()
	lineEnd()
	point(x, y float64)
}

// curveKind selects an interpolation.
type curveKind uint8

// curveSpec is a resolved interpolation with its tension-like parameter.
type curveSpec struct {
	kind  curveKind
	param float64 // beta (bundle), tension (cardinal*), alpha (catmull-rom*)
}

const (
	curveBasis curveKind = iota + 1
	curveBasisClosed
	curveBasisOpen
	curveBundle
	curveCardinal
	curveCardinalClosed
	curveCardinalOpen
	curveCatmullRom
	curveCatmullRomClosed
	curveCatmullRomOpen
	curveLinear
	curveLinearClosed
	curveMonotoneX
	curveMonotoneY
	curveNatural
	curveStep
	curveStepAfter
	curveStepBefore
)

// lookupCurve resolves vega's interpolate/orient/tension triple (curves.js).
// tension applies only to bundle (beta), cardinal (tension) and catmull-rom
// (alpha) families, and only when the item set one. ok is false for an unknown
// interpolate name, where upstream would fail with a TypeError.
func lookupCurve(interp, orient string, tension Num) (curveSpec, bool) {
	var s curveSpec
	switch interp {
	case "basis":
		s.kind = curveBasis
	case "basis-closed":
		s.kind = curveBasisClosed
	case "basis-open":
		s.kind = curveBasisOpen
	case "bundle":
		s.kind, s.param = curveBundle, 0.85
	case "cardinal":
		s.kind = curveCardinal
	case "cardinal-open":
		s.kind = curveCardinalOpen
	case "cardinal-closed":
		s.kind = curveCardinalClosed
	case "catmull-rom":
		s.kind, s.param = curveCatmullRom, 0.5
	case "catmull-rom-closed":
		s.kind, s.param = curveCatmullRomClosed, 0.5
	case "catmull-rom-open":
		s.kind, s.param = curveCatmullRomOpen, 0.5
	case "linear":
		s.kind = curveLinear
	case "linear-closed":
		s.kind = curveLinearClosed
	case "monotone":
		// 'horizontal' orientation interpolates y as the function of x.
		if orient == "horizontal" {
			s.kind = curveMonotoneY
		} else {
			s.kind = curveMonotoneX
		}
	case "natural":
		s.kind = curveNatural
	case "step":
		s.kind = curveStep
	case "step-after":
		s.kind = curveStepAfter
	case "step-before":
		s.kind = curveStepBefore
	default:
		return s, false
	}
	switch s.kind {
	case curveBundle, curveCardinal, curveCardinalOpen, curveCardinalClosed,
		curveCatmullRom, curveCatmullRomClosed, curveCatmullRomOpen:
		if tension.Set() {
			s.param = tension.Val()
		}
	}
	return s, true
}

func (s curveSpec) new(ctx PathContext) curve {
	switch s.kind {
	case curveBasis:
		return newBasis(ctx)
	case curveBasisClosed:
		return &basisClosed{ctx: ctx}
	case curveBasisOpen:
		return &basisOpen{ctx: ctx, line: math.NaN()}
	case curveBundle:
		if s.param == 1 {
			return newBasis(ctx)
		}
		return &bundle{basis: newBasis(ctx), beta: s.param}
	case curveCardinal:
		return newCardinal(ctx, s.param)
	case curveCardinalClosed:
		return &cardinalClosed{ctx: ctx, k: (1 - s.param) / 6}
	case curveCardinalOpen:
		return &cardinalOpen{ctx: ctx, k: (1 - s.param) / 6, line: math.NaN()}
	case curveCatmullRom:
		if s.param == 0 || s.param != s.param {
			// `alpha ? CatmullRom : Cardinal(0)`: 0 and NaN are falsy.
			return newCardinal(ctx, 0)
		}
		return &catmullRom{ctx: ctx, line: math.NaN(), crState: crState{alpha: s.param}}
	case curveCatmullRomClosed:
		if s.param == 0 || s.param != s.param {
			return &cardinalClosed{ctx: ctx, k: 1.0 / 6}
		}
		return &catmullRomClosed{ctx: ctx, crState: crState{alpha: s.param}}
	case curveCatmullRomOpen:
		if s.param == 0 || s.param != s.param {
			return &cardinalOpen{ctx: ctx, k: 1.0 / 6, line: math.NaN()}
		}
		return &catmullRomOpen{ctx: ctx, line: math.NaN(), crState: crState{alpha: s.param}}
	case curveLinearClosed:
		return &linearClosed{ctx: ctx}
	case curveMonotoneX:
		return &monotone{ctx: ctx, line: math.NaN()}
	case curveMonotoneY:
		return &monotone{ctx: reflectCtx{ctx}, line: math.NaN(), swap: true}
	case curveNatural:
		return &natural{ctx: ctx, line: math.NaN()}
	case curveStep:
		return &step{ctx: ctx, t: 0.5, line: math.NaN()}
	case curveStepAfter:
		return &step{ctx: ctx, t: 1, line: math.NaN()}
	case curveStepBefore:
		return &step{ctx: ctx, t: 0, line: math.NaN()}
	}
	return &linear{ctx: ctx, line: math.NaN()}
}

// The `_line` state of d3's curves starts undefined (NaN here), becomes 0 in
// areaStart and NaN in areaEnd, and is flipped by lineEnd; JavaScript
// truthiness of NaN and the `!== 0` comparisons below depend on that.
func lineTruthy(l float64) bool { return l != 0 && l == l }

// ---- linear

type linear struct {
	ctx   PathContext
	line  float64
	state int
}

func (c *linear) areaStart() { c.line = 0 }
func (c *linear) areaEnd()   { c.line = math.NaN() }
func (c *linear) lineStart() { c.state = 0 }
func (c *linear) lineEnd() {
	if lineTruthy(c.line) || (c.line != 0 && c.state == 1) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
}
func (c *linear) point(x, y float64) {
	switch c.state {
	case 0:
		c.state = 1
		if lineTruthy(c.line) {
			c.ctx.LineTo(x, y)
		} else {
			c.ctx.MoveTo(x, y)
		}
	default:
		c.state = 2
		c.ctx.LineTo(x, y)
	}
}

type linearClosed struct {
	ctx   PathContext
	state int
}

func (c *linearClosed) areaStart() {}
func (c *linearClosed) areaEnd()   {}
func (c *linearClosed) lineStart() { c.state = 0 }
func (c *linearClosed) lineEnd() {
	if c.state != 0 {
		c.ctx.ClosePath()
	}
}
func (c *linearClosed) point(x, y float64) {
	if c.state != 0 {
		c.ctx.LineTo(x, y)
	} else {
		c.state = 1
		c.ctx.MoveTo(x, y)
	}
}

// ---- basis

type basis struct {
	ctx            PathContext
	line           float64
	x0, x1, y0, y1 float64
	state          int
}

func newBasis(ctx PathContext) *basis { return &basis{ctx: ctx, line: math.NaN()} }

func (c *basis) areaStart() { c.line = 0 }
func (c *basis) areaEnd()   { c.line = math.NaN() }
func (c *basis) lineStart() {
	c.x0, c.x1, c.y0, c.y1 = math.NaN(), math.NaN(), math.NaN(), math.NaN()
	c.state = 0
}
func basisPoint(c PathContext, x0, y0, x1, y1, x, y float64) {
	c.BezierCurveTo(
		(2*x0+x1)/3, (2*y0+y1)/3,
		(x0+2*x1)/3, (y0+2*y1)/3,
		(x0+4*x1+x)/6, (y0+4*y1+y)/6)
}
func (c *basis) lineEnd() {
	switch c.state {
	case 3:
		basisPoint(c.ctx, c.x0, c.y0, c.x1, c.y1, c.x1, c.y1)
		c.ctx.LineTo(c.x1, c.y1)
	case 2:
		c.ctx.LineTo(c.x1, c.y1)
	}
	if lineTruthy(c.line) || (c.line != 0 && c.state == 1) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
}
func (c *basis) point(x, y float64) {
	switch c.state {
	case 0:
		c.state = 1
		if lineTruthy(c.line) {
			c.ctx.LineTo(x, y)
		} else {
			c.ctx.MoveTo(x, y)
		}
	case 1:
		c.state = 2
	case 2:
		c.state = 3
		c.ctx.LineTo((float64(5*c.x0)+c.x1)/6, (float64(5*c.y0)+c.y1)/6)
		basisPoint(c.ctx, c.x0, c.y0, c.x1, c.y1, x, y)
	default:
		basisPoint(c.ctx, c.x0, c.y0, c.x1, c.y1, x, y)
	}
	c.x0, c.x1 = c.x1, x
	c.y0, c.y1 = c.y1, y
}

type basisClosed struct {
	ctx                                    PathContext
	x0, x1, x2, x3, x4, y0, y1, y2, y3, y4 float64
	state                                  int
}

func (c *basisClosed) areaStart() {}
func (c *basisClosed) areaEnd()   {}
func (c *basisClosed) lineStart() {
	n := math.NaN()
	c.x0, c.x1, c.x2, c.x3, c.x4 = n, n, n, n, n
	c.y0, c.y1, c.y2, c.y3, c.y4 = n, n, n, n, n
	c.state = 0
}
func (c *basisClosed) lineEnd() {
	switch c.state {
	case 1:
		c.ctx.MoveTo(c.x2, c.y2)
		c.ctx.ClosePath()
	case 2:
		c.ctx.MoveTo((c.x2+2*c.x3)/3, (c.y2+2*c.y3)/3)
		c.ctx.LineTo((c.x3+2*c.x2)/3, (c.y3+2*c.y2)/3)
		c.ctx.ClosePath()
	case 3:
		c.point(c.x2, c.y2)
		c.point(c.x3, c.y3)
		c.point(c.x4, c.y4)
	}
}
func (c *basisClosed) point(x, y float64) {
	switch c.state {
	case 0:
		c.state = 1
		c.x2, c.y2 = x, y
	case 1:
		c.state = 2
		c.x3, c.y3 = x, y
	case 2:
		c.state = 3
		c.x4, c.y4 = x, y
		c.ctx.MoveTo((c.x0+4*c.x1+x)/6, (c.y0+4*c.y1+y)/6)
	default:
		basisPoint(c.ctx, c.x0, c.y0, c.x1, c.y1, x, y)
	}
	c.x0, c.x1 = c.x1, x
	c.y0, c.y1 = c.y1, y
}

type basisOpen struct {
	ctx            PathContext
	line           float64
	x0, x1, y0, y1 float64
	state          int
}

func (c *basisOpen) areaStart() { c.line = 0 }
func (c *basisOpen) areaEnd()   { c.line = math.NaN() }
func (c *basisOpen) lineStart() {
	c.x0, c.x1, c.y0, c.y1 = math.NaN(), math.NaN(), math.NaN(), math.NaN()
	c.state = 0
}
func (c *basisOpen) lineEnd() {
	if lineTruthy(c.line) || (c.line != 0 && c.state == 3) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
}
func (c *basisOpen) point(x, y float64) {
	switch c.state {
	case 0:
		c.state = 1
	case 1:
		c.state = 2
	case 2:
		c.state = 3
		px, py := (c.x0+4*c.x1+x)/6, (c.y0+4*c.y1+y)/6
		if lineTruthy(c.line) {
			c.ctx.LineTo(px, py)
		} else {
			c.ctx.MoveTo(px, py)
		}
	case 3:
		c.state = 4
		basisPoint(c.ctx, c.x0, c.y0, c.x1, c.y1, x, y)
	default:
		basisPoint(c.ctx, c.x0, c.y0, c.x1, c.y1, x, y)
	}
	c.x0, c.x1 = c.x1, x
	c.y0, c.y1 = c.y1, y
}

// ---- bundle

type bundle struct {
	basis *basis
	beta  float64
	x, y  []float64
}

func (c *bundle) areaStart() { c.basis.areaStart() }
func (c *bundle) areaEnd()   { c.basis.areaEnd() }
func (c *bundle) lineStart() {
	c.x, c.y = c.x[:0], c.y[:0]
	c.basis.lineStart()
}
func (c *bundle) lineEnd() {
	x, y := c.x, c.y
	j := len(x) - 1
	if j > 0 {
		x0, y0 := x[0], y[0]
		dx, dy := x[j]-x0, y[j]-y0
		for i := 0; i <= j; i++ {
			t := float64(i) / float64(j)
			c.basis.point(
				float64(c.beta*x[i])+float64((1-c.beta)*(x0+float64(t*dx))),
				float64(c.beta*y[i])+float64((1-c.beta)*(y0+float64(t*dy))))
		}
	}
	c.x, c.y = c.x[:0], c.y[:0]
	c.basis.lineEnd()
}
func (c *bundle) point(x, y float64) {
	c.x = append(c.x, x)
	c.y = append(c.y, y)
}

// ---- cardinal

type cardinal struct {
	ctx                    PathContext
	line, k                float64
	x0, x1, x2, y0, y1, y2 float64
	state                  int
}

func newCardinal(ctx PathContext, tension float64) *cardinal {
	return &cardinal{ctx: ctx, k: (1 - tension) / 6, line: math.NaN()}
}

func (c *cardinal) areaStart() { c.line = 0 }
func (c *cardinal) areaEnd()   { c.line = math.NaN() }
func (c *cardinal) lineStart() {
	n := math.NaN()
	c.x0, c.x1, c.x2, c.y0, c.y1, c.y2 = n, n, n, n, n, n
	c.state = 0
}
func cardinalPoint(ctx PathContext, k, x0, y0, x1, y1, x2, y2, x, y float64) {
	ctx.BezierCurveTo(
		x1+float64(k*(x2-x0)), y1+float64(k*(y2-y0)),
		x2+float64(k*(x1-x)), y2+float64(k*(y1-y)),
		x2, y2)
}
func (c *cardinal) lineEnd() {
	switch c.state {
	case 2:
		c.ctx.LineTo(c.x2, c.y2)
	case 3:
		cardinalPoint(c.ctx, c.k, c.x0, c.y0, c.x1, c.y1, c.x2, c.y2, c.x1, c.y1)
	}
	if lineTruthy(c.line) || (c.line != 0 && c.state == 1) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
}
func (c *cardinal) point(x, y float64) {
	switch c.state {
	case 0:
		c.state = 1
		if lineTruthy(c.line) {
			c.ctx.LineTo(x, y)
		} else {
			c.ctx.MoveTo(x, y)
		}
	case 1:
		c.state = 2
		c.x1, c.y1 = x, y
	case 2:
		c.state = 3
		cardinalPoint(c.ctx, c.k, c.x0, c.y0, c.x1, c.y1, c.x2, c.y2, x, y)
	default:
		cardinalPoint(c.ctx, c.k, c.x0, c.y0, c.x1, c.y1, c.x2, c.y2, x, y)
	}
	c.x0, c.x1, c.x2 = c.x1, c.x2, x
	c.y0, c.y1, c.y2 = c.y1, c.y2, y
}

type cardinalClosed struct {
	ctx                                            PathContext
	k                                              float64
	x0, x1, x2, x3, x4, x5, y0, y1, y2, y3, y4, y5 float64
	state                                          int
}

func (c *cardinalClosed) areaStart() {}
func (c *cardinalClosed) areaEnd()   {}
func (c *cardinalClosed) lineStart() {
	n := math.NaN()
	c.x0, c.x1, c.x2, c.x3, c.x4, c.x5 = n, n, n, n, n, n
	c.y0, c.y1, c.y2, c.y3, c.y4, c.y5 = n, n, n, n, n, n
	c.state = 0
}
func (c *cardinalClosed) lineEnd() {
	switch c.state {
	case 1:
		c.ctx.MoveTo(c.x3, c.y3)
		c.ctx.ClosePath()
	case 2:
		c.ctx.LineTo(c.x3, c.y3)
		c.ctx.ClosePath()
	case 3:
		c.point(c.x3, c.y3)
		c.point(c.x4, c.y4)
		c.point(c.x5, c.y5)
	}
}
func (c *cardinalClosed) point(x, y float64) {
	switch c.state {
	case 0:
		c.state = 1
		c.x3, c.y3 = x, y
	case 1:
		c.state = 2
		c.x4, c.y4 = x, y
		c.ctx.MoveTo(x, y)
	case 2:
		c.state = 3
		c.x5, c.y5 = x, y
	default:
		cardinalPoint(c.ctx, c.k, c.x0, c.y0, c.x1, c.y1, c.x2, c.y2, x, y)
	}
	c.x0, c.x1, c.x2 = c.x1, c.x2, x
	c.y0, c.y1, c.y2 = c.y1, c.y2, y
}

type cardinalOpen struct {
	ctx                    PathContext
	line, k                float64
	x0, x1, x2, y0, y1, y2 float64
	state                  int
}

func (c *cardinalOpen) areaStart() { c.line = 0 }
func (c *cardinalOpen) areaEnd()   { c.line = math.NaN() }
func (c *cardinalOpen) lineStart() {
	n := math.NaN()
	c.x0, c.x1, c.x2, c.y0, c.y1, c.y2 = n, n, n, n, n, n
	c.state = 0
}
func (c *cardinalOpen) lineEnd() {
	if lineTruthy(c.line) || (c.line != 0 && c.state == 3) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
}
func (c *cardinalOpen) point(x, y float64) {
	switch c.state {
	case 0:
		c.state = 1
	case 1:
		c.state = 2
	case 2:
		c.state = 3
		if lineTruthy(c.line) {
			c.ctx.LineTo(c.x2, c.y2)
		} else {
			c.ctx.MoveTo(c.x2, c.y2)
		}
	case 3:
		c.state = 4
		cardinalPoint(c.ctx, c.k, c.x0, c.y0, c.x1, c.y1, c.x2, c.y2, x, y)
	default:
		cardinalPoint(c.ctx, c.k, c.x0, c.y0, c.x1, c.y1, c.x2, c.y2, x, y)
	}
	c.x0, c.x1, c.x2 = c.x1, c.x2, x
	c.y0, c.y1, c.y2 = c.y1, c.y2, y
}

// ---- catmull-rom

// crState holds the chord-length terms shared by the three catmull-rom curves.
type crState struct {
	alpha                  float64
	x0, x1, x2, y0, y1, y2 float64
	l01a, l12a, l23a       float64
	l01_2a, l12_2a, l23_2a float64
}

func (s *crState) reset() {
	n := math.NaN()
	s.x0, s.x1, s.x2, s.y0, s.y1, s.y2 = n, n, n, n, n, n
	s.l01a, s.l12a, s.l23a, s.l01_2a, s.l12_2a, s.l23_2a = 0, 0, 0, 0, 0, 0
}

// chord updates the l23 terms for the incoming point (only after the first).
func (s *crState) chord(x, y float64) {
	x23, y23 := s.x2-x, s.y2-y
	s.l23_2a = jsmath.Pow(float64(x23*x23)+float64(y23*y23), s.alpha)
	s.l23a = math.Sqrt(s.l23_2a)
}

func (s *crState) shift(x, y float64) {
	s.l01a, s.l12a = s.l12a, s.l23a
	s.l01_2a, s.l12_2a = s.l12_2a, s.l23_2a
	s.x0, s.x1, s.x2 = s.x1, s.x2, x
	s.y0, s.y1, s.y2 = s.y1, s.y2, y
}

func (s *crState) bezier(ctx PathContext, x, y float64) {
	x1, y1, x2, y2 := s.x1, s.y1, s.x2, s.y2
	if s.l01a > epsilon12 {
		a := 2*s.l01_2a + float64(3*s.l01a*s.l12a) + s.l12_2a
		n := 3 * s.l01a * (s.l01a + s.l12a)
		x1 = (float64(x1*a) - float64(s.x0*s.l12_2a) + float64(s.x2*s.l01_2a)) / n
		y1 = (float64(y1*a) - float64(s.y0*s.l12_2a) + float64(s.y2*s.l01_2a)) / n
	}
	if s.l23a > epsilon12 {
		b := 2*s.l23_2a + float64(3*s.l23a*s.l12a) + s.l12_2a
		m := 3 * s.l23a * (s.l23a + s.l12a)
		x2 = (float64(x2*b) + float64(s.x1*s.l23_2a) - float64(x*s.l12_2a)) / m
		y2 = (float64(y2*b) + float64(s.y1*s.l23_2a) - float64(y*s.l12_2a)) / m
	}
	ctx.BezierCurveTo(x1, y1, x2, y2, s.x2, s.y2)
}

// epsilon12 is d3-shape's math epsilon.
const epsilon12 = 1e-12

type catmullRom struct {
	ctx   PathContext
	line  float64
	state int
	crState
}

func (c *catmullRom) areaStart() { c.line = 0 }
func (c *catmullRom) areaEnd()   { c.line = math.NaN() }
func (c *catmullRom) lineStart() { c.reset(); c.state = 0 }
func (c *catmullRom) lineEnd() {
	switch c.state {
	case 2:
		c.ctx.LineTo(c.x2, c.y2)
	case 3:
		c.point(c.x2, c.y2)
	}
	if lineTruthy(c.line) || (c.line != 0 && c.state == 1) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
}
func (c *catmullRom) point(x, y float64) {
	if c.state != 0 {
		c.chord(x, y)
	}
	switch c.state {
	case 0:
		c.state = 1
		if lineTruthy(c.line) {
			c.ctx.LineTo(x, y)
		} else {
			c.ctx.MoveTo(x, y)
		}
	case 1:
		c.state = 2
	case 2:
		c.state = 3
		c.bezier(c.ctx, x, y)
	default:
		c.bezier(c.ctx, x, y)
	}
	c.shift(x, y)
}

type catmullRomClosed struct {
	ctx                    PathContext
	state                  int
	x3, x4, x5, y3, y4, y5 float64
	crState
}

func (c *catmullRomClosed) areaStart() {}
func (c *catmullRomClosed) areaEnd()   {}
func (c *catmullRomClosed) lineStart() {
	c.reset()
	n := math.NaN()
	c.x3, c.x4, c.x5, c.y3, c.y4, c.y5 = n, n, n, n, n, n
	c.state = 0
}
func (c *catmullRomClosed) lineEnd() {
	switch c.state {
	case 1:
		c.ctx.MoveTo(c.x3, c.y3)
		c.ctx.ClosePath()
	case 2:
		c.ctx.LineTo(c.x3, c.y3)
		c.ctx.ClosePath()
	case 3:
		c.point(c.x3, c.y3)
		c.point(c.x4, c.y4)
		c.point(c.x5, c.y5)
	}
}
func (c *catmullRomClosed) point(x, y float64) {
	if c.state != 0 {
		c.chord(x, y)
	}
	switch c.state {
	case 0:
		c.state = 1
		c.x3, c.y3 = x, y
	case 1:
		c.state = 2
		c.x4, c.y4 = x, y
		c.ctx.MoveTo(x, y)
	case 2:
		c.state = 3
		c.x5, c.y5 = x, y
	default:
		c.bezier(c.ctx, x, y)
	}
	c.shift(x, y)
}

type catmullRomOpen struct {
	ctx   PathContext
	line  float64
	state int
	crState
}

func (c *catmullRomOpen) areaStart() { c.line = 0 }
func (c *catmullRomOpen) areaEnd()   { c.line = math.NaN() }
func (c *catmullRomOpen) lineStart() { c.reset(); c.state = 0 }
func (c *catmullRomOpen) lineEnd() {
	if lineTruthy(c.line) || (c.line != 0 && c.state == 3) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
}
func (c *catmullRomOpen) point(x, y float64) {
	if c.state != 0 {
		c.chord(x, y)
	}
	switch c.state {
	case 0:
		c.state = 1
	case 1:
		c.state = 2
	case 2:
		c.state = 3
		if lineTruthy(c.line) {
			c.ctx.LineTo(c.x2, c.y2)
		} else {
			c.ctx.MoveTo(c.x2, c.y2)
		}
	case 3:
		c.state = 4
		c.bezier(c.ctx, x, y)
	default:
		c.bezier(c.ctx, x, y)
	}
	c.shift(x, y)
}

// ---- monotone

// reflectCtx swaps x and y for the horizontal monotone curve.
type reflectCtx struct{ PathContext }

func (r reflectCtx) MoveTo(x, y float64) { r.PathContext.MoveTo(y, x) }
func (r reflectCtx) LineTo(x, y float64) { r.PathContext.LineTo(y, x) }
func (r reflectCtx) BezierCurveTo(x1, y1, x2, y2, x, y float64) {
	r.PathContext.BezierCurveTo(y1, x1, y2, x2, y, x)
}

type monotone struct {
	ctx            PathContext
	line           float64
	x0, x1, y0, y1 float64
	t0             float64
	state          int
	swap           bool
}

func sign(x float64) float64 {
	if x < 0 {
		return -1
	}
	return 1
}

// slopeDiv is `h || h1 < 0 && -0`: the step, or a signed zero when it is 0/NaN.
func slopeDiv(h, other float64) float64 {
	if h != 0 && h == h {
		return h
	}
	if other < 0 {
		return math.Copysign(0, -1)
	}
	return 0
}

func (c *monotone) slope3(x2, y2 float64) float64 {
	h0 := c.x1 - c.x0
	h1 := x2 - c.x1
	s0 := (c.y1 - c.y0) / slopeDiv(h0, h1)
	s1 := (y2 - c.y1) / slopeDiv(h1, h0)
	p := (float64(s0*h1) + float64(s1*h0)) / (h0 + h1)
	r := (sign(s0) + sign(s1)) * math.Min(math.Min(math.Abs(s0), math.Abs(s1)), 0.5*math.Abs(p))
	if r == 0 || r != r {
		return 0
	}
	return r
}

func (c *monotone) slope2(t float64) float64 {
	h := c.x1 - c.x0
	if h != 0 && h == h {
		return (3*(c.y1-c.y0)/h - t) / 2
	}
	return t
}

func (c *monotone) bez(t0, t1 float64) {
	dx := (c.x1 - c.x0) / 3
	c.ctx.BezierCurveTo(c.x0+dx, c.y0+float64(dx*t0), c.x1-dx, c.y1-float64(dx*t1), c.x1, c.y1)
}

func (c *monotone) areaStart() { c.line = 0 }
func (c *monotone) areaEnd()   { c.line = math.NaN() }
func (c *monotone) lineStart() {
	n := math.NaN()
	c.x0, c.x1, c.y0, c.y1, c.t0 = n, n, n, n, n
	c.state = 0
}
func (c *monotone) lineEnd() {
	switch c.state {
	case 2:
		c.ctx.LineTo(c.x1, c.y1)
	case 3:
		c.bez(c.t0, c.slope2(c.t0))
	}
	if lineTruthy(c.line) || (c.line != 0 && c.state == 1) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
}

// point: MonotoneY is the same algorithm with its coordinates exchanged on the
// way in (the reflect context exchanges them again on the way out), so the
// generator calls point(y, x) for it; see the area/line drivers.
func (c *monotone) point(x, y float64) {
	if c.swap {
		x, y = y, x
	}
	t1 := math.NaN()
	if x == c.x1 && y == c.y1 {
		return // ignore coincident points
	}
	switch c.state {
	case 0:
		c.state = 1
		if lineTruthy(c.line) {
			c.ctx.LineTo(x, y)
		} else {
			c.ctx.MoveTo(x, y)
		}
	case 1:
		c.state = 2
	case 2:
		c.state = 3
		t1 = c.slope3(x, y)
		c.bez(c.slope2(t1), t1)
	default:
		t1 = c.slope3(x, y)
		c.bez(c.t0, t1)
	}
	c.x0, c.x1 = c.x1, x
	c.y0, c.y1 = c.y1, y
	c.t0 = t1
}

// ---- natural

type natural struct {
	ctx  PathContext
	line float64
	x, y []float64
}

func (c *natural) areaStart() { c.line = 0 }
func (c *natural) areaEnd()   { c.line = math.NaN() }
func (c *natural) lineStart() { c.x, c.y = c.x[:0], c.y[:0] }
func (c *natural) lineEnd() {
	x, y := c.x, c.y
	n := len(x)
	if n > 0 {
		if lineTruthy(c.line) {
			c.ctx.LineTo(x[0], y[0])
		} else {
			c.ctx.MoveTo(x[0], y[0])
		}
		if n == 2 {
			c.ctx.LineTo(x[1], y[1])
		} else {
			// n == 1 has no segments; controlPoints needs n >= 3 (n-1 >= 2).
			if n > 2 {
				pxa, pxb := naturalControls(x)
				pya, pyb := naturalControls(y)
				for i0, i1 := 0, 1; i1 < n; i0, i1 = i0+1, i1+1 {
					c.ctx.BezierCurveTo(pxa[i0], pya[i0], pxb[i0], pyb[i0], x[i1], y[i1])
				}
			}
		}
	}
	if lineTruthy(c.line) || (c.line != 0 && n == 1) {
		c.ctx.ClosePath()
	}
	c.line = 1 - c.line
	c.x, c.y = c.x[:0], c.y[:0]
}
func (c *natural) point(x, y float64) {
	c.x = append(c.x, x)
	c.y = append(c.y, y)
}

// naturalControls computes the two control-point arrays of a natural cubic
// spline through x (see https://www.particleincell.com/2012/bezier-splines/).
func naturalControls(x []float64) (a, b []float64) {
	n := len(x) - 1
	a = make([]float64, n)
	b = make([]float64, n)
	r := make([]float64, n)
	a[0], b[0], r[0] = 0, 2, x[0]+2*x[1]
	for i := 1; i < n-1; i++ {
		a[i], b[i], r[i] = 1, 4, 4*x[i]+2*x[i+1]
	}
	a[n-1], b[n-1], r[n-1] = 2, 7, 8*x[n-1]+x[n]
	for i := 1; i < n; i++ {
		m := a[i] / b[i-1]
		b[i] -= m
		r[i] -= float64(m * r[i-1])
	}
	a[n-1] = r[n-1] / b[n-1]
	for i := n - 2; i >= 0; i-- {
		a[i] = (r[i] - a[i+1]) / b[i]
	}
	b[n-1] = (x[n] + a[n-1]) / 2
	for i := 0; i < n-1; i++ {
		b[i] = 2*x[i+1] - a[i+1]
	}
	return a, b
}

// ---- step

type step struct {
	ctx   PathContext
	line  float64
	t     float64
	x, y  float64
	state int
}

func (c *step) areaStart() { c.line = 0 }
func (c *step) areaEnd()   { c.line = math.NaN() }
func (c *step) lineStart() {
	c.x, c.y = math.NaN(), math.NaN()
	c.state = 0
}
func (c *step) lineEnd() {
	if 0 < c.t && c.t < 1 && c.state == 2 {
		c.ctx.LineTo(c.x, c.y)
	}
	if lineTruthy(c.line) || (c.line != 0 && c.state == 1) {
		c.ctx.ClosePath()
	}
	if c.line >= 0 {
		c.t = 1 - c.t
		c.line = 1 - c.line
	}
}
func (c *step) point(x, y float64) {
	switch c.state {
	case 0:
		c.state = 1
		if lineTruthy(c.line) {
			c.ctx.LineTo(x, y)
		} else {
			c.ctx.MoveTo(x, y)
		}
	default:
		c.state = 2
		if c.t <= 0 {
			c.ctx.LineTo(c.x, y)
			c.ctx.LineTo(x, y)
		} else {
			x1 := float64(c.x*(1-c.t)) + float64(x*c.t)
			c.ctx.LineTo(x1, c.y)
			c.ctx.LineTo(x1, y)
		}
	}
	c.x, c.y = x, y
}

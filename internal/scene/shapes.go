package scene

import (
	"errors"
	"math"
	"sync"

	"github.com/mgilbir/aster/internal/jsmath"
)

// This file holds vega-scenegraph's path/shapes.js: the generators that turn an
// item (or, for line/area/trail, a whole mark's items) into path commands.

// ErrUnknownInterpolate reports an `interpolate` value with no curve; upstream
// fails with a TypeError when it tries to use the missing curve.
var ErrUnknownInterpolate = errors.New("scene: unknown interpolate type")

// ErrNoShapeGenerator reports a shape mark item with no generator (neither
// the mark's nor the item's); upstream calls `.context` on undefined.
var ErrNoShapeGenerator = errors.New("Cannot read properties of undefined (reading 'context')")

// ErrCurveNoArea reports an area drawn with the bundle interpolation, which d3
// implements only for lines; upstream fails with a TypeError.
var ErrCurveNoArea = errors.New("scene: bundle interpolation cannot draw areas")

// HasCornerRadius reports whether any corner radius is set and non-zero
// (`item.cornerRadius || item.cornerRadiusTopLeft || ...`).
func (it *Item) HasCornerRadius() bool {
	return it.truthyProp("cornerRadius", it.CornerRadius) ||
		it.truthyProp("cornerRadiusTopLeft", it.CornerRadiusTopLeft) ||
		it.truthyProp("cornerRadiusTopRight", it.CornerRadiusTopRight) ||
		it.truthyProp("cornerRadiusBottomRight", it.CornerRadiusBottomRight) ||
		it.truthyProp("cornerRadiusBottomLeft", it.CornerRadiusBottomLeft)
}

func lineCurve(first *Item) (curveSpec, error) {
	interp := first.Interpolate
	if interp == "" {
		interp = "linear"
	}
	spec, ok := lookupCurve(interp, first.Orient, first.Tension)
	if !ok {
		return spec, ErrUnknownInterpolate
	}
	return spec, nil
}

// Line draws the polyline through items (a line mark). Interpolation, orient and
// tension come from the first item; items with `defined === false` split the
// line into segments.
func Line(ctx PathContext, items []*Item) error {
	if len(items) == 0 {
		return nil
	}
	spec, err := lineCurve(items[0])
	if err != nil {
		return err
	}
	out := spec.new(ctx)
	defined0 := false
	n := len(items)
	for i := 0; i <= n; i++ {
		def := i < n && items[i].Defined != No
		if def != defined0 {
			defined0 = def
			if def {
				out.lineStart()
			} else {
				out.lineEnd()
			}
		}
		if defined0 {
			out.point(items[i].OrZero("x"), items[i].OrZero("y"))
		}
	}
	return ctxErr(ctx)
}

// Area draws the filled band between each item's (x, y) and its baseline
// (y+height for vertical areas, x+width for horizontal ones).
func Area(ctx PathContext, items []*Item) error {
	if len(items) == 0 {
		return nil
	}
	first := items[0]
	spec, err := lineCurve(first)
	if err != nil {
		return err
	}
	if spec.kind == curveBundle {
		// d3's bundle curve has no areaStart/areaEnd, so upstream throws.
		return ErrCurveNoArea
	}
	horizontal := first.Orient == "horizontal"
	out := spec.new(ctx)
	n := len(items)
	bx := make([]float64, n)
	by := make([]float64, n)
	defined0 := false
	j := 0
	for i := 0; i <= n; i++ {
		def := i < n && items[i].Defined != No
		if def != defined0 {
			defined0 = def
			if def {
				j = i
				out.areaStart()
				out.lineStart()
			} else {
				out.lineEnd()
				out.lineStart()
				for k := i - 1; k >= j; k-- {
					out.point(bx[k], by[k])
				}
				out.lineEnd()
				out.areaEnd()
			}
		}
		if defined0 {
			it := items[i]
			x, y := it.OrZero("x"), it.OrZero("y")
			if horizontal {
				// area().y(y).x1(x).x0(xw): baseline at x+width, same y.
				bx[i], by[i] = x+it.Width.Zero(), y
			} else {
				// area().x(x).y1(y).y0(yh): baseline at y+height, same x.
				bx[i], by[i] = x, y+it.Height.Zero()
			}
			out.point(x, y)
		}
	}
	return ctxErr(ctx)
}

// Trail draws a variable-width stroke along the items as a chain of filled
// capsule segments; each item's size is the diameter at that point.
func Trail(ctx PathContext, items []*Item) error {
	var (
		ready      bool
		x1, y1, r1 float64
	)
	point := func(x2, y2, w2 float64) {
		r2 := w2 / 2
		if ready {
			ux, uy := y1-y2, x2-x1
			if ux != 0 || uy != 0 {
				ud := jsmath.Hypot(ux, uy)
				ux /= ud
				uy /= ud
				rx, ry := float64(ux*r1), float64(uy*r1)
				t := jsmath.Atan2(uy, ux)
				ctx.MoveTo(x1-rx, y1-ry)
				ctx.LineTo(x2-float64(ux*r2), y2-float64(uy*r2))
				ctx.Arc(x2, y2, r2, t-math.Pi, t, false)
				ctx.LineTo(x1+rx, y1+ry)
				ctx.Arc(x1, y1, r1, t, t+math.Pi, false)
			} else {
				ctx.Arc(x2, y2, r2, 0, tau, false)
			}
			ctx.ClosePath()
		} else {
			ready = true
		}
		x1, y1, r1 = x2, y2, r2
	}
	n := len(items)
	defined0 := false
	for i := 0; i <= n; i++ {
		def := i < n && items[i].Defined != No
		if def != defined0 {
			defined0 = def
			if def {
				ready = false
			}
		}
		if defined0 {
			it := items[i]
			size := it.Size.Zero()
			if size == 0 {
				size = 1
			}
			point(it.OrZero("x"), it.OrZero("y"), size)
		}
	}
	return ctxErr(ctx)
}

// Rectangle draws the item's box at its own position, with per-corner radii.
func Rectangle(ctx PathContext, item *Item) {
	rectangle(ctx, item, item.OrZero("x"), item.OrZero("y"))
}

// RectangleAt draws the item's box (width, height, radii) with its top-left
// corner at (x, y) instead of the item's own position.
func RectangleAt(ctx PathContext, item *Item, x, y float64) {
	rectangle(ctx, item, x, y)
}

// rectC is 1 - c for the Bézier approximation of a quarter circle
// (http://spencermortensen.com/articles/bezier-circle/).
const rectC = 0.448084975506

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(v, hi)) }

func rectangle(ctx PathContext, it *Item, x1, y1 float64) {
	w, h := it.OrZero("width"), it.OrZero("height")
	s := math.Min(w, h) / 2
	// value(item.cornerRadiusX, item.cornerRadius) || 0, then clamped.
	// A word given as a radius is truthy and converts to NaN, which the clamp
	// keeps: the path is drawn with NaN corners, as upstream draws it.
	corner := func(prop string, specific Num) float64 {
		return clamp(it.orZeroProp(prop, specific, "cornerRadius", it.CornerRadius), 0, s)
	}
	tl := corner("cornerRadiusTopLeft", it.CornerRadiusTopLeft)
	tr := corner("cornerRadiusTopRight", it.CornerRadiusTopRight)
	bl := corner("cornerRadiusBottomLeft", it.CornerRadiusBottomLeft)
	br := corner("cornerRadiusBottomRight", it.CornerRadiusBottomRight)

	if tl <= 0 && tr <= 0 && bl <= 0 && br <= 0 {
		ctx.Rect(x1, y1, w, h)
		return
	}
	x2, y2 := x1+w, y1+h
	ctx.MoveTo(x1+tl, y1)
	ctx.LineTo(x2-tr, y1)
	ctx.BezierCurveTo(x2-float64(rectC*tr), y1, x2, y1+float64(rectC*tr), x2, y1+tr)
	ctx.LineTo(x2, y2-br)
	ctx.BezierCurveTo(x2, y2-float64(rectC*br), x2-float64(rectC*br), y2, x2-br, y2)
	ctx.LineTo(x1+bl, y2)
	ctx.BezierCurveTo(x1+float64(rectC*bl), y2, x1, y2-float64(rectC*bl), x1, y2-bl)
	ctx.LineTo(x1, y1+tl)
	ctx.BezierCurveTo(x1, y1+float64(rectC*tl), x1+float64(rectC*tl), y1, x1+tl, y1)
	ctx.ClosePath()
}

// Arc draws a pie/donut sector as d3-shape's arc generator does, including
// pad angle and corner rounding.
func Arc(ctx PathContext, it *Item) error {
	r0 := it.InnerRadius.Zero()
	r1 := it.OuterRadius.Zero()
	a0 := it.StartAngle.Zero() - halfPi
	a1 := it.EndAngle.Zero() - halfPi
	da := math.Abs(a1 - a0)
	cw := a1 > a0

	if r1 < r0 {
		r0, r1 = r1, r0
	}

	const eps = epsilon12
	switch {
	case !(r1 > eps):
		ctx.MoveTo(0, 0)
	case da > tau-eps:
		ctx.MoveTo(float64(r1*jsmath.Cos(a0)), float64(r1*jsmath.Sin(a0)))
		ctx.Arc(0, 0, r1, a0, a1, !cw)
		if r0 > eps {
			ctx.MoveTo(float64(r0*jsmath.Cos(a1)), float64(r0*jsmath.Sin(a1)))
			ctx.Arc(0, 0, r0, a1, a0, cw)
		}
	default:
		arcSector(ctx, it, r0, r1, a0, a1, da, cw)
	}
	ctx.ClosePath()
	return ctxErr(ctx)
}

func arcSector(ctx PathContext, it *Item, r0, r1, a0, a1, da float64, cw bool) {
	const eps = epsilon12
	a01, a11, a00, a10 := a0, a1, a0, a1
	da0, da1 := da, da
	ap := it.PadAngle.Zero() / 2
	var rp float64
	if ap > eps {
		rp = math.Sqrt(float64(r0*r0) + float64(r1*r1))
	}
	rc := math.Min(math.Abs(r1-r0)/2, it.CornerRadius.Zero())
	rc0, rc1 := rc, rc

	// Apply padding? Note that since r1 >= r0, da1 >= da0.
	if rp > eps {
		p0 := dAsin(rp / r0 * jsmath.Sin(ap))
		p1 := dAsin(rp / r1 * jsmath.Sin(ap))
		da0 -= p0 * 2
		if da0 > eps {
			if cw {
				// p0 unchanged
			} else {
				p0 = -p0
			}
			a00 += p0
			a10 -= p0
		} else {
			da0 = 0
			a00 = (a0 + a1) / 2
			a10 = a00
		}
		da1 -= p1 * 2
		if da1 > eps {
			if !cw {
				p1 = -p1
			}
			a01 += p1
			a11 -= p1
		} else {
			da1 = 0
			a01 = (a0 + a1) / 2
			a11 = a01
		}
	}

	x01, y01 := float64(r1*jsmath.Cos(a01)), float64(r1*jsmath.Sin(a01))
	x10, y10 := float64(r0*jsmath.Cos(a10)), float64(r0*jsmath.Sin(a10))

	var x11, y11, x00, y00 float64
	// Apply rounded corners?
	if rc > eps {
		x11, y11 = float64(r1*jsmath.Cos(a11)), float64(r1*jsmath.Sin(a11))
		x00, y00 = float64(r0*jsmath.Cos(a00)), float64(r0*jsmath.Sin(a00))

		// Restrict the corner radius according to the sector angle. If this
		// intersection fails, it's probably because the arc is too small, so
		// disable the corner radius entirely.
		if da < pi {
			if ox, oy, ok := arcIntersect(x01, y01, x00, y00, x11, y11, x10, y10); ok {
				ax, ay := x01-ox, y01-oy
				bx, by := x11-ox, y11-oy
				kc := 1 / jsmath.Sin(dAcos((float64(ax*bx)+float64(ay*by))/(math.Sqrt(float64(ax*ax)+float64(ay*ay))*math.Sqrt(float64(bx*bx)+float64(by*by))))/2)
				lc := math.Sqrt(float64(ox*ox) + float64(oy*oy))
				rc0 = math.Min(rc, (r0-lc)/(kc-1))
				rc1 = math.Min(rc, (r1-lc)/(kc+1))
			} else {
				rc0, rc1 = 0, 0
			}
		}
	}

	// Is the sector collapsed to a line?
	switch {
	case !(da1 > eps):
		ctx.MoveTo(x01, y01)
	case rc1 > eps:
		// The sector's outer ring has rounded corners.
		t0 := cornerTangents(x00, y00, x01, y01, r1, rc1, cw)
		t1 := cornerTangents(x11, y11, x10, y10, r1, rc1, cw)
		ctx.MoveTo(t0.cx+t0.x01, t0.cy+t0.y01)
		if rc1 < rc {
			// The corners have merged.
			ctx.Arc(t0.cx, t0.cy, rc1, jsmath.Atan2(t0.y01, t0.x01), jsmath.Atan2(t1.y01, t1.x01), !cw)
		} else {
			ctx.Arc(t0.cx, t0.cy, rc1, jsmath.Atan2(t0.y01, t0.x01), jsmath.Atan2(t0.y11, t0.x11), !cw)
			ctx.Arc(0, 0, r1, jsmath.Atan2(t0.cy+t0.y11, t0.cx+t0.x11), jsmath.Atan2(t1.cy+t1.y11, t1.cx+t1.x11), !cw)
			ctx.Arc(t1.cx, t1.cy, rc1, jsmath.Atan2(t1.y11, t1.x11), jsmath.Atan2(t1.y01, t1.x01), !cw)
		}
	default:
		ctx.MoveTo(x01, y01)
		ctx.Arc(0, 0, r1, a01, a11, !cw)
	}

	// Is there no inner ring, and it's a circular sector? Or perhaps it's an
	// annular sector collapsed due to padding?
	switch {
	case !(r0 > eps) || !(da0 > eps):
		ctx.LineTo(x10, y10)
	case rc0 > eps:
		t0 := cornerTangents(x10, y10, x11, y11, r0, -rc0, cw)
		t1 := cornerTangents(x01, y01, x00, y00, r0, -rc0, cw)
		ctx.LineTo(t0.cx+t0.x01, t0.cy+t0.y01)
		if rc0 < rc {
			ctx.Arc(t0.cx, t0.cy, rc0, jsmath.Atan2(t0.y01, t0.x01), jsmath.Atan2(t1.y01, t1.x01), !cw)
		} else {
			ctx.Arc(t0.cx, t0.cy, rc0, jsmath.Atan2(t0.y01, t0.x01), jsmath.Atan2(t0.y11, t0.x11), !cw)
			ctx.Arc(0, 0, r0, jsmath.Atan2(t0.cy+t0.y11, t0.cx+t0.x11), jsmath.Atan2(t1.cy+t1.y11, t1.cx+t1.x11), cw)
			ctx.Arc(t1.cx, t1.cy, rc0, jsmath.Atan2(t1.y11, t1.x11), jsmath.Atan2(t1.y01, t1.x01), !cw)
		}
	default:
		ctx.Arc(0, 0, r0, a10, a00, cw)
	}
}

// dAsin and dAcos clamp their argument like d3-shape's math helpers.
func dAsin(x float64) float64 {
	switch {
	case x >= 1:
		return halfPi
	case x <= -1:
		return -halfPi
	}
	return jsmath.Asin(x)
}

func dAcos(x float64) float64 {
	switch {
	case x > 1:
		return 0
	case x < -1:
		return pi
	}
	return jsmath.Acos(x)
}

func arcIntersect(x0, y0, x1, y1, x2, y2, x3, y3 float64) (float64, float64, bool) {
	x10, y10 := x1-x0, y1-y0
	x32, y32 := x3-x2, y3-y2
	t := float64(y32*x10) - float64(x32*y10)
	if t*t < epsilon12 {
		return 0, 0, false
	}
	t = (float64(x32*(y0-y2)) - float64(y32*(x0-x2))) / t
	return x0 + float64(t*x10), y0 + float64(t*y10), true
}

type cornerTan struct{ cx, cy, x01, y01, x11, y11 float64 }

// cornerTangents computes the perpendicular offset line of length rc
// (http://mathworld.wolfram.com/Circle-LineIntersection.html).
func cornerTangents(x0, y0, x1, y1, r1, rc float64, cw bool) cornerTan {
	x01, y01 := x0-x1, y0-y1
	lo := rc / math.Sqrt(float64(x01*x01)+float64(y01*y01))
	if !cw {
		lo = -rc / math.Sqrt(float64(x01*x01)+float64(y01*y01))
	}
	ox, oy := float64(lo*y01), float64(-lo*x01)
	x11, y11 := x0+ox, y0+oy
	x10, y10 := x1+ox, y1+oy
	x00, y00 := (x11+x10)/2, (y11+y10)/2
	dx, dy := x10-x11, y10-y11
	d2 := float64(dx*dx) + float64(dy*dy)
	r := r1 - rc
	D := float64(x11*y10) - float64(x10*y11)
	sgn := 1.0
	if dy < 0 {
		sgn = -1
	}
	d := sgn * math.Sqrt(math.Max(0, float64(r*r*d2)-float64(D*D)))
	cx0 := (float64(D*dy) - float64(dx*d)) / d2
	cy0 := (float64(-D*dx) - float64(dy*d)) / d2
	cx1 := (float64(D*dy) + float64(dx*d)) / d2
	cy1 := (float64(-D*dx) + float64(dy*d)) / d2
	dx0, dy0 := cx0-x00, cy0-y00
	dx1, dy1 := cx1-x00, cy1-y00
	// Pick the closer of the two intersection points.
	if float64(dx0*dx0)+float64(dy0*dy0) > float64(dx1*dx1)+float64(dy1*dy1) {
		cx0, cy0 = cx1, cy1
	}
	return cornerTan{
		cx: cx0, cy: cy0,
		x01: -ox, y01: -oy,
		x11: float64(cx0 * (r1/r - 1)),
		y11: float64(cy0 * (r1/r - 1)),
	}
}

// ---- symbols

// Symbol draws the item's symbol: a built-in shape by name, or a custom SVG
// path scaled to the symbol size, centred at the origin.
func Symbol(ctx PathContext, it *Item) error {
	size := it.Size.Or(64)
	name := it.Shape.Name
	if name == "" {
		name = "circle"
	}
	if drawBuiltinSymbol(ctx, name, size) {
		return ctxErr(ctx)
	}
	cmds, err := customSymbol(name)
	if err != nil {
		return err
	}
	r := math.Sqrt(size) / 2
	if err := RenderPath(ctx, cmds, 0, 0, r, r); err != nil {
		return err
	}
	return ctxErr(ctx)
}

const tan30 = 0.5773502691896257

func drawBuiltinSymbol(ctx PathContext, name string, size float64) bool {
	switch name {
	case "circle":
		r := math.Sqrt(size) / 2
		ctx.MoveTo(r, 0)
		ctx.Arc(0, 0, r, 0, tau, false)
	case "cross":
		r := math.Sqrt(size) / 2
		s := r / 2.5
		ctx.MoveTo(-r, -s)
		ctx.LineTo(-r, s)
		ctx.LineTo(-s, s)
		ctx.LineTo(-s, r)
		ctx.LineTo(s, r)
		ctx.LineTo(s, s)
		ctx.LineTo(r, s)
		ctx.LineTo(r, -s)
		ctx.LineTo(s, -s)
		ctx.LineTo(s, -r)
		ctx.LineTo(-s, -r)
		ctx.LineTo(-s, -s)
		ctx.ClosePath()
	case "diamond":
		r := math.Sqrt(size) / 2
		ctx.MoveTo(-r, 0)
		ctx.LineTo(0, -r)
		ctx.LineTo(r, 0)
		ctx.LineTo(0, r)
		ctx.ClosePath()
	case "square":
		w := math.Sqrt(size)
		x := -w / 2
		ctx.Rect(x, x, w, w)
	case "arrow":
		r := math.Sqrt(size) / 2
		s, t, v := r/7, r/2.5, r/8
		ctx.MoveTo(-s, r)
		ctx.LineTo(s, r)
		ctx.LineTo(s, -v)
		ctx.LineTo(t, -v)
		ctx.LineTo(0, -r)
		ctx.LineTo(-t, -v)
		ctx.LineTo(-s, -v)
		ctx.ClosePath()
	case "wedge":
		r := math.Sqrt(size) / 2
		h := float64(halfSqrt3 * r)
		o := h - float64(r*tan30)
		b := r / 4
		ctx.MoveTo(0, -h-o)
		ctx.LineTo(-b, h-o)
		ctx.LineTo(b, h-o)
		ctx.ClosePath()
	case "triangle":
		r := math.Sqrt(size) / 2
		h := float64(halfSqrt3 * r)
		o := h - float64(r*tan30)
		ctx.MoveTo(0, -h-o)
		ctx.LineTo(-r, h-o)
		ctx.LineTo(r, h-o)
		ctx.ClosePath()
	case "triangle-up":
		r := math.Sqrt(size) / 2
		h := float64(halfSqrt3 * r)
		ctx.MoveTo(0, -h)
		ctx.LineTo(-r, h)
		ctx.LineTo(r, h)
		ctx.ClosePath()
	case "triangle-down":
		r := math.Sqrt(size) / 2
		h := float64(halfSqrt3 * r)
		ctx.MoveTo(0, h)
		ctx.LineTo(-r, -h)
		ctx.LineTo(r, -h)
		ctx.ClosePath()
	case "triangle-right":
		r := math.Sqrt(size) / 2
		h := float64(halfSqrt3 * r)
		ctx.MoveTo(h, 0)
		ctx.LineTo(-h, -r)
		ctx.LineTo(-h, r)
		ctx.ClosePath()
	case "triangle-left":
		r := math.Sqrt(size) / 2
		h := float64(halfSqrt3 * r)
		ctx.MoveTo(-h, 0)
		ctx.LineTo(h, -r)
		ctx.LineTo(h, r)
		ctx.ClosePath()
	case "stroke":
		r := math.Sqrt(size) / 2
		ctx.MoveTo(-r, 0)
		ctx.LineTo(r, 0)
	default:
		return false
	}
	return true
}

// Custom symbol paths come from the specification, so the parse cache is
// bounded: it is dropped wholesale when it grows past customSymbolCacheMax.
const customSymbolCacheMax = 256

var customSymbols struct {
	sync.Mutex
	m map[string][]PathCmd
}

func customSymbol(path string) ([]PathCmd, error) {
	customSymbols.Lock()
	cmds, ok := customSymbols.m[path]
	customSymbols.Unlock()
	if ok {
		return cmds, nil
	}
	cmds, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	customSymbols.Lock()
	if customSymbols.m == nil || len(customSymbols.m) >= customSymbolCacheMax {
		customSymbols.m = make(map[string][]PathCmd)
	}
	customSymbols.m[path] = cmds
	customSymbols.Unlock()
	return cmds, nil
}

// ---- path strings as the SVG renderer emits them

// ItemPathData returns the `d` attribute of a non-nested, non-rect mark item
// (arc, symbol, shape): the generator output, built with d3-shape's default
// three-digit rounding. ok is false when the generator produced nothing
// (upstream emits no `d` then).
func ItemPathData(sp *StringPath, mark MarkType, it *Item) (d []byte, ok bool, err error) {
	sp.Reset()
	sp.SetDigits(3)
	switch mark {
	case MarkShape:
		fn := it.Shape.Func
		if it.Mark != nil && it.Mark.Shape != nil {
			fn = it.Mark.Shape
		}
		if fn == nil {
			return nil, false, ErrNoShapeGenerator
		}
		// `path.context(null)(item)`: the generator formats its own string.
		s := fn(nil, it)
		sp.buf = append(sp.buf[:0], s...)
		return sp.buf, s != "", nil
	case MarkArc:
		err = Arc(sp, it)
	case MarkSymbol:
		err = Symbol(sp, it)
	}
	if err != nil {
		return nil, false, err
	}
	return sp.Bytes(), sp.Len() > 0, nil
}

// MultiPathData returns the `d` attribute of a nested mark (line, area, trail)
// built from all its items. ok is false for an empty path.
func MultiPathData(sp *StringPath, mark MarkType, items []*Item) (d []byte, ok bool, err error) {
	sp.Reset()
	switch mark {
	case MarkLine:
		sp.SetDigits(3)
		err = Line(sp, items)
	case MarkArea:
		sp.SetDigits(3)
		err = Area(sp, items)
	case MarkTrail:
		// vega's trail generator uses d3-path without rounding.
		sp.SetDigits(-1)
		err = Trail(sp, items)
	}
	if err != nil {
		return nil, false, err
	}
	return sp.Bytes(), sp.Len() > 0, nil
}

// RectPathData returns the path data of a rectangle-like item (a rect mark, or
// a group background at offset (x, y) when at is true). Full precision, like
// vega's rectangle generator.
func RectPathData(sp *StringPath, it *Item, at bool, x, y float64) []byte {
	sp.Reset()
	sp.SetDigits(-1)
	if at {
		RectangleAt(sp, it, x, y)
	} else {
		Rectangle(sp, it)
	}
	return sp.Bytes()
}

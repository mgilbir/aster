package transforms

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/jsval"
)

// Note on arithmetic: on some architectures Go fuses x*y+z into a single
// rounding. Upstream's JavaScript rounds each operation, so products that feed
// a sum are wrapped in float64(...) throughout this file to forbid fusing.

// RegressionModel is a fitted curve.
type RegressionModel struct {
	// Coef are the coefficients in the method's own convention (intercept
	// first for linear/quad/poly; [a, b] of a*x^b for pow, and of a*e^(b*x) for exp).
	Coef []float64
	// CoefValues, when not nil, are the coefficients as upstream's array holds
	// them where one is not a number (a polynomial whose order made k a string
	// leaves `k + y` text); it then replaces Coef in the params output.
	CoefValues []jsval.Value
	RSquared   float64
	Predict    func(x float64) float64
}

// MaxPolyOrder bounds the order of the poly method.
const MaxPolyOrder = 100

// regressionMethods lists the methods the transform accepts.
var regressionMethods = map[string]bool{
	"constant": true, "linear": true, "log": true, "exp": true, "pow": true, "quad": true, "poly": true,
}

// FitRegression fits the named method (constant, linear, log, exp, pow, quad,
// poly) to the tuples. Points whose x or y is null or not a number are ignored.
// order only applies to poly.
func FitRegression(method string, data []jsval.Value, x, y Accessor, order float64) (RegressionModel, error) {
	return FitRegressionCtx(context.Background(), method, data, x, y, order)
}

// FitRegressionCtx is FitRegression that stops with the context's error when
// ctx is cancelled (a high-order polynomial fit costs order^2 passes).
func FitRegressionCtx(ctx context.Context, method string, data []jsval.Value, x, y Accessor, order float64) (RegressionModel, error) {
	// Each fit reads every point, some several times: x is read for every
	// point, so polling there polls throughout.
	x, cancelled := polledAccessor(ctx, x)
	m, err := fitRegression(ctx, method, data, x, y, order)
	if cerr := cancelled(); cerr != nil {
		return RegressionModel{}, cerr
	}
	return m, err
}

func fitRegression(ctx context.Context, method string, data []jsval.Value, x, y Accessor, order float64) (RegressionModel, error) {
	switch method {
	case "constant":
		return FitConstant(data, x, y), nil
	case "linear":
		return FitLinear(data, x, y), nil
	case "log":
		return FitLog(data, x, y), nil
	case "exp":
		return FitExp(data, x, y), nil
	case "pow":
		return FitPow(data, x, y), nil
	case "quad":
		return FitQuad(data, x, y), nil
	case "poly":
		return fitPoly(ctx, data, x, y, order)
	}
	return RegressionModel{}, fmt.Errorf("Invalid regression method: %s", method)
}

// polyGeneral is the context key of an order that is no number (see
// RegressionParams.OrderIsNumber), its value the order as given:
// regressionPoly's `order === 0`, 1 and 2 shortcuts fail for it, and its
// k = order + 1 is what JavaScript's + makes (text for a string, an array or
// an object).
type polyGeneral struct{}

// regressionNum is `(u = +u) >= u`: the number, and whether it is not NaN
// (null and undefined are rejected first).
func regressionNum(v jsval.Value) (float64, bool) {
	if v.IsNullish() {
		return 0, false
	}
	f := jsval.ToNumber(v)
	return f, f >= f
}

// regressionVisit is vega-statistics' visitPoints.
func regressionVisit(data []jsval.Value, x, y Accessor, fn func(dx, dy float64)) {
	for _, d := range data {
		u, ok := regressionNum(x(d))
		if !ok {
			continue
		}
		v, ok := regressionNum(y(d))
		if !ok {
			continue
		}
		fn(u, v)
	}
}

type regressionPointSet struct {
	x, y   []float64 // mean centred
	ux, uy float64
}

// regressionPoints is vega-statistics' points(): the valid points, optionally
// sorted by x (stable), mean centred, with the means.
func regressionPoints(data []jsval.Value, x, y Accessor, sortX bool) regressionPointSet {
	type pt struct{ x, y float64 }
	pts := make([]pt, 0, len(data))
	regressionVisit(data, x, y, func(dx, dy float64) { pts = append(pts, pt{dx, dy}) })
	if sortX {
		slices.SortStableFunc(pts, func(a, b pt) int {
			switch {
			case a.x < b.x:
				return -1
			case a.x > b.x:
				return 1
			}
			return 0
		})
	}
	n := len(pts)
	ps := regressionPointSet{x: make([]float64, n), y: make([]float64, n)}
	for i, p := range pts {
		ps.x[i], ps.y[i] = p.x, p.y
		ps.ux += (p.x - ps.ux) / float64(i+1)
		ps.uy += (p.y - ps.uy) / float64(i+1)
	}
	for i := range ps.x {
		ps.x[i] -= ps.ux
		ps.y[i] -= ps.uy
	}
	return ps
}

// regressionOLS is ordinary least squares from the means: [intercept, slope].
// A near-zero variance gives slope 0 rather than a division blow-up.
func regressionOLS(uX, uY, uXY, uX2 float64) (float64, float64) {
	delta := uX2 - float64(uX*uX)
	slope := 0.0
	if !(math.Abs(delta) < 1e-24) {
		slope = (uXY - float64(uX*uY)) / delta
	}
	return uY - float64(slope*uX), slope
}

func regressionRSquared(data []jsval.Value, x, y Accessor, uY float64, predict func(float64) float64) float64 {
	var sse, sst float64
	regressionVisit(data, x, y, func(dx, dy float64) {
		e := dy - predict(dx)
		t := dy - uY
		sse += float64(e * e)
		sst += float64(t * t)
	})
	return 1 - sse/sst
}

// FitConstant is the mean of y (rSquared is 0 by definition).
func FitConstant(data []jsval.Value, x, y Accessor) RegressionModel {
	mean, n := 0.0, 0
	for _, d := range data {
		v := y(d)
		// Upstream skips a null x, a null y and any y that isNaN() (which
		// coerces, so "" and "5" count), and does not check that x is a number.
		if x(d).IsNullish() || v.IsNullish() {
			continue
		}
		f := jsval.ToNumber(v)
		if f != f {
			continue
		}
		n++
		mean += (f - mean) / float64(n)
	}
	return RegressionModel{Coef: []float64{mean}, RSquared: 0, Predict: func(float64) float64 { return mean }}
}

// FitLinear is least-squares line fitting; Coef is [intercept, slope].
func FitLinear(data []jsval.Value, x, y Accessor) RegressionModel {
	var X, Y, XY, X2 float64
	n := 0
	regressionVisit(data, x, y, func(dx, dy float64) {
		n++
		fn := float64(n)
		X += (dx - X) / fn
		Y += (dy - Y) / fn
		XY += (float64(dx*dy) - XY) / fn
		X2 += (float64(dx*dx) - X2) / fn
	})
	a, b := regressionOLS(X, Y, XY, X2)
	predict := func(x float64) float64 { return a + float64(b*x) }
	return RegressionModel{Coef: []float64{a, b}, Predict: predict, RSquared: regressionRSquared(data, x, y, Y, predict)}
}

// FitLog fits y = a + b*ln(x); Coef is [a, b].
func FitLog(data []jsval.Value, x, y Accessor) RegressionModel {
	var X, Y, XY, X2 float64
	n := 0
	regressionVisit(data, x, y, func(dx, dy float64) {
		n++
		fn := float64(n)
		dx = jsmath.Log(dx)
		X += (dx - X) / fn
		Y += (dy - Y) / fn
		XY += (float64(dx*dy) - XY) / fn
		X2 += (float64(dx*dx) - X2) / fn
	})
	a, b := regressionOLS(X, Y, XY, X2)
	predict := func(x float64) float64 { return a + float64(b*jsmath.Log(x)) }
	return RegressionModel{Coef: []float64{a, b}, Predict: predict, RSquared: regressionRSquared(data, x, y, Y, predict)}
}

// FitExp fits y = a*exp(b*x) by the weighted log-linear least squares of
// d3-regression; Coef is [a, b].
func FitExp(data []jsval.Value, x, y Accessor) RegressionModel {
	ps := regressionPoints(data, x, y, false)
	var YL, XY, XYL, X2Y float64
	n := 0
	regressionVisit(data, x, y, func(_, dy float64) {
		dx := ps.x[n]
		n++
		fn := float64(n)
		ly := jsmath.Log(dy)
		xy := float64(dx * dy)
		YL += (float64(dy*ly) - YL) / fn
		XY += (xy - XY) / fn
		XYL += (float64(xy*ly) - XYL) / fn
		X2Y += (float64(dx*xy) - X2Y) / fn
	})
	uy := ps.uy
	c0, c1 := regressionOLS(XY/uy, YL/uy, XYL/uy, X2Y/uy)
	ux := ps.ux
	predict := func(x float64) float64 { return jsmath.Exp(c0 + float64(c1*(x-ux))) }
	return RegressionModel{
		Coef:     []float64{jsmath.Exp(c0 - float64(c1*ux)), c1},
		Predict:  predict,
		RSquared: regressionRSquared(data, x, y, uy, predict),
	}
}

// FitPow fits y = a*x^b in log space; Coef is [a, b].
func FitPow(data []jsval.Value, x, y Accessor) RegressionModel {
	var X, Y, XY, X2, YS float64
	n := 0
	regressionVisit(data, x, y, func(dx, dy float64) {
		lx, ly := jsmath.Log(dx), jsmath.Log(dy)
		n++
		fn := float64(n)
		X += (lx - X) / fn
		Y += (ly - Y) / fn
		XY += (float64(lx*ly) - XY) / fn
		X2 += (float64(lx*lx) - X2) / fn
		YS += (dy - YS) / fn
	})
	c0, c1 := regressionOLS(X, Y, XY, X2)
	// Upstream's predict closes over the coefficient array and reads c0
	// after it has been replaced by exp(c0), so it predicts with exp(c0).
	a := jsmath.Exp(c0)
	predict := func(x float64) float64 { return a * jsmath.Pow(x, c1) }
	return RegressionModel{
		Coef:     []float64{jsmath.Exp(c0), c1},
		Predict:  predict,
		RSquared: regressionRSquared(data, x, y, YS, predict),
	}
}

// FitQuad fits a parabola on mean-centred data; Coef is [c, b, a] for a*x^2+b*x+c.
func FitQuad(data []jsval.Value, x, y Accessor) RegressionModel {
	ps := regressionPoints(data, x, y, false)
	var X2, X3, X4, XY, X2Y float64
	for i := range ps.x {
		dx, dy := ps.x[i], ps.y[i]
		fi := float64(i + 1)
		x2 := float64(dx * dx)
		X2 += (x2 - X2) / fi
		X3 += (float64(x2*dx) - X3) / fi
		X4 += (float64(x2*x2) - X4) / fi
		XY += (float64(dx*dy) - XY) / fi
		X2Y += (float64(x2*dy) - X2Y) / fi
	}
	X2X2 := X4 - float64(X2*X2)
	d := float64(X2*X2X2) - float64(X3*X3)
	a := (float64(X2Y*X2) - float64(XY*X3)) / d
	b := (float64(XY*X2X2) - float64(X2Y*X3)) / d
	c := float64(-a * X2)
	ux, uy := ps.ux, ps.uy
	predict := func(x float64) float64 {
		x = x - ux
		return float64(float64(a*x)*x) + float64(b*x) + c + uy
	}
	return RegressionModel{
		Coef: []float64{
			c - float64(b*ux) + float64(float64(a*ux)*ux) + uy,
			b - float64(float64(2)*a*ux),
			a,
		},
		Predict:  predict,
		RSquared: regressionRSquared(data, x, y, uy, predict),
	}
}

func fitPoly(ctx context.Context, data []jsval.Value, x, y Accessor, order float64) (RegressionModel, error) {
	raw, general := ctx.Value(polyGeneral{}).(jsval.Value)
	if !general {
		switch order {
		case 0:
			return FitConstant(data, x, y), nil
		case 1:
			return FitLinear(data, x, y), nil
		case 2:
			return FitQuad(data, x, y), nil
		}
	}
	// k = order + 1 (JavaScript's +): text where the order is a string, an
	// array or an object, a number otherwise.
	kText, kIsText := "", false
	loop := order + 1 // what `i < k` compares: the number k reads as
	if general {
		w := jsval.WalkerFor(ctx)
		switch raw.Kind() {
		case jsval.KindStr, jsval.KindArr, jsval.KindObj, jsval.KindTimestamp, jsval.KindPattern:
			kText, kIsText = w.String(raw)+"1", true
			loop = jsval.StringToNumber(kText)
		default:
			loop = jsval.ToNumber(raw) + 1
		}
	}
	// Upstream takes any number for the order, k = order + 1 coefficients; once
	// the normal equations are solved it allocates Array(k) for the expanded
	// polynomial, which throws unless k is an integer that is a valid array
	// length. A fractional, NaN or negative-below-minus-one order therefore ends
	// in that RangeError (the system it would have solved first has no effect),
	// and an order of -1 fits nothing: its coefficients are NaN. Text k never
	// throws: Array("21") is the one-element array ["21"].
	if !kIsText {
		if k := loop; k != math.Trunc(k) || k < 0 || k > math.MaxUint32 {
			return RegressionModel{}, errors.New("RangeError: Invalid array length")
		}
	}
	if loop-1 > MaxPolyOrder {
		return RegressionModel{}, fmt.Errorf("regression: poly order %g out of range [-1,%d]", loop-1, MaxPolyOrder)
	}
	ps := regressionPoints(data, x, y, false)
	n := len(ps.x)
	// rows is how often `for (i=0; i<k; ++i)` runs; width is the length of
	// the Float64Array(k) each row is (ToIndex truncates; a fraction below 1,
	// NaN or a negative k never reaches the loop).
	rows, width := 0, 0
	if loop > 0 {
		rows, width = int(math.Ceil(loop)), int(loop)
	}
	matrix := make([][]float64, 0, rows+1)
	lhs := make([]float64, 0, rows)
	for i := 0; i < rows; i++ {
		if err := ctx.Err(); err != nil {
			return RegressionModel{}, err
		}
		v := 0.0
		for l := 0; l < n; l++ {
			v += float64(jsmath.Pow(ps.x[l], float64(i)) * ps.y[l])
		}
		lhs = append(lhs, v)
		c := make([]float64, width)
		for j := 0; j < rows; j++ {
			// One pass over the data per (i, j) pair: poll per pass.
			if err := ctx.Err(); err != nil {
				return RegressionModel{}, err
			}
			v = 0
			for l := 0; l < n; l++ {
				v += jsmath.Pow(ps.x[l], float64(i+j))
			}
			if j < width { // a write past a typed array's end is dropped
				c[j] = v
			}
		}
		matrix = append(matrix, c)
	}
	matrix = append(matrix, lhs)
	var coef []float64
	if width == rows {
		coef = regressionGauss(matrix)
	} else {
		coef = regressionGaussTyped(matrix)
	}
	ux, uy := ps.ux, ps.uy
	predict := func(x float64) float64 {
		x -= ux
		// coef[i] past the end of an order -1 fit is undefined, and NaN with it.
		at := func(i int) float64 {
			if i < len(coef) {
				return coef[i]
			}
			return math.NaN()
		}
		y := uy + at(0) + float64(at(1)*x) + float64(float64(at(2)*x)*x)
		for i := 3; float64(i) < loop; i++ {
			y += float64(at(i) * jsmath.Pow(x, float64(i)))
		}
		return y
	}
	m := RegressionModel{Predict: predict, RSquared: regressionRSquared(data, x, y, uy, predict)}
	switch {
	case !kIsText:
		m.Coef = regressionUncenter(rows, coef, -ux, uy)
	case loop <= 0 || loop != loop:
		// Array(k) holds the text; the loops that would replace it never run,
		// and `z[0] += y` concatenates.
		m.Coef = []float64{math.NaN()}
		m.CoefValues = []jsval.Value{jsval.Str(kText + jsval.WalkerFor(ctx).String(jsval.Num(uy)))}
	case loop != math.Trunc(loop):
		// i = k - 1 is fractional: every z[i] it touches is a property, not
		// an element, so only the zero fill and the bias term remain.
		m.Coef = make([]float64, rows)
		m.Coef[0] = uy
	default:
		m.Coef = regressionUncenter(rows, coef, -ux, uy)
	}
	return m, nil
}

// regressionGaussTyped is regressionGauss for rows that are Float64Arrays
// shorter than the system: a read past the end is undefined (NaN in any
// arithmetic) and a write past it is dropped. The last row (the right-hand
// side) is a plain array of full length.
func regressionGaussTyped(matrix [][]float64) []float64 {
	n := len(matrix) - 1
	get := func(r, c int) float64 {
		if c < len(matrix[r]) {
			return matrix[r][c]
		}
		return math.NaN()
	}
	set := func(r, c int, v float64) {
		if c < len(matrix[r]) {
			matrix[r][c] = v
		}
	}
	coef := make([]float64, n)
	for i := 0; i < n; i++ {
		r := i
		for j := i + 1; j < n; j++ {
			if math.Abs(get(i, j)) > math.Abs(get(i, r)) {
				r = j
			}
		}
		for k := i; k < n+1; k++ {
			t := get(k, i)
			set(k, i, get(k, r))
			set(k, r, t)
		}
		for j := i + 1; j < n; j++ {
			for k := n; k >= i; k-- {
				set(k, j, get(k, j)-float64(get(k, i)*get(i, j))/get(i, i))
			}
		}
	}
	for j := n - 1; j >= 0; j-- {
		t := 0.0
		for k := j + 1; k < n; k++ {
			t += float64(get(k, j) * coef[k])
		}
		coef[j] = (get(n, j) - t) / get(j, j)
	}
	return coef
}

// regressionUncenter expands the polynomial back out of mean-centred space.
func regressionUncenter(k int, a []float64, x, y float64) []float64 {
	if k == 0 {
		return []float64{math.NaN()} // z[0] += y on the empty array
	}
	z := make([]float64, k)
	for i := k - 1; i >= 0; i-- {
		v := a[i]
		c := 1.0
		z[i] += v
		for j := 1; j <= i; j++ {
			c *= float64(i+1-j) / float64(j) // binomial coefficient
			z[i-j] += float64(float64(v*jsmath.Pow(x, float64(j))) * c)
		}
	}
	z[0] += y
	return z
}

// regressionGauss solves the augmented system as upstream does. matrix holds
// k columns-as-rows followed by the right-hand side; a singular system yields
// NaN/Inf coefficients, not an error.
func regressionGauss(matrix [][]float64) []float64 {
	n := len(matrix) - 1
	coef := make([]float64, n)
	for i := 0; i < n; i++ {
		r := i
		for j := i + 1; j < n; j++ {
			if math.Abs(matrix[i][j]) > math.Abs(matrix[i][r]) {
				r = j
			}
		}
		for k := i; k < n+1; k++ {
			matrix[k][i], matrix[k][r] = matrix[k][r], matrix[k][i]
		}
		for j := i + 1; j < n; j++ {
			for k := n; k >= i; k-- {
				matrix[k][j] -= float64(matrix[k][i]*matrix[i][j]) / matrix[i][i]
			}
		}
	}
	for j := n - 1; j >= 0; j-- {
		t := 0.0
		for k := j + 1; k < n; k++ {
			t += float64(matrix[k][j] * coef[k])
		}
		coef[j] = (matrix[n][j] - t) / matrix[j][j]
	}
	return coef
}

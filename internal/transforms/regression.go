package transforms

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/aster/internal/jsval"
)

// RegressionParams configures Regression (vega-regression's regression).
type RegressionParams struct {
	X, Y    Field
	GroupBy []Field
	// Method is constant, linear, log, exp, pow, quad or poly; empty means linear.
	Method string
	// Order is the polynomial order for poly. Use DefaultRegressionOrder when
	// the specification gave none. Upstream takes any number: only an integer
	// of at least -1 gets a fit (see fitPoly).
	Order float64
	// Extent is the x domain of the drawn curve; nil computes it per group.
	Extent []float64
	// ExtentValues, when not nil, is the extent as given, which replaces
	// Extent: a linear or constant fit draws each of its elements as is
	// (null stays null), a curve samples between its first two.
	ExtentValues []jsval.Value
	// ExtentNotArray is set when the extent was given as something other than
	// an array (a signal answering a string, say): upstream draws a linear or
	// constant fit with dom.forEach, which only an array has.
	ExtentNotArray bool
	// Strict, when set, reports the method as upstream compares it:
	// MethodIsString is false for a method that is no string (["poly"] fits
	// the polynomial its text names, but no `method === 'poly'` holds: one
	// degree of freedom, a sampled curve, no log extent check). An Order
	// that is no number (OrderIsNumber false) skips the polynomial's order
	// shortcuts, with OrderValue the number the degrees of freedom compare.
	Strict         bool
	MethodIsString bool
	OrderIsNumber  bool
	// OrderValue is the order as a number (Order is then k - 1); with
	// OrderIsNumber false the degrees of freedom are converted from OrderRaw
	// where they are compared.
	OrderValue float64
	// OrderRaw is the order as given, read when OrderIsNumber is false:
	// regressionPoly's `order + 1` concatenates for a string, array or object.
	OrderRaw jsval.Value
	// Params outputs one coefficient tuple per group instead of curve points.
	Params bool
	// As names the output x and y fields; nil or short means the accessor names.
	As []string
}

// DefaultRegressionOrder is upstream's default polynomial order.
const DefaultRegressionOrder = 3

// Regression fits each group and emits either the trend line or the fit
// parameters. Groups with no more points than parameters are skipped.
// Linear and constant fits emit only the two extent end points; other methods
// emit an adaptively sampled curve (25 to 200 points).
func Regression(ctx context.Context, data []jsval.Value, p RegressionParams) ([]jsval.Value, error) {
	method := p.Method
	if method == "" && !p.Strict { // Strict: the caller applied `|| 'linear'`
		method = "linear"
	}
	if !regressionMethods[method] {
		return nil, fmt.Errorf("Invalid regression method: %s", method)
	}
	// is reports `method === m`.
	is := func(m string) bool { return method == m && (!p.Strict || p.MethodIsString) }
	// An order beyond the limit is only an error for a group that has enough
	// points to fit it (FitRegression checks); smaller groups are skipped.
	dof := 1.0
	switch {
	case is("poly"):
		dof = p.Order
		if p.Strict && !p.OrderIsNumber {
			dof = jsval.ToNumber(p.OrderRaw) // `n <= order`
		}
	case is("quad"):
		dof = 2
	}
	var domain []jsval.Value
	if p.ExtentValues != nil {
		domain = p.ExtentValues
	} else if p.Extent != nil {
		domain = make([]jsval.Value, len(p.Extent))
		for i, d := range p.Extent {
			domain[i] = jsval.Num(d)
		}
	}
	// method === 'log' && domain[0] <= 0
	if domain != nil && is("log") && LessEq(jsval.WalkerFor(ctx), firstOf(domain), jsval.Num(0)) {
		domain = nil // upstream warns and ignores it
	}
	fitCtx := ctx
	if p.Strict && !p.OrderIsNumber {
		fitCtx = context.WithValue(ctx, polyGeneral{}, p.OrderRaw)
	}
	names := make([]string, len(p.GroupBy))
	for i, g := range p.GroupBy {
		names[i] = g.Key()
	}
	as0, as1 := p.X.Key(), p.Y.Key()
	if len(p.As) > 0 {
		as0 = p.As[0]
	}
	if len(p.As) > 1 {
		as1 = p.As[1]
	}
	groups := regressionGroups(keysOf(ctx), data, p.GroupBy)
	var out []jsval.Value
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if float64(len(g.Tuples)) <= dof {
			continue
		}
		// A fitted curve is at most 200 points (two for a line).
		if err := reserveRows(ctx, len(out)+201); err != nil {
			return nil, err
		}
		model, err := FitRegressionCtx(fitCtx, method, g.Tuples, p.X.Get, p.Y.Get, p.Order)
		if err != nil {
			return nil, err
		}
		if p.Params {
			o := jsval.NewObject(3)
			if g.Dims != nil {
				o.Set("keys", jsval.Arr(g.Dims))
			}
			coef := model.CoefValues
			if coef == nil {
				coef = make([]jsval.Value, len(model.Coef))
				for i, c := range model.Coef {
					coef[i] = jsval.Num(c)
				}
			}
			o.Set("coef", jsval.Arr(coef))
			o.Set("rSquared", jsval.Num(model.RSquared))
			out = append(out, jsval.Obj(o))
			continue
		}
		var dom []jsval.Value
		if p.ExtentNotArray && (is("linear") || is("constant")) {
			return nil, errors.New("TypeError: dom.forEach is not a function")
		}
		if domain != nil {
			dom = domain
		} else {
			lo, hi, _, err := ExtentOf(jsval.WalkerFor(ctx), g.Tuples, p.X)
			if err != nil {
				return nil, err
			}
			dom = []jsval.Value{lo, hi}
		}
		add := func(x jsval.Value, y float64) {
			t := jsval.NewObject(len(names) + 2)
			for i, n := range names {
				t.Set(n, g.Dims[i])
			}
			t.Set(as0, x)
			t.Set(as1, jsval.Num(y))
			out = append(out, jsval.Obj(t))
		}
		if is("linear") || is("constant") {
			for _, x := range dom {
				add(x, model.Predict(jsval.ToNumber(x)))
			}
		} else {
			// sampleCurve(model.predict, dom, 25, 200) with the extent as it
			// is: an end that is a date, a string or an array adds as text.
			d0, d1 := jsval.Undefined, jsval.Undefined
			if len(dom) > 0 {
				d0 = dom[0]
			}
			if len(dom) > 1 {
				d1 = dom[1]
			}
			pts, err := sampleCurveJS(jsval.WalkerFor(ctx), model.Predict, d0, d1, StepsSpec{Min: jsval.Num(25), Max: jsval.Num(200)})
			if err != nil {
				return nil, err
			}
			for _, pt := range pts {
				add(pt.x, pt.y)
			}
		}
	}
	return out, nil
}

// regressionGroups is upstream's partition: no group-by (nil) is one group of
// everything; an explicitly empty list is one empty-dims group when there is
// data and none otherwise.
func regressionGroups(k Keys, data []jsval.Value, groupby []Field) []Group {
	if groupby != nil && len(groupby) == 0 {
		if len(data) == 0 {
			return nil
		}
		return []Group{{Dims: []jsval.Value{}, Tuples: data}}
	}
	return Partition(k, data, groupby)
}

// firstOf is a list's first element, undefined for none.
func firstOf(l []jsval.Value) jsval.Value {
	if len(l) == 0 {
		return jsval.Undefined
	}
	return l[0]
}

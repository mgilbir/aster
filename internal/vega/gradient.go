package vega

import (
	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

// scaleGradient is vega-functions' gradient(scale, p0, p1, count): a linear
// gradient whose stops sample the scale at its ticks (plus the domain ends).
func scaleGradient(v *runView, s scale.Scale, p0, p1, count jsval.Value) jsval.Value {
	ctx := v.ctx
	pt := func(p jsval.Value, i int, def float64) jsval.Value {
		if p.IsTruthy() {
			return p.Index(i)
		}
		return jsval.Num(def)
	}
	// `stops = scale.domain()`: every domain value is a stop unless the
	// scale has ticks (C32): a threshold, quantile or bin-ordinal scale
	// keeps its interior stops. The domain is a copy, charged as it is
	// handed to the expression.
	stops := s.Domain()
	v.chargeItems(len(stops))
	min, max := jsval.Undefined, jsval.Undefined
	if len(stops) > 0 {
		min, max = stops[0], stops[len(stops)-1]
	}
	fraction := func(v jsval.Value) jsval.Value { return v }

	span := jsval.ToNumber(max) - jsval.ToNumber(min)
	if span == 0 || span != span {
		// A zero-span domain is expanded so the gradient still shows the
		// ramp: `scale.interpolator ? sequential().interpolator(…) :
		// linear().interpolate(scale.interpolate()).range(scale.range())`.
		// A scale with neither (an ordinal, band or discretizing scale)
		// throws there upstream.
		var ns scale.Scale
		if in, ok := s.(interface {
			Interpolator() scale.UnitInterpolator
		}); ok {
			seq := scale.NewSequential()
			seq.SetInterpolator(in.Interpolator())
			ns = seq
		} else {
			ip, ok := s.(scale.Interpolating)
			if !ok {
				panic(&expr.Error{Name: "TypeError", Msg: "scale.interpolate is not a function"})
			}
			lin := scale.NewLinear()
			lin.SetInterpolate(ip.Interpolate())
			lin.SetRange(s.Range())
			ns = lin
		}
		min, max = jsval.Num(0), jsval.Num(1)
		ns.SetDomain([]jsval.Value{min, max})
		s = ns
	} else {
		fraction = scale.ScaleFraction(s, min, max)
	}

	if t, ok := s.(scale.Ticker); ok {
		n := jsval.ToNumber(count)
		if n == 0 || n != n {
			n = 15
		}
		ticks, err := t.Ticks(ctx, scale.Count(n))
		if err != nil {
			// a limit or the end of the call, raised through the
			// expression that called gradient()
			panic(&budget.Stop{Err: err})
		}
		v.chargeItems(len(ticks) + 2)
		stops = make([]jsval.Value, 0, len(ticks)+2)
		for _, f := range ticks {
			if min.IsTimestamp() {
				stops = append(stops, jsval.Timestamp(f))
			} else {
				stops = append(stops, jsval.Num(f))
			}
		}
		// Dates compare by identity upstream, so the ends are always added.
		if len(stops) == 0 || min.IsTimestamp() || !jsval.SameRef(min, stops[0]) {
			stops = append([]jsval.Value{min}, stops...)
		}
		if min.IsTimestamp() || !jsval.SameRef(max, stops[len(stops)-1]) {
			stops = append(stops, max)
		}
	}
	// Each stop is an object of two members.
	v.chargeItems(4 * len(stops))
	out := make([]jsval.Value, len(stops))
	for i, x := range stops {
		out[i] = obj("offset", fraction(x), "color", s.Apply(x))
	}
	return obj(
		"gradient", sv("linear"),
		"x1", pt(p0, 0, 0), "y1", pt(p0, 1, 0),
		"x2", pt(p1, 0, 1), "y2", pt(p1, 1, 0),
		"stops", jsval.Arr(out),
	)
}

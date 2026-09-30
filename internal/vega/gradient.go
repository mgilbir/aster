package vega

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

// scaleGradient is vega-functions' gradient(scale, p0, p1, count): a linear
// gradient whose stops sample the scale at its ticks (plus the domain ends).
func scaleGradient(s scale.Scale, p0, p1, count jsval.Value) jsval.Value {
	pt := func(p jsval.Value, i int, def float64) jsval.Value {
		if p.IsTruthy() {
			return p.Index(i)
		}
		return jsval.Num(def)
	}
	domain := s.Domain()
	if len(domain) == 0 {
		return jsval.Undefined
	}
	min, max := domain[0], domain[len(domain)-1]
	fraction := func(v jsval.Value) jsval.Value { return v }

	span := jsval.ToNumber(max) - jsval.ToNumber(min)
	if span == 0 || span != span {
		// A zero-span domain is expanded so the gradient still shows the ramp.
		var ns scale.Scale
		if in, ok := s.(interface {
			Interpolator() scale.UnitInterpolator
		}); ok {
			seq := scale.NewSequential()
			seq.SetInterpolator(in.Interpolator())
			ns = seq
		} else {
			lin := scale.NewLinear()
			if ip, ok := s.(scale.Interpolating); ok {
				lin.SetInterpolate(ip.Interpolate())
			}
			lin.SetRange(s.Range())
			ns = lin
		}
		min, max = jsval.Num(0), jsval.Num(1)
		ns.SetDomain([]jsval.Value{min, max})
		s = ns
	} else {
		fraction = scale.ScaleFraction(s, jsval.ToNumber(min), jsval.ToNumber(max))
	}

	stops := []jsval.Value{min, max}
	if t, ok := s.(scale.Ticker); ok {
		n := jsval.ToNumber(count)
		if n == 0 || n != n {
			n = 15
		}
		ticks := t.Ticks(scale.Count(n))
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
	out := make([]jsval.Value, len(stops))
	for i, v := range stops {
		out[i] = obj("offset", fraction(v), "color", s.Apply(v))
	}
	return obj(
		"gradient", sv("linear"),
		"x1", pt(p0, 0, 0), "y1", pt(p0, 1, 0),
		"x2", pt(p1, 0, 1), "y2", pt(p1, 1, 0),
		"stops", jsval.Arr(out),
	)
}

package transforms

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// This file is vega-statistics' sampleCurve over a domain and step bounds of
// any type, as JavaScript computes it: the domain's ends are the samples'
// first and last x as they are (a string, an array, undefined), the grid's
// x are `minX + (i / minSteps) * span` (a string minX concatenates), a
// midpoint is `(p0[0] + p1[0]) / 2`, and the uniform grid is drawn only when
// `minSteps === maxSteps` holds strictly (a string minsteps never equals the
// number Math.max makes). Numbers keep SampleCurve.

// StepsSpec is a density's step bounds as values: `_.steps || _.minsteps ||
// 25` and `_.steps || _.maxsteps || 200` (a kde with a shared resolve makes
// both `_.steps || maxsteps`).
type StepsSpec struct{ Min, Max jsval.Value }

// stepsSpecOf is StepsSpec of the numeric step parameters (0 or NaN unset).
func stepsSpecOf(steps, minsteps, maxsteps float64) StepsSpec {
	lo, hi := stepBounds(steps, minsteps, maxsteps)
	return StepsSpec{Min: jsval.Num(lo), Max: jsval.Num(hi)}
}

// curvePoint is a sample: x as upstream holds it, and its curve value.
type curvePoint struct {
	x  jsval.Value
	xn float64 // x as a number
	y  float64
}

// maxSamplesOf is maxSamples of step bounds of any type.
func maxSamplesOf(s StepsSpec) int {
	minS, maxS := s.Min, s.Max
	if !minS.IsTruthy() {
		minS = jsval.Num(25)
	}
	if !maxS.IsTruthy() {
		maxS = jsval.Num(200)
	}
	return maxSamples(jsval.ToNumber(minS), jsval.ToNumber(maxS))
}

// sampleCurveJS is sampleCurve(f, [lo, hi], s.Min, s.Max) with JavaScript's
// operators; numbers take SampleCurve.
func sampleCurveJS(w jsval.Walker, f func(float64) float64, lo, hi jsval.Value, s StepsSpec) ([]curvePoint, error) {
	minS := s.Min
	if !minS.IsTruthy() {
		minS = jsval.Num(25)
	}
	maxS := s.Max
	if !maxS.IsTruthy() {
		maxS = jsval.Num(200)
	}
	if lo.IsNum() && hi.IsNum() && minS.IsNum() && maxS.IsNum() {
		pts, err := SampleCurve(f, [2]float64{lo.NumValue(), hi.NumValue()}, minS.NumValue(), maxS.NumValue())
		if err != nil {
			return nil, err
		}
		out := make([]curvePoint, len(pts))
		for i, p := range pts {
			out[i] = curvePoint{x: jsval.Num(p[0]), xn: p[0], y: p[1]}
		}
		return out, nil
	}
	minN := jsval.ToNumber(minS)
	maxN := math.Max(minN, jsval.ToNumber(maxS))
	if maxN > MaxSteps {
		return nil, limitErr("steps", maxN, MaxSteps)
	}
	point := func(x jsval.Value) curvePoint {
		xn := jsval.ToNumber(x)
		return curvePoint{x: x, xn: xn, y: f(xn)}
	}
	minX := jsval.ToNumber(lo)
	span := jsval.ToNumber(hi) - minX
	stop := span / maxN
	prev := []curvePoint{point(lo)}
	var next []curvePoint
	at := func(i float64) jsval.Value { return jsAdd(w, lo, jsval.Num(float64((i/minN)*span))) }
	if minS.IsNum() && minN == maxN {
		for i := 1.0; i < maxN; i++ {
			prev = append(prev, point(at(i)))
		}
		return append(prev, point(hi)), nil
	}
	next = append(next, point(hi))
	for i := minN - 1; i > 0; i-- {
		next = append(next, point(at(i)))
	}
	p0 := prev[0]
	sx := 1 / span
	ys := make([][2]float64, len(next))
	for i, p := range next {
		ys[i] = [2]float64{p.xn, p.y}
	}
	sy := curveScaleY(p0.y, ys)
	budget := 8 * MaxSteps
	for len(next) > 0 {
		if budget--; budget < 0 {
			return nil, limitErr("curve samples", 8*MaxSteps+1, 8*MaxSteps)
		}
		p1 := next[len(next)-1]
		pm := point(jsval.Num(jsval.ToNumber(jsAdd(w, p0.x, p1.x)) / 2))
		dx := pm.xn-p0.xn >= stop
		if dx && curveAngleDelta([2]float64{p0.xn, p0.y}, [2]float64{pm.xn, pm.y}, [2]float64{p1.xn, p1.y}, sx, sy) > minRadians {
			next = append(next, pm)
		} else {
			p0 = p1
			prev = append(prev, p1)
			next = next[:len(next)-1]
		}
	}
	return prev, nil
}

// rawExtent is vega-util's extent over raw values: the least and greatest
// value as they are (undefined for none).
func rawExtent(w jsval.Walker, vs []jsval.Value) (lo, hi jsval.Value) {
	l, h, ok := Extent(w, len(vs), func(i int) jsval.Value { return vs[i] })
	if !ok {
		return jsval.Undefined, jsval.Undefined
	}
	return l, h
}

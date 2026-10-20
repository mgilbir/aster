package transforms

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// This file is vega-statistics' bin() and the Bin transform's _bins for
// parameters of any type, as JavaScript computes them: a truthy step or a
// step of steps is used as it is, and with nice off the extent's ends are
// too, so `start + ...` concatenates when one is a string (or an array, an
// object: their text), and the bins become strings. Bin keeps the numeric
// computation for numbers.

// BinSpec is a bin transform's parameters as values: Undefined where the
// specification gave none.
type BinSpec struct {
	MaxBins, Base, Span, Step, MinStep, Nice jsval.Value
	// Extent0 and Extent1 are _.extent[0] and _.extent[1].
	Extent0, Extent1 jsval.Value
	// Divide is `_.divide || [5, 2]` (nil: the default) and Steps _.steps
	// when truthy (nil otherwise), each as the elements an index reads and
	// the length a loop runs to (NaN when it has none). An index past the
	// elements reads undefined, which changes nothing in either loop, so
	// they stop at the last element.
	Divide, Steps       []jsval.Value
	DivideLen, StepsLen float64
}

// jsAdd is JavaScript's a + b: concatenation when either operand is a string
// or an object (an array, a date: what ToPrimitive makes text of), numeric
// addition otherwise.
func jsAdd(w jsval.Walker, a, b jsval.Value) jsval.Value {
	if a.IsNum() && b.IsNum() {
		return jsval.Num(a.NumValue() + b.NumValue())
	}
	if addsText(a) || addsText(b) {
		return jsval.Str(w.String(a) + w.String(b))
	}
	return jsval.Num(jsval.ToNumber(a) + jsval.ToNumber(b))
}

// addsText reports a value + makes text of.
func addsText(v jsval.Value) bool {
	switch v.Kind() {
	case jsval.KindStr, jsval.KindArr, jsval.KindObj, jsval.KindTimestamp, jsval.KindPattern:
		return true
	}
	return false
}

func num(v jsval.Value) float64 { return jsval.ToNumber(v) }

// BinJS is bin(_) for parameters of any type (see BinSpec): the start, stop
// and step it returns, numbers unless the specification's values are kept
// (a step, a step of steps, the extent with nice off). The bounds Bin
// enforces hold here too: a maxbins above MaxBins and a base of at most 1
// are refused, and the step search is bounded.
func BinJS(w jsval.Walker, s BinSpec) (start, stop, step jsval.Value, err error) {
	maxb := s.MaxBins
	if !maxb.IsTruthy() {
		maxb = jsval.Num(20)
	}
	maxbN := num(maxb)
	if maxbN > MaxBins {
		return jsval.Undefined, jsval.Undefined, jsval.Undefined, limitErr("maxbins", maxbN, MaxBins)
	}
	base := s.Base
	if !base.IsTruthy() {
		base = jsval.Num(10)
	}
	baseN := num(base)
	logb := jsmath.Log(baseN)
	min, max := s.Extent0, s.Extent1
	span := s.Span
	if !span.IsTruthy() {
		if d := num(max) - num(min); d != 0 && !math.IsNaN(d) {
			span = jsval.Num(d)
		} else if a := math.Abs(num(min)); a != 0 && !math.IsNaN(a) {
			span = jsval.Num(a)
		} else {
			span = jsval.Num(1)
		}
	}
	spanN := num(span)

	switch {
	case s.Step.IsTruthy():
		step = s.Step
	case s.Steps != nil:
		v := jsval.Num(spanN / maxbN)
		i := 0
		for n := s.StepsLen; float64(i) < n && i < len(s.Steps) && Less(w, s.Steps[i], v); i++ {
		}
		step = jsval.Undefined
		if k := max0(i - 1); k < len(s.Steps) {
			step = s.Steps[k]
		}
	default:
		if !(baseN > 1) || math.IsInf(baseN, 1) {
			// See Bin: upstream computes NaN or searches forever.
			return jsval.Undefined, jsval.Undefined, jsval.Undefined, errBinBase(baseN)
		}
		level := math.Ceil(jsmath.Log(maxbN) / logb)
		minstep := 0.0
		if s.MinStep.IsTruthy() {
			minstep = num(s.MinStep)
		}
		st := math.Max(minstep, jsmath.Pow(baseN, binJSRound(jsmath.Log(spanN)/logb)-level))
		for n := 0; math.Ceil(spanN/st) > maxbN; n++ {
			if n > maxStepSearch || st == 0 {
				return jsval.Undefined, jsval.Undefined, jsval.Undefined, errBinSearch(maxbN, baseN)
			}
			st *= baseN
		}
		div, divLen := []jsval.Value{jsval.Num(5), jsval.Num(2)}, 2.0
		if s.Divide != nil {
			div, divLen = s.Divide, s.DivideLen
		}
		for i := 0; float64(i) < divLen && i < len(div); i++ {
			if v := st / num(div[i]); v >= minstep && spanN/v <= maxbN {
				st = v
			}
		}
		step = jsval.Num(st)
	}

	stepN := num(step)
	v := jsmath.Log(stepN)
	precision := 0.0
	if !(v >= 0) {
		precision = binToInt32(-v/logb) + 1
	}
	eps := jsmath.Pow(baseN, -precision-1)
	if s.Nice.IsTruthy() || s.Nice.IsUndefined() {
		minN := num(min)
		v = float64(math.Floor(minN/stepN+eps) * stepN)
		if minN < v {
			min = jsval.Num(v - stepN)
		} else {
			min = jsval.Num(v)
		}
		max = jsval.Num(math.Ceil(num(max)/stepN) * stepN)
	}
	stop = max
	if jsval.SameRef(max, min) { // max === min
		stop = jsAdd(w, min, step)
	}
	return min, stop, step, nil
}

func max0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

// binBounds is the Bin transform's _bins of bin(_)'s start, stop and step:
// the stop at a whole number of steps, both shifted to the anchor a (nil
// for none).
func binBounds(w jsval.Walker, start, stop, step jsval.Value, a *jsval.Value) (jsval.Value, jsval.Value) {
	stepN := num(step)
	// stop = start + Math.ceil((bins.stop - start) / step) * step
	stop = jsAdd(w, start, jsval.Num(math.Ceil((num(stop)-num(start))/stepN)*stepN))
	if a != nil {
		aN := num(*a)
		// d = a - (start + step * Math.floor((a - start) / step))
		d := jsval.Num(aN - num(jsAdd(w, start, jsval.Num(stepN*math.Floor((aN-num(start))/stepN)))))
		start = jsAdd(w, start, d)
		stop = jsAdd(w, stop, d)
	}
	return start, stop
}

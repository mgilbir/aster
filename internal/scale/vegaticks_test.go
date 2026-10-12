package scale

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// minStepLoop is vega-scale's tickCount loop as upstream writes it, for
// counts small enough to run it.
func minStepLoop(lo, hi, count, minStep float64) float64 {
	for count > 1 && TickStep(lo, hi, count) < minStep {
		count--
	}
	return count
}

// TestMinStepReductionMatchesTheLoop is C14 (ADR 0011): the bisection gives
// the count upstream's linear loop gives.
func TestMinStepReductionMatchesTheLoop(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		lo := (r.Float64() - 0.5) * math.Pow(10, float64(r.Intn(12)-4))
		hi := lo + r.Float64()*math.Pow(10, float64(r.Intn(14)-6))
		count := float64(r.Intn(3000)) + []float64{0, 0.5, 0.25, 0.75}[r.Intn(4)]
		minStep := (hi - lo) / (r.Float64() * float64(1+r.Intn(4000)))
		if r.Intn(4) == 0 {
			minStep = TickStep(lo, hi, float64(1+r.Intn(500))) // on a step boundary
		}
		want := minStepLoop(lo, hi, count, minStep)
		got, err := reduceForMinStep(count, func(c float64) bool { return TickStep(lo, hi, c) < minStep })
		if err != nil || !(got == want || got != got && want != want) {
			t.Fatalf("lo=%v hi=%v count=%v minStep=%v: got %v, %v; the loop gives %v", lo, hi, count, minStep, got, err, want)
		}
	}
}

// TestMinStepHugeCount is C14: a count of 1e9 or more with a small minimum
// step is answered at once, with the count upstream's loop reaches or a
// limit error when that count is past MaxTicks.
func TestMinStepHugeCount(t *testing.T) {
	s := NewLinear()
	s.SetType(TypeLinear)
	s.SetDomain([]jsval.Value{jsval.Num(0), jsval.Num(1)})
	for _, c := range []struct {
		count, minStep float64
		want           float64 // 0: a limit error
	}{
		{1e9, 1.3e-9, 0},                        // upstream stops at 7.07e8, past MaxTicks
		{1e12, 1e-12, 1e12},                     // the step is already the minimum: no loop
		{1e300, 1e-300, 9.999999999999999e+299}, // node
		{1e300, 1.3e-300, 0},                    // a loop --count cannot shorten
		{1e9, 0.3, 3},                           // capped by the span first: floor(1/0.3)+1 = 4, then 4 -> 3
		{1e9, 1e-4, 10001},
		{1e9, 1e-3, 1001},
		{1e9, 1.5e-3, 667},
	} {
		tc, err := TickCountFor(s, jsval.Num(c.count), jsval.Num(c.minStep))
		switch {
		case c.want == 0 && !errors.Is(err, budget.ErrLimit):
			t.Errorf("tickCount(%v, %v) = %+v, %v; want a limit error", c.count, c.minStep, tc, err)
		case c.want != 0 && (err != nil || tc.N != c.want):
			t.Errorf("tickCount(%v, %v) = %+v, %v; want %v", c.count, c.minStep, tc, err, c.want)
		}
	}
}

// TestMinStepReductionIsLogarithmic is C14's work guard, counted rather than
// timed: the reduction evaluates the tick step a few dozen times however
// large the count, where upstream's loop evaluates it once per count it
// passes (700 million times for tickCount 1e9 at tickMinStep 1.3e-9).
func TestMinStepReductionIsLogarithmic(t *testing.T) {
	for _, c := range []struct{ count, minStep float64 }{
		{1e9, 1.3e-9}, {1e9, 1e-4}, {1e9, 1e-3}, {MaxTicks + 0.5, 1e-9}, {1e15, 1e-12}, {5000, 1e-9},
	} {
		calls := 0
		below := func(n float64) bool {
			if calls++; calls > 200 {
				panic("more than 200 tick steps")
			}
			return TickStep(0, 1, n) < c.minStep
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("count %v, minStep %v: %v", c.count, c.minStep, r)
				}
			}()
			_, _ = reduceForMinStep(math.Min(c.count, math.Floor(1/c.minStep)+1), below)
		}()
	}
}

// TestTickCountPassesValuesThrough is C101: the one tickCount follows
// upstream for every kind of count (values from node, vega-scale 8.1.3).
func TestTickCountPassesValuesThrough(t *testing.T) {
	lin := NewLinear()
	lin.SetType(TypeLinear)
	u := NewUTC()
	u.SetType(TypeUTC)
	obj := func(kv ...any) jsval.Value {
		o := jsval.NewObject(2)
		for i := 0; i < len(kv); i += 2 {
			o.Set(kv[i].(string), kv[i+1].(jsval.Value))
		}
		return jsval.Obj(o)
	}
	none := jsval.Undefined
	// true is the number 1 wherever upstream uses the count
	if tc, err := TickCountFor(lin, jsval.True, none); err != nil || !tc.HasN || tc.N != 1 {
		t.Errorf("tickCount(true) = %+v, %v", tc, err)
	}
	// every("0") is null: the default count
	if tc, err := TickCountFor(u, obj("interval", jsval.Str("day"), "step", jsval.Str("0")), none); err != nil || tc.HasN || tc.Interval != nil {
		t.Errorf("tickCount({day, step '0'}) = %+v, %v; want unspecified", tc, err)
	}
	// every(-2) is null too
	if tc, err := TickCountFor(u, obj("interval", jsval.Str("day"), "step", jsval.Num(-2)), none); err != nil || tc.Interval != nil {
		t.Errorf("tickCount({day, step -2}) = %+v, %v; want unspecified", tc, err)
	}
	// an unknown unit is undefined: the default count
	if tc, err := TickCountFor(u, jsval.Str("fortnight"), none); err != nil || tc.HasN || tc.Interval != nil || tc.Null {
		t.Errorf("tickCount('fortnight') = %+v, %v; want undefined", tc, err)
	}
	// ... and undefined.every(2) throws, as does the quarter's every
	if _, err := TickCountFor(u, obj("interval", jsval.Str("fortnight"), "step", jsval.Num(2)), none); err == nil {
		t.Error("tickCount({fortnight, step 2}) must throw")
	}
	if _, err := TickCountFor(u, obj("interval", jsval.Str("quarter"), "step", jsval.Num(2)), none); err == nil {
		t.Error("tickCount({quarter, step 2}) must throw")
	}
	// {interval: null} is null, which arithmetic reads as 0; {} is undefined
	if tc, _ := TickCountFor(u, obj("interval", jsval.Null), none); !tc.Null {
		t.Errorf("tickCount({interval: null}) = %+v; want null", tc)
	}
	if tc, _ := TickCountFor(u, obj(), none); tc.Null || tc.HasN {
		t.Errorf("tickCount({}) = %+v; want undefined", tc)
	}
}

// TestUndefinedCountHidesLogLabels is C101: upstream's tickLog with an
// undefined count computes k = NaN and hides every label (node:
// tickFormat(locale, log, undefined) gives "" for 1..100, labelValues
// gives []), where true labels them all.
func TestUndefinedCountHidesLogLabels(t *testing.T) {
	log := NewLog()
	log.SetType(TypeLog)
	log.SetDomain([]jsval.Value{jsval.Num(1), jsval.Num(1000)})
	loc := format.DefaultLocale()
	ctx := context.Background()
	f, err := TickFormat(ctx, loc, log, TickCount{}, jsval.Null, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := f(jsval.Num(10)).AsString(); got != "" {
		t.Errorf("label of 10 with an undefined count = %q, want none", got)
	}
	if l, err := LabelValues(ctx, log, TickCount{}); err != nil || len(l.Values) != 0 {
		t.Errorf("labelValues = %v, %v; want none", l.Values, err)
	}
	f, _ = TickFormat(ctx, loc, log, Count(1), jsval.Null, "", false)
	if got := f(jsval.Num(20)).AsString(); got != "20" {
		t.Errorf("label of 20 with count true = %q, want 20", got)
	}
	// formatSpan with an undefined count: precision NaN, ",f" gives six digits
	lin := NewLinear()
	lin.SetType(TypeLinear)
	lin.SetDomain([]jsval.Value{jsval.Num(0), jsval.Num(100)})
	f, _ = TickFormat(ctx, loc, lin, TickCount{}, jsval.Null, "", false)
	if got := f(jsval.Num(25)).AsString(); got != "25.000000" {
		t.Errorf("linear label with an undefined count = %q, want 25.000000", got)
	}
}

// TestRangeLabelOfNoFiniteValue is C101: formatRange reads "≥ null" when
// neither end is a finite number (guides had it right, scale did not).
func TestRangeLabelOfNoFiniteValue(t *testing.T) {
	f := formatRange(defaultFormatter)
	if got := f(jsval.Num(math.Inf(-1)), 0, Labels{Values: []jsval.Value{jsval.Num(math.Inf(-1))}, Max: math.Inf(1), HasMax: true}).AsString(); got != "≥ null" {
		t.Errorf("label = %q, want ≥ null", got)
	}
}

// TestDescendingPiecewiseReversesWholeArrays is C56: polymap reverses the
// whole domain and range before it pairs them (node: domain [2,1,0] range
// [0,10,20,30] maps 0.5 to 25; domain [3,2,1,0] range [0,10,20] maps 0.5 to
// 15 and 2.5 to -5).
func TestDescendingPiecewiseReversesWholeArrays(t *testing.T) {
	for _, c := range []struct {
		domain, rng []float64
		in, want    float64
	}{
		{[]float64{2, 1, 0}, []float64{0, 10, 20, 30}, 0.5, 25},
		{[]float64{3, 2, 1, 0}, []float64{0, 10, 20}, 0.5, 15},
		{[]float64{3, 2, 1, 0}, []float64{0, 10, 20}, 2.5, -5},
	} {
		s := NewLinear()
		s.SetDomain(nums(c.domain...))
		s.SetRange(nums(c.rng...))
		if got := s.Apply(jsval.Num(c.in)).NumValue(); got != c.want {
			t.Errorf("domain %v range %v: %v -> %v, want %v", c.domain, c.rng, c.in, got, c.want)
		}
		// the generic flavour (a range of strings that are numbers is not
		// numeric to d3.interpolate, but colours are)
		s.SetRange([]jsval.Value{jsval.Str("#000"), jsval.Str("#111"), jsval.Str("#222"), jsval.Str("#333")}[:len(c.rng)])
		s.Apply(jsval.Num(c.in))
	}
}

// TestPow10OfLargeExponents is X1: pow10 of an integer exponent past the
// table is what "1e" + n parses to, without converting n to an int64 (which
// is implementation-defined past 2^63).
func TestPow10OfLargeExponents(t *testing.T) {
	for _, c := range []struct{ x, want float64 }{
		{309, math.Inf(1)}, {1e19, math.Inf(1)}, {9.9e20, math.Inf(1)},
		{-324, 0}, {-1e19, 0}, {308, 1e308}, {-323, 1e-323},
	} {
		if got := pow10(c.x); got != c.want {
			t.Errorf("pow10(%v) = %v, want %v", c.x, got, c.want)
		}
	}
}

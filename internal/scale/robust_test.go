package scale

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"runtime/debug"
	"sort"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

func randValue(r *rand.Rand, depth int) jsval.Value {
	switch r.Intn(14) {
	case 0:
		return jsval.Undefined
	case 1:
		return jsval.Null
	case 2:
		return jsval.Bool(r.Intn(2) == 0)
	case 3:
		return jsval.Num(math.NaN())
	case 4:
		return jsval.Num(math.Inf(1 - 2*r.Intn(2)))
	case 5:
		return jsval.Str([]string{"", "a", "b", "red", "#ff0000", "rgb(1,2,3)", "10px", "1e3", "abc", "5", "hsl(10,20%,30%)"}[r.Intn(11)])
	case 6:
		return jsval.Timestamp(float64(r.Int63n(4e12)) - 1e12)
	case 7:
		if depth < 2 {
			n := r.Intn(4)
			items := make([]jsval.Value, n)
			for i := range items {
				items[i] = randValue(r, depth+1)
			}
			return jsval.Arr(items)
		}
		return jsval.Num(1)
	case 8:
		if depth < 2 {
			o := jsval.NewObject(2)
			o.Set("a", randValue(r, depth+1))
			return jsval.Obj(o)
		}
		return jsval.Num(2)
	case 9:
		return jsval.Num(r.NormFloat64() * 1e6)
	case 10:
		return jsval.Num(math.MaxFloat64 * float64(1-2*r.Intn(2)))
	case 11:
		return jsval.Num(5e-324)
	}
	return jsval.Num(float64(r.Intn(200) - 50))
}

func randValues(r *rand.Rand, max int) []jsval.Value {
	n := r.Intn(max + 1)
	out := make([]jsval.Value, n)
	for i := range out {
		out[i] = randValue(r, 0)
	}
	return out
}

// TestNoPanics drives every scale type with hostile values and configuration.
// Specifications are untrusted, so nothing here may panic or run away.
func TestNoPanics(t *testing.T) {
	loc := format.DefaultLocale()
	types := []string{}
	for typ := range registry {
		types = append(types, typ)
	}
	sort.Strings(types)
	r := rand.New(rand.NewSource(7))
	deadline := time.Now().Add(6 * time.Second)
	props := []string{"domain", "range", "rangeRound", "clamp", "round", "unknown", "base", "exponent", "constant", "padding", "paddingInner", "paddingOuter", "align"}
	for iter := 0; iter < 400 && time.Now().Before(deadline); iter++ {
		for _, typ := range types {
			s, _ := New(typ)
			func() {
				defer func() {
					if e := recover(); e != nil {
						if _, ok := e.(*Thrown); ok {
							return // a JavaScript exception, as upstream throws
						}
						t.Fatalf("%s: panic: %v\n%s", typ, e, debug.Stack())
					}
				}()
				for k := 0; k < 6; k++ {
					Set(s, props[r.Intn(len(props))], func() jsval.Value {
						if r.Intn(3) == 0 {
							return randValue(r, 0)
						}
						return jsval.Arr(randValues(r, 6))
					}())
				}
				if b, ok := s.(Typed); ok && r.Intn(4) == 0 {
					b.SetBins([]float64{r.Float64(), r.Float64() * 10, 20})
				}
				for k := 0; k < 8; k++ {
					x := randValue(r, 0)
					s.Apply(x)
					if iv, ok := s.(Inverter); ok {
						iv.Invert(x)
					}
					InvertRange(s, x, randValue(r, 0))
					if ei, ok := s.(ExtentInverter); ok {
						ei.InvertExtent(x)
					}
				}
				var count TickCount
				switch r.Intn(3) {
				case 0:
					count = Count(r.NormFloat64() * 30)
				case 1:
					count = Count(math.Inf(1))
				}
				if n, ok := s.(Niceable); ok {
					n.Nice(count)
				}
				ctx := context.Background()
				_, _ = TickValues(ctx, s, count)
				_, _ = LabelValues(ctx, s, count)
				LabelFraction(s)(1)
				if f, err := TickFormat(ctx, loc, s, count, randValue(r, 0), "", false); err == nil {
					f(randValue(r, 0))
				}
				if f, err := LabelFormat(ctx, loc, s, count, SymbolLegend, jsval.Undefined, "", false); err == nil {
					if l, err := LabelValues(ctx, s, count); err == nil {
						for i, v := range l.Values {
							f(v, i, l)
						}
					}
				}
				_, _ = DomainCaption(ctx, loc, s, CaptionOptions{})
				if _, err := TickCountFor(s, randValue(r, 0), jsval.Undefined); err != nil {
					_ = err
				}
				c := ScaleCopy(s)
				c.Apply(randValue(r, 0))
				s.Domain()
				s.Range()
			}()
		}
	}
}

// TestBoundedWork: a specification controls counts and extents. Where d3
// would list more values than the engine keeps, the helpers fail with a
// limit; they never answer an empty list instead (an axis without ticks).
func TestBoundedWork(t *testing.T) {
	if got, err := Ticks(0, 1e300, 1e9); !errors.Is(err, budget.ErrLimit) || got != nil {
		t.Errorf("Ticks(0, 1e300, 1e9) = %d values, %v; want a limit error", len(got), err)
	}
	if got, err := Ticks(0, 1, math.Inf(1)); err != nil || len(got) != 0 {
		t.Errorf("Ticks(inf) = %v, %v; want none, as d3", got, err)
	}
	l := NewLog()
	l.SetBase(1e12)
	l.SetDomain(nums(1, 1e300))
	start := time.Now()
	if got, err := l.ticksN(context.Background(), Count(1e12), MaxTicks); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("log ticks = %d values, %v; want a limit error", len(got), err)
	}
	// The steps are bounded (maxLogIter). Calibrated, although the site is named
	// a hang guard: it asserts after the fact on the elapsed time. CI baseline
	// 0.0092 s (0.0275 s under -race, 0.0055 s unscaled; Heavy gates timings
	// run); limit 2x, floor 1 s: slower is a finding to investigate.
	d := time.Since(start)
	testutil.Timing(t, "log ticks with a huge base (hang guard)", d, testutil.Scaled(1*time.Second))
	if d > testutil.Scaled(1*time.Second) {
		t.Errorf("log ticks took %v", d)
	}
	if _, err := QuantizeInterpolator(func(float64) jsval.Value { return jsval.Null }, 1<<30); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("QuantizeInterpolator(2^30) = %v; want a limit error", err)
	}
	u := NewUTC()
	u.SetDomain([]jsval.Value{jsval.Num(0), jsval.Num(3e12)})
	iv, _ := format.UTC.Minute().Every(2)
	if _, err := u.ticksN(context.Background(), IntervalCount(iv), MaxTicks); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("utc ticks every 2 minutes over 95 years: %v; want a limit error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := u.Ticks(ctx, IntervalCount(format.UTC.Day())); !errors.Is(err, context.Canceled) {
		t.Errorf("ticks after the call ended: %v; want its error", err)
	}
}

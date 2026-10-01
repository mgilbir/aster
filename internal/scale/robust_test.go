package scale

import (
	"math"
	"math/rand"
	"runtime/debug"
	"sort"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
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
				TickValues(s, count)
				LabelValues(s, count)
				LabelFraction(s)(1)
				if f, err := TickFormat(loc, s, count, randValue(r, 0), "", false); err == nil {
					f(randValue(r, 0))
				}
				if f, err := LabelFormat(loc, s, count, SymbolLegend, jsval.Undefined, "", false); err == nil {
					l := LabelValues(s, count)
					for i, v := range l.Values {
						f(v, i, l)
					}
				}
				DomainCaption(loc, s, CaptionOptions{})
				if _, err := TickCountFor(s, randValue(r, 0), nil); err != nil {
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

func TestBoundedWork(t *testing.T) {
	// A specification controls counts and extents; none may be able to allocate
	// without bound.
	if got := Ticks(0, 1e300, 1e9); len(got) > maxTicks {
		t.Errorf("Ticks returned %d values", len(got))
	}
	if got := Ticks(0, 1, math.Inf(1)); len(got) > maxTicks {
		t.Errorf("Ticks(inf) returned %d values", len(got))
	}
	l := NewLog()
	l.SetBase(1e12)
	l.SetDomain(nums(1, 1e300))
	start := time.Now()
	if got := l.Ticks(Count(1e12)); len(got) > maxTicks {
		t.Errorf("log ticks returned %d", len(got))
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("log ticks took %v", time.Since(start))
	}
	if got := QuantizeSamples(func(float64) jsval.Value { return jsval.Null }, 1<<30); got != nil {
		t.Error("QuantizeSamples should refuse a huge count")
	}
	if got := QuantizeInterpolator(func(float64) jsval.Value { return jsval.Null }, 1<<30); got != nil {
		t.Error("QuantizeInterpolator should refuse a huge count")
	}
}

package scale

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// decVal decodes the generator's tagged JSON encoding into a jsval.Value.
func decVal(v any) jsval.Value {
	switch x := v.(type) {
	case nil:
		return jsval.Null
	case bool:
		return jsval.Bool(x)
	case float64:
		return jsval.Num(x)
	case string:
		return jsval.Str(x)
	case []any:
		items := make([]jsval.Value, len(x))
		for i, e := range x {
			items[i] = decVal(e)
		}
		return jsval.Arr(items)
	case map[string]any:
		switch x["$"] {
		case "u":
			return jsval.Undefined
		case "nan":
			return jsval.Num(math.NaN())
		case "inf":
			return jsval.Num(math.Inf(1))
		case "-inf":
			return jsval.Num(math.Inf(-1))
		case "date":
			if x["v"] == nil {
				return jsval.Timestamp(math.NaN())
			}
			return jsval.Timestamp(timeClip(x["v"].(float64))) // new Date(ms) truncates and clips
		case "fn":
			return jsval.Str("<fn>")
		case "obj":
			o := jsval.NewObject(0)
			m := x["v"].(map[string]any)
			// key order is irrelevant to the comparison below
			for k, e := range m {
				o.Set(k, decVal(e))
			}
			return jsval.Obj(o)
		}
	}
	panic(fmt.Sprintf("bad encoding %v", v))
}

func isThrow(v any) bool {
	m, ok := v.(map[string]any)
	return ok && m["$"] == "throw"
}

// closeEnough compares two numbers allowing a few ulps: Go's math.Pow, Log and
// friends are not bit-identical to V8's.
func closeEnough(a, b float64) bool {
	if sameFloat(a, b) {
		return true
	}
	if math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) {
		return false
	}
	d := math.Abs(a - b)
	return d <= 1e-12*math.Max(math.Abs(a), math.Abs(b)) || d < 1e-300
}

type cmpStats struct {
	exact, approx int
	maxRel        float64
}

func valuesMatch(a, b jsval.Value, st *cmpStats) bool {
	if a.Kind() != b.Kind() {
		// a Timestamp and a number never match; but Undefined vs Null is a real difference too
		return false
	}
	switch a.Kind() {
	case jsval.KindNum, jsval.KindTimestamp:
		if sameFloat(a.NumValue(), b.NumValue()) {
			st.exact++
			return true
		}
		if closeEnough(a.NumValue(), b.NumValue()) {
			st.approx++
			if r := math.Abs(a.NumValue()-b.NumValue()) / math.Max(math.Abs(a.NumValue()), math.Abs(b.NumValue())); r > st.maxRel {
				st.maxRel = r
			}
			return true
		}
		return false
	case jsval.KindArr:
		x, y := a.Items(), b.Items()
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !valuesMatch(x[i], y[i], st) {
				return false
			}
		}
		return true
	case jsval.KindObj:
		x, y := a.ObjValue(), b.ObjValue()
		if x.Len() != y.Len() {
			return false
		}
		for i := 0; i < x.Len(); i++ {
			bv, ok := y.Get(x.KeyAt(i))
			if !ok || !valuesMatch(x.ValueAt(i), bv, st) {
				return false
			}
		}
		return true
	}
	return jsval.Equal(a, b)
}

func valuesMatchList(a, b []jsval.Value, st *cmpStats) bool {
	return valuesMatch(jsval.Arr(a), jsval.Arr(b), st)
}

type goldenCase struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Ops     [][]any `json:"ops"`
	Queries [][]any `json:"queries"`
}

func loadGolden(t testing.TB, file string) []goldenCase {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.NewDecoder(zr).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func tickCountOf(v any) TickCount {
	if v == nil {
		return TickCount{}
	}
	return Count(decVal(v).NumValue())
}

func applyOps(t testing.TB, c goldenCase) (Scale, bool) {
	s, ok := New(c.Type)
	if !ok {
		t.Fatalf("%s: unknown type %s", c.Name, c.Type)
	}
	for _, op := range c.Ops {
		name := op[0].(string)
		var arg jsval.Value
		var raw any
		if len(op) > 1 {
			raw = op[1]
			arg = decVal(raw)
		}
		switch name {
		case "copy":
			s = ScaleCopy(s)
		case "interpolate":
			items := arg.Items()
			gamma, hasGamma := 0.0, false
			if len(items) > 1 {
				gamma, hasGamma = items[1].NumValue(), true
			}
			it, ok := Interpolate(items[0].StrValue(), gamma, hasGamma)
			if !ok {
				return s, false
			}
			ip, ok := s.(Interpolating)
			if !ok {
				return s, false
			}
			ip.SetInterpolate(it)
		case "nice":
			n, ok := s.(Niceable)
			if !ok {
				return s, false
			}
			n.Nice(tickCountOf(raw))
		case "implicit":
			s.(*Ordinal).SetImplicit()
		case "bins":
			s.(Typed).SetBins(toNums(raw))
		case "interpolator":
			sc, ok := LookupScheme(arg.StrValue())
			if !ok || sc.Interpolator == nil {
				t.Fatalf("%s: no interpolating scheme %s", c.Name, arg.StrValue())
			}
			switch x := s.(type) {
			case *Sequential:
				x.SetInterpolator(sc.Interpolator)
			case *Diverging:
				x.SetInterpolator(sc.Interpolator)
			}
		default:
			if !Set(s, name, arg) {
				t.Errorf("%s: scale %T has no setter %s", c.Name, s, name)
				return s, false
			}
		}
	}
	return s, true
}

func runQuery(t testing.TB, s Scale, q []any) (res jsval.Value, ok bool) {
	kind := q[0].(string)
	arg := decVal(q[1])
	switch kind {
	case "apply":
		return s.Apply(arg), true
	case "invert":
		if iv, ok := s.(Inverter); ok {
			return iv.Invert(arg), true
		}
	case "invertRange", "invertRangeVega":
		items := arg.Items()
		v, ok := InvertRange(s, items[0], items[1])
		if !ok {
			return jsval.Undefined, true
		}
		return v, true
	case "invertExtent":
		if ei, ok := s.(ExtentInverter); ok {
			e := ei.InvertExtent(arg)
			return jsval.ArrOf(e[0], e[1]), true
		}
	case "ticks":
		if tk, ok := s.(Ticker); ok {
			ticks := tk.Ticks(tickCountOf(q[1]))
			if _, temporal := s.(*Time); temporal {
				return jsval.Arr(numsToTimestamps(ticks)), true
			}
			return jsval.Arr(numsToValues(ticks)), true
		}
	case "domain":
		return jsval.Arr(s.Domain()), true
	case "range":
		return jsval.Arr(s.Range()), true
	case "quantiles":
		if x, ok := s.(*Quantile); ok {
			th := x.Quantiles()
			out := make([]jsval.Value, len(th))
			for i := range th {
				out[i] = thresholdOrUndef(th, i)
			}
			return jsval.Arr(out), true
		}
	case "thresholds":
		switch x := s.(type) {
		case *Quantize:
			return jsval.Arr(numsToValues(x.Thresholds())), true
		}
	case "bandwidth":
		return jsval.Num(s.(*Band).Bandwidth()), true
	case "step":
		return jsval.Num(s.(*Band).Step()), true
	default:
		if v, ok := Get(s, kind); ok {
			return v, true
		}
	}
	return jsval.Undefined, false
}

func TestScalesGolden(t *testing.T) { runGolden(t, "testdata/scales.json.gz") }

func runGolden(t *testing.T, file string) {
	cases := loadGolden(t, file)
	var st cmpStats
	fail := 0
	nq := 0
	for _, c := range cases {
		s, ok := applyOps(t, c)
		if !ok {
			continue
		}
		for _, q := range c.Queries {
			want := q[2]
			var got jsval.Value
			var ran bool
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s: panic in %v: %v", c.Name, q[:2], r)
						fail++
					}
				}()
				got, ran = runQuery(t, s, q[:2])
			}()
			if isThrow(want) || !ran {
				continue
			}
			nq++
			if !valuesMatch(got, decVal(want), &st) {
				fail++
				if fail <= 40 {
					t.Errorf("%s: %v = %v, want %v", c.Name, q[:2], got, decVal(want))
				}
			}
		}
	}
	t.Logf("%d cases, %d queries, %d failures; numbers exact=%d approx=%d (max rel err %g)", len(cases), nq, fail, st.exact, st.approx, st.maxRel)
}

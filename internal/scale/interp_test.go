package scale

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

type interpGolden struct {
	Interps []struct {
		F  string `json:"f"`
		A  any    `json:"a"`
		B  any    `json:"b"`
		Ts []any  `json:"ts"`
		R  any    `json:"r"`
	} `json:"interps"`
	Helpers []struct {
		F        string   `json:"f"`
		Args     any      `json:"args"`
		Ts       []any    `json:"ts"`
		R        any      `json:"r"`
		Duration *float64 `json:"duration"`
	} `json:"helpers"`
	Schemes []struct {
		Name   string   `json:"name"`
		Colors []string `json:"colors"`
		Ts     []any    `json:"ts"`
		R      []any    `json:"r"`
	} `json:"schemes"`
	SchemeCase [][]string `json:"schemeCase"`
}

func loadInterp(t testing.TB) *interpGolden {
	f, err := os.Open("testdata/interp.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var g interpGolden
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	return &g
}

var twoArg = map[string]Interpolator{
	"number": InterpolateNumber, "round": InterpolateRound, "rgb": InterpolateRGB, "hsl": InterpolateHSL,
	"hslLong": InterpolateHSLLong, "lab": InterpolateLab, "hcl": InterpolateHCL, "hclLong": InterpolateHCLLong,
	"cubehelix": InterpolateCubehelix, "cubehelixLong": InterpolateCubehelixLong, "string": InterpolateString,
	"array": InterpolateArray, "object": InterpolateObject, "date": InterpolateDate, "hue": InterpolateHue,
	"value": InterpolateValue, "rgb2": RGBGamma(2.2), "cubehelix2": CubehelixGamma(2, false),
	"cubehelixLong3": CubehelixGamma(0.5, true),
}

func TestInterpolatorsGolden(t *testing.T) {
	g := loadInterp(t)
	var st cmpStats
	fail := 0
	for _, c := range g.Interps {
		if _, ok := c.R.(string); ok {
			continue
		}
		f, ok := twoArg[c.F]
		if !ok {
			t.Fatalf("no interpolator %s", c.F)
		}
		i := f(decVal(c.A), decVal(c.B))
		want := c.R.([]any)
		for k, tv := range c.Ts {
			got := i(decVal(tv).NumValue())
			if !valuesMatch(got, decVal(want[k]), &st) {
				fail++
				if fail < 30 {
					t.Errorf("%s(%v,%v)(%v) = %v want %v", c.F, c.A, c.B, tv, got, decVal(want[k]))
				}
			}
		}
	}
	t.Logf("interpolators: %d failures, numbers exact=%d approx=%d", fail, st.exact, st.approx)
}

func toVals(a any) []jsval.Value { return decVal(a).Items() }

func toNums(a any) []float64 {
	items := toVals(a)
	out := make([]float64, len(items))
	for i, v := range items {
		out[i] = v.NumValue()
	}
	return out
}

func TestHelpersGolden(t *testing.T) {
	g := loadInterp(t)
	var st cmpStats
	fail := 0
	check := func(name string, args any, ts []any, want any, f func(t float64) jsval.Value) {
		w, ok := want.([]any)
		if !ok {
			return
		}
		for k, tv := range ts {
			got := f(decVal(tv).NumValue())
			if !valuesMatch(got, decVal(w[k]), &st) {
				fail++
				if fail < 30 {
					t.Errorf("%s(%v)(%v) = %v want %v", name, args, tv, got, decVal(w[k]))
				}
			}
		}
	}
	for _, c := range g.Helpers {
		switch c.F {
		case "basis":
			f := Basis(toNums(c.Args))
			check(c.F, c.Args, c.Ts, c.R, func(t float64) jsval.Value { return jsval.Num(f(t)) })
		case "basisClosed":
			f := BasisClosed(toNums(c.Args))
			check(c.F, c.Args, c.Ts, c.R, func(t float64) jsval.Value { return jsval.Num(f(t)) })
		case "rgbBasis":
			check(c.F, c.Args, c.Ts, c.R, RGBBasis(toVals(c.Args)))
		case "rgbBasisClosed":
			check(c.F, c.Args, c.Ts, c.R, RGBBasisClosed(toVals(c.Args)))
		case "discrete":
			check(c.F, c.Args, c.Ts, c.R, Discrete(toVals(c.Args)))
		case "piecewise":
			check(c.F, c.Args, c.Ts, c.R, Piecewise(nil, toVals(c.Args)))
		case "quantize":
			n := int(decVal(c.Args.([]any)[0]).NumValue())
			var f func(float64) jsval.Value
			if n == 3 {
				f = InterpolateRGB(jsval.Str("red"), jsval.Str("blue"))
			} else {
				f = InterpolateNumber(jsval.Num(0), jsval.Num(1))
			}
			got := QuantizeSamples(f, n)
			if !valuesMatch(jsval.Arr(got), decVal(c.R), &st) {
				t.Errorf("quantize %d = %v want %v", n, got, decVal(c.R))
			}
		case "quantizeInterpolator":
			n := int(decVal(c.Args.([]any)[0]).NumValue())
			var f UnitInterpolator
			if n == 3 {
				f = InterpolateRGB(jsval.Str("red"), jsval.Str("blue"))
			} else {
				f = InterpolateNumber(jsval.Num(0), jsval.Num(1))
			}
			got := QuantizeInterpolator(f, n)
			if !valuesMatch(jsval.Arr(got), decVal(c.R), &st) {
				t.Errorf("quantizeInterpolator %d = %v want %v", n, got, decVal(c.R))
			}
		case "zoom":
			a := c.Args.([]any)
			p := func(x any) (r [3]float64) {
				for i, v := range x.([]any) {
					r[i] = v.(float64)
				}
				return
			}
			rho := math.Sqrt2
			if a[2] != nil {
				rho = a[2].(float64)
			}
			f, dur := Zoom(rho)(p(a[0]), p(a[1]))
			// exp/log differences between Go and V8 are amplified by the
			// cancellation inside zoom's cosh/tanh/sinh, so compare loosely.
			near := func(a, b float64) bool {
				return math.Abs(a-b) <= 1e-8*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
			}
			if c.Duration != nil && !near(dur, *c.Duration) {
				t.Errorf("zoom duration %v want %v", dur, *c.Duration)
			}
			for k, tv := range c.Ts {
				w, ok := c.R.([]any)
				if !ok {
					break
				}
				r := f(decVal(tv).NumValue())
				want := toNums(w[k])
				for i := 0; i < 3; i++ {
					if !near(r[i], want[i]) && !(math.IsNaN(r[i]) && math.IsNaN(want[i])) {
						t.Errorf("zoom %v(%v)[%d] = %v want %v", c.Args, tv, i, r[i], want[i])
					}
				}
			}
		case "interpolateColors":
			a := c.Args.([]any)
			typ, gamma, hasGamma := "", 0.0, false
			if a[1] != nil {
				typ = a[1].(string)
			}
			if a[2] != nil {
				gamma, hasGamma = a[2].(float64), true
			}
			check(c.F, c.Args, c.Ts, c.R, InterpolateColors(toVals(a[0]), typ, gamma, hasGamma))
		case "interpolateRange":
			rng := toNums(c.Args.([]any)[0])
			f := InterpolateRange(func(t float64) jsval.Value { return InterpolateNumber(jsval.Num(0), jsval.Num(100))(t) }, rng)
			check(c.F, c.Args, c.Ts, c.R, f)
		default:
			t.Errorf("unhandled helper %s", c.F)
		}
	}
	t.Logf("helpers: %d failures", fail)
}

func TestSchemesGolden(t *testing.T) {
	g := loadInterp(t)
	var st cmpStats
	for _, c := range g.Schemes {
		s, ok := LookupScheme(c.Name)
		if !ok {
			t.Errorf("scheme %s missing", c.Name)
			continue
		}
		if c.Colors != nil {
			if !s.IsDiscrete() || len(s.Colors) != len(c.Colors) {
				t.Errorf("scheme %s: %v want %v", c.Name, s.Colors, c.Colors)
				continue
			}
			for i := range c.Colors {
				if s.Colors[i] != c.Colors[i] {
					t.Errorf("scheme %s[%d] = %s want %s", c.Name, i, s.Colors[i], c.Colors[i])
				}
			}
			continue
		}
		if s.IsDiscrete() || s.Interpolator == nil {
			t.Errorf("scheme %s should be an interpolator", c.Name)
			continue
		}
		for k, tv := range c.Ts {
			if m, ok := c.R[k].(map[string]any); ok && m["$"] == "throw" {
				continue
			}
			want := decVal(c.R[k])
			got := s.Interpolator(decVal(tv).NumValue())
			if !valuesMatch(got, want, &st) {
				t.Errorf("scheme %s(%v) = %v want %v", c.Name, tv, got, want)
			}
		}
	}
	if _, ok := LookupScheme("Category10"); !ok {
		t.Error("lookup should be case-insensitive")
	}
	if _, ok := LookupScheme("nonexistent"); ok {
		t.Error("unknown scheme found")
	}
	t.Logf("%d schemes", len(g.Schemes))
}

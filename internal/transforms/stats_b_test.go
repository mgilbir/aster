package transforms

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

const bStatsTol = 1e-12

func bLoadJSONFile(t testing.TB, name string) jsval.Value {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsval.ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return decodeDialect(v)
}

func bFloats(v jsval.Value) []float64 {
	out := make([]float64, v.Len())
	for i, it := range v.Items() {
		out[i] = it.NumValue()
	}
	return out
}

func bOptNum(v jsval.Value) float64 {
	if v.IsNullish() {
		return 0
	}
	return v.NumValue()
}

func bCloseTo(got, want float64) bool {
	if math.IsNaN(got) && math.IsNaN(want) || got == want {
		return true
	}
	return math.Abs(got-want) <= bStatsTol*math.Max(1, math.Abs(want))
}

func bBinConfigFrom(v jsval.Value) BinConfig {
	c := BinConfig{
		Extent:  [2]float64{v.Get("extent").Index(0).NumValue(), v.Get("extent").Index(1).NumValue()},
		MaxBins: bOptNum(v.Get("maxbins")), Base: bOptNum(v.Get("base")), Step: bOptNum(v.Get("step")),
		MinStep: bOptNum(v.Get("minstep")), Span: bOptNum(v.Get("span")),
	}
	if s := v.Get("steps"); s.IsArr() {
		c.Steps = bFloats(s)
	}
	if s := v.Get("divide"); s.IsArr() {
		c.Divide = bFloats(s)
	}
	if n := v.Get("nice"); n.IsBool() && !n.BoolValue() {
		c.NoNice = true
	}
	return c
}

func TestBinGolden(t *testing.T) {
	root := bLoadJSONFile(t, "bin_stats.json")
	for i, c := range root.Get("bin").Items() {
		got, err := Bin(bBinConfigFrom(c.Get("cfg")))
		if err != nil {
			t.Fatalf("case %d %v: %v", i, c.Get("cfg"), err)
		}
		w := c.Get("out")
		for k, g := range map[string]float64{"start": got.Start, "stop": got.Stop, "step": got.Step} {
			want := math.NaN() // upstream's undefined
			if v := w.Get(k); v.IsNum() {
				want = v.NumValue()
			}
			if !bCloseTo(g, want) {
				t.Errorf("case %d %v: %s got %v want %v", i, c.Get("cfg"), k, g, want)
			}
		}
	}
}

func TestBinLimits(t *testing.T) {
	if _, err := Bin(BinConfig{Extent: [2]float64{0, 1}, MaxBins: 1e9}); err == nil {
		t.Error("expected maxbins limit error")
	}
	// a base upstream bins with is binned with (vega.bin: base 1 is a NaN
	// step, 0.5 a step of 0.5); one whose search for fewer bins never ends
	// upstream (node runs vega.bin({extent: [0, 3], maxbins: 10, base: 0.1})
	// until it is killed) is refused with ErrLimit (ADR 0011)
	if got, err := Bin(BinConfig{Extent: [2]float64{0, 3}, Base: 1, MaxBins: 10}); err != nil || !math.IsNaN(got.Step) {
		t.Errorf("base 1: %v, %v; want a NaN step", got, err)
	}
	if got, err := Bin(BinConfig{Extent: [2]float64{0, 3}, Base: 0.5, MaxBins: 10}); err != nil || got != (BinResult{Start: 0.5, Stop: 3, Step: 0.5}) {
		t.Errorf("base 0.5: %v, %v; want {0.5 3 0.5}", got, err)
	}
	if _, err := Bin(BinConfig{Extent: [2]float64{0, 3}, Base: 0.1, MaxBins: 10}); !errors.Is(err, ErrLimit) {
		t.Errorf("base 0.1: %v; want ErrLimit", err)
	}
	// never panics on hostile numbers
	for _, e := range [][2]float64{{math.NaN(), 1}, {math.Inf(-1), math.Inf(1)}, {1, math.NaN()}} {
		_, _ = Bin(BinConfig{Extent: e, Step: math.NaN()})
		_, _ = Bin(BinConfig{Extent: e})
	}
}

func bSpecDist(t *testing.T, s jsval.Value) Distribution {
	t.Helper()
	num := func(k string) *float64 {
		if v := s.Get(k); v.IsNum() {
			f := v.NumValue()
			return &f
		}
		return nil
	}
	switch s.Get("kind").StrValue() {
	case "normal", "lognormal", "uniform":
		d, err := NewDistribution(context.Background(), DistSpec{Kind: s.Get("kind").StrValue(), Mean: num("mean"), Stdev: num("stdev"), Min: num("min"), Max: num("max")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return d
	case "integer":
		return NewInteger(s.Get("min").NumValue(), s.Get("max").NumValue())
	case "kde":
		var vals []jsval.Value
		vals = append(vals, s.Get("data").Items()...)
		k, err := NewKernelDensity(context.Background(), vals, s.Get("bandwidth").NumValue())
		if err != nil {
			t.Fatal(err)
		}
		return k
	case "mixture":
		var ds []Distribution
		for _, p := range s.Get("parts").Items() {
			ds = append(ds, bSpecDist(t, p))
		}
		var ws []float64
		for _, w := range s.Get("weights").Items() {
			if w.IsNullish() {
				ws = append(ws, math.NaN())
			} else {
				ws = append(ws, w.NumValue())
			}
		}
		return NewMixture(ds, ws)
	}
	t.Fatalf("unknown kind in %v", s)
	return nil
}

func TestDistributionsGolden(t *testing.T) {
	root := bLoadJSONFile(t, "bin_stats.json")
	for _, c := range root.Get("dists").Items() {
		t.Run(c.Get("name").StrValue(), func(t *testing.T) {
			d := bSpecDist(t, c.Get("spec"))
			xs := bFloats(c.Get("xs"))
			pdf, cdf := bFloats(c.Get("pdf")), bFloats(c.Get("cdf"))
			for i, x := range xs {
				if g := d.PDF(x); !bCloseTo(g, pdf[i]) {
					t.Errorf("pdf(%v) got %v want %v", x, g, pdf[i])
				}
				if g := d.CDF(x); !bCloseTo(g, cdf[i]) {
					t.Errorf("cdf(%v) got %v want %v", x, g, cdf[i])
				}
			}
			if ic := c.Get("icdf"); ic.IsArr() {
				ps, want := bFloats(c.Get("ps")), bFloats(ic)
				for i, p := range ps {
					g, err := d.ICDF(p)
					if err != nil || !bCloseTo(g, want[i]) {
						t.Errorf("icdf(%v) got %v (%v) want %v", p, g, err, want[i])
					}
				}
			} else if _, err := d.ICDF(0.5); err == nil {
				t.Error("expected icdf error")
			}
			if b := c.Get("bandwidth"); b.IsNum() {
				if g := d.(*KernelDensity).Bandwidth; !bCloseTo(g, b.NumValue()) {
					t.Errorf("bandwidth got %v want %v", g, b.NumValue())
				}
			}
		})
	}
}

func TestBandwidthGolden(t *testing.T) {
	root := bLoadJSONFile(t, "bin_stats.json")
	for _, c := range root.Get("bandwidth").Items() {
		got, err := EstimateBandwidth(context.Background(), c.Get("data").Items())
		if err != nil {
			t.Fatal(err)
		}
		if want := c.Get("bw").NumValue(); !bCloseTo(got, want) {
			t.Errorf("%v: got %v want %v", c.Get("data"), got, want)
		}
	}
}

func TestSampleCurveGolden(t *testing.T) {
	root := bLoadJSONFile(t, "bin_stats.json")
	for _, c := range root.Get("curves").Items() {
		var f func(float64) float64
		switch c.Get("kind").StrValue() {
		case "mixture2":
			f = NewMixture([]Distribution{NewNormal(-2, 0.4), NewNormal(2, 0.4)}, nil).PDF
		case "uniform01":
			f = NewUniform(0, 1).PDF
		default:
			f = NewNormal(0, 1).PDF
		}
		e := bFloats(c.Get("extent"))
		got, err := SampleCurve(context.Background(), f, [2]float64{e[0], e[1]}, bOptNum(c.Get("min")), bOptNum(c.Get("max")), 0)
		if err != nil {
			t.Fatal(err)
		}
		want := c.Get("pts").Items()
		if len(got) != len(want) {
			t.Errorf("%s: %d points, want %d", c.Get("name").StrValue(), len(got), len(want))
			continue
		}
		for i, p := range want {
			if !bCloseTo(got[i][0], p.Index(0).NumValue()) || !bCloseTo(got[i][1], p.Index(1).NumValue()) {
				t.Errorf("%s: point %d got %v want %v", c.Get("name").StrValue(), i, got[i], p)
				break
			}
		}
	}
}

func TestSamplingGolden(t *testing.T) {
	root := bLoadJSONFile(t, "bin_stats.json")
	lcg := LCG(123)
	for i, w := range bFloats(root.Get("lcg")) {
		if g := lcg(); g != w {
			t.Errorf("lcg %d: got %v want %v", i, g, w)
		}
	}
	for _, c := range root.Get("samples").Items() {
		t.Run(c.Get("name").StrValue(), func(t *testing.T) {
			d := bSpecDist(t, c.Get("spec"))
			r := LCG(c.Get("seed").NumValue())
			for i, w := range bFloats(c.Get("samples")) {
				if g := d.Sample(r); !bCloseTo(g, w) {
					t.Fatalf("sample %d got %v want %v", i, g, w)
				}
			}
		})
	}
}

func TestSampleCurveLimit(t *testing.T) {
	if _, err := SampleCurve(context.Background(), math.Sin, [2]float64{0, 1}, 25, 1e12, 0); err == nil {
		t.Error("expected limit error")
	}
	// pathological extents must terminate
	for _, e := range [][2]float64{{1, 0}, {math.NaN(), 1}, {0, math.Inf(1)}} {
		_, _ = SampleCurve(context.Background(), func(x float64) float64 { return math.Sin(1e3 * x) }, e, 25, 200, 0)
	}
}

func TestDotBinBasics(t *testing.T) {
	if got := DotBin(nil, 1, true); len(got) != 0 {
		t.Errorf("empty: %v", got)
	}
	if got := DotBin([]float64{1}, 1, true); len(got) != 1 || got[0] != 1 {
		t.Errorf("single: %v", got)
	}
}

var _ = context.Background

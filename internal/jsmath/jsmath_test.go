package jsmath

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
)

func decode(t *testing.T, v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		switch x {
		case "NaN":
			return math.NaN()
		case "Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		}
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	t.Fatalf("bad value %v", v)
	return 0
}

func TestAgainstV8(t *testing.T) {
	f, err := os.Open("testdata/math.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string][][]any
	if err := json.NewDecoder(zr).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	funcs := map[string]func(a []float64) float64{
		"sin":   func(a []float64) float64 { return Sin(a[0]) },
		"cos":   func(a []float64) float64 { return Cos(a[0]) },
		"atan":  func(a []float64) float64 { return Atan(a[0]) },
		"atan2": func(a []float64) float64 { return Atan2(a[0], a[1]) },
		"asin":  func(a []float64) float64 { return Asin(a[0]) },
		"acos":  func(a []float64) float64 { return Acos(a[0]) },
		"pow":   func(a []float64) float64 { return Pow(a[0], a[1]) },
		"hypot": func(a []float64) float64 { return Hypot(a[0], a[1]) },
	}
	for name, fn := range funcs {
		rows := doc[name]
		bad := 0
		for _, row := range rows {
			vals := make([]float64, len(row))
			for i, v := range row {
				vals[i] = decode(t, v)
			}
			want := vals[len(vals)-1]
			got := fn(vals[:len(vals)-1])
			same := got == want || (math.IsNaN(got) && math.IsNaN(want))
			if same && got == 0 {
				same = math.Signbit(got) == math.Signbit(want)
			}
			if !same {
				bad++
				if bad <= 3 {
					t.Errorf("%s(%v) = %v (%#x), V8 %v (%#x)", name, vals[:len(vals)-1], got, math.Float64bits(got), want, math.Float64bits(want))
				}
			}
		}
		if bad > 0 {
			t.Errorf("%s: %d of %d differ", name, bad, len(rows))
		}
	}
}

// TestPowCases pins Pow to the results of node 24.21.0 on Linux (glibc's pow),
// among them arguments on which the fdlibm pow V8 once used and macOS's libm
// round differently, and the ECMAScript special cases.
func TestPowCases(t *testing.T) {
	inf, nan, nz := math.Inf(1), math.NaN(), math.Copysign(0, -1)
	for _, c := range []struct{ x, y, want float64 }{
		{10, -5, 1e-5},
		{0.527924914041446, 2.4, 0.21586050011389926},
		{10, 23, 1.0000000000000001e+23},
		{10, -17, 1e-17},
		{2, 983.8424675024525, 1.4658641590558648e+296},
		{0.11504498964210919, 44.625, 1.233733987039986e-42},
		{0.1, 0.1, 0.7943282347242815},
		{1.0000000000000002, 1e17, 4398196873.9457445},
		{2, 0.5, math.Sqrt2},
		{9, 0.5, 3},
		{27, 1.0 / 3, 3},
		{-8, 1.0 / 3, nan},
		{-27, 1.0 / 3, nan},
		{nan, 0, 1},
		{nan, 1, nan},
		{2, nan, nan},
		{3, nz, 1},
		{1, inf, nan},
		{-1, -inf, nan},
		{0, -1, inf},
		{nz, -1, -inf},
		{nz, -3, -inf},
		{nz, 3, nz},
		{nz, 2, 0},
		{nz, 0.5, 0},
		{-2, 3, -8},
		{-2, -3, -0.125},
		{inf, -2, 0},
		{inf, 0.5, inf},
		{-inf, 3, -inf},
		{-inf, -3, nz},
		{-inf, 0.5, inf},
		{2, 1024, inf},
		{2, -1074, 5e-324},
		{2, -1075, 0},
		{0.5, 1074, 5e-324},
		{5e-324, 0.5, 2.2227587494850775e-162},
		{5e-324, 1, 5e-324},
		{1e-310, 1.5, 0},
		{math.MaxFloat64, 1.0000000000000002, inf},
		{-math.MaxFloat64, 3, -inf},
	} {
		if got := Pow(c.x, c.y); !sameFloat(got, c.want) {
			t.Errorf("Pow(%v, %v) = %v (%#x), want %v (%#x)", c.x, c.y, got, math.Float64bits(got), c.want, math.Float64bits(c.want))
		}
	}
}

var sink float64

func BenchmarkSinCos(b *testing.B) {
	for i := 0; i < b.N; i++ {
		x := float64(i%1000) * 0.0137
		sink += Sin(x) + Cos(x)
	}
}

func BenchmarkAtan2(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sink += Atan2(float64(i%97)-48, float64(i%89)-44)
	}
}

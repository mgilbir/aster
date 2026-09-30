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

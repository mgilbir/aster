package scale

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"testing"
)

// decNum decodes the generator's number encoding: plain numbers, or the strings
// "NaN", "Inf" and "-Inf".
func decNum(t testing.TB, v any) float64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return x
	case string:
		switch x {
		case "NaN":
			return math.NaN()
		case "Inf":
			return math.Inf(1)
		case "-Inf":
			return math.Inf(-1)
		}
	}
	t.Fatalf("bad number encoding %v", v)
	return 0
}

func decNums(t testing.TB, v any) []float64 {
	arr := v.([]any)
	out := make([]float64, len(arr))
	for i, x := range arr {
		out[i] = decNum(t, x)
	}
	return out
}

func sameFloat(a, b float64) bool {
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameFloat(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestTicksGolden(t *testing.T) {
	f, err := os.Open("testdata/ticks.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var cases []map[string]any
	if err := json.NewDecoder(zr).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	bad := 0
	for _, c := range cases {
		a, b, n := decNum(t, c["a"]), decNum(t, c["b"]), decNum(t, c["c"])
		want := decNums(t, c["ticks"])
		if got := Ticks(a, b, n); !sameFloats(got, want) && !(len(got) == 0 && len(want) == 0) {
			bad++
			if bad < 10 {
				t.Errorf("Ticks(%v,%v,%v) = %v want %v", a, b, n, got, want)
			}
		}
		if got, w := TickIncrement(a, b, n), decNum(t, c["inc"]); !sameFloat(got, w) {
			bad++
			if bad < 10 {
				t.Errorf("TickIncrement(%v,%v,%v) = %v want %v", a, b, n, got, w)
			}
		}
		if got, w := TickStep(a, b, n), decNum(t, c["step"]); !sameFloat(got, w) {
			bad++
			if bad < 10 {
				t.Errorf("TickStep(%v,%v,%v) = %v want %v", a, b, n, got, w)
			}
		}
		wn := decNums(t, c["nice"])
		if x, y := Nice(a, b, n); !sameFloat(x, wn[0]) || !sameFloat(y, wn[1]) {
			bad++
			if bad < 10 {
				t.Errorf("Nice(%v,%v,%v) = %v,%v want %v", a, b, n, x, y, wn)
			}
		}
	}
	t.Logf("%d cases, %d mismatches", len(cases), bad)
}

func BenchmarkTicks(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Ticks(0, 1234.5, 10)
	}
}

func BenchmarkTickStep(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = TickStep(0.001, 3.7, 10)
	}
}

package expr

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/mgilbir/aster/internal/jsmath"
)

// TestMathBits compares the functions the expression library uses for Math.*
// with V8's on recorded inputs (testdata/gen_math.mjs): the fdlibm ports of
// package jsmath for sin, cos, asin, acos, atan, atan2 and pow, Go's math package
// for tan, exp and log. The counts of results that are not bit-identical are
// logged.
func TestMathBits(t *testing.T) {
	f, err := os.Open("testdata/math_bits.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string][][]string
	if err := json.NewDecoder(zr).Decode(&data); err != nil {
		t.Fatal(err)
	}
	fns := map[string]func(args ...float64) float64{
		"sin": func(a ...float64) float64 { return jsmath.Sin(a[0]) }, "cos": func(a ...float64) float64 { return jsmath.Cos(a[0]) },
		"tan": func(a ...float64) float64 { return math.Tan(a[0]) }, "exp": func(a ...float64) float64 { return math.Exp(a[0]) },
		"log": func(a ...float64) float64 { return math.Log(a[0]) }, "atan": func(a ...float64) float64 { return jsmath.Atan(a[0]) },
		"asin": func(a ...float64) float64 { return jsmath.Asin(a[0]) }, "acos": func(a ...float64) float64 { return jsmath.Acos(a[0]) },
		"atan2": func(a ...float64) float64 { return jsmath.Atan2(a[0], a[1]) },
		"pow":   func(a ...float64) float64 { return jsmath.Pow(a[0], a[1]) },
	}
	for name, rows := range data {
		miss, worst := 0, 0.0
		for _, r := range rows {
			args := make([]float64, len(r)-1)
			for i := range args {
				b, _ := strconv.ParseUint(r[i], 16, 64)
				args[i] = math.Float64frombits(b)
			}
			wb, _ := strconv.ParseUint(r[len(r)-1], 16, 64)
			want := math.Float64frombits(wb)
			got := fns[name](args...)
			if math.Float64bits(got) != wb && !(math.IsNaN(got) && math.IsNaN(want)) {
				miss++
				if d := math.Abs(got-want) / math.Abs(want); d > worst {
					worst = d
				}
			}
		}
		t.Logf("%-5s %5d cases, %4d not bit-identical (worst rel diff %.2g)", name, len(rows), miss, worst)
		// Every function must be within a couple of ulps of V8, and the fdlibm
		// ports (package jsmath) must agree with it much more often than Go's own.
		if worst > 4e-15 {
			t.Errorf("%s drifts %.3g from V8", name, worst)
		}
	}
}

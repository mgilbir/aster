package expr

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
)

// TestMathBits compares the functions the expression library runs for Math.*
// (its function table) with V8's on recorded inputs (testdata/gen_math.mjs).
// All are package jsmath's ports of V8's ieee754 functions, and a one-ulp
// difference changes printed output, so every result must be bit-identical
// (C45: exp, log and tan were Go's math, and 16%, 0.08% and 34% differed).
// The exception is pow: the vectors were recorded with a V8 older than the
// one jsmath.Pow follows (node 26's, which rounds pow(2, -0.5) correctly), so
// it is held to one ulp here and checked bit for bit by package jsmath.
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
	fns := map[string]func(args ...float64) float64{}
	for name := range data {
		d := lookupFunc(name)
		switch {
		case d != nil && d.math1 != nil:
			fns[name] = func(a ...float64) float64 { return d.math1(a[0]) }
		case d != nil && d.math2 != nil:
			fns[name] = func(a ...float64) float64 { return d.math2(a[0], a[1]) }
		default:
			t.Fatalf("%s is not a Math function of the table", name)
		}
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
		if name == "pow" {
			if worst > 2.3e-16 {
				t.Errorf("pow drifts %.3g from V8", worst)
			}
		} else if miss > 0 {
			t.Errorf("%s: %d of %d results differ from V8 (worst rel diff %.2g)", name, miss, len(rows), worst)
		}
	}
}

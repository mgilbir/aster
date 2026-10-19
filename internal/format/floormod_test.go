package format

import (
	"math"
	"math/rand/v2"
	"testing"
)

// floorMod's integer path gives math.Mod's answer, sign of zero included.
func TestFloorModIntegerPath(t *testing.T) {
	ref := func(a, b float64) float64 {
		m := math.Mod(a, b)
		if m < 0 {
			m += b
		}
		return m
	}
	r := rand.New(rand.NewPCG(1, 2))
	bs := []float64{1000, 60000, 3600000, 86400000, 604800000, 7, 1}
	as := []float64{0, math.Copysign(0, -1), -5000, 5000, -1, 1, 8.64e15, -8.64e15, 1<<53 - 1, -(1<<53 - 1), 0.5, -0.5, math.NaN(), math.Inf(1)}
	for range 100000 {
		as = append(as, math.Trunc((r.Float64()*2-1)*8.64e15), float64(r.Int64N(1<<20)-1<<19))
	}
	for _, b := range bs {
		for _, a := range as {
			got, want := floorMod(a, b), ref(a, b)
			if math.Float64bits(got) != math.Float64bits(want) && !(math.IsNaN(got) && math.IsNaN(want)) {
				t.Fatalf("floorMod(%v, %v) = %v, math.Mod gives %v", a, b, got, want)
			}
		}
	}
}

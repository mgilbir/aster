package jsmath

import (
	"math"
	"math/rand/v2"
	"testing"
)

// TestRemIsMod holds Rem to math.Mod bit for bit, across the integer fast
// path's edges and the seeded generator's own sequence.
func TestRemIsMod(t *testing.T) {
	check := func(x, y float64) {
		t.Helper()
		if got, want := Rem(x, y), math.Mod(x, y); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("Rem(%v, %v) = %v, math.Mod = %v", x, y, got, want)
		}
	}
	edges := []float64{0, math.Copysign(0, -1), 0.5, 1, 1.5, 2, 3, 12345, 2147483647, 1 << 52, 1<<53 - 1, 1 << 53, 1<<53 + 2,
		1 << 62, 1<<63 - 1024, 1 << 63, 1 << 64, math.MaxFloat64, math.Inf(1), math.NaN(), math.SmallestNonzeroFloat64}
	for _, x := range edges {
		for _, y := range edges {
			check(x, y)
			check(-x, y)
			check(x, -y)
			check(-x, -y)
		}
	}
	r := rand.New(rand.NewPCG(1, 2))
	for range 1_000_000 {
		x := math.Trunc(r.Float64() * (1 << 63))
		y := math.Trunc(r.Float64()*(1<<40)) + 1
		check(x, y)
		check(x, 2147483647)
		check(r.Float64()*1e20, y)
	}
	seed := 123456789.0
	for range 1_000_000 {
		x := float64(1103515245*seed) + 12345
		check(x, 2147483647)
		seed = Rem(x, 2147483647)
	}
}

func BenchmarkRem(b *testing.B) {
	x := float64(1103515245*123456789.0) + 12345
	b.Run("Rem", func(b *testing.B) {
		s := 0.0
		for b.Loop() {
			s += Rem(x, 2147483647)
		}
		_ = s
	})
	b.Run("math.Mod", func(b *testing.B) {
		s := 0.0
		for b.Loop() {
			s += math.Mod(x, 2147483647)
		}
		_ = s
	})
}

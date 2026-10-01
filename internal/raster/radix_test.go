package raster

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// TestRadixSortCrossings: the radix sort must return a permutation of its
// input ordered by x — the only property fill relies on (crossings at one x
// make zero-length spans, so their order among themselves cannot matter).
func TestRadixSortCrossings(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	r := &rasterizer{}
	for _, qmax := range []int32{0, 1, 2047, 2048, 4095, 1 << 20, 1<<31 - 1} {
		for _, n := range []int{25, 100, 5000} {
			in := make([]crossing, n)
			for i := range in {
				in[i] = crossing{x: int32(rng.Int64N(int64(qmax) + 1)), dir: int8(rng.IntN(3) - 1)}
			}
			want := slices.Clone(in)
			slices.SortStableFunc(want, func(a, b crossing) int { return int(a.x) - int(b.x) })
			got := slices.Clone(r.radixSort(slices.Clone(in), qmax))
			// LSD radix sorting is stable, so the result is exactly the
			// stable comparison sort's.
			if !slices.Equal(got, want) {
				t.Fatalf("qmax %d, n %d: radix sort differs from a stable sort", qmax, n)
			}
		}
	}
}

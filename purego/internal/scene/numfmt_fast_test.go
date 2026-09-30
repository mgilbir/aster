package scene

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

// The scaled-integer fast path of StringPath.num must print what the generic
// shortest-number formatting prints for the rounded value.
func TestStringPathFastNumbersMatchGeneric(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for d := 0; d <= 8; d++ {
		p := &StringPath{}
		p.SetDigits(d)
		k := math.Pow10(d)
		for i := 0; i < 20000; i++ {
			var f float64
			switch i % 4 {
			case 0:
				f = rng.NormFloat64() * 1000
			case 1:
				f = float64(rng.Intn(2000000)-1000000) / 1000
			case 2:
				f = rng.Float64() * 1e-3
			default:
				f = (rng.Float64() - 0.5) * 1e13
			}
			want := string(AppendNumber(nil, jsRound(float64(f*k))/k))
			p.Reset()
			p.num(f)
			if got := string(p.Bytes()); got != want {
				t.Fatalf("d=%d f=%v: got %q, want %q", d, f, got, want)
			}
		}
	}
}

func TestAppendNumberIntegers(t *testing.T) {
	for _, f := range []float64{1, -1, 42, 1e14, -1e14, 999999999999999, 1e15, 1e20, 123456789012345678} {
		want := string(appendFloatF(nil, f))
		if got := string(AppendNumber(nil, f)); got != want {
			t.Errorf("%v: got %q want %q", f, got, want)
		}
	}
}

func appendFloatF(dst []byte, f float64) []byte { return append(dst, formatF(f)...) }

func formatF(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

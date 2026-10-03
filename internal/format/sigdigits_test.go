package format

import (
	"math"
	"math/rand"
	"strconv"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// sigDigitsRef is sigDigits without the shortcut for digits that fit: every
// p <= 14 rounds the exact value with strconv unless it is a tie.
func sigDigitsRef(x float64, p int) ([]byte, int) {
	var tmp [40]byte
	short, _ := parseExp(nil, strconv.AppendFloat(tmp[:0], x, 'e', -1, 64))
	if !(len(short) == p+1 && short[p] == '5') {
		return parseExp(nil, strconv.AppendFloat(tmp[:0], x, 'e', p-1, 64))
	}
	return parseExp(nil, []byte(jsval.ToExponential(x, p-1)))
}

func TestSigDigitsShortcut(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	check := func(x float64) {
		t.Helper()
		if x <= 0 || math.IsInf(x, 0) || math.IsNaN(x) {
			return
		}
		for _, p := range []int{1, 2, 3, 6, 10, 12, 14} {
			var buf [32]byte
			got, ge := sigDigits(buf[:0], x, p)
			want, we := sigDigitsRef(x, p)
			if string(got) != string(want) || ge != we {
				t.Fatalf("sigDigits(%v, %d) = %s e%d, want %s e%d", x, p, got, ge, want, we)
			}
		}
	}
	for range 200000 {
		check(math.Float64frombits(r.Uint64() &^ (1 << 63)))
		check(float64(r.Intn(1000000)) / math.Pow(10, float64(r.Intn(12))))
		check(r.Float64())
		check(math.Round(r.Float64()*1e6) / 1e3)
		check(r.Float64() * math.Pow(10, float64(r.Intn(40)-20)))
	}
	for _, x := range []float64{5e-324, 1e-323, 2.2250738585072014e-308, 2.225073858507201e-308, 1.7976931348623157e308, 0.1, 0.2, 0.30000000000000004, 1, 10, 12345678901234, 0.5, 2.5, 1e21, 1e-7} {
		check(x)
	}
}

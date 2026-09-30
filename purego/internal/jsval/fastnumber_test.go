package jsval

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

// fastNumber must agree with ParseFloat wherever it answers.
func TestFastNumberMatchesParseFloat(t *testing.T) {
	check := func(s string) {
		t.Helper()
		f, ok := fastNumber([]byte(s))
		if !ok {
			return
		}
		want, _ := strconv.ParseFloat(s, 64)
		if math.Float64bits(f) != math.Float64bits(want) {
			t.Fatalf("%q: got %v, want %v", s, f, want)
		}
	}
	for _, s := range []string{"0", "-0", "0.5", "-0.5", "123456789012345", "0.000000000000001", "0.0000000000000001", "1.7976931348623157", "99999999999999.9", "0.1", "0.2", "0.3", "4.35", "1234567890123456"} {
		check(s)
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200000; i++ {
		digits := 1 + rng.Intn(17)
		b := make([]byte, 0, 24)
		if rng.Intn(2) == 0 {
			b = append(b, '-')
		}
		b = append(b, byte('1'+rng.Intn(9)))
		for j := 1; j < digits; j++ {
			b = append(b, byte('0'+rng.Intn(10)))
		}
		if rng.Intn(3) > 0 {
			pos := len(b) - rng.Intn(digits)
			if b[0] == '-' && pos == 1 {
				pos = 2
			}
			if pos < len(b) {
				b = append(b[:pos], append([]byte{'.'}, b[pos:]...)...)
			}
		}
		check(string(b))
	}
}

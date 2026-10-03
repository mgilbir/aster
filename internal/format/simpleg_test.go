package format

import (
	"math"
	"math/rand"
	"testing"
)

// The positional shortcut of a trimmed "g" must write what the general path
// writes, for any number, in any locale.
func TestSimpleGMatchesGeneral(t *testing.T) {
	de := NewNumberLocale(NumberLocaleDef{Decimal: ptr(","), Thousands: ptr("."), Minus: ptr("−")})
	r := rand.New(rand.NewSource(7))
	for _, loc := range []*NumberLocale{NewNumberLocale(NumberLocaleDef{}), de} {
		for _, spec := range []string{"", ".12~g", ".3~g", ".1~g", ".14~g", "~g", ".6~g"} {
			f, err := loc.Format(spec)
			if err != nil {
				t.Fatal(err)
			}
			if !f.simpleG {
				t.Fatalf("%q should take the shortcut", spec)
			}
			g := *f
			g.simpleG = false
			check := func(x float64) {
				t.Helper()
				if got, want := f.Format(x), g.Format(x); got != want {
					t.Fatalf("%q Format(%v) = %q, general path %q", spec, x, got, want)
				}
			}
			for range 30000 {
				check(math.Float64frombits(r.Uint64()))
				check(float64(r.Intn(2000000)-1000000) / math.Pow(10, float64(r.Intn(14))))
				check((r.Float64() - 0.5) * math.Pow(10, float64(r.Intn(30)-12)))
				check(math.Round(r.NormFloat64()*1e4) / 1e2)
			}
			for _, x := range []float64{0, math.Copysign(0, -1), 1, -1, 0.1, 0.1 + 0.2, 1e-6, 1e-7, 9.99e-7, 123456789012, 999999999999, 1e12, 1e11, 1.5e11, 0.5, -0.000001234, math.NaN(), math.Inf(1), math.Inf(-1), 5e-324, math.MaxFloat64, 12.5, 100, 1e5} {
				check(x)
			}
		}
	}
}

package svgpdf

import (
	"math"
	"math/rand"
	"testing"
)

func TestParseColor(t *testing.T) {
	cases := []struct {
		in   string
		want Color
	}{
		{"#000", Color{0, 0, 0}},
		{"#fff", Color{1, 1, 1}},
		{"#4c78a8", Color{0x4c / 255.0, 0x78 / 255.0, 0xa8 / 255.0}},
		{"#ddd", Color{0xdd / 255.0, 0xdd / 255.0, 0xdd / 255.0}},
		{"rgb(255, 0, 128)", Color{1, 0, 128 / 255.0}},
		{"rgb(100%, 0%, 50%)", Color{1, 0, 128 / 255.0}}, // 8 bits a channel, as the PNG writer and browsers
		{"white", Color{1, 1, 1}},
		{"black", Color{0, 0, 0}},
		{"steelblue", Color{70 / 255.0, 130 / 255.0, 180 / 255.0}},
		{"White", Color{1, 1, 1}}, // keywords are case-insensitive
	}
	for _, c := range cases {
		got, _, err := parseColor(c.in)
		if err != nil {
			t.Errorf("parseColor(%q): %v", c.in, err)
			continue
		}
		if !almostEqual(got.R, c.want.R) || !almostEqual(got.G, c.want.G) || !almostEqual(got.B, c.want.B) {
			t.Errorf("parseColor(%q): got %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseColorErrors(t *testing.T) {
	for _, s := range []string{
		"#12345",   // bad hex length
		"#gggggg",  // bad hex digits
		"rgb(1,2)", // missing component
		"notacolor",
		"",
	} {
		if _, _, err := parseColor(s); err == nil {
			t.Errorf("parseColor(%q): expected error, got none", s)
		}
	}
}

// Every CSS colour parses, and its alpha is kept for the opacity.
func TestParseColorCSS(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  Color
		alpha float64
	}{
		{"teal", Color{0, 128 / 255.0, 128 / 255.0}, 1},
		{"aliceblue", Color{240 / 255.0, 248 / 255.0, 1}, 1},
		{"rebeccapurple", Color{102 / 255.0, 51 / 255.0, 153 / 255.0}, 1},
		{"#c8edf1a2", Color{0xc8 / 255.0, 0xed / 255.0, 0xf1 / 255.0}, 0xa2 / 255.0},
		{"#f008", Color{1, 0, 0}, 0x88 / 255.0},
		{"rgba(255, 0, 0, 0.5)", Color{1, 0, 0}, 0.5},
		{"hsl(120, 100%, 25%)", Color{0, 128 / 255.0, 0}, 1},
	} {
		got, alpha, err := parseColor(tc.in)
		if err != nil || !almostEqual(got.R, tc.want.R) || !almostEqual(got.G, tc.want.G) || !almostEqual(got.B, tc.want.B) || math.Abs(alpha-tc.alpha) > 1e-6 {
			t.Errorf("parseColor(%q) = %+v %g %v, want %+v %g", tc.in, got, alpha, err, tc.want, tc.alpha)
		}
	}
}

func TestParsePaint(t *testing.T) {
	p, err := parsePaint("none")
	if err != nil || !p.None {
		t.Errorf("parsePaint(none): got %+v, err %v", p, err)
	}
	p, err = parsePaint("transparent")
	if err != nil || !p.None {
		t.Errorf("parsePaint(transparent): got %+v, err %v", p, err)
	}
	p, err = parsePaint("#4c78a8")
	if err != nil || p.None {
		t.Errorf("parsePaint(#4c78a8): got %+v, err %v", p, err)
	}
	// Gradient/pattern references must error, not degrade.
	if _, err := parsePaint("url(#gradient_0)"); err == nil {
		t.Error("parsePaint(url(#...)): expected error, got none")
	}
}

func TestFmtNum(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1.5, "-1.5"},
		{0.5, "0.5"},
		{200.5, "200.5"},
		{1.0 / 3.0, "0.3333"},
		{1e-9, "0"},  // rounds to zero, not exponent notation
		{-1e-9, "0"}, // never "-0"
		{124, "124"},
	}
	for _, c := range cases {
		if got := fmtNum(c.in); got != c.want {
			t.Errorf("fmtNum(%g): got %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAppendNumMatchesStrconv checks the integer-arithmetic formatter against
// strconv on edge cases, ties, and hundreds of thousands of random values of every
// magnitude.
func TestAppendNumMatchesStrconv(t *testing.T) {
	check := func(v float64) {
		t.Helper()
		got := string(appendNum(nil, v))
		want := string(appendNumSlow(nil, v))
		if got != want {
			t.Fatalf("appendNum(%v = %#x): got %q, want %q", v, math.Float64bits(v), got, want)
		}
	}
	for _, v := range []float64{
		0, math.Copysign(0, -1), 1, -1, 0.5, 0.00005, 0.00015, 0.00025, 0.03125, 0.09375, 0.00004999999999999999, 0.00005000000000000001,
		1e-4, 1e-5, 5e-5, -5e-5, 9.99995, 9.99994999, 99999.99995, 1e13, 1e14 - 0.5, 1e14, 1e15, 1e300,
		math.SmallestNonzeroFloat64, math.MaxFloat64, math.NaN(), math.Inf(1), math.Inf(-1),
		0.1, 0.2, 0.3, 1.0 / 3, 2.0 / 3, 123456.78905, 599.99995, 4.00005, 0.30000000000000004,
	} {
		check(v)
	}
	// Exact ties: k/32 and k/64 have few binary places, so k*1e4/32 hits .5.
	for k := -400; k <= 400; k++ {
		for _, d := range []float64{32, 64, 128, 16384, 1 << 20} {
			check(float64(k) / d)
		}
	}
	rng := rand.New(rand.NewSource(7))
	for range 300_000 {
		check(math.Float64frombits(rng.Uint64()))
		// Spread over magnitudes 1e-6 to 1e7, as scaled coordinates are.
		check((rng.Float64() - 0.5) * math.Pow(10, float64(rng.Intn(14)-6)))
		// Short decimals, as Vega's data coordinates are.
		check(float64(rng.Intn(2_000_000)-1_000_000) / math.Pow(10, float64(rng.Intn(7))))
	}
}

func BenchmarkAppendNum(b *testing.B) {
	vals := []float64{12, 0.3333333, 200.5, 123.456789, 0.00001, -98.7654321, 599.9999}
	for _, f := range []struct {
		name string
		fn   func([]byte, float64) []byte
	}{{"strconv", appendNumSlow}, {"integer", appendNum}} {
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			var buf [32]byte
			for i := 0; i < b.N; i++ {
				_ = f.fn(buf[:0], vals[i%len(vals)])
			}
		})
	}
}

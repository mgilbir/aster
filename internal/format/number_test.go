package format

import (
	"encoding/json"
	"math"
	"math/rand"
	"strconv"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// parseJS decodes the numbers gen_number.mjs writes as strings.
func parseJS(t testing.TB, s string) float64 {
	t.Helper()
	switch s {
	case "NaN":
		return math.NaN()
	case "Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	case "-0":
		return math.Copysign(0, -1)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("bad number %q: %v", s, err)
	}
	return f
}

type numberGolden struct {
	Values  []string                   `json:"values"`
	Locales map[string]json.RawMessage `json:"locales"`
	Cases   []struct {
		Locale string   `json:"locale"`
		Spec   string   `json:"spec"`
		Out    []string `json:"out"`
	} `json:"cases"`
	Invalid map[string]bool `json:"invalid"`
	Prefix  []struct {
		Locale string   `json:"locale"`
		Spec   string   `json:"spec"`
		Ref    string   `json:"ref"`
		Out    []string `json:"out"`
	} `json:"prefix"`
	Precision []struct {
		Fn  string          `json:"fn"`
		A   string          `json:"a"`
		B   string          `json:"b"`
		Out json.RawMessage `json:"out"`
	} `json:"precision"`
}

func loadNumberGolden(t testing.TB) (*numberGolden, map[string]*NumberLocale) {
	t.Helper()
	var g numberGolden
	readGolden(t, "number.json.gz", &g)
	locs := map[string]*NumberLocale{"default": DefaultNumberLocale()}
	for name, raw := range g.Locales {
		if string(raw) == "null" {
			continue
		}
		v, err := jsval.ParseJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		l, err := NumberLocaleFromValue(v)
		if err != nil {
			t.Fatal(err)
		}
		locs[name] = l
	}
	return &g, locs
}

func TestNumberFormatGolden(t *testing.T) {
	g, locs := loadNumberGolden(t)
	vals := make([]float64, len(g.Values))
	for i, s := range g.Values {
		vals[i] = parseJS(t, s)
	}
	fails := 0
	for _, c := range g.Cases {
		f, err := locs[c.Locale].Format(c.Spec)
		if err != nil {
			t.Errorf("%s %q: %v", c.Locale, c.Spec, err)
			continue
		}
		for i, v := range vals {
			if got := f.Format(v); got != c.Out[i] {
				if fails++; fails < 60 {
					t.Errorf("%s format(%q)(%s) = %q, want %q", c.Locale, c.Spec, g.Values[i], got, c.Out[i])
				}
			}
		}
	}
	for k, wantErr := range g.Invalid {
		loc, spec := "default", k
		for i := range k {
			if k[i] == '|' {
				loc, spec = k[:i], k[i+1:]
				break
			}
		}
		_, err := locs[loc].Format(spec)
		if (err != nil) != wantErr {
			t.Errorf("%s format(%q): error %v, want error=%v", loc, spec, err, wantErr)
		}
	}
}

func TestFormatPrefixGolden(t *testing.T) {
	g, locs := loadNumberGolden(t)
	in := []float64{0, 1, 1.5, 12345, 0.000123, 2.5e6, -3.2e-7, 4e10, 1e-9, 123456789, math.NaN(), 6e27}
	fails := 0
	for _, c := range g.Prefix {
		f, err := locs[c.Locale].FormatPrefix(c.Spec, parseJS(t, c.Ref))
		if err != nil {
			t.Errorf("%q: %v", c.Spec, err)
			continue
		}
		for i, v := range in {
			if got := f(v); got != c.Out[i] {
				if fails++; fails < 40 {
					t.Errorf("%s formatPrefix(%q, %s)(%v) = %q, want %q", c.Locale, c.Spec, c.Ref, v, got, c.Out[i])
				}
			}
		}
	}
}

func TestPrecisionGolden(t *testing.T) {
	g, _ := loadNumberGolden(t)
	for _, p := range g.Precision {
		var want float64
		var s string
		if json.Unmarshal(p.Out, &s) == nil {
			want = parseJS(t, s)
		} else if err := json.Unmarshal(p.Out, &want); err != nil {
			t.Fatal(err)
		}
		a := parseJS(t, p.A)
		var got float64
		switch p.Fn {
		case "fixed":
			got = PrecisionFixed(a)
		case "prefix":
			got = PrecisionPrefix(a, parseJS(t, p.B))
		case "round":
			got = PrecisionRound(a, parseJS(t, p.B))
		}
		if got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
			t.Errorf("precision%s(%s,%s) = %v, want %v", p.Fn, p.A, p.B, got, want)
		}
	}
}

func TestSpecifierString(t *testing.T) {
	cases := map[string]string{
		"":         " >-",
		",.2f":     " >-,.2f",
		"$,.0f":    " >-$,.0f",
		"*^+#010,": "*^+#010,",
		"00":       " >-01",
		"~s":       " >-~s",
	}
	for in, want := range cases {
		s, err := ParseSpecifier(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := s.String(); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseSpecifier("=="); err != nil {
		t.Errorf(`"==" is fill "=" align "=": %v`, err)
	}
}

func TestWidthBound(t *testing.T) {
	if _, err := DefaultNumberLocale().Format("99999999999999d"); err == nil {
		t.Fatal("huge width accepted")
	}
}

// The strconv-backed digit generators must agree with the exact jsval ones,
// including on exact ties.
func TestDigitGeneratorsMatchJSVal(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	check := func(x float64) {
		for _, p := range []int{0, 1, 2, 3, 6, 10, 20} {
			if got, want := toFixed(x, p), jsval.ToFixed(x, p); got != want {
				t.Fatalf("toFixed(%v,%d) = %s, want %s", x, p, got, want)
			}
		}
		for _, p := range []int{1, 2, 3, 5, 6, 12, 14, 15, 21} {
			if got, want := toPrecision(x, p), jsval.ToPrecision(x, p); got != want {
				t.Fatalf("toPrecision(%v,%d) = %s, want %s", x, p, got, want)
			}
		}
		for _, d := range []int{-1, 0, 1, 2, 5, 13, 20} {
			if got, want := toExponential(x, d), jsval.ToExponential(x, d); got != want {
				t.Fatalf("toExponential(%v,%d) = %s, want %s", x, d, got, want)
			}
		}
	}
	for _, x := range []float64{0.5, 1.5, 2.5, 0.125, 0.375, 0.0625, 1e21, 25, 35, 45, 125, 1250, 0.15, 0.25, 0.35, 2.675, 1.005, 5e-324, 1e-7, 123456789.5, 4503599627370497.5} {
		check(x)
	}
	for i := 0; i < 3000; i++ {
		switch i % 4 {
		case 0:
			check(rng.Float64() * math.Pow(10, float64(rng.Intn(40)-20)))
		case 1:
			check(float64(rng.Intn(1000000)) / 8) // dyadic: lots of exact ties
		case 2:
			check(float64(rng.Intn(100000)) / 1000)
		default:
			check(math.Float64frombits(rng.Uint64() &^ (1 << 63)))
		}
	}
}

func BenchmarkNumberFormat(b *testing.B) {
	for _, spec := range []string{",.2f", "~s", "d", ".3g", "$,.0f", ".1%"} {
		f, err := DefaultNumberLocale().Format(spec)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(spec, func(b *testing.B) {
			b.ReportAllocs()
			var buf [64]byte
			for i := 0; i < b.N; i++ {
				_ = f.AppendFormat(buf[:0], 1234567.891+float64(i&1023))
			}
		})
	}
}

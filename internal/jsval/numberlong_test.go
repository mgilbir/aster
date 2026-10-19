package jsval

import (
	"context"
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// oldNumber is how the parser read a number literal before long ones were
// cut short: ParseFloat over the whole literal, out-of-range literals read
// as ±Infinity or 0.
func oldNumber(t testing.TB, lit string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			t.Fatalf("%.60q: %v", lit, err)
		}
	}
	return f
}

// shortened is lit read through shortNumber, unmetered.
func shortened(t testing.TB, lit string) float64 {
	t.Helper()
	var p parser
	short, err := p.shortNumber([]byte(lit))
	if err != nil {
		t.Fatal(err)
	}
	if len(short) > numberDigits+32 {
		t.Fatalf("%.60q cut to %d bytes", lit, len(short))
	}
	return oldNumber(t, string(short))
}

// exactNumber is the float64 nearest the decimal literal lit, ties to
// even, from its exact value as a big.Rat (what JSON.parse gives); ok is
// false for an exponent too large to make the rational of.
func exactNumber(lit string) (f float64, ok bool) {
	mant, exp, _ := strings.Cut(strings.ToLower(lit), "e")
	if e, err := strconv.Atoi(exp); exp != "" && (err != nil || e > 20000 || e < -20000) {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(lit)
	if !ok {
		return 0, false
	}
	f, _ = r.Float64()
	if f == 0 && strings.HasPrefix(mant, "-") {
		f = math.Copysign(0, -1)
	}
	return f, true
}

// numberWant is what a literal must read as: ParseFloat over the whole of
// it, which the parser used, except where that is not the nearest float64
// (a literal of over 800 digits before its point, which strconv misreads),
// where it is the nearest. mis counts the literals ParseFloat misreads.
func numberWant(t testing.TB, lit string, mis *int) float64 {
	t.Helper()
	old := oldNumber(t, lit)
	if exact, ok := exactNumber(lit); ok && math.Float64bits(exact) != math.Float64bits(old) {
		ip, _, _ := strings.Cut(strings.TrimPrefix(lit, "-"), ".")
		if ip, _, _ = strings.Cut(strings.ToLower(ip), "e"); len(ip) <= 800 {
			t.Fatalf("%.60q: ParseFloat reads %v, the nearest float64 is %v, and the literal has only %d digits before its point", lit, old, exact, len(ip))
		}
		*mis++
		return exact
	}
	return old
}

// boundaryLiterals are decimal literals on, just above and just below
// every kind of boundary between two roundings of a float64, written out
// exactly (hundreds of significant digits, more than numberDigits for the
// ones offset from it), with the decimal point where it falls and moved
// into an exponent.
func boundaryLiterals(rng *rand.Rand) []string {
	var mids []*big.Float
	exact := func(f float64) *big.Float { return new(big.Float).SetPrec(2200).SetFloat64(f) }
	add := func(f float64) {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return
		}
		mids = append(mids, exact(f)) // the float itself
		if n := math.Nextafter(f, math.Inf(1)); !math.IsInf(n, 0) {
			m := new(big.Float).SetPrec(2200).Add(exact(f), exact(n))
			mids = append(mids, m.Quo(m, big.NewFloat(2))) // the midpoint, exact
		}
	}
	for _, f := range []float64{0x1p-1074, 0x1p-1073, 3 * 0x1p-1074, 0x1p-1022, math.Nextafter(0x1p-1022, 0), 1, 0.1, 0.5,
		1 << 53, (1 << 53) + 2, 9007199254740993, 1e23, 8.98846567431158e307, math.MaxFloat64, math.Nextafter(math.MaxFloat64, 0), 2.2250738585072011e-308} {
		add(f)
		add(math.Nextafter(f, 0))
	}
	for range 300 {
		switch rng.IntN(3) {
		case 0: // any finite
			add(math.Abs(math.Float64frombits(rng.Uint64()&^(0x7ff<<52) | uint64(rng.IntN(2047))<<52)))
		case 1: // a subnormal
			add(math.Float64frombits(rng.Uint64() & (1<<52 - 1)))
		default: // a power of two and below it, where the spacing changes
			add(math.Ldexp(1, rng.IntN(2098)-1074))
		}
	}
	// The overflow threshold, MaxFloat64 plus half its spacing, and half
	// the smallest subnormal, under which a literal rounds to 0.
	two := func(e int) *big.Float { return new(big.Float).SetPrec(2200).SetMantExp(big.NewFloat(1), e) }
	mids = append(mids, new(big.Float).SetPrec(2200).Sub(two(1024), two(970)), two(-1075))

	tiny := two(-2100)
	var out []string
	for _, m := range mids {
		for _, off := range []int{0, 1, -1} {
			x := new(big.Float).SetPrec(4400).Set(m)
			switch off {
			case 1:
				x.Add(x, tiny)
			case -1:
				x.Sub(x, tiny)
			}
			s := strings.TrimRight(x.Text('f', 2200), "0") // exact
			s = strings.TrimSuffix(s, ".")
			if s[0] == '-' { // a zero less tiny
				continue
			}
			out = append(out, s, "-"+s)
			// The same digits with the point moved into an exponent.
			if ip, fp, ok := strings.Cut(s, "."); ok {
				d := strings.TrimLeft(ip+fp, "0") // JSON has no leading zeros
				out = append(out, d+"e-"+strconv.Itoa(len(fp)), "0."+strings.Repeat("0", 1500)+ip+fp+"E+"+strconv.Itoa(1500+len(ip)))
			}
		}
	}
	return out
}

// Cutting a literal to its deciding digits rounds every literal as
// ParseFloat over the whole of it does, bit for bit, except the literals
// ParseFloat misreads, which it reads as the nearest float64 (as
// JSON.parse does): on the boundaries
// between roundings, a sticky digit's width from them, and in literals of
// every shape (long runs of zeros before and after the digits, exponents
// of thousands of digits, ±0, overflow and underflow).
func TestShortNumberRoundsAsParseFloat(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	lits := boundaryLiterals(rng)
	zeros := strings.Repeat("0", 3000)
	nines := strings.Repeat("9", 3000)
	lits = append(lits,
		"0", "-0", "0.0", "-0.000e99999", "0e-5", "1", "-1", "123.456", "1e308", "1.8e308", "-1e309", "4.9e-324", "2.4e-324", "2.5e-324", "1e-400",
		"1"+zeros, "1"+zeros+"e-3000", "1"+zeros+"e-3308", "1"+zeros+"e-3309", "0."+zeros+"1", "0."+zeros+"1e3000", "0."+zeros+"1e2999",
		"0."+zeros+"1e+3324", "-0."+zeros+"0", nines, "0."+nines, "9."+nines+"e307", "1e"+zeros+"1", "1e-"+zeros+"1", "1e+"+zeros, "1e"+strings.Repeat("9", 40),
		"1e-"+strings.Repeat("9", 40), "0.5e-"+zeros, "2."+zeros+"1", "1."+strings.Repeat("0", 799)+"1", "1."+strings.Repeat("0", 798)+"1",
	)
	for range 2000 {
		var b strings.Builder
		if rng.IntN(2) == 0 {
			b.WriteByte('-')
		}
		if rng.IntN(3) == 0 {
			b.WriteByte('0')
		} else {
			b.WriteByte(byte('1' + rng.IntN(9)))
			for range rng.IntN(1200) {
				b.WriteByte(byte('0' + rng.IntN(10)))
			}
		}
		if rng.IntN(2) == 0 {
			b.WriteByte('.')
			b.WriteString(strings.Repeat("0", rng.IntN(1000)))
			for range 1 + rng.IntN(1200) {
				b.WriteByte(byte('0' + rng.IntN(10)))
			}
		}
		if rng.IntN(2) == 0 {
			b.WriteString([]string{"e", "E", "e+", "e-", "E-"}[rng.IntN(5)])
			b.WriteString(strconv.Itoa(rng.IntN(4000)))
		}
		lits = append(lits, b.String())
	}
	mis := 0
	for _, lit := range lits {
		want, got := numberWant(t, lit, &mis), shortened(t, lit)
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("%.80q… (%d bytes): %v (%#x), want %v (%#x)", lit, len(lit), got, math.Float64bits(got), want, math.Float64bits(want))
		}
		// And through the parser, metered and not, as a document and in one.
		if len(lit) > numberDigits {
			for _, doc := range []string{lit, "[" + lit + "]"} {
				v, err := ParseJSONString(doc)
				if err != nil {
					t.Fatal(err)
				}
				m := budget.NewMeter(context.Background())
				mv, err := parseJSON(&m, 1+len(lit)%61, []byte(doc), 0)
				if err != nil {
					t.Fatal(err)
				}
				if doc[0] == '[' {
					v, mv = v.Index(0), mv.Index(0)
				}
				if math.Float64bits(v.NumValue()) != math.Float64bits(want) || math.Float64bits(mv.NumValue()) != math.Float64bits(want) {
					t.Fatalf("%.80q…: parsed %v, metered %v; want %v", lit, v.NumValue(), mv.NumValue(), want)
				}
			}
		}
	}
	t.Logf("%d literals, %d of them misread by ParseFloat", len(lits), mis)
}

// FuzzShortNumber holds shortNumber to ParseFloat over the whole literal
// (or to the nearest float64 where ParseFloat misreads it),
// for literals of every shape the fuzzer builds out of digits, a point,
// runs of zeros and an exponent.
func FuzzShortNumber(f *testing.F) {
	f.Add(true, "1", "5", uint16(0), uint16(0), "", 0)
	f.Add(false, "2", "2250738585072011", uint16(300), uint16(800), "-", 308)
	f.Add(false, "0", "1", uint16(1100), uint16(0), "+", 1100)
	f.Add(false, "179769313486231580793728971405301", "", uint16(0), uint16(276), "", 0)
	f.Fuzz(func(t *testing.T, neg bool, ip, fp string, lead, trail uint16, esign string, exp int) {
		digitsOf := func(s string) string {
			b := []byte(s)
			for i := range b {
				b[i] = '0' + b[i]%10
			}
			return string(b)
		}
		ip, fp = digitsOf(ip), digitsOf(fp)
		if ip == "" || (ip[0] == '0' && len(ip) > 1) {
			ip = "1" + ip
		}
		var b strings.Builder
		if neg {
			b.WriteByte('-')
		}
		b.WriteString(ip)
		if fp != "" || lead > 0 {
			b.WriteByte('.')
			b.WriteString(strings.Repeat("0", int(lead%4096)))
			b.WriteString(fp + "0")
		}
		b.WriteString(strings.Repeat("0", int(trail%4096)))
		if esign != "" || exp != 0 {
			b.WriteByte('e')
			if esign == "-" || esign == "+" {
				b.WriteString(esign)
			}
			e := exp % 100000
			b.WriteString(strconv.Itoa(max(e, -e)))
		}
		lit := b.String()
		mis := 0
		if want, got := numberWant(t, lit, &mis), shortened(t, lit); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("%q: %v, want %v", lit, got, want)
		}
	})
}

// The pass that cuts a long literal short charges its bytes as the scan
// does, and polls between them: a megabyte of digits is not read in one
// stretch twice. Counted in polls, not timed.
func TestShortNumberPolls(t *testing.T) {
	lit := []byte("0." + strings.Repeat("0", 1<<20) + "1e5")
	ctx := &pollCtx{Context: context.Background()}
	m := budget.NewMeter(ctx)
	p := parser{meter: &m, every: jsonPollBytes}
	if _, err := p.shortNumber(lit); err != nil {
		t.Fatal(err)
	}
	if min := len(lit) / (32 << 10); ctx.polls < min {
		t.Errorf("%d polls over %d bytes; want at least %d", ctx.polls, len(lit), min)
	}
	half := &pollCtx{Context: context.Background(), stop: ctx.polls / 2}
	m = budget.NewMeter(half)
	p = parser{meter: &m, every: jsonPollBytes}
	if _, err := p.shortNumber(lit); !errors.Is(err, context.Canceled) || p.stop == nil {
		t.Errorf("a context ended at poll %d of %d did not stop the pass: %v", half.stop, ctx.polls, err)
	}
}

// Go's strconv.ParseFloat (1.27.2) reads this literal as 0: 1.5 times the
// smallest subnormal plus 2^-2100, written as a 1,777-digit integer times
// 1e-2100. Near a midpoint its fast path gives up, and the slow path's
// decimal.set (internal/strconv/atof.go, b.dp = b.nd at lines 110 and 133)
// takes the decimal point to follow the 800th digit it keeps. JSON.parse
// (node 24) and the exact value give 1e-323, as the parser must, whatever
// ParseFloat does: the test holds the parse to big.Rat's exact rounding and
// only logs ParseFloat's answer, so a Go that fixes it changes nothing here.
func TestParseJSONReadsLongIntegerLiteralExactly(t *testing.T) {
	x := new(big.Float).SetPrec(5000).SetMantExp(new(big.Float).SetPrec(5000).SetInt64(3), -1075)
	x.Add(x, new(big.Float).SetPrec(5000).SetMantExp(new(big.Float).SetPrec(5000).SetInt64(1), -2100))
	s := strings.TrimRight(x.Text('f', 2200), "0")
	_, fp, _ := strings.Cut(s, ".")
	digits := strings.TrimLeft(fp, "0")
	for _, lit := range []string{
		digits + "e-" + strconv.Itoa(len(fp)),                                              // no point
		digits[:1000] + "." + digits[1000:] + "e" + strconv.Itoa(len(digits)-1000-len(fp)), // 1,000 digits before it
	} {
		want, ok := exactNumber(lit)
		if !ok || want != 1e-323 {
			t.Fatalf("the literal's exact value rounds to %v, want 1e-323", want)
		}
		pf, _ := strconv.ParseFloat(lit, 64)
		t.Logf("%d bytes: strconv.ParseFloat reads %v (Go misreads it when 0)", len(lit), pf)
		for _, doc := range []string{lit, "[" + lit + "]"} {
			v, err := ParseJSONString(doc)
			if err != nil {
				t.Fatal(err)
			}
			if doc[0] == '[' {
				v = v.Index(0)
			}
			if got := v.NumValue(); math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("ParseJSON(%.40q…) = %v, want %v (the exact rounding, and JSON.parse's)", doc, got, want)
			}
		}
	}
}

// longMidpointLiterals are the literals of
// TestParseJSONReadsLongIntegerLiteralExactly, which strconv.ParseFloat
// misreads, and whose exact value rounds to 1e-323.
func longMidpointLiterals() []string {
	x := new(big.Float).SetPrec(5000).SetMantExp(new(big.Float).SetPrec(5000).SetInt64(3), -1075)
	x.Add(x, new(big.Float).SetPrec(5000).SetMantExp(new(big.Float).SetPrec(5000).SetInt64(1), -2100))
	s := strings.TrimRight(x.Text('f', 2200), "0")
	_, fp, _ := strings.Cut(s, ".")
	digits := strings.TrimLeft(fp, "0")
	return []string{
		digits + "e-" + strconv.Itoa(len(fp)),
		digits[:1000] + "." + digits[1000:] + "e" + strconv.Itoa(len(digits)-1000-len(fp)),
		"0." + fp,
	}
}

// C99 (audit 2026-10-18): every conversion of decimal text to a number is the
// one exact routine JSON.parse uses (ParseDecimal): Number() of a string
// (StringToNumber), and through it the CSV and expression coercions, read
// the long literal that ParseFloat misreads as V8 does. The expression
// evaluator's literal, parseFloat and parseInt are checked in its package.
func TestEveryDecimalConversionIsExact(t *testing.T) {
	for _, lit := range longMidpointLiterals() {
		want, ok := exactNumber(lit)
		if !ok || want != 1e-323 {
			t.Fatalf("the literal's exact value rounds to %v, want 1e-323", want)
		}
		for name, got := range map[string]float64{
			"ParseDecimal":            must(ParseDecimal(lit)),
			"ParseDecimal +":          must(ParseDecimal("+" + lit)),
			"StringToNumber":          StringToNumber(" \u2028" + lit + "\ufeff"),
			"ToNumber of [lit]":       ToNumber(ArrOf(Str(lit))),
			"AsDouble":                Str(lit).AsDouble(),
			"-StringToNumber of -lit": -StringToNumber("-" + lit),
		} {
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("%s of the %d-byte literal = %v, want %v", name, len(lit), got, want)
			}
		}
	}
	for _, c := range []struct {
		lit  string
		want float64
		ok   bool
	}{
		{"1", 1, true}, {"-0", math.Copysign(0, -1), true}, {".5", 0.5, true}, {"5.", 5, true},
		{"1e400", math.Inf(1), true}, {"-1e400", math.Inf(-1), true}, {"1e-400", 0, true},
		{"+12.5e1", 125, true}, {"", math.NaN(), false}, {".", math.NaN(), false}, {"1e", math.NaN(), false},
		{"Infinity", math.NaN(), false}, {"0x10", math.NaN(), false}, {"1_0", math.NaN(), false},
	} {
		got, ok := ParseDecimal(c.lit)
		if ok != c.ok || math.Float64bits(got) != math.Float64bits(c.want) && !(math.IsNaN(got) && math.IsNaN(c.want)) {
			t.Errorf("ParseDecimal(%q) = %v, %v; want %v, %v", c.lit, got, ok, c.want, c.ok)
		}
	}
}

func must(f float64, ok bool) float64 {
	if !ok {
		return math.NaN()
	}
	return f
}

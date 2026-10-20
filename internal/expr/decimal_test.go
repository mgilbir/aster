package expr

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"
)

// C99 (audit 2026-10-18): an expression's numeric literal, parseFloat,
// parseInt in radix 10 and the unary + read decimal text with the one exact
// routine JSON.parse uses (jsval.ParseDecimal), so the 1,783-byte literal
// strconv.ParseFloat misreads as 0 is 1e-323 everywhere, as in V8.
func TestDecimalTextIsReadExactlyEverywhere(t *testing.T) {
	x := new(big.Float).SetPrec(5000).SetMantExp(new(big.Float).SetPrec(5000).SetInt64(3), -1075)
	x.Add(x, new(big.Float).SetPrec(5000).SetMantExp(new(big.Float).SetPrec(5000).SetInt64(1), -2100))
	s := strings.TrimRight(x.Text('f', 2200), "0")
	_, fp, _ := strings.Cut(s, ".")
	digits := strings.TrimLeft(fp, "0")
	lit := digits + "e-" + strconv.Itoa(len(fp))
	for _, src := range []string{
		lit,
		"parseFloat('" + lit + "')",
		"parseFloat(' " + lit + "x')",
		"+'" + lit + "'",
		"toNumber('" + lit + "')",
		"0." + fp,
	} {
		got := evalWith(t, nil, src)
		if f := got.NumValue(); f != 1e-323 {
			t.Errorf("%.30s… = %v, want 1e-323", src, f)
		}
	}
	// parseInt reads the digits of a long integer as a literal: 2^1000
	// written out, then with leading zeros past 800 digits.
	p := new(big.Int).Lsh(big.NewInt(1), 1000).String()
	for _, src := range []string{"parseInt('" + p + "', 10)", "parseInt('" + strings.Repeat("0", 900) + p + "')"} {
		if f := evalWith(t, nil, src).NumValue(); f != math.Ldexp(1, 1000) {
			t.Errorf("%.30s… = %v, want 2^1000", src, f)
		}
	}
}

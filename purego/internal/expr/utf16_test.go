package expr

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestUTF16RoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 2000; i++ {
		units := make([]uint16, rng.IntN(12))
		for j := range units {
			switch rng.IntN(4) {
			case 0:
				units[j] = uint16(0xD800 + rng.IntN(0x800)) // any surrogate, paired or not
			case 1:
				units[j] = uint16(rng.IntN(0x80))
			default:
				units[j] = uint16(rng.IntN(0x10000))
			}
		}
		s := fromUTF16(units)
		back := toUTF16(s)
		if len(back) != len(units) {
			t.Fatalf("%x -> %q -> %x", units, s, back)
		}
		for j := range units {
			if back[j] != units[j] {
				t.Fatalf("%x -> %q -> %x", units, s, back)
			}
		}
		if utf16Len(s) != len(units) {
			t.Fatalf("utf16Len(%q) = %d, want %d (%x)", s, utf16Len(s), len(units), units)
		}
		if isASCII(s) != !strings.ContainsFunc(s, func(r rune) bool { return r >= 0x80 }) {
			t.Fatal("isASCII")
		}
	}
}

func TestCompareUTF16(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	rand16 := func() []uint16 {
		u := make([]uint16, rng.IntN(5))
		for j := range u {
			switch rng.IntN(3) {
			case 0:
				u[j] = uint16(0xD800 + rng.IntN(0x800))
			case 1:
				u[j] = uint16(0xE000 + rng.IntN(0x2000))
			default:
				u[j] = uint16(rng.IntN(0x300))
			}
		}
		return u
	}
	for i := 0; i < 5000; i++ {
		a, b := rand16(), rand16()
		want := 0
		for j := 0; j < len(a) && j < len(b) && want == 0; j++ {
			switch {
			case a[j] < b[j]:
				want = -1
			case a[j] > b[j]:
				want = 1
			}
		}
		if want == 0 {
			switch {
			case len(a) < len(b):
				want = -1
			case len(a) > len(b):
				want = 1
			}
		}
		if got := compareUTF16(fromUTF16(a), fromUTF16(b)); got != want {
			t.Fatalf("compare(%x, %x) = %d, want %d", a, b, got, want)
		}
	}
	// Supplementary characters sort by their leading surrogate.
	if compareUTF16("\U0001F600", "￿") >= 0 {
		t.Error("U+1F600 must sort before U+FFFF in UTF-16 order")
	}
	_ = utf16.IsSurrogate
}

func TestJSOrder(t *testing.T) {
	p, err := Compile("merge({b: 1, 10: 2}, {2: 3, a: 4})")
	if err != nil {
		t.Fatal(err)
	}
	v, _ := p.Eval(NewScope(nil))
	if got := strings.Join(v.ObjValue().Keys(), ","); got != "2,10,b,a" {
		t.Errorf("key order %s, want 2,10,b,a", got)
	}
}

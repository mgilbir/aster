package expr

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mgilbir/aster/internal/jsval"
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

// naiveIndex is String.prototype.indexOf / lastIndexOf on code units, the
// reference the linear searches are held to.
func naiveIndex(a, b []uint16, from int, last bool) int {
	match := func(i int) bool {
		for j := range b {
			if a[i+j] != b[j] {
				return false
			}
		}
		return true
	}
	if last {
		for i := min(from, len(a)-len(b)); i >= 0; i-- {
			if match(i) {
				return i
			}
		}
		return -1
	}
	for i := from; i+len(b) <= len(a); i++ {
		if match(i) {
			return i
		}
	}
	return -1
}

// The searches work on canonical WTF-8, so lone and paired surrogates on
// either side of a match, long needles (Knuth-Morris-Pratt) and short ones
// (strings.Index) must all agree with a search on code units (C21, C97).
func TestSearchMatchesCodeUnits(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	alphabet := []uint16{'a', 'b', 0xE9, 0xD83D, 0xDE00, 0xDE01}
	gen := func(n int) []uint16 {
		u := make([]uint16, n)
		for i := range u {
			u[i] = alphabet[rng.IntN(len(alphabet))]
		}
		return u
	}
	s := NewScope(nil)
	for i := 0; i < 20000; i++ {
		a := gen(rng.IntN(40))
		var b []uint16
		if len(a) > 0 && rng.IntN(2) == 0 {
			x := rng.IntN(len(a))
			b = append(b, a[x:x+rng.IntN(len(a)-x+1)]...)
		} else {
			b = gen(rng.IntN(4))
		}
		if rng.IntN(8) == 0 { // a needle past shortNeedle bytes
			a = append(gen(200), a...)
			b = append(append([]uint16(nil), a[:70]...), b...)
		}
		from := rng.IntN(len(a) + 1)
		sa, sb := fromUTF16(a), fromUTF16(b)
		if got, want := s.indexOf(sa, sb, from), naiveIndex(a, b, from, false); got != want {
			t.Fatalf("indexOf(%x, %x, %d) = %d, want %d", a, b, from, got, want)
		}
		if got, want := s.lastIndexOf(sa, sb, from), naiveIndex(a, b, from, true); got != want {
			t.Fatalf("lastIndexOf(%x, %x, %d) = %d, want %d", a, b, from, got, want)
		}
		x, y := rng.IntN(len(a)+1), rng.IntN(len(a)+1)
		if x > y {
			x, y = y, x
		}
		if got, want := s.substr(sa, x, y), fromUTF16(a[x:y]); got != want {
			t.Fatalf("substr(%x, %d, %d) = %q, want %q", a, x, y, got, want)
		}
		// Concatenation joins halves: the result is the canonical string of
		// the joined code units.
		c := gen(rng.IntN(5))
		if got, want := s.concat(sa, fromUTF16(c)), fromUTF16(append(append([]uint16(nil), a...), c...)); got != want {
			t.Fatalf("concat(%x, %x) = %q, want %q", a, c, got, want)
		}
	}
}

// Searching is linear (C21): a needle that almost matches everywhere, the
// worst case of a naive search (quadratic), costs twice the comparison steps
// when the haystack and the needle double. The steps are counted
// (Scope.searchSteps), not timed, so the guard reads the same on any machine
// and under -race: linear work doubles them, quadratic work quadruples them.
func TestSearchIsLinear(t *testing.T) {
	type input struct{ hay, needle string }
	for _, c := range []struct {
		src  string
		make func(n int) input
	}{
		{"indexof(h, x)", func(n int) input { return input{strings.Repeat("a", 2*n), strings.Repeat("a", n) + "b"} }},
		{"lastindexof(h, x)", func(n int) input { return input{strings.Repeat("a", 2*n), strings.Repeat("a", n) + "b"} }},
		{"lastindexof(h, x)", func(n int) input { return input{strings.Repeat("a", 2*n), "b" + strings.Repeat("a", n)} }},
		{"length(split(h, x))", func(n int) input { return input{strings.Repeat("a", 2*n), strings.Repeat("a", n) + "b"} }},
		{"replace(h, x, 'y')", func(n int) input { return input{strings.Repeat("a", 2*n), strings.Repeat("a", n) + "b"} }},
		{"indexof(h, x)", func(n int) input { return input{strings.Repeat("é", 2*n), strings.Repeat("é", n) + "b"} }},
		// A needle that begins with a low surrogate is searched on code units.
		{"indexof(h, x)", func(n int) input {
			return input{strings.Repeat("\U0001F600", n), fromUTF16([]uint16{0xDE00}) + strings.Repeat("\U0001F600", n/2) + "b"}
		}},
	} {
		p, err := Compile(c.src)
		if err != nil {
			t.Fatal(err)
		}
		steps := func(n int) int64 {
			in := c.make(n)
			s := NewScope(hostileEnv{"h": jsval.Str(in.hay), "x": jsval.Str(in.needle)})
			if _, err := p.Eval(s); err != nil {
				t.Fatalf("%s: %v", c.src, err)
			}
			return s.searchSteps
		}
		const n = 1 << 12 // the needle is far past shortNeedle: the counted search runs
		a, b := steps(n), steps(2*n)
		if a == 0 {
			t.Errorf("%s: no search steps counted at n=%d; the search is not the counted one", c.src, n)
			continue
		}
		// 2 for linear work, 4 for quadratic; the counts are exact, so the
		// bound needs no margin for noise.
		if r := float64(b) / float64(a); r > 2.25 {
			t.Errorf("%s: %d search steps at n=%d, %d at n=%d: ratio %.2f, past 2.25 (linear is 2, quadratic 4)", c.src, a, n, b, 2*n, r)
		}
	}
}

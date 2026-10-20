package jsval

import (
	"compress/gzip"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The string functions against V8 on all of Unicode: testdata/strings.json.gz
// is recorded from node by testdata/gen_strings.mjs.
func TestJSStringFunctionsMatchV8(t *testing.T) {
	f, err := os.Open("testdata/strings.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseJSON(raw) // keeps the lone surrogates the vectors hold
	if err != nil {
		t.Fatal(err)
	}
	lower, upper := doc.Get("lower").ObjValue(), doc.Get("upper").ObjValue()
	if lower.Len() < 1000 || upper.Len() < 1000 {
		t.Fatalf("too few vectors: %d lower, %d upper", lower.Len(), upper.Len())
	}
	bad := 0
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		s := string(cp)
		key := strconv.Itoa(int(cp))
		for _, c := range []struct {
			name string
			fn   func(string) string
			want *Object
		}{{"toLowerCase", JSLower, lower}, {"toUpperCase", JSUpper, upper}} {
			want := s
			if w, ok := c.want.Get(key); ok {
				want = w.StrValue()
			}
			if got := c.fn(s); got != want && bad < 20 {
				bad++
				t.Errorf("U+%04X.%s() = %+q, V8 gives %+q", cp, c.name, got, want)
			}
		}
	}
	for _, c := range doc.Get("ctx").Items() {
		s := c.Get("s").StrValue()
		if got, want := JSLower(s), c.Get("lower").StrValue(); got != want {
			t.Errorf("%+q.toLowerCase() = %+q, V8 gives %+q", s, got, want)
		}
		if got, want := JSUpper(s), c.Get("upper").StrValue(); got != want {
			t.Errorf("%+q.toUpperCase() = %+q, V8 gives %+q", s, got, want)
		}
	}
	// The final sigma, beside every code point: before a sigma, after one,
	// and between one and a cased letter (the Cased and Case_Ignorable
	// tables decide them, and Go's unicode package has a Unicode version of
	// its own).
	rangeSet := func(list Value) []bool {
		if list.Len() < 50 {
			t.Fatal("too few sigma vectors")
		}
		in := make([]bool, 0x110000)
		for _, r := range list.Items() {
			for cp := int(r.Items()[0].NumValue()); cp <= int(r.Items()[1].NumValue()); cp++ {
				in[cp] = true
			}
		}
		return in
	}
	before, after, mid := rangeSet(doc.Get("sigma").Get("before")), rangeSet(doc.Get("sigma").Get("after")), rangeSet(doc.Get("sigma").Get("mid"))
	sigmaBad := 0
	for cp := rune(0); cp <= 0x10FFFF && sigmaBad < 20; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		s := string(cp)
		for _, c := range []struct {
			name string
			got  bool
			want bool
		}{
			{`"A" c "Σ"`, strings.HasSuffix(JSLower("A"+s+"Σ"), "ς"), before[cp]},
			{`"AΣ" c`, strings.HasPrefix(JSLower("AΣ" + s)[1:], "ς"), after[cp]},
			{`"AΣ" c "A"`, strings.HasPrefix(JSLower("AΣ" + s + "A")[1:], "ς"), mid[cp]},
		} {
			if c.got != c.want {
				sigmaBad++
				t.Errorf("U+%04X in %s: final sigma %v, V8 gives %v", cp, c.name, c.got, c.want)
			}
		}
	}
	space := map[rune]bool{}
	for _, v := range doc.Get("space").Items() {
		space[rune(v.NumValue())] = true
	}
	for r := rune(0); r <= 0xFFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if IsJSSpace(r) != space[r] {
			t.Errorf("IsJSSpace(U+%04X) = %v, V8 trims it: %v", r, IsJSSpace(r), space[r])
		}
	}
	for _, c := range doc.Get("trims").Items() {
		s := c.Get("s").StrValue()
		if got := JSTrim(s); got != c.Get("trim").StrValue() {
			t.Errorf("%+q.trim() = %+q, V8 gives %+q", s, got, c.Get("trim").StrValue())
		}
		if got := JSTrimStart(s); got != c.Get("start").StrValue() {
			t.Errorf("%+q.trimStart() = %+q", s, got)
		}
		if got := JSTrimEnd(s); got != c.Get("end").StrValue() {
			t.Errorf("%+q.trimEnd() = %+q", s, got)
		}
	}
}

func TestUTF16LenAndToWellFormed(t *testing.T) {
	lone := string(AppendSurrogate(nil, 0xD83D))
	for _, c := range []struct {
		s    string
		n    int
		well string
	}{
		{"", 0, ""}, {"abc", 3, "abc"}, {"é", 1, "é"}, {"😀", 2, "😀"},
		{"a" + lone + "b", 3, "a�b"}, {lone + lone, 2, "��"},
		{"\xff", 1, "\xff"}, {"\xed\x9f\xbf", 1, "\xed\x9f\xbf"}, // U+D7FF is no surrogate
	} {
		if got := UTF16Len(c.s); got != c.n {
			t.Errorf("UTF16Len(%+q) = %d, want %d", c.s, got, c.n)
		}
		if got := ToWellFormed(c.s); got != c.well {
			t.Errorf("ToWellFormed(%+q) = %+q, want %+q", c.s, got, c.well)
		}
	}
	if JSLower("A"+lone+"Σ") != "a"+lone+"σ" {
		t.Errorf("toLowerCase across a lone surrogate = %+q", JSLower("A"+lone+"Σ"))
	}
	if !strings.Contains(JSUpper("ß"+lone), "SS") {
		t.Errorf("toUpperCase(ß) = %+q", JSUpper("ß"+lone))
	}
}

// RegExpFoldEqual against node 24's new RegExp("^(?:"+a+")", "i").test(b).
func TestRegExpFoldEqual(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"k", "K", false}, {"s", "ſ", false}, {"é", "É", true}, {"i", "İ", false},
		{"ı", "I", false}, {"µ", "Μ", true}, {"ǆ", "ǅ", true}, {"March", "mARCH", true}, {"ab", "a", false},
	} {
		if got := RegExpFoldEqual(c.a, c.b); got != c.want {
			t.Errorf("RegExpFoldEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

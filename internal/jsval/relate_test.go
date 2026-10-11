package jsval

import "testing"

func TestCompareUTF16(t *testing.T) {
	// U+FF5E is a BMP character above the surrogate range; a supplementary
	// character's high surrogate (0xD83D) sorts before it in UTF-16 but after
	// it in UTF-8 byte order.
	if CompareUTF16("\U0001F600", "～") >= 0 {
		t.Error("emoji must sort before U+FF5E in UTF-16 order")
	}
	if CompareUTF16("a", "ab") >= 0 || CompareUTF16("b", "a") <= 0 || CompareUTF16("x", "x") != 0 {
		t.Error("basic ordering")
	}
}

// CompareUTF16 orders by code unit: two characters above U+FFFF that share a
// high surrogate by their low ones, and a lone surrogate (three WTF-8 bytes,
// as the expression evaluator writes it) as the unit it is (C98).
func TestCompareUTF16Units(t *testing.T) {
	const hi, lo = "\xed\xa0\xbd", "\xed\xb8\x80" // U+D83D, U+DE00 alone
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"\U00010000", "\U00010001", -1},
		{"\U0001F601", "\U0001F600", 1},
		{"\U0001F600", hi, 1}, // D83D DE00 > D83D
		{hi, "\U0001F600", -1},
		{lo, "\U0001F600", 1}, // DE00 > D83D
		{lo, "￿", -1},
		{"", "\U0001F600", 1}, // E000 > D83D
		{"a" + hi, "a" + lo, -1},
		{"\x80b", "\x81a", 1}, // invalid bytes read as U+FFFD alike
		{"x", "x", 0},
	} {
		if got := CompareUTF16(c.a, c.b); got != c.want {
			t.Errorf("CompareUTF16(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

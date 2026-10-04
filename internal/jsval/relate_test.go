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

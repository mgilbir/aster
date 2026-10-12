package textcut

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A cut lands on a character boundary whatever byte it falls on, is at most
// n bytes plus the ellipsis, is a prefix of the text, and is stable.
func TestCut(t *testing.T) {
	const n = 1024
	for pad := 0; pad < 4; pad++ {
		s := strings.Repeat("a", pad) + strings.Repeat("é€😀", 400)
		c := Cut(s, n)
		if len(c) > n+len("…") || !utf8.ValidString(c) || !strings.HasSuffix(c, "…") || !strings.HasPrefix(s, strings.TrimSuffix(c, "…")) {
			t.Errorf("pad %d: cut to %d bytes, %.40q…", pad, len(c), c[max(0, len(c)-40):])
		}
		if cc := Cut(c, n); cc != c {
			t.Errorf("pad %d: cutting again gives %d bytes, want %d", pad, len(cc), len(c))
		}
	}
	if s := strings.Repeat("a", n); Cut(s, n) != s {
		t.Error("a text of exactly n bytes is cut")
	}
	if Prefix("é", 1) != "" || Prefix("ab", -1) != "" || Prefix("ab", 5) != "ab" {
		t.Error("Prefix at the edges")
	}
}

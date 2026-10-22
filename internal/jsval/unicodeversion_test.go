package jsval

import (
	"context"
	"testing"
	"unicode"
)

// The regular expressions run on goecma262, which takes its Unicode data from
// the Go standard library: unicode.SimpleFold for the case-insensitive
// flags, unicode.Categories and Scripts for \p{...}. The module therefore
// requires the Go whose Unicode is the pinned node's (go.mod: Go 1.26 ships
// Unicode 15, Go 1.27 Unicode 17), and this test fails when the toolchain's
// Unicode is not the version the case tables were recorded from, which is
// the pinned node's process.versions.unicode.
func TestToolchainUnicodeIsNodes(t *testing.T) {
	if want := caseUnicodeVersion + ".0"; unicode.Version != want {
		t.Fatalf("the Go toolchain has Unicode %s, the pinned node %s: regular expressions (\\p{...}, the i flag) take the toolchain's Unicode data and would differ from node's; build with the Go go.mod requires", unicode.Version, want)
	}
}

// Code points of Unicode 16 and 17 in regular expressions, each answered by
// node 24.21.0 (process.versions.unicode 17.0) as listed, and not by Go 1.26.
func TestPatternUnicode17IsNodes(t *testing.T) {
	for _, c := range []struct {
		src, flags, text string
		want             bool
	}{
		{`\p{L}`, "u", "\U00010D4A", true},            // GARAY LETTER A, Unicode 16
		{`^\p{Lu}$`, "u", "Ɤ", true},                  // LATIN CAPITAL LETTER RAMS HORN, Unicode 16
		{`\p{Script=Garay}`, "u", "\U00010D50", true}, // GARAY CAPITAL LETTER A
		{`\u{A7CB}`, "iu", "ɤ", true},                 // it folds to U+0264
		{`\u{1C89}`, "iu", "ᲊ", true},                 // CYRILLIC TJE, Unicode 16
		{`\u{10D50}`, "iu", "\U00010D70", true},       // GARAY CAPITAL/SMALL LETTER A
		{`^\p{Lu}$`, "u", "\U00010D70", false},        // the small letter is not upper case
	} {
		p, err := NewPattern(context.Background(), c.src, c.flags, func(int64) error { return nil })
		if err != nil {
			t.Fatalf("/%s/%s: %v", c.src, c.flags, err)
		}
		if got := p.Re.MatchString(c.text); got != c.want {
			t.Errorf("/%s/%s.test(%+q) = %v, node says %v", c.src, c.flags, c.text, got, c.want)
		}
	}
}

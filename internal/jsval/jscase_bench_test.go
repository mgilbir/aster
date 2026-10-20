package jsval

import (
	"strings"
	"testing"
)

// The case mapping on the text it meets: ASCII (the fast path), Latin text
// with a few accents, Greek with a final sigma, and CJK, which nothing in it
// maps.
func BenchmarkCaseMapping(b *testing.B) {
	texts := []struct{ name, s string }{
		{"ascii", strings.Repeat("Hello, World! ", 20)},
		{"latin", strings.Repeat("Crème brûlée Straße Ökonomie ", 10)},
		{"greek", strings.Repeat("ΟΔΥΣΣΕΎΣ ΑΣ ", 20)},
		{"cjk", strings.Repeat("日本語のテキスト", 30)},
	}
	for _, c := range texts {
		b.Run("lower/"+c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.s)))
			for i := 0; i < b.N; i++ {
				JSLower(c.s)
			}
		})
		b.Run("upper/"+c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.s)))
			for i := 0; i < b.N; i++ {
				JSUpper(c.s)
			}
		})
	}
}

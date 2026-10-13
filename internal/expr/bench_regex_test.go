package expr

import "testing"

// BenchmarkCompileRegex compiles expressions holding regular expression
// literals, which every compile builds afresh: the cost of a pattern's
// compile, its bound and its charge, for typical patterns.
func BenchmarkCompileRegex(b *testing.B) {
	for _, src := range []string{
		"test(/^[a-z]+$/i, datum.cat)",
		"replace(datum.name, /\\s+\\(([^)]*)\\)$/, '')",
		"test(/^(\\d{4})-(\\d{2})-(\\d{2})(?:T(\\d{2}):(\\d{2}))?$/, datum.date)",
		"test(/(?:foo|bar|baz){20}/, datum.cat)",
	} {
		b.Run(src[:12], func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compile(src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

package expr

import "testing"

func BenchmarkCompileTemplatesCached(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		for _, src := range benchExprs {
			if _, err := CompileCached(src); err != nil {
				b.Fatal(err)
			}
		}
	}
}

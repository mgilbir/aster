package expr

import "testing"

var benchExprs = []string{
	"datum['Miles_per_Gallon']",
	"datum.a > 3 && datum.b < 10",
	"isValid(datum['x']) && isFinite(+datum['x'])",
	"format(datum.value, '.2f') + ' units'",
	"scale('x', datum.a) + bandwidth('y') / 2",
}

func BenchmarkCompileTemplates(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		for _, src := range benchExprs {
			if _, err := Compile(src); err != nil {
				b.Fatal(err)
			}
		}
	}
}

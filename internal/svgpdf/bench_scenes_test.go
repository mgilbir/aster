package svgpdf_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/svgpdf"
	"github.com/mgilbir/aster/internal/text"
)

// benchSVG renders a Vega-Lite chart of n rows to SVG, with rows made by row
// from a fixed pseudo-random sequence.
func benchSVG(b testing.TB, mark, enc string, n int, row func(i int, r func() float64) string) string {
	b.Helper()
	seed := uint32(42)
	next := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed) / (1 << 32)
	}
	var v strings.Builder
	v.WriteByte('[')
	for i := range n {
		if i > 0 {
			v.WriteByte(',')
		}
		v.WriteString(row(i, next))
	}
	v.WriteByte(']')
	spec := fmt.Sprintf(`{"width":600,"height":400,"data":{"values":%s},"mark":%q,"encoding":%s}`, v.String(), mark, enc)
	c, err := aster.New()
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	svg, err := c.VegaLiteToSVG([]byte(spec))
	if err != nil {
		b.Fatal(err)
	}
	return svg
}

// benchScenes returns the SVG of the benchmark scenes that stress the PDF
// stage (see scenes_bench_test.go in the root).
func benchScenes(b testing.TB) []struct{ name, svg string } {
	xy := `{"x":{"field":"x","type":"quantitative"},"y":{"field":"y","type":"quantitative"}`
	scenes := []struct{ name, svg string }{
		{"symbols-100000", benchSVG(b, "point", xy+`}`, 100000, func(_ int, r func() float64) string {
			return fmt.Sprintf(`{"x":%.4f,"y":%.4f}`, r(), r())
		})},
		{"line-100000", benchSVG(b, "line", `{"x":{"field":"i","type":"quantitative"},"y":{"field":"v","type":"quantitative"}}`, 100000, func(i int, r func() float64) string {
			return fmt.Sprintf(`{"i":%d,"v":%.3f}`, i, r())
		})},
		{"dense-labels-2000", benchSVG(b, "text", xy+`,"text":{"field":"t"}}`, 2000, func(i int, r func() float64) string {
			return fmt.Sprintf(`{"x":%.4f,"y":%.4f,"t":"label %d"}`, r(), r(), i)
		})},
	}
	return scenes
}

// BenchmarkConvertScenes times svgpdf.Convert alone, on the SVG of the scenes
// that stress the PDF stage.
func BenchmarkConvertScenes(b *testing.B) {
	m, err := text.New()
	if err != nil {
		b.Fatal(err)
	}
	for _, sc := range benchScenes(b) {
		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := svgpdf.Convert(sc.svg, m, svgpdf.Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

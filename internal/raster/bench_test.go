package raster

import (
	"testing"
)

var benchFiles = []string{"bar_1d", "scatter_image", "area_gradient", "geo_sphere", "interactive_geo_earthquakes", "repeat_splom", "text_format", "trellis_line_quarter"}

func benchSVG(b *testing.B, name string) []byte { return corpusSVG(b, name) }

// BenchmarkRender measures rasterization only (SVG bytes to pixels);
// BenchmarkRenderPNG includes PNG encoding. The inputs are upstream's SVGs
// from the node oracle, so the benchmarks skip without it.
func BenchmarkRender(b *testing.B) {
	for _, n := range benchFiles {
		svg := benchSVG(b, n)
		b.Run(n, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(svg)))
			for i := 0; i < b.N; i++ {
				if _, err := Render(svg, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRenderPNG(b *testing.B) {
	for _, n := range benchFiles {
		svg := benchSVG(b, n)
		b.Run(n, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := RenderPNG(svg, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRenderScale2(b *testing.B) {
	svg := benchSVG(b, "trellis_line_quarter")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Render(svg, Options{Scale: 2}); err != nil {
			b.Fatal(err)
		}
	}
}

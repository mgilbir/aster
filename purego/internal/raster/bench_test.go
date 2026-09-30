package raster

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

var benchFiles = []string{"bar_1d", "scatter_image", "area_gradient", "geo_sphere", "interactive_geo_earthquakes", "repeat_splom", "text_format", "trellis_line_quarter"}

func benchSVG(b *testing.B, name string) []byte {
	svg, err := os.ReadFile(filepath.Join(corpusDir, name+".svg"))
	if err != nil {
		b.Skip("corpus not found")
	}
	return svg
}

// BenchmarkRender measures rasterization only (SVG bytes to pixels);
// BenchmarkResvg runs the same input through the WASM resvg renderer and
// includes PNG encoding, so BenchmarkRenderPNG is the comparable figure.
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

func BenchmarkResvg(b *testing.B) {
	r, err := refRenderer()
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range benchFiles {
		svg := benchSVG(b, n)
		b.Run(n, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := r.Render(context.Background(), svg, 1); err != nil {
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

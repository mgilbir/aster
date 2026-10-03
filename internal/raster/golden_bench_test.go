package raster

import (
	"os"
	"path/filepath"
	"testing"
)

// goldenSVGs are checked-in SVGs (vl-convert's expected renderings), so these
// benchmarks need no node oracle and can gate performance in CI
// (scripts/benchgate.sh); BenchmarkRender and BenchmarkRenderPNG cover the
// larger corpus where the oracle is available.
var goldenSVGs = []string{"circle_binned", "bar_chart_trellis_compact", "stacked_bar_h"}

func goldenSVG(b *testing.B, name string) []byte {
	b.Helper()
	svg, err := os.ReadFile(filepath.Join("..", "..", "testdata", "vl-convert", "expected", "v5_8", name+".svg"))
	if err != nil {
		b.Fatal(err)
	}
	return svg
}

func BenchmarkGoldenRender(b *testing.B) {
	for _, n := range goldenSVGs {
		svg := goldenSVG(b, n)
		b.Run(n, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Render(svg, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGoldenRenderPNG(b *testing.B) {
	for _, n := range goldenSVGs {
		svg := goldenSVG(b, n)
		b.Run(n, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := RenderPNG(svg, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

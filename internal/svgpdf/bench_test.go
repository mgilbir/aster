package svgpdf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster/internal/text"
)

// BenchmarkConvert translates checked-in SVGs (vl-convert's expected
// renderings) to PDF; it needs no node oracle, so scripts/benchgate.sh can
// run it in CI.
func BenchmarkConvert(b *testing.B) {
	m, err := text.New()
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range []string{"circle_binned", "bar_chart_trellis_compact", "stacked_bar_h"} {
		svg, err := os.ReadFile(filepath.Join("..", "..", "testdata", "vl-convert", "expected", "v5_8", n+".svg"))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(n, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Convert(string(svg), m, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

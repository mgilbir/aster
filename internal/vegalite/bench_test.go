package vegalite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func benchSpec(b *testing.B, name string) {
	data, err := os.ReadFile(filepath.Join("testdata", "specs", name))
	if err != nil {
		b.Fatal(err)
	}
	spec, err := jsval.ParseJSON(data)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(spec, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileBar(b *testing.B)     { benchSpec(b, "gallery/bar.vl.json") }
func BenchmarkCompileTrellis(b *testing.B) { benchSpec(b, "gallery/trellis_bar_histogram.vl.json") }
func BenchmarkCompileLayered(b *testing.B) { benchSpec(b, "gallery/layer_bar_annotations.vl.json") }
func BenchmarkCompileSplom(b *testing.B)   { benchSpec(b, "gallery/interactive_splom.vl.json") }
func BenchmarkCompileBoxplot(b *testing.B) { benchSpec(b, "gallery/boxplot_2D_vertical.vl.json") }

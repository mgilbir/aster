package purego_test

import (
	"os"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/purego"
)

// benchSpecs are Vega-Lite examples chosen to span chart sizes: a trivial
// bar chart, a faceted chart, a large scatter-plot matrix and a choropleth.
var benchSpecs = []string{
	"bar",
	"trellis_bar",
	"repeat_splom",
	"geo_choropleth",
}

func benchSpec(b *testing.B, name string) []byte {
	b.Helper()
	spec, err := os.ReadFile("../testdata/vega-lite/v6.4.3/specs/" + name + ".vl.json")
	if err != nil {
		b.Skip(err)
	}
	return spec
}

// renderer is the part of the API shared by both engines' converters.
type renderer interface {
	VegaLiteToSVG([]byte) (string, error)
	Close() error
}

func benchEngines(b *testing.B, run func(b *testing.B, r renderer, spec []byte)) {
	// Each converter gets its own loader: Close closes the loader.
	engines := []struct {
		name string
		make func() (renderer, error)
	}{
		{"quickjs", func() (renderer, error) { return aster.New(aster.WithLoader(corpusLoader(b))) }},
		{"purego", func() (renderer, error) { return purego.New(purego.WithLoader(corpusLoader(b))) }},
	}
	for _, name := range benchSpecs {
		spec := benchSpec(b, name)
		for _, e := range engines {
			b.Run(name+"/"+e.name, func(b *testing.B) {
				r, err := e.make()
				if err != nil {
					b.Fatal(err)
				}
				defer r.Close()
				// Warm up: first renders build caches (fonts, compiled JS).
				if _, err := r.VegaLiteToSVG(spec); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				run(b, r, spec)
			})
		}
	}
}

func BenchmarkVegaLiteToSVG(b *testing.B) {
	benchEngines(b, func(b *testing.B, r renderer, spec []byte) {
		for b.Loop() {
			if _, err := r.VegaLiteToSVG(spec); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkVegaLiteToPNG(b *testing.B) {
	benchEngines(b, func(b *testing.B, r renderer, spec []byte) {
		p, ok := r.(interface {
			VegaLiteToPNG([]byte, ...purego.PNGOption) ([]byte, error)
		})
		if !ok {
			a := r.(*aster.Converter)
			for b.Loop() {
				if _, err := a.VegaLiteToPNG(spec); err != nil {
					b.Fatal(err)
				}
			}
			return
		}
		for b.Loop() {
			if _, err := p.VegaLiteToPNG(spec); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkNewAndFirstRender measures a cold start: creating a converter
// and rendering one small chart.
func BenchmarkNewAndFirstRender(b *testing.B) {
	spec := benchSpec(b, "bar")
	b.Run("quickjs", func(b *testing.B) {
		for b.Loop() {
			c, err := aster.New()
			if err != nil {
				b.Fatal(err)
			}
			if _, err := c.VegaLiteToSVG(spec); err != nil {
				b.Fatal(err)
			}
			c.Close()
		}
	})
	b.Run("purego", func(b *testing.B) {
		for b.Loop() {
			c, err := purego.New()
			if err != nil {
				b.Fatal(err)
			}
			if _, err := c.VegaLiteToSVG(spec); err != nil {
				b.Fatal(err)
			}
			c.Close()
		}
	})
}

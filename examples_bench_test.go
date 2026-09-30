package aster_test

import (
	"os"
	"testing"

	"github.com/mgilbir/aster"
)

// benchSpecs are Vega-Lite examples chosen to span chart sizes: a trivial
// bar chart, a faceted chart, a large scatter-plot matrix and a choropleth.
var benchSpecs = []string{
	"bar",
	"trellis_bar",
	"repeat_splom",
	"geo_choropleth",
}

func exampleSpec(b *testing.B, name string) []byte {
	b.Helper()
	spec, err := os.ReadFile("testdata/vega-lite/v6.4.3/specs/" + name + ".vl.json")
	if err != nil {
		b.Skip(err)
	}
	return spec
}

// benchExamples runs one sub-benchmark per example on a warm converter.
func benchExamples(b *testing.B, run func(b *testing.B, c *aster.Converter, spec []byte)) {
	for _, name := range benchSpecs {
		spec := exampleSpec(b, name)
		b.Run(name, func(b *testing.B) {
			c, err := aster.New(aster.WithLoader(corpusLoader(b)))
			if err != nil {
				b.Fatal(err)
			}
			defer c.Close()
			// Warm up: first renders fill the font and expression caches.
			if _, err := c.VegaLiteToSVG(spec); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			run(b, c, spec)
		})
	}
}

func BenchmarkExamplesSVG(b *testing.B) {
	benchExamples(b, func(b *testing.B, c *aster.Converter, spec []byte) {
		for b.Loop() {
			if _, err := c.VegaLiteToSVG(spec); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkExamplesPNG(b *testing.B) {
	benchExamples(b, func(b *testing.B, c *aster.Converter, spec []byte) {
		for b.Loop() {
			if _, err := c.VegaLiteToPNG(spec); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkNewAndFirstRender measures a cold start: creating a converter
// and rendering one small chart.
func BenchmarkNewAndFirstRender(b *testing.B) {
	spec := exampleSpec(b, "bar")
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
}

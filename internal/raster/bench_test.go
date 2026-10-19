package raster

import (
	"fmt"
	"strings"
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
	for b.Loop() {
		if _, err := Render(svg, Options{Scale: 2}); err != nil {
			b.Fatal(err)
		}
	}
}

// styledSVG is a document of style sheets as a styled chart has them: 200
// rules of type, class, id, attribute and descendant selectors, 2,000
// elements with a few classes each.
func styledSVG() []byte {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400"><style>`)
	for i := range 200 {
		switch i % 5 {
		case 0:
			fmt.Fprintf(&b, ".c%d { fill: #%06x }\n", i, i*7919)
		case 1:
			fmt.Fprintf(&b, "g.row%d > rect { stroke: #%06x }\n", i%20, i*104729)
		case 2:
			fmt.Fprintf(&b, "#e%d { opacity: 0.5 }\n", i*10)
		case 3:
			fmt.Fprintf(&b, "rect[data-k=\"%d\"] { stroke-width: 2 }\n", i)
		default:
			fmt.Fprintf(&b, "svg g rect.c%d:first-child { fill-opacity: 0.8 }\n", i)
		}
	}
	b.WriteString(`</style>`)
	for row := range 20 {
		fmt.Fprintf(&b, `<g class="row%d">`, row)
		for col := range 100 {
			i := row*100 + col
			fmt.Fprintf(&b, `<rect id="e%d" class="c%d c%d mark" data-k="%d" x="%d" y="%d" width="4" height="20"/>`, i, i%200, (i+5)%200, i%200, col*4, row*20)
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

// BenchmarkRenderStyled is BenchmarkRender of a document with style sheets:
// its parse and its selector matching.
func BenchmarkRenderStyled(b *testing.B) {
	svg := styledSVG()
	b.ReportAllocs()
	b.SetBytes(int64(len(svg)))
	for b.Loop() {
		if _, err := Render(svg, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

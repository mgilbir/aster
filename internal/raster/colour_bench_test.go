package raster

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// emojiLabels is an SVG of n labels, each a word and four emoji, in family:
// a chart labelled with emoji.
func emojiLabels(n int, family string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600">`)
	for i := range n {
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="%s" font-size="12" fill="#333">label &#x1F534;&#x1F308;&#x1F600;&#x1F3A8;</text>`,
			(i%10)*80, 12+(i/10)*6%600, family)
	}
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

// BenchmarkColourGlyphs draws 1000 emoji labels: in colour, from
// ColourTest.ttf (layers, a gradient, a rotation and a composite), and in
// the embedded monochrome Noto Emoji, the cost colour is weighed against.
func BenchmarkColourGlyphs(b *testing.B) {
	data, err := os.ReadFile("../../testdata/colourfonts/ColourTest.ttf")
	if err != nil {
		b.Fatal(err)
	}
	colour, err := NewShaper(FontData{Family: "ColourTest", Data: data})
	if err != nil {
		b.Fatal(err)
	}
	mono, err := NewShaper()
	if err != nil {
		b.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		shaper Shaper
		family string
	}{{"colour", colour, "ColourTest, sans-serif"}, {"mono", mono, "sans-serif"}} {
		svg := emojiLabels(1000, c.family)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Render(svg, Options{Shaper: c.shaper}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

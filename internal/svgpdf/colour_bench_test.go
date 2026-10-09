package svgpdf

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/text"
)

// BenchmarkConvertColour converts 1000 emoji labels to PDF, in colour from
// ColourTest.ttf and in the embedded monochrome Noto Emoji.
func BenchmarkConvertColour(b *testing.B) {
	data, err := os.ReadFile("../../testdata/colourfonts/ColourTest.ttf")
	if err != nil {
		b.Fatal(err)
	}
	colour, err := text.New(text.WithFont("ColourTest", data))
	if err != nil {
		b.Fatal(err)
	}
	mono, err := text.New()
	if err != nil {
		b.Fatal(err)
	}
	for _, c := range []struct {
		name, family string
		m            *text.Measurer
	}{{"colour", "ColourTest, sans-serif", colour}, {"mono", "sans-serif", mono}} {
		var s strings.Builder
		s.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600">`)
		for i := range 1000 {
			fmt.Fprintf(&s, `<text transform="translate(%d,%d)" font-family="%s" font-size="12" fill="#333">label `+"\U0001F534\U0001F308\U0001F600\U0001F3A8"+`</text>`,
				(i%10)*80, 12+(i/10)*6%600, c.family)
		}
		s.WriteString(`</svg>`)
		svg := s.String()
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Convert(svg, c.m, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

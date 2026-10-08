package svgpdf

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/fuzzutil"
	"github.com/mgilbir/aster/internal/text"
)

// FuzzColourPainter plays op strings through the PDF painter, after the check
// that decides whether it draws them, as a glyph's painting is: it may not
// panic, and what it writes must keep its q and Q balanced.
func FuzzColourPainter(f *testing.F) {
	for _, s := range fuzzutil.PaintSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		fuzzutil.Within(t, 10*time.Second, "painting", func() {
			var check colourCheck
			fuzzutil.PlayPaint(ops, &check)
			_ = check.vector()
			r := &renderer{w: newContentWriter(), images: newImageCatalog(nil, Limits{}.withDefaults()), lim: Limits{}.withDefaults()}
			p := &pdfPainter{r: r, upem: 1000, box: shape.Rect{XMax: 1000, YMax: 1000}, m: []Matrix{Identity()}}
			p.beginGroup()
			fuzzutil.PlayPaint(ops, p)
			p.unwind()
			content := string(p.endGroup())
			depth := 0
			for _, tok := range strings.Fields(content) {
				switch tok {
				case "q":
					depth++
				case "Q":
					if depth--; depth < 0 {
						t.Fatalf("unbalanced Q:\n%s", content)
					}
				}
			}
			if depth != 0 {
				t.Fatalf("%d q left open:\n%s", depth, content)
			}
			for _, fm := range r.forms {
				if strings.Count(string(fm.content), "q\n") != strings.Count(string(fm.content), "Q\n") {
					t.Fatalf("form %s unbalanced:\n%s", fm.res, fm.content)
				}
			}
		})
	})
}

// FuzzColourFont converts every glyph of a mutated colour font to PDF.
func FuzzColourFont(f *testing.F) {
	for _, name := range []string{"ColourTest.ttf", "SbixTest.ttf", "SvgTest.ttf"} {
		data, err := os.ReadFile("../../testdata/colourfonts/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzutil.Within(t, 10*time.Second, "drawing a colour font", func() {
			m, err := text.New(text.WithFont("F", data))
			if err != nil {
				return
			}
			_, _ = Convert(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="40"><text transform="translate(0,30)" font-family="F" font-size="30" fill-opacity="0.5">`+
				"\U0001F534\U0001F7E2\U0001F308\U0001F31E\U0001F300\U0001F600\U0001F3A8\U0001F4A0\U0001F4A1\U0001F52E\U0001F4A7A</text></svg>", m, Options{})
		})
	})
}

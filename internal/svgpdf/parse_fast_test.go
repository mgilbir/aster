package svgpdf

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// sameTree reports whether two element trees are equal, treating nil and
// empty attribute lists alike.
func sameTree(a, b *element) bool {
	if a.name != b.name || a.text != b.text || len(a.attrs) != len(b.attrs) || len(a.children) != len(b.children) {
		return false
	}
	for i := range a.attrs {
		if a.attrs[i] != b.attrs[i] {
			return false
		}
	}
	for i := range a.children {
		if !sameTree(a.children[i], b.children[i]) {
			return false
		}
	}
	return true
}

// checkFastParse verifies the scanner's contract on one document: when it
// accepts, encoding/xml accepts too and builds the same tree. It reports
// whether the scanner accepted.
func checkFastParse(t *testing.T, svg string) bool {
	t.Helper()
	lim := Limits{}.withDefaults()
	fast, ok, err := parseSVGFast(context.Background(), svg, lim)
	if err != nil {
		t.Fatalf("%q: %v", svg, err)
	}
	if !ok {
		return false
	}
	slow, err := parseSVGXML(context.Background(), svg, lim)
	if err != nil {
		t.Fatalf("scanner accepted a document encoding/xml rejects (%v): %q", err, svg)
	}
	if !sameTree(fast, slow) {
		t.Fatalf("scanner and encoding/xml disagree on %q", svg)
	}
	return true
}

func TestParseSVGFastMatchesXML(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "vl-convert", "expected", "v5_8", "*.svg"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden SVGs (%v)", err)
	}
	var docs []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !checkFastParse(t, string(b)) {
			t.Errorf("%s: the scanner declined a Vega document", filepath.Base(f))
		}
		docs = append(docs, string(b))
	}

	accepted := []string{
		`<svg width="1" height="1"/>`,
		"  <svg width='1' height='1'>\n</svg>\n",
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="x" width="1"><g a = "1"  b='2'><text>a &amp; b &lt; &quot;c&quot; &apos;d&apos; &gt; é</text></g></svg>`,
		`<svg><text>one<g/>two</text></svg>`,
		`<svg><g a="1" a="2"/></svg>`,
		`<svg><g d="M0 0
L1 1"/></svg>`,
		`<svg><text>a > b ]] c</text></svg>`,
	}
	rejected := []string{
		``, `<svg>`, `<svg></g>`, `<svg></svg><svg></svg>`, `<g/>`, `<rect/>`, `x<svg/>`,
		`<?xml version="1.0"?><svg/>`, `<!-- c --><svg/>`, `<svg><!-- c --></svg>`, `<svg><![CDATA[x]]></svg>`,
		`<svg><text>a&#65;</text></svg>`, `<svg><text>a&nbsp;</text></svg>`, `<svg><text>a&amp</text></svg>`,
		"<svg><text>a\r\nb</text></svg>", `<svg><text>]]></text></svg>`, `<svg a="]]>"/>`,
		`<svg a=1/>`, `<svg a/>`, `<svg a="1"b="2"/>`, `<svg a="<"/>`, `<svg a="1/>`, `<svg><g></svg>`,
		`<svg><g></g `, `<svg><g></gx></g></svg>`, `<svg:g/>`, `<svg><x:g/></svg>`, `<svg xlink:href="a"/>`,
		`<svg><tref/></svg>`, "<svg a=\"\x01\"/>", "<svg a=\"\xff\"/>", "<svg><text>\xef\xbf\xbe</text></svg>",
		`<svg xmlns:="x"/>`, `<svg><g/ ></svg>`, `<svg><g`, `<svg><`,
	}
	for _, d := range accepted {
		if !checkFastParse(t, d) {
			t.Errorf("the scanner declined %q", d)
		}
	}
	for _, d := range rejected {
		if checkFastParse(t, d) {
			t.Errorf("the scanner accepted %q", d)
		}
	}

	// Mutations of real documents: whatever the scanner accepts must be what
	// encoding/xml builds.
	rng := rand.New(rand.NewSource(1))
	for range 4000 {
		d := []byte(docs[rng.Intn(len(docs))])
		if len(d) > 4000 {
			d = d[:4000]
		}
		for range 1 + rng.Intn(3) {
			i := rng.Intn(len(d))
			switch rng.Intn(4) {
			case 0:
				d[i] = "<>&\"'/= \r\n;!?]:x"[rng.Intn(16)]
			case 1:
				d = append(d[:i], d[i+1:]...)
			case 2:
				d = d[:i]
			default:
				d = append(d[:i], append([]byte{"<>&\"'/="[rng.Intn(7)]}, d[i:]...)...)
			}
			if len(d) == 0 {
				d = []byte("<svg/>")
			}
		}
		checkFastParse(t, string(d))
	}
}

func FuzzParseSVGFast(f *testing.F) {
	f.Add(`<svg width="1"><g a="x"><text>t&amp;</text></g></svg>`)
	f.Fuzz(func(t *testing.T, svg string) { checkFastParse(t, svg) })
}

func BenchmarkParseSVG(b *testing.B) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "vl-convert", "expected", "v5_8", "*.svg"))
	var docs []string
	for _, f := range files {
		d, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		docs = append(docs, string(d))
	}
	lim := Limits{}.withDefaults()
	for _, mode := range []struct {
		name  string
		parse func(context.Context, string, Limits) (*element, error)
	}{{"xml", parseSVGXML}, {"fast", parseSVG}} {
		b.Run(mode.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, d := range docs {
					if _, err := mode.parse(context.Background(), d, lim); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

package svgpdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/textmeasure"
	pdf0 "github.com/mgilbir/pdf0"
)

// TestConvertGoldenSVGs translates every checked-in Vega golden SVG and
// verifies the resulting PDF end to end: it must parse back with pdf0, have
// a well-formed page tree, and carry a decodable, q/Q-balanced content
// stream. This pins the translator to the exact SVG vocabulary Vega emits.
func TestConvertGoldenSVGs(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "vl-convert", "expected", "v5_8", "*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no golden SVGs found")
	}

	m, err := textmeasure.New()
	if err != nil {
		t.Fatalf("textmeasure.New: %v", err)
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			svg, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			pdf, err := Convert(string(svg), m, Options{})
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			verifyPDF(t, pdf)
		})
	}
}

// verifyPDF checks the invariants every produced PDF must satisfy.
func verifyPDF(t *testing.T, pdf []byte) {
	t.Helper()

	if len(pdf) == 0 {
		t.Fatal("empty PDF")
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("output does not start with %%PDF-: %q", pdf[:min(16, len(pdf))])
	}

	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatalf("pdf0.Read: %v", err)
	}

	// Page tree: Root → Pages → one Page with MediaBox and Contents.
	root := doc.ResolveDict(doc.Trailer.Get("Root"))
	if root == nil {
		t.Fatal("no catalog")
	}
	pages := doc.ResolveDict(root.Get("Pages"))
	if pages == nil {
		t.Fatal("no page tree")
	}
	if count, ok := pages.Get("Count").(pdf0.Integer); !ok || count != 1 {
		t.Fatalf("page count: got %v, want 1", pages.Get("Count"))
	}
	kids, ok := pages.Get("Kids").(pdf0.Array)
	if !ok || len(kids) != 1 {
		t.Fatalf("Kids: got %v", pages.Get("Kids"))
	}
	page := doc.ResolveDict(kids[0])
	if page == nil {
		t.Fatal("page is not a dictionary")
	}
	if mb, ok := page.Get("MediaBox").(pdf0.Array); !ok || len(mb) != 4 {
		t.Fatalf("MediaBox: got %v", page.Get("MediaBox"))
	}

	// Content stream must FlateDecode and balance q/Q.
	contents := doc.Resolve(page.Get("Contents"))
	stream, ok := contents.(*pdf0.Stream)
	if !ok {
		t.Fatalf("Contents: got %T", contents)
	}
	if f, ok := stream.Dict.Get("Filter").(pdf0.Name); !ok || f != "FlateDecode" {
		t.Fatalf("Filter: got %v", stream.Dict.Get("Filter"))
	}
	content, err := flateDecode(stream.Data)
	if err != nil {
		t.Fatalf("decoding content stream: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("empty content stream")
	}
	if err := checkContentStream(string(content), page, doc); err != nil {
		t.Fatal(err)
	}
}

func flateDecode(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// checkContentStream verifies q/Q balance and that every /GSn gs reference
// resolves to an ExtGState resource on the page.
func checkContentStream(content string, page *pdf0.Dictionary, doc *pdf0.Document) error {
	depth := 0
	tokens := strings.Fields(content)

	var extGState *pdf0.Dictionary
	if res := doc.ResolveDict(page.Get("Resources")); res != nil {
		extGState = doc.ResolveDict(res.Get("ExtGState"))
	}

	for i, tok := range tokens {
		switch tok {
		case "q":
			depth++
		case "Q":
			depth--
			if depth < 0 {
				return fmt.Errorf("unbalanced Q at token %d", i)
			}
		case "gs":
			if i == 0 || !strings.HasPrefix(tokens[i-1], "/") {
				return fmt.Errorf("gs without a name operand at token %d", i)
			}
			name := strings.TrimPrefix(tokens[i-1], "/")
			if extGState == nil || extGState.Get(pdf0.Name(name)) == nil {
				return fmt.Errorf("gs references missing ExtGState /%s", name)
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("unbalanced q/Q: depth %d at end of stream", depth)
	}
	return nil
}

// TestConvertDeterministic verifies byte-identical output across runs.
func TestConvertDeterministic(t *testing.T) {
	file := filepath.Join("..", "..", "testdata", "vl-convert", "expected", "v5_8", "stacked_bar_h.svg")
	svg, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	a, err := Convert(string(svg), m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Convert(string(svg), m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("output is not deterministic across conversions")
	}
}

// TestConvertUnsupportedConstructsError pins the fail-loud contract:
// constructs outside the Vega subset must error so callers can fall back to
// raster output.
func TestConvertUnsupportedConstructsError(t *testing.T) {
	cases := []struct {
		name string
		svg  string
	}{
		{"unsupported element", `<svg width="10" height="10"><circle cx="5" cy="5" r="2"/></svg>`},
		{"gradient paint", `<svg width="10" height="10"><rect width="5" height="5" fill="url(#g0)"/></svg>`},
		{"unknown attribute", `<svg width="10" height="10"><rect width="5" height="5" filter="blur(1)"/></svg>`},
		{"unknown color keyword", `<svg width="10" height="10"><rect width="5" height="5" fill="rebeccapurple"/></svg>`},
		{"missing clip target", `<svg width="10" height="10"><g clip-path="url(#nope)"/></svg>`},
		{"tspan", `<svg width="10" height="10"><text><tspan>x</tspan></text></svg>`},
		{"style attribute", `<svg width="10" height="10"><rect width="5" height="5" style="fill:red"/></svg>`},
		{"no dimensions", `<svg><rect width="5" height="5"/></svg>`},
	}
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Convert(c.svg, m, Options{}); err == nil {
				t.Errorf("expected error for %s, got none", c.name)
			}
		})
	}
}

// TestConvertClipPath exercises the defs/clipPath path, which the golden
// files do not cover (Vega emits it for clipped marks, e.g. line charts
// with clip: true).
func TestConvertClipPath(t *testing.T) {
	svg := `<svg width="100" height="100">` +
		`<defs><clipPath id="c0"><rect x="10" y="10" width="50" height="50"/></clipPath></defs>` +
		`<g clip-path="url(#c0)"><rect x="0" y="0" width="100" height="100" fill="#4c78a8"/></g>` +
		`</svg>`
	pdf, err := Convert(svg, nil, Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	verifyPDF(t, pdf)

	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	content := contentStreamOf(t, doc)
	if !strings.Contains(content, "W\nn\n") {
		t.Errorf("clip operators missing from content stream:\n%s", content)
	}
}

func contentStreamOf(t *testing.T, doc *pdf0.Document) string {
	t.Helper()
	root := doc.ResolveDict(doc.Trailer.Get("Root"))
	pages := doc.ResolveDict(root.Get("Pages"))
	kids := pages.Get("Kids").(pdf0.Array)
	page := doc.ResolveDict(kids[0])
	stream := doc.Resolve(page.Get("Contents")).(*pdf0.Stream)
	content, err := flateDecode(stream.Data)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// TestConvertCoordinateFlip pins the global y-flip: the first operator in
// the stream must be the "1 0 0 -1 0 H cm" transform.
func TestConvertCoordinateFlip(t *testing.T) {
	pdf, err := Convert(`<svg width="40" height="30"><rect width="40" height="30" fill="white"/></svg>`, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	content := contentStreamOf(t, doc)
	if !strings.HasPrefix(content, "1 0 0 -1 0 30 cm\n") {
		t.Errorf("content stream does not start with the y-flip:\n%s", content)
	}
}

// TestPDFAValidatorGaps runs pdf0's PDF/A validator as a structural oracle.
// The output is deliberately plain PDF 1.4, not PDF/A, so exactly three
// PDF/A-only rules fire (file ID, output intent, XMP metadata); anything
// else would indicate a structural defect in the generated document.
func TestPDFAValidatorGaps(t *testing.T) {
	file := filepath.Join("..", "..", "testdata", "vl-convert", "expected", "v5_8", "stacked_bar_h.svg")
	svg, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	m, err := textmeasure.New()
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := Convert(string(svg), m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	// PDF/A-only requirements a plain PDF 1.4 intentionally does not meet.
	allowed := map[string]bool{
		"6.1.3": true, // trailer must contain /ID array
		"6.2.4": true, // DeviceRGB requires an OutputIntent
		"6.7.2": true, // catalog must have /Metadata
	}
	for _, e := range pdf0.ValidatePDFABytes(doc, pdf0.PDFA1b, pdf) {
		if !allowed[e.Rule] {
			t.Errorf("unexpected validation error: %v", e)
		}
	}
}

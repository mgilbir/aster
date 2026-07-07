package svgpdf

import (
	"bytes"
	"compress/zlib"
	"fmt"

	pdf0 "github.com/mgilbir/pdf0"
)

// buildPDF assembles a single-page PDF document around the rendered content
// stream. pdf0 has no plain-document constructor (only NewPDFADocument), so
// the Catalog/Pages/Page skeleton is built directly with the object model.
//
// The declared version is 1.4: nothing beyond transparency (ExtGState CA/ca,
// a 1.4 feature) is used, and a low version keeps strict embedders like
// xdvipdfmx (LaTeX \includegraphics) from warning about downlevel output.
//
// The output is deterministic: fixed object numbering, insertion-ordered
// dictionaries, no timestamps, no /Info and no /ID.
func buildPDF(content []byte, gsList []gsEntry, width, height float64) ([]byte, error) {
	// Object 1: Catalog
	catalog := &pdf0.Dictionary{}
	catalog.Set("Type", pdf0.Name("Catalog"))
	catalog.Set("Pages", pdf0.IndirectRef{Number: 2})

	// Object 2: Pages
	pages := &pdf0.Dictionary{}
	pages.Set("Type", pdf0.Name("Pages"))
	pages.Set("Kids", pdf0.Array{pdf0.IndirectRef{Number: 3}})
	pages.Set("Count", pdf0.Integer(1))

	// Resources: ExtGState entries for the opacity values the content
	// stream references, in first-use order (deterministic).
	resources := &pdf0.Dictionary{}
	if len(gsList) > 0 {
		extGState := &pdf0.Dictionary{}
		for _, gs := range gsList {
			d := &pdf0.Dictionary{}
			d.Set("Type", pdf0.Name("ExtGState"))
			d.Set("ca", pdf0.Real(gs.alpha.fill))
			d.Set("CA", pdf0.Real(gs.alpha.stroke))
			extGState.Set(pdf0.Name(gs.name), d)
		}
		resources.Set("ExtGState", extGState)
	}

	// Object 3: Page
	page := &pdf0.Dictionary{}
	page.Set("Type", pdf0.Name("Page"))
	page.Set("Parent", pdf0.IndirectRef{Number: 2})
	page.Set("MediaBox", pdf0.Array{
		pdf0.Integer(0), pdf0.Integer(0), pdf0.Real(width), pdf0.Real(height),
	})
	page.Set("Resources", resources)
	page.Set("Contents", pdf0.IndirectRef{Number: 4})

	// Object 4: content stream, Flate-compressed. zlib output is
	// deterministic for a given input and compression level.
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(content); err != nil {
		return nil, fmt.Errorf("svgpdf: compressing content stream: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("svgpdf: compressing content stream: %w", err)
	}
	contents := &pdf0.Stream{Data: compressed.Bytes()}
	contents.Dict.Set("Length", pdf0.Integer(compressed.Len()))
	contents.Dict.Set("Filter", pdf0.Name("FlateDecode"))

	doc := &pdf0.Document{
		Version: "1.4",
		Objects: map[int]*pdf0.IndirectObject{
			1: {Number: 1, Value: catalog},
			2: {Number: 2, Value: pages},
			3: {Number: 3, Value: page},
			4: {Number: 4, Value: contents},
		},
		Trailer: pdf0.Dictionary{
			Keys:   []pdf0.Name{"Root"},
			Values: []pdf0.Object{pdf0.IndirectRef{Number: 1}},
		},
	}

	var out bytes.Buffer
	if err := doc.Write(&out); err != nil {
		return nil, fmt.Errorf("svgpdf: writing PDF: %w", err)
	}
	return out.Bytes(), nil
}

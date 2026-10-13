package aster_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster"
)

// linkLoader allows every link (Vega's own scheme allow-list still
// applies) and loads nothing.
type linkLoader struct{}

func (linkLoader) Load(context.Context, string) ([]byte, error) { return nil, errors.New("no loading") }
func (linkLoader) Sanitize(_ context.Context, uri string) (string, error) {
	return uri, nil
}

// TestVegaHrefPDF: a mark with an href is drawn in the PDF with a Link
// annotation that opens its URL; a javascript: href is no link anywhere.
func TestVegaHrefPDF(t *testing.T) {
	c, err := aster.New(aster.WithLoader(linkLoader{}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	spec := `{"width":100,"height":50,"marks":[
	 {"type":"rect","encode":{"update":{"x":{"value":10},"y":{"value":10},"width":{"value":20},"height":{"value":10},"fill":{"value":"red"},"href":{"value":"https://example.com/a?q=(1) é"}}}},
	 {"type":"rect","encode":{"update":{"x":{"value":50},"y":{"value":10},"width":{"value":20},"height":{"value":10},"href":{"value":"javascript:alert(1)"}}}}]}`
	pdf, err := c.VegaToPDF([]byte(spec), aster.WithErrorHandler(func(error) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	page := doc.ResolveDict(doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages")).Get("Kids").(pdf0.Array)[0])
	annots, _ := doc.Resolve(page.Get("Annots")).(pdf0.Array)
	if len(annots) != 1 {
		t.Fatalf("%d annotations, want 1", len(annots))
	}
	a := doc.ResolveDict(annots[0])
	if uri := string(doc.ResolveDict(a.Get("A")).Get("URI").(pdf0.String).Value); uri != "https://example.com/a?q=(1)%20%C3%A9" {
		t.Errorf("URI %q", uri)
	}
	var r []float64
	for _, v := range a.Get("Rect").(pdf0.Array) {
		switch n := v.(type) {
		case pdf0.Real:
			r = append(r, float64(n))
		case pdf0.Integer:
			r = append(r, float64(n))
		}
	}
	// The rect is 10..30 across and 10..20 down a 50-high page.
	if len(r) != 4 || r[0] != 10 || r[1] != 30 || r[2] != 30 || r[3] != 40 {
		t.Errorf("Rect %v", r)
	}
}

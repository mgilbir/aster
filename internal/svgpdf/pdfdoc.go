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
// a 1.4 feature) and Type0/CIDFontType2 fonts (1.3) is used, and a low
// version keeps strict embedders like xdvipdfmx (LaTeX \includegraphics)
// from warning about downlevel output.
//
// The output is deterministic: fixed object numbering (fonts follow the
// four skeleton objects in first-use order), insertion-ordered dictionaries,
// no timestamps, no /Info and no /ID.
func buildPDF(content [][]byte, gsList []gsEntry, fonts *fontCatalog, images *imageCatalog, paints paintDefs, width, height float64) ([]byte, error) {
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
	// Soft masks name the form XObjects they are drawn from, which are
	// numbered last; they are filled in then.
	type pendingMask struct {
		gs         *pdf0.Dictionary
		form       string
		invert     bool
		luminosity bool
	}
	var masks []pendingMask
	if len(gsList) > 0 {
		extGState := &pdf0.Dictionary{}
		for _, gs := range gsList {
			d := &pdf0.Dictionary{}
			d.Set("Type", pdf0.Name("ExtGState"))
			switch {
			case gs.bm != "":
				d.Set("BM", pdf0.Name(gs.bm))
			case gs.mask != "":
				masks = append(masks, pendingMask{d, gs.mask, gs.maskInvert, gs.maskLuminosity})
			default:
				d.Set("ca", pdf0.Real(gs.alpha.fill))
				d.Set("CA", pdf0.Real(gs.alpha.stroke))
			}
			extGState.Set(pdf0.Name(gs.name), d)
		}
		resources.Set("ExtGState", extGState)
	}

	// Font resources and their objects (Type0/CIDFontType2/descriptor/
	// font program/ToUnicode), numbered after the fixed skeleton objects.
	fontObjects := map[int]*pdf0.IndirectObject{}
	next := 5
	if fonts != nil && len(fonts.list) > 0 {
		var fontRes *pdf0.Dictionary
		var err error
		fontObjects, fontRes, next, err = buildFontObjects(fonts, next)
		if err != nil {
			return nil, err
		}
		if fontRes.Len() > 0 {
			resources.Set("Font", fontRes)
		}
	}

	// Shadings of the gradients drawn, numbered after the fonts, in
	// first-use order, then the patterns of gradient strokes, which refer to
	// them.
	if len(paints.shadings) > 0 {
		shRes := &pdf0.Dictionary{}
		shNum := map[*gradient]int{}
		for _, g := range paints.shadings {
			fontObjects[next] = &pdf0.IndirectObject{Number: next, Value: g.shading()}
			shRes.Set(pdf0.Name(g.res), pdf0.IndirectRef{Number: next})
			shNum[g] = next
			next++
		}
		resources.Set("Shading", shRes)
		if len(paints.patterns) > 0 {
			patRes := &pdf0.Dictionary{}
			for _, p := range paints.patterns {
				pat := &pdf0.Dictionary{}
				pat.Set("Type", pdf0.Name("Pattern"))
				pat.Set("PatternType", pdf0.Integer(2))
				pat.Set("Shading", pdf0.IndirectRef{Number: shNum[p.g]})
				m := p.matrix
				pat.Set("Matrix", pdf0.Array{pdf0.Real(m.A), pdf0.Real(m.B), pdf0.Real(m.C), pdf0.Real(m.D), pdf0.Real(m.E), pdf0.Real(m.F)})
				fontObjects[next] = &pdf0.IndirectObject{Number: next, Value: pat}
				patRes.Set(pdf0.Name(p.res), pdf0.IndirectRef{Number: next})
				next++
			}
			resources.Set("Pattern", patRes)
		}
	}

	// Image XObjects, numbered after the fonts, in first-use order.
	var xobjRes *pdf0.Dictionary
	if images != nil && len(images.uses) > 0 {
		var imageObjects map[int]*pdf0.IndirectObject
		imageObjects, xobjRes = buildImageObjects(images, next)
		for n, obj := range imageObjects {
			fontObjects[n] = obj
			next = max(next, n+1)
		}
		resources.Set("XObject", xobjRes)
	}

	// Form XObjects, the transparency groups of colour glyphs, numbered
	// last. They draw with the page's resources, which are then an object
	// of their own that the page and each form refer to.
	pageResources := pdf0.Object(resources)
	if len(paints.forms) > 0 {
		if xobjRes == nil {
			xobjRes = &pdf0.Dictionary{}
			resources.Set("XObject", xobjRes)
		}
		resNum := next
		next++
		fontObjects[resNum] = &pdf0.IndirectObject{Number: resNum, Value: resources}
		pageResources = pdf0.IndirectRef{Number: resNum}
		formNum := map[string]int{}
		for _, f := range paints.forms {
			s, err := f.stream(pdf0.IndirectRef{Number: resNum})
			if err != nil {
				return nil, err
			}
			fontObjects[next] = &pdf0.IndirectObject{Number: next, Value: s}
			xobjRes.Set(pdf0.Name(f.res), pdf0.IndirectRef{Number: next})
			formNum[f.res] = next
			next++
		}
		for _, m := range masks {
			sm := &pdf0.Dictionary{}
			sm.Set("Type", pdf0.Name("Mask"))
			if m.luminosity {
				sm.Set("S", pdf0.Name("Luminosity"))
			} else {
				sm.Set("S", pdf0.Name("Alpha"))
			}
			sm.Set("G", pdf0.IndirectRef{Number: formNum[m.form]})
			if m.invert {
				fn := &pdf0.Dictionary{}
				fn.Set("FunctionType", pdf0.Integer(2))
				fn.Set("Domain", pdf0.Array{pdf0.Integer(0), pdf0.Integer(1)})
				fn.Set("C0", pdf0.Array{pdf0.Integer(1)})
				fn.Set("C1", pdf0.Array{pdf0.Integer(0)})
				fn.Set("N", pdf0.Integer(1))
				sm.Set("TR", fn)
			}
			m.gs.Set("SMask", sm)
		}
	}

	// Object 3: Page
	page := &pdf0.Dictionary{}
	page.Set("Type", pdf0.Name("Page"))
	page.Set("Parent", pdf0.IndirectRef{Number: 2})
	page.Set("MediaBox", pdf0.Array{
		pdf0.Integer(0), pdf0.Integer(0), pdf0.Real(width), pdf0.Real(height),
	})
	page.Set("Resources", pageResources)
	page.Set("Contents", pdf0.IndirectRef{Number: 4})

	// Object 4: content stream, Flate-compressed. zlib output is
	// deterministic for a given input and compression level.
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	for _, chunk := range content {
		if _, err := zw.Write(chunk); err != nil {
			return nil, fmt.Errorf("svgpdf: compressing content stream: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("svgpdf: compressing content stream: %w", err)
	}
	contents := &pdf0.Stream{Data: compressed.Bytes()}
	contents.Dict.Set("Length", pdf0.Integer(compressed.Len()))
	contents.Dict.Set("Filter", pdf0.Name("FlateDecode"))

	objects := map[int]*pdf0.IndirectObject{
		1: {Number: 1, Value: catalog},
		2: {Number: 2, Value: pages},
		3: {Number: 3, Value: page},
		4: {Number: 4, Value: contents},
	}
	for n, obj := range fontObjects {
		objects[n] = obj
	}

	doc := &pdf0.Document{
		Version: "1.4",
		Objects: objects,
	}
	doc.Trailer.Set("Root", pdf0.IndirectRef{Number: 1})

	var out bytes.Buffer
	if err := doc.Write(&out); err != nil {
		return nil, fmt.Errorf("svgpdf: writing PDF: %w", err)
	}
	return out.Bytes(), nil
}

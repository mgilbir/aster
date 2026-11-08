package svgpdf

import (
	"bytes"
	"fmt"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/pdfa"
)

// buildPDF assembles a single-page PDF document around the rendered content
// stream. pdf0 has no plain-document constructor (only NewPDFADocument), so
// the Catalog/Pages/Page skeleton is built directly with the object model.
//
// The declared version is the lowest that has every feature used: 1.4
// (transparency, ExtGState CA/ca, soft masks and blend modes, are 1.4;
// Type0 fonts, CIDFontType0C and CIDFontType2 programs 1.3), or 1.5 for a
// document with ActualText marked content (ISO 32000-1, 14.9.4: a glyph
// standing for two texts, a run whose font cannot be written; C159). A low
// version keeps strict embedders like xdvipdfmx (LaTeX \includegraphics)
// from warning about downlevel output.
//
// The output is deterministic: fixed object numbering (fonts follow the
// four skeleton objects in first-use order), insertion-ordered dictionaries,
// no timestamps, no /Info and no /ID.
//
// pdfa (Options.PDFA) makes the colour device-independent, as PDF/A requires
// of a document whatever its output intent: object 5 is an sRGB ICC profile,
// the resources map DefaultRGB onto it (so every DeviceRGB colour, image and
// shading the page and its forms draw is sRGB), the page is a transparency
// group blending in it, as are the forms, and links carry the print flag.
func buildPDF(content [][]byte, gsList []gsEntry, fonts *fontCatalog, images *imageCatalog, paints paintDefs, width, height float64, pdfa bool) ([]byte, error) {
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
	// Every object of the document, by number: sized for the skeleton and
	// what a font writes (a dozen objects), so it seldom grows.
	nFonts := 0
	if fonts != nil {
		nFonts = len(fonts.list)
	}
	objects := make(map[int]*pdf0.IndirectObject, 8+12*nFonts)
	next := 5
	// blendCS is the colour space transparency groups blend in.
	blendCS := pdf0.Object(pdf0.Name("DeviceRGB"))
	if pdfa {
		icc, err := srgbICC()
		if err != nil {
			return nil, err
		}
		objects[next] = &pdf0.IndirectObject{Number: next, Value: icc}
		srgb := pdf0.Array{pdf0.Name("ICCBased"), pdf0.IndirectRef{Number: next}}
		next++
		cs := &pdf0.Dictionary{}
		cs.Set("DefaultRGB", srgb)
		resources.Set("ColorSpace", cs)
		blendCS = srgb
	}
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
			case gs.maskNone:
				d.Set("SMask", pdf0.Name("None"))
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
	fontsVersion := "1.1"
	if fonts != nil && len(fonts.list) > 0 {
		var fontRes *pdf0.Dictionary
		var err error
		fontRes, next, fontsVersion, err = buildFontObjects(fonts, objects, next)
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
			objects[next] = &pdf0.IndirectObject{Number: next, Value: g.shading()}
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
				objects[next] = &pdf0.IndirectObject{Number: next, Value: pat}
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
			objects[n] = obj
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
		objects[resNum] = &pdf0.IndirectObject{Number: resNum, Value: resources}
		pageResources = pdf0.IndirectRef{Number: resNum}
		formNum := map[string]int{}
		groupNum := next
		next++
		objects[groupNum] = &pdf0.IndirectObject{Number: groupNum, Value: transparencyGroup(blendCS)}
		for _, f := range paints.forms {
			s, err := f.stream(pdf0.IndirectRef{Number: resNum}, pdf0.IndirectRef{Number: groupNum})
			if err != nil {
				return nil, err
			}
			objects[next] = &pdf0.IndirectObject{Number: next, Value: s}
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
	if pdfa {
		group := &pdf0.Dictionary{}
		group.Set("Type", pdf0.Name("Group"))
		group.Set("S", pdf0.Name("Transparency"))
		group.Set("CS", blendCS)
		page.Set("Group", group)
	}
	if len(paints.links) > 0 {
		page.Set("Annots", annotations(paints.links, pdfa))
	}
	page.Set("Contents", pdf0.IndirectRef{Number: 4})

	// Object 4: content stream, Flate-compressed. zlib output is
	// deterministic for a given input and compression level.
	var compressed bytes.Buffer
	if err := compressTo(&compressed, content...); err != nil {
		return nil, fmt.Errorf("svgpdf: compressing content stream: %w", err)
	}
	contents := &pdf0.Stream{Data: compressed.Bytes()}
	contents.Dict.Set("Length", pdf0.Integer(compressed.Len()))
	contents.Dict.Set("Filter", pdf0.Name("FlateDecode"))

	objects[1] = &pdf0.IndirectObject{Number: 1, Value: catalog}
	objects[2] = &pdf0.IndirectObject{Number: 2, Value: pages}
	objects[3] = &pdf0.IndirectObject{Number: 3, Value: page}
	objects[4] = &pdf0.IndirectObject{Number: 4, Value: contents}

	version := "1.4"
	if paints.actualText {
		version = "1.5"
	}
	if fontsVersion > version {
		// pdf0 says what the font objects it wrote need; written bare and
		// as CIDFontType2, they need no more than 1.3.
		version = fontsVersion
	}
	doc := &pdf0.Document{
		Version: version,
		Objects: objects,
	}
	doc.Trailer.Set("Root", pdf0.IndirectRef{Number: 1})

	var out bytes.Buffer
	if err := doc.Write(&out); err != nil {
		return nil, fmt.Errorf("svgpdf: writing PDF: %w", err)
	}
	return out.Bytes(), nil
}

// srgbICC is the ICCBased colour space stream of the sRGB profile pdf0
// carries for PDF/A output intents (ICC v2.1, which every PDF/A part reads).
func srgbICC() (*pdf0.Stream, error) {
	profile, err := pdfa.DefaultSRGBProfile()
	if err != nil {
		return nil, fmt.Errorf("svgpdf: the sRGB profile: %w", err)
	}
	data, err := flateCompress(profile)
	if err != nil {
		return nil, err
	}
	s := &pdf0.Stream{Data: data}
	s.Dict.Set("N", pdf0.Integer(3))
	s.Dict.Set("Alternate", pdf0.Name("DeviceRGB"))
	s.Dict.Set("Length", pdf0.Integer(len(data)))
	s.Dict.Set("Filter", pdf0.Name("FlateDecode"))
	return s, nil
}

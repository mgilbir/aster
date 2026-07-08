package aster

import (
	"fmt"

	"github.com/mgilbir/aster/internal/fontsubset"
	"github.com/mgilbir/aster/internal/svgpdf"
)

// FontUsage reports the source bytes and referenced glyph IDs of one face in a
// rendered PDF; see svgpdf.FontUsage.
//
// It enables higher-level, file-level font embedding: render many charts with
// WithPDFText(PDFTextNamed) (which does not embed fonts), union the reported
// GIDs per face across all of them, build one shared subset with SubsetFont,
// and embed that single subset into the composed document. Compared with
// PDFTextEmbed (a subset per chart), this stores each font's glyphs once no
// matter how many charts share them.
type FontUsage = svgpdf.FontUsage

// SVGToPDFUsage is SVGToPDF plus the per-face glyph usage of the produced PDF
// (see FontUsage). It is intended with WithPDFText(PDFTextNamed): the returned
// usage is what a caller needs to embed one shared subset across many charts.
func (c *Converter) SVGToPDFUsage(svg string, opts ...PDFOption) ([]byte, []FontUsage, error) {
	if c.closed {
		// Without this guard a post-Close call would lazily build a fresh PDF
		// measurer (matching SVGToPNG's closed-converter contract).
		return nil, nil, errConverterClosed
	}
	cfg := defaultPDFConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	var mode svgpdf.TextMode
	switch cfg.text {
	case PDFTextEmbed:
		mode = svgpdf.TextEmbed
	case PDFTextNamed:
		mode = svgpdf.TextNamed
	case PDFTextOutlines:
		mode = svgpdf.TextOutlines
	default:
		return nil, nil, fmt.Errorf("aster: unknown PDF text mode %d", cfg.text)
	}

	m, err := c.pdfMeasurerInit()
	if err != nil {
		return nil, nil, err
	}
	out, uses, err := svgpdf.ConvertWithUsage(svg, m, svgpdf.Options{Text: mode})
	if err != nil {
		return nil, nil, fmt.Errorf("aster: rendering PDF: %w", err)
	}
	return out, uses, nil
}

// VegaToPDFUsage renders a Vega spec (JSON) to a vector PDF and reports its
// per-face glyph usage; see SVGToPDFUsage.
func (c *Converter) VegaToPDFUsage(spec []byte, opts ...PDFOption) ([]byte, []FontUsage, error) {
	if c.closed {
		return nil, nil, errConverterClosed
	}
	svg, err := c.VegaToSVG(spec)
	if err != nil {
		return nil, nil, err
	}
	return c.SVGToPDFUsage(svg, opts...)
}

// VegaLiteToPDFUsage renders a Vega-Lite spec (JSON) to a vector PDF and
// reports its per-face glyph usage; see SVGToPDFUsage.
func (c *Converter) VegaLiteToPDFUsage(spec []byte, opts ...PDFOption) ([]byte, []FontUsage, error) {
	if c.closed {
		return nil, nil, errConverterClosed
	}
	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		return nil, nil, err
	}
	return c.SVGToPDFUsage(svg, opts...)
}

// SubsetFont builds a TrueType subset of source containing gids, preserving the
// source's original glyph numbering. Because numbering is preserved, the subset
// resolves content that references glyphs by original GID through an Identity
// CIDToGIDMap — as PDFTextNamed output does — so it can be embedded once at a
// higher level and shared by every chart that references the same face.
//
// It returns the subset program and the source's PostScript name (matching the
// PostScriptName reported by FontUsage and the /BaseFont written by TextNamed,
// including the shared fallback for fonts that carry no PostScript name).
// Only fonts with TrueType glyph outlines can be subset; others return an
// error. Glyph IDs outside the font's range are ignored — GIDs sourced from
// FontUsage are always in range, but hand-built inputs are not validated.
func SubsetFont(source []byte, gids []uint16) (subset []byte, postScriptName string, err error) {
	f, err := fontsubset.Parse(source)
	if err != nil {
		return nil, "", fmt.Errorf("aster: parse font: %w", err)
	}
	name := svgpdf.PostScriptNameOrFallback(f.PostScriptName())
	if !f.CanSubset() {
		return nil, name, fmt.Errorf("aster: font %q has no TrueType outlines to subset", name)
	}
	set := make(map[uint16]bool, len(gids))
	for _, g := range gids {
		set[g] = true
	}
	sub, err := f.Subset(set)
	if err != nil {
		return nil, name, fmt.Errorf("aster: subset font: %w", err)
	}
	return sub, name, nil
}

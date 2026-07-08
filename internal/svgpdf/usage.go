package svgpdf

// FontUsage reports, for one shaped face used in a converted document, the
// source font bytes and the glyph IDs actually referenced.
//
// It is meant for the TextNamed mode: named output defers font embedding so a
// higher-level assembler can embed a single shared subset across many rendered
// documents instead of one subset per document. To build that shared subset the
// assembler needs, per face, which glyphs were used and the source program they
// came from — which is what FontUsage carries.
//
// GIDs are the original glyph numbers of the source font. TextNamed content
// streams reference these directly with an Identity CIDToGIDMap, so a subset
// built from Source that preserves glyph numbering (see the Subset routine used
// for TextEmbed) resolves them without remapping.
type FontUsage struct {
	// PostScriptName is the /BaseFont value TextNamed writes for this face; the
	// assembler matches it against the non-embedded font descriptors in the
	// composed document.
	PostScriptName string
	// Source is a copy of the font program the face was shaped against (the
	// same bytes TextEmbed would subset). It may be a caller-provided, system,
	// or built-in face, so callers cannot always reconstruct it themselves.
	// The copy is the caller's to keep or mutate.
	Source []byte
	// GIDs are the glyph IDs of Source referenced by this document, sorted
	// ascending and deduplicated.
	GIDs []uint16
}

// ConvertWithUsage is Convert plus the per-face glyph usage of the produced
// document (see FontUsage). Usage is most useful with Options{Text: TextNamed};
// with TextEmbed the fonts are already embedded and usage is informational.
func ConvertWithUsage(svg string, shaper TextShaper, opts Options) ([]byte, []FontUsage, error) {
	root, err := parseSVG(svg)
	if err != nil {
		return nil, nil, err
	}
	content, gsList, fonts, width, height, err := render(root, shaper, opts)
	if err != nil {
		return nil, nil, err
	}
	pdf, err := buildPDF(content, gsList, fonts, width, height)
	if err != nil {
		return nil, nil, err
	}
	return pdf, fonts.usage(), nil
}

// usage snapshots the catalog's per-face glyph usage in first-use order. The
// catalog is nil in TextOutlines mode (all text is drawn as paths, no fonts are
// referenced), for which there is no usage to report. Faces that ended up with
// no used glyphs are skipped, mirroring buildFontObjects: they have no
// /BaseFont in the document to match against.
func (c *fontCatalog) usage() []FontUsage {
	if c == nil {
		return nil
	}
	out := make([]FontUsage, 0, len(c.list))
	for _, f := range c.list {
		if len(f.used) == 0 {
			continue
		}
		out = append(out, FontUsage{
			// The same name /BaseFont carries in TextNamed output (including
			// the PostScriptNameOrFallback rule for fonts without a PostScript
			// name), so matching holds by construction.
			PostScriptName: f.baseFontName(TextNamed),
			// Copied: the internal slice aliases process-global bytes (the
			// embedded fonts' go:embed data, or the caller's WithFont slice);
			// handing it out mutable across the public API boundary would let
			// one caller silently corrupt font handling process-wide.
			Source: append([]byte(nil), f.data...),
			GIDs:   f.sortedGIDs(),
		})
	}
	return out
}

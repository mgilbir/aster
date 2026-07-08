package svgpdf

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"fmt"
	"sort"
	"unicode/utf16"

	"github.com/go-text/typesetting/font"
	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster/internal/fontsubset"
)

// TextMode selects how <text> elements are represented in the PDF.
type TextMode int

const (
	// TextEmbed emits real PDF text with subset TrueType fonts embedded:
	// glyphs are referenced by ID (2 bytes each) and only the used outlines
	// ship, once, in the font program. Smallest self-contained output, and
	// text is selectable. Runs whose face cannot be recovered or subset
	// (CFF fonts, unloadable system fonts) fall back to outlines.
	TextEmbed TextMode = iota

	// TextNamed emits the same PDF text structure without the font program:
	// the font is referenced by name only. Output is tiny and text renders
	// exactly once the consuming document embeds the same font file — glyph
	// IDs are specific to the TTF used at generation time, so this mode is
	// for pipelines that assemble many charts and embed the font once at
	// assembly time. Standalone viewers substitute a different font and may
	// show wrong glyphs.
	TextNamed

	// TextOutlines converts every glyph to filled path outlines. No fonts
	// are involved at all, at the cost of much larger output and
	// unselectable text.
	TextOutlines
)

// Options configures Convert.
type Options struct {
	Text TextMode
}

// pdfFont accumulates the state of one font resource while rendering.
type pdfFont struct {
	res    string // resource name (F0, F1, ...)
	data   []byte
	parsed *fontsubset.Font
	used   map[uint16]bool
	toUni  map[uint16]string
}

// fontCatalog resolves shaped faces to PDF font resources, caching failures
// so unusable faces consistently fall back to outline drawing.
type fontCatalog struct {
	mode   TextMode
	shaper TextShaper
	fonts  map[*font.Face]*pdfFont
	failed map[*font.Face]bool
	list   []*pdfFont // first-use order, for deterministic output
}

func newFontCatalog(mode TextMode, shaper TextShaper) *fontCatalog {
	return &fontCatalog{
		mode:   mode,
		shaper: shaper,
		fonts:  make(map[*font.Face]*pdfFont),
		failed: make(map[*font.Face]bool),
	}
}

// fontFor returns the PDF font for a shaped face, or nil when the face
// cannot be represented as a PDF font (the caller then draws outlines).
func (c *fontCatalog) fontFor(face *font.Face) *pdfFont {
	if f, ok := c.fonts[face]; ok {
		return f
	}
	if c.failed[face] {
		return nil
	}
	data := c.shaper.FontData(face)
	if data == nil {
		c.failed[face] = true
		return nil
	}
	parsed, err := fontsubset.Parse(data)
	if err != nil {
		c.failed[face] = true
		return nil
	}
	if c.mode == TextEmbed && !parsed.CanSubset() {
		// No TrueType outlines to embed (CFF font); outlines still render it
		// faithfully.
		c.failed[face] = true
		return nil
	}
	f := &pdfFont{
		res:    fmt.Sprintf("F%d", len(c.list)),
		data:   data,
		parsed: parsed,
		used:   make(map[uint16]bool),
		toUni:  make(map[uint16]string),
	}
	c.fonts[face] = f
	c.list = append(c.list, f)
	return f
}

// PostScriptNameOrFallback returns the name used as /BaseFont for a font with
// the given PostScript name ("Unknown" when the font carries none). It is the
// single source of the fallback rule, shared by the document writer, FontUsage
// reporting, and the public SubsetFont, so the three always agree.
func PostScriptNameOrFallback(name string) string {
	if name == "" {
		return "Unknown"
	}
	return name
}

// baseFontName builds the /BaseFont value. Embedded subsets carry the
// spec-mandated 6-letter subset prefix, derived deterministically from the
// font bytes and the used glyph set.
func (f *pdfFont) baseFontName(mode TextMode) string {
	name := PostScriptNameOrFallback(f.parsed.PostScriptName())
	if mode != TextEmbed {
		return name
	}
	h := sha256.New()
	h.Write(f.data)
	for _, g := range f.sortedGIDs() {
		h.Write([]byte{byte(g >> 8), byte(g)})
	}
	sum := h.Sum(nil)
	prefix := make([]byte, 6)
	for i := range prefix {
		prefix[i] = 'A' + sum[i]%26
	}
	return string(prefix) + "+" + name
}

func (f *pdfFont) sortedGIDs() []uint16 {
	gids := make([]uint16, 0, len(f.used))
	for g := range f.used {
		gids = append(gids, g)
	}
	sort.Slice(gids, func(i, j int) bool { return gids[i] < gids[j] })
	return gids
}

// widthsArray builds the CIDFont /W array: runs of consecutive glyph IDs with
// their advances in glyph space (1000 units per em).
func (f *pdfFont) widthsArray() pdf0.Array {
	gids := f.sortedGIDs()
	upem := float64(f.parsed.UnitsPerEm())
	var out pdf0.Array
	for i := 0; i < len(gids); {
		j := i
		for j+1 < len(gids) && gids[j+1] == gids[j]+1 {
			j++
		}
		var ws pdf0.Array
		for k := i; k <= j; k++ {
			w := float64(f.parsed.Advance(gids[k])) * 1000 / upem
			ws = append(ws, pdf0.Real(w))
		}
		out = append(out, pdf0.Integer(gids[i]), ws)
		i = j + 1
	}
	return out
}

// toUnicodeCMap renders the ToUnicode CMap stream content for text
// extraction (copy/paste, search) from the glyph→text mapping collected
// during rendering.
func (f *pdfFont) toUnicodeCMap() []byte {
	var b bytes.Buffer
	b.WriteString(`/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
`)
	gids := f.sortedGIDs()
	var mapped []uint16
	for _, g := range gids {
		if f.toUni[g] != "" {
			mapped = append(mapped, g)
		}
	}
	// The spec caps bfchar blocks at 100 entries.
	for i := 0; i < len(mapped); i += 100 {
		n := min(100, len(mapped)-i)
		fmt.Fprintf(&b, "%d beginbfchar\n", n)
		for _, g := range mapped[i : i+n] {
			fmt.Fprintf(&b, "<%04X> <", g)
			for _, u := range utf16.Encode([]rune(f.toUni[g])) {
				fmt.Fprintf(&b, "%04X", u)
			}
			b.WriteString(">\n")
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString(`endcmap
CMapName currentdict /CMap defineresource pop
end
end
`)
	return b.Bytes()
}

// buildFontObjects creates the PDF objects for every font in the catalog,
// numbering them sequentially from nextNum. It returns the objects, the
// /Font resource dictionary, and the next free object number.
func buildFontObjects(catalog *fontCatalog, nextNum int) (map[int]*pdf0.IndirectObject, *pdf0.Dictionary, int, error) {
	objects := make(map[int]*pdf0.IndirectObject)
	fontRes := &pdf0.Dictionary{}
	add := func(v pdf0.Object) pdf0.IndirectRef {
		n := nextNum
		nextNum++
		objects[n] = &pdf0.IndirectObject{Number: n, Value: v}
		return pdf0.IndirectRef{Number: n}
	}

	for _, f := range catalog.list {
		if len(f.used) == 0 {
			continue
		}
		baseName := f.baseFontName(catalog.mode)
		m := f.parsed.Metrics()
		scale := 1000 / float64(f.parsed.UnitsPerEm())

		descriptor := &pdf0.Dictionary{}
		descriptor.Set("Type", pdf0.Name("FontDescriptor"))
		descriptor.Set("FontName", pdf0.Name(baseName))
		flags := 1 << 2 // Symbolic: glyphs addressed by ID, outside StandardEncoding
		if m.FixedPitch {
			flags |= 1 << 0
		}
		descriptor.Set("Flags", pdf0.Integer(flags))
		descriptor.Set("FontBBox", pdf0.Array{
			pdf0.Real(float64(m.BBox[0]) * scale), pdf0.Real(float64(m.BBox[1]) * scale),
			pdf0.Real(float64(m.BBox[2]) * scale), pdf0.Real(float64(m.BBox[3]) * scale),
		})
		descriptor.Set("ItalicAngle", pdf0.Real(m.ItalicAngle))
		descriptor.Set("Ascent", pdf0.Real(float64(m.Ascent)*scale))
		descriptor.Set("Descent", pdf0.Real(float64(m.Descent)*scale))
		descriptor.Set("CapHeight", pdf0.Real(float64(m.CapHeight)*scale))
		// StemV is required but absent from TrueType fonts; 80 is the
		// customary text-weight estimate.
		descriptor.Set("StemV", pdf0.Integer(80))

		if catalog.mode == TextEmbed {
			subset, err := f.parsed.Subset(f.used)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("svgpdf: subsetting %s: %w", baseName, err)
			}
			compressed, err := flateCompress(subset)
			if err != nil {
				return nil, nil, 0, err
			}
			fontFile := &pdf0.Stream{Data: compressed}
			fontFile.Dict.Set("Length", pdf0.Integer(len(compressed)))
			fontFile.Dict.Set("Filter", pdf0.Name("FlateDecode"))
			fontFile.Dict.Set("Length1", pdf0.Integer(len(subset)))
			descriptor.Set("FontFile2", add(fontFile))

			// CIDSet: the used-CID bitmap PDF/A requires for subset CIDFonts
			// (ISO 19005-1, 6.2.10). Bit 0 of byte 0 is the high bit.
			bitmap := make([]byte, (f.parsed.NumGlyphs()+7)/8)
			for g := range f.used {
				if int(g) < f.parsed.NumGlyphs() {
					bitmap[g/8] |= 0x80 >> (g % 8)
				}
			}
			bitmap[0] |= 0x80 // .notdef is always part of the subset
			compressedSet, err := flateCompress(bitmap)
			if err != nil {
				return nil, nil, 0, err
			}
			cidSet := &pdf0.Stream{Data: compressedSet}
			cidSet.Dict.Set("Length", pdf0.Integer(len(compressedSet)))
			cidSet.Dict.Set("Filter", pdf0.Name("FlateDecode"))
			descriptor.Set("CIDSet", add(cidSet))
		}
		descRef := add(descriptor)

		cidSystem := &pdf0.Dictionary{}
		cidSystem.Set("Registry", pdf0.String{Value: []byte("Adobe")})
		cidSystem.Set("Ordering", pdf0.String{Value: []byte("Identity")})
		cidSystem.Set("Supplement", pdf0.Integer(0))

		cidFont := &pdf0.Dictionary{}
		cidFont.Set("Type", pdf0.Name("Font"))
		cidFont.Set("Subtype", pdf0.Name("CIDFontType2"))
		cidFont.Set("BaseFont", pdf0.Name(baseName))
		cidFont.Set("CIDSystemInfo", cidSystem)
		cidFont.Set("FontDescriptor", descRef)
		// Content-stream glyph codes ARE glyph IDs of the (subset) font
		// program: the sparse subset preserves original glyph numbering.
		cidFont.Set("CIDToGIDMap", pdf0.Name("Identity"))
		cidFont.Set("W", f.widthsArray())
		cidRef := add(cidFont)

		cmap := f.toUnicodeCMap()
		compressedCMap, err := flateCompress(cmap)
		if err != nil {
			return nil, nil, 0, err
		}
		toUni := &pdf0.Stream{Data: compressedCMap}
		toUni.Dict.Set("Length", pdf0.Integer(len(compressedCMap)))
		toUni.Dict.Set("Filter", pdf0.Name("FlateDecode"))
		toUniRef := add(toUni)

		type0 := &pdf0.Dictionary{}
		type0.Set("Type", pdf0.Name("Font"))
		type0.Set("Subtype", pdf0.Name("Type0"))
		type0.Set("BaseFont", pdf0.Name(baseName))
		type0.Set("Encoding", pdf0.Name("Identity-H"))
		type0.Set("DescendantFonts", pdf0.Array{cidRef})
		type0.Set("ToUnicode", toUniRef)
		fontRes.Set(pdf0.Name(f.res), add(type0))
	}
	return objects, fontRes, nextNum, nil
}

func flateCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return nil, fmt.Errorf("svgpdf: compressing stream: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("svgpdf: compressing stream: %w", err)
	}
	return buf.Bytes(), nil
}

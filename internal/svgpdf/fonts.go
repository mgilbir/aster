package svgpdf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"unicode/utf16"

	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/fontsubset"
	"github.com/mgilbir/aster/internal/text"
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
	// Context, when non-nil, is polled while parsing and rendering; the
	// conversion stops with the context's error once it is done.
	Context context.Context
	// Limits bounds the work an untrusted SVG may cause; zero fields select
	// the defaults (see Limits).
	Limits Limits
	// Images fetches the image an <image> element refers to by anything but
	// a data: URI (which is decoded in place). It is called once per distinct
	// href, before drawing, from several goroutines at once. nil leaves such
	// images undrawn. An image that cannot be fetched is left out, as a broken
	// image is; an error wrapping budget.ErrLimit fails the conversion.
	Images func(ctx context.Context, href string) ([]byte, error)
	// ImageError, when non-nil, is told of each <image> left out because it
	// could not be read (not fetched, not a PNG, JPEG or GIF, or not
	// decodable), with its href. Errors wrapping budget.ErrLimit fail the
	// conversion instead.
	ImageError func(href string, err error)
}

// pdfFont accumulates the state of one font resource while rendering.
type pdfFont struct {
	res    string // resource name (F0, F1, ...)
	data   []byte
	parsed *fontsubset.Font
	face   *text.Face
	// embed is set when the font program is embedded (TextEmbed): a subset
	// of its TrueType outlines, or of its CFF ones (cff).
	embed bool
	// cff is set when the embedded program is a subset of the font's CFF
	// outlines, cut by forme's subsetter (FontFile3, CIDFontType0C), and
	// cid when that font is CID-keyed: its glyphs are addressed by CID.
	cff, cid bool
	// outlined is set for a font whose runs are drawn as outlines and
	// written as invisible text in it, by name (see hiddenFor): in
	// TextEmbed, a font whose program cannot be embedded.
	outlined bool
	used     map[uint16]bool   // by glyph
	toUni    map[uint16]string // by glyph
	// checked records, by glyph, whether a glyph can be kept in the
	// TrueType subset (fontsubset.CheckGlyph), as each is first drawn.
	checked map[uint16]bool
}

// code is the number the content stream, the widths and the CIDSet address
// glyph gid by: its CID in a CID-keyed CFF font, its glyph ID otherwise
// (Identity, or a CFF without CIDs, whose CIDs are its glyph IDs; ISO
// 32000-1, 9.7.4.2).
func (f *pdfFont) code(gid uint16) uint16 {
	if f.cid {
		return uint16(f.face.GlyphCode(int(gid)))
	}
	return gid
}

// fontCatalog resolves shaped faces to PDF font resources, caching failures
// so unusable faces consistently fall back to outline drawing.
type fontCatalog struct {
	ctx    context.Context // the call's, whose ledger FontData charges
	mode   TextMode
	shaper TextShaper
	fonts  map[*text.Face]*pdfFont
	// hidden are the by-name fonts of faces whose runs, some of them, are
	// drawn as outlines (see hiddenFor).
	hidden map[*text.Face]*pdfFont
	failed map[*text.Face]bool
	list   []*pdfFont // first-use order, for deterministic output
}

func newFontCatalog(ctx context.Context, mode TextMode, shaper TextShaper) *fontCatalog {
	if ctx == nil {
		ctx = context.Background()
	}
	return &fontCatalog{
		ctx:    ctx,
		mode:   mode,
		shaper: shaper,
		fonts:  make(map[*text.Face]*pdfFont),
		hidden: make(map[*text.Face]*pdfFont),
		failed: make(map[*text.Face]bool),
	}
}

// fontFor returns the PDF font for a shaped face, or nil when the face
// cannot be represented as a PDF font at all (no font program, or one that
// does not parse: the caller then draws outlines). The error is a limit the
// font program's bytes exceed.
//
// In TextEmbed a TrueType font is embedded as a subset of its outlines and
// a CFF one as a subset of its CFF program (C43). One whose program cannot
// be subset (glyph data that is malformed, .notdef among it) is written by
// name, its runs drawn as outlines over invisible text in it: the text is
// still found and copied (C116).
func (c *fontCatalog) fontFor(face *text.Face) (*pdfFont, error) {
	if f, ok := c.fonts[face]; ok {
		return f, nil
	}
	if c.failed[face] {
		return nil, nil
	}
	data, err := c.shaper.FontData(c.ctx, face)
	if err != nil {
		return nil, err
	}
	if data == nil {
		c.failed[face] = true
		return nil, nil
	}
	parsed, err := fontsubset.Parse(data)
	if err != nil {
		c.failed[face] = true
		return nil, nil
	}
	f := c.add(face, data, parsed)
	if c.mode == TextEmbed {
		switch {
		case parsed.CanSubset():
			// .notdef is in every subset: a font whose .notdef cannot be
			// kept cannot be embedded at all.
			f.embed = parsed.CheckGlyph(0) == nil
		case parsed.IsCFF():
			f.embed, f.cff, f.cid = true, true, face.IsCIDKeyed()
			if fontsubset.Table(data, "CFF ") == nil {
				// A CFF2 font, which forme embeds as the CFF of its
				// instance: one it cannot subset has no CFF to fall back
				// to (see cffProgram).
				_, _, err := face.SubsetProgram(nil)
				f.embed = err == nil
			}
		}
		f.outlined = !f.embed
		if !f.embed {
			f.cff, f.cid = false, false
		}
	}
	return f, nil
}

// add registers a font resource of face.
func (c *fontCatalog) add(face *text.Face, data []byte, parsed *fontsubset.Font) *pdfFont {
	f := &pdfFont{
		res:     fmt.Sprintf("F%d", len(c.list)),
		data:    data,
		parsed:  parsed,
		face:    face,
		used:    make(map[uint16]bool),
		toUni:   make(map[uint16]string),
		checked: make(map[uint16]bool),
	}
	c.fonts[face] = f
	c.list = append(c.list, f)
	return f
}

// forRun is the font a run is written in: f, or, for a run with a glyph
// that cannot be kept in f's subset (C116), the face's font by name, in
// which the run is drawn as outlines over invisible text.
func (c *fontCatalog) forRun(f *pdfFont, run text.Run) *pdfFont {
	if f == nil || !f.embed || f.cff {
		return f
	}
	for _, g := range run.Glyphs {
		gid := uint16(g.GID)
		ok, seen := f.checked[gid]
		if !seen {
			ok = f.parsed.CheckGlyph(gid) == nil
			f.checked[gid] = ok
		}
		if !ok {
			return c.hiddenFor(run.Face, f)
		}
	}
	return f
}

// hiddenFor is face's font written by name, for the runs drawn as outlines
// over invisible text: one resource of its own, beside f, the embedded one.
func (c *fontCatalog) hiddenFor(face *text.Face, f *pdfFont) *pdfFont {
	if h, ok := c.hidden[face]; ok {
		return h
	}
	h := &pdfFont{
		res: fmt.Sprintf("F%d", len(c.list)), data: f.data, parsed: f.parsed, face: face, outlined: true,
		used: make(map[uint16]bool), toUni: make(map[uint16]string), checked: make(map[uint16]bool),
	}
	c.hidden[face] = h
	c.list = append(c.list, h)
	return h
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
	if mode != TextEmbed || !f.embed {
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

// coded is a glyph and its code (see code).
type coded struct{ code, gid uint16 }

// sortedCodes are the glyphs used, by code.
func (f *pdfFont) sortedCodes() []coded {
	gids := f.sortedGIDs()
	out := make([]coded, len(gids))
	for i, g := range gids {
		out[i] = coded{f.code(g), g}
	}
	if f.cid {
		sort.Slice(out, func(i, j int) bool { return out[i].code < out[j].code })
	}
	return out
}

// widthsArray builds the CIDFont /W array: runs of consecutive codes with
// their glyphs' advances in glyph space (1000 units per em).
func (f *pdfFont) widthsArray() pdf0.Array {
	cs := f.sortedCodes()
	upem := float64(f.parsed.UnitsPerEm())
	var out pdf0.Array
	for i := 0; i < len(cs); {
		j := i
		for j+1 < len(cs) && cs[j+1].code == cs[j].code+1 {
			j++
		}
		var ws pdf0.Array
		for k := i; k <= j; k++ {
			w := float64(f.parsed.Advance(cs[k].gid)) * 1000 / upem
			ws = append(ws, pdf0.Real(w))
		}
		out = append(out, pdf0.Integer(cs[i].code), ws)
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
	var mapped []coded
	for _, c := range f.sortedCodes() {
		if f.toUni[c.gid] != "" {
			mapped = append(mapped, c)
		}
	}
	// The spec caps bfchar blocks at 100 entries.
	for i := 0; i < len(mapped); i += 100 {
		n := min(100, len(mapped)-i)
		fmt.Fprintf(&b, "%d beginbfchar\n", n)
		for _, c := range mapped[i : i+n] {
			fmt.Fprintf(&b, "<%04X> <", c.code)
			for _, u := range utf16.Encode([]rune(f.toUni[c.gid])) {
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

		if catalog.mode == TextEmbed && f.embed {
			program, codes, err := catalog.program(f)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("svgpdf: embedding %s: %w", baseName, err)
			}
			compressed, err := flateCompress(program)
			if err != nil {
				return nil, nil, 0, err
			}
			fontFile := &pdf0.Stream{Data: compressed}
			fontFile.Dict.Set("Length", pdf0.Integer(len(compressed)))
			fontFile.Dict.Set("Filter", pdf0.Name("FlateDecode"))
			if f.cff {
				// A bare CFF program (ISO 32000-1, Table 126).
				fontFile.Dict.Set("Subtype", pdf0.Name("CIDFontType0C"))
				descriptor.Set("FontFile3", add(fontFile))
			} else {
				fontFile.Dict.Set("Length1", pdf0.Integer(len(program)))
				descriptor.Set("FontFile2", add(fontFile))
			}

			// CIDSet: the CIDs the font program holds, which PDF/A
			// requires for a subset CIDFont (ISO 19005-1, 6.3.5): every
			// glyph the subset keeps, the components of composites
			// among them (C114). Bit 0 of byte 0 is the high bit.
			// As long as the font has glyphs (a CID-keyed one's, as its
			// highest CID): a reader checks the bitmap against them.
			top := f.parsed.NumGlyphs() - 1
			for _, c := range codes {
				top = max(top, int(c))
			}
			bitmap := make([]byte, top/8+1)
			for _, c := range codes {
				bitmap[c/8] |= 0x80 >> (c % 8)
			}
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

		// A CID-keyed font's own character collection, whose CIDs the
		// codes are; Identity otherwise. Identity-H suits either.
		registry, ordering, supplement := "Adobe", "Identity", 0
		if f.cid {
			if r, o, s, ok := f.face.CharacterCollection(); ok {
				registry, ordering, supplement = r, o, s
			}
		}
		cidSystem := &pdf0.Dictionary{}
		cidSystem.Set("Registry", pdf0.String{Value: []byte(registry)})
		cidSystem.Set("Ordering", pdf0.String{Value: []byte(ordering)})
		cidSystem.Set("Supplement", pdf0.Integer(supplement))

		cidFont := &pdf0.Dictionary{}
		cidFont.Set("Type", pdf0.Name("Font"))
		// A CFF font's CIDFont is a CIDFontType0 (ISO 32000-2, 9.7.4.1).
		if f.parsed.IsCFF() {
			cidFont.Set("Subtype", pdf0.Name("CIDFontType0"))
		} else {
			cidFont.Set("Subtype", pdf0.Name("CIDFontType2"))
		}
		cidFont.Set("BaseFont", pdf0.Name(baseName))
		cidFont.Set("CIDSystemInfo", cidSystem)
		cidFont.Set("FontDescriptor", descRef)
		// Content-stream glyph codes ARE glyph IDs of the (subset) font
		// program: the sparse subset preserves original glyph numbering.
		// (A CIDFontType0 has no CIDToGIDMap: its codes are CIDs, mapped
		// to glyphs by its charset, or glyph indices for a CFF without
		// one; see code.)
		if !f.parsed.IsCFF() {
			cidFont.Set("CIDToGIDMap", pdf0.Name("Identity"))
		}
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
	b, err := compress(data)
	if err != nil {
		return nil, fmt.Errorf("svgpdf: compressing stream: %w", err)
	}
	return b, nil
}

// program is the font program f embeds, and the codes of the glyphs it
// holds, for its CIDSet. The bytes it makes are bounded by the font's own,
// which are charged to the call's ledger while it is made.
func (c *fontCatalog) program(f *pdfFont) (program []byte, codes []uint16, err error) {
	l := budget.LedgerFrom(c.ctx)
	if err := l.Alloc(int64(len(f.data))); err != nil {
		return nil, nil, err
	}
	defer l.Free(int64(len(f.data)))
	if f.cff {
		return f.cffProgram()
	}
	// Every glyph used was checked as it was drawn (forRun): the subset
	// cannot fail on one.
	program, kept, err := f.parsed.SubsetKept(f.used)
	if err != nil {
		return nil, nil, err
	}
	for g := range kept {
		codes = append(codes, g)
	}
	return program, codes, nil
}

// cffProgram is the CFF program of f's subset, cut by forme's subsetter,
// and the codes of the glyphs it holds. A subset forme cannot cut (a glyph
// it cannot write as CFF) falls back to the font's whole CFF program, which
// holds every glyph, at the codes the content addresses them by.
func (f *pdfFont) cffProgram() (program []byte, codes []uint16, err error) {
	gids := f.sortedGIDs()
	want := make([]int, len(gids))
	for i, g := range gids {
		want[i] = int(g)
	}
	if sub, kept, err := f.face.SubsetProgram(want); err == nil {
		if cff := fontsubset.Table(sub, "CFF "); cff != nil {
			for _, g := range kept {
				codes = append(codes, f.code(uint16(g)))
			}
			return cff, codes, nil
		}
	}
	cff := fontsubset.Table(f.data, "CFF ")
	if cff == nil {
		return nil, nil, fmt.Errorf("svgpdf: the font has no CFF program to embed")
	}
	for g := range f.parsed.NumGlyphs() {
		codes = append(codes, f.code(uint16(g)))
	}
	return cff, codes, nil
}

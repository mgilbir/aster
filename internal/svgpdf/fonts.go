package svgpdf

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/fonts"

	"github.com/mgilbir/aster/internal/budget"
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
	// PDFA writes the chart so that a PDF/A-2 or PDF/A-3 document (ISO
	// 19005-2, -3) can embed it without breaking a rule of the standard:
	// colour is device-independent (sRGB: DefaultRGB in its resources, the
	// blending colour space of its transparency groups), images carry no
	// /Interpolate, links the print flag, and a
	// run of text that would show a font's .notdef glyph is drawn as
	// outlines, its text kept as ActualText. The document-level parts (file
	// identifier, XMP metadata, PDF/A identification, output intent) are the
	// embedding document's. TextNamed, whose fonts are not embedded, is
	// refused.
	PDFA bool
}

// pdfFont is one font resource of the document: a face drawn as PDF text,
// which pdf0 embeds (or writes by name) from what the drawing recorded.
type pdfFont struct {
	res  string // resource name (F0, F1, ...)
	face *text.Face
	// pf is the document's own wrapper of the face (a ShapeClone, adopted):
	// what its runs record (Record) is what pdf0 writes the font from — the
	// glyphs used, what each stands for, /W, /CIDSet, the subset.
	pf *fonts.Face
	// data is the face's font program as the shaper hands it out (FontData),
	// charged to the call's ledger: FontUsage reports it.
	data []byte
	// embed is set when the font program is embedded (TextEmbed); a font
	// that is not is written by name (fonts.Forms.Named).
	embed bool
	// outlined is set for a font whose runs are drawn as outlines and
	// written as invisible text in it, by name (see hiddenFor): in
	// TextEmbed, a font whose program cannot be embedded.
	outlined bool
	// checks records, by glyph, whether the subset can keep it
	// (fonts.Face.CheckGlyph), as each is first drawn.
	checks glyphChecks
}

// glyphChecks are the answers CheckGlyph gave, by glyph index: two bits a
// glyph (asked, keepable), in a set made the first time one is asked, sized
// to the face's glyphs. A glyph outside them is asked each time.
type glyphChecks struct {
	asked, ok []uint64
}

// get is what was recorded for gid, and whether anything was.
func (c *glyphChecks) get(gid int) (ok, seen bool) {
	if gid < 0 || gid/64 >= len(c.asked) || c.asked[gid/64]&(1<<(gid%64)) == 0 {
		return false, false
	}
	return c.ok[gid/64]&(1<<(gid%64)) != 0, true
}

// set records ok for gid, a glyph of a face of numGlyphs glyphs.
func (c *glyphChecks) set(gid int, ok bool, numGlyphs int) {
	if gid < 0 || gid >= numGlyphs {
		return
	}
	if c.asked == nil {
		words := (numGlyphs + 63) / 64
		bits := make([]uint64, 2*words) // one allocation for both
		c.asked, c.ok = bits[:words:words], bits[words:]
	}
	c.asked[gid/64] |= 1 << (gid % 64)
	if ok {
		c.ok[gid/64] |= 1 << (gid % 64)
	}
}

// fontCatalog resolves shaped faces to PDF font resources, caching failures
// so unusable faces consistently fall back to outline drawing.
type fontCatalog struct {
	ctx    context.Context // the call's, whose ledger FontData and embedding charge
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
// cannot be represented as a PDF font at all (no font program, or one pdf0
// writes as Type 3 fonts, which the content stream cannot show text in: the
// caller then draws outlines). The error is a limit the font program's bytes
// exceed.
//
// In TextEmbed the font is embedded as a subset (C43). One whose program
// cannot be subset at all (its licence, a fault that refuses every glyph) is
// written by name, its runs drawn as outlines over invisible text in it: the
// text is still found and copied (C116).
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
	pf, ok := adopt(face)
	if !ok {
		c.failed[face] = true
		return nil, nil
	}
	f := c.add(face, pf, data)
	if c.mode == TextEmbed {
		f.embed = faceEmbeddable(f)
		f.outlined = !f.embed
	}
	return f, nil
}

// adopt is a document's own pdf0 face of face, and whether pdf0 can write
// text in it: not one it writes as Type 3 fonts (a face of bitmap glyphs
// only, which has no outlines to embed), whose glyphs are not shown by code.
func adopt(face *text.Face) (pf *fonts.Face, ok bool) {
	defer func() {
		if recover() != nil {
			pf, ok = nil, false
		}
	}()
	defer face.Guard()()
	pf = fonts.Adopt(face.ShapeClone())
	// Colour glyphs are painted here (colour.go) and only their text is
	// written in the font: a COLR face is embedded as its outlines, as the
	// font program, not as Type 3 fonts painting the colour again.
	pf.SetColourGlyphs(false)
	if _, err := pf.AppendCode(nil, 0); err != nil {
		return nil, false
	}
	return pf, true
}

// faceEmbeddable reports whether pdf0 can embed f's program at all: a
// refusal that is not about one glyph's data (shape.GlyphError) refuses
// every glyph of the face. .notdef that cannot be kept is written empty when
// nothing shows it.
func faceEmbeddable(f *pdfFont) bool {
	err := f.checkGlyph(0)
	var ge *shape.GlyphError
	return err == nil || errors.As(err, &ge)
}

// checkGlyph is fonts.Face.CheckGlyph, guarded.
func (f *pdfFont) checkGlyph(gid int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("svgpdf: checking glyph %d: %v", gid, r)
		}
	}()
	defer f.face.Guard()()
	return f.pf.CheckGlyph(gid)
}

// add registers a font resource of face.
func (c *fontCatalog) add(face *text.Face, pf *fonts.Face, data []byte) *pdfFont {
	f := &pdfFont{
		res:  fmt.Sprintf("F%d", len(c.list)),
		face: face,
		pf:   pf,
		data: data,
	}
	c.fonts[face] = f
	c.list = append(c.list, f)
	return f
}

// forRun is the font a run is written in: f, or, for a run with a glyph
// that cannot be kept in f's subset (C116), the face's font by name, in
// which the run is drawn as outlines over invisible text.
func (c *fontCatalog) forRun(f *pdfFont, run text.Run) *pdfFont {
	if f == nil || !f.embed {
		return f
	}
	for _, g := range run.Glyphs {
		ok, seen := f.checks.get(g.GID)
		if !seen {
			ok = f.checkGlyph(g.GID) == nil
			f.checks.set(g.GID, ok, f.pf.NumGlyphs())
		}
		if !ok {
			return c.hiddenFor(run.Face, f)
		}
	}
	return f
}

// hiddenFor is face's font written by name, for the runs drawn as outlines
// over invisible text: one resource of its own, beside f, the embedded one,
// recording the glyphs of those runs only.
func (c *fontCatalog) hiddenFor(face *text.Face, f *pdfFont) *pdfFont {
	if h, ok := c.hidden[face]; ok {
		return h
	}
	pf, ok := adopt(face)
	if !ok {
		pf = f.pf // unreachable: f was adopted from the same face
	}
	h := &pdfFont{res: fmt.Sprintf("F%d", len(c.list)), face: face, pf: pf, data: f.data, outlined: true}
	c.hidden[face] = h
	c.list = append(c.list, h)
	return h
}

// used are the glyphs f's runs recorded, ascending.
func (f *pdfFont) used() []int {
	u := slices.Clone(f.pf.Used())
	slices.Sort(u)
	return slices.Compact(u)
}

// postScriptName is the name f is written under by name (TextNamed, and the
// /BaseFont FontUsage reports): the face's PostScript name, as pdf0 writes it.
func (f *pdfFont) postScriptName() string {
	defer f.face.Guard()()
	return f.pf.Name()
}

// SubsetFont is aster.SubsetFont and Converter.SubsetFont: forme's subset of
// source keeping gids (and .notdef and the components of the composites among
// them), numbered as source numbers its glyphs, which is how pdf0 embeds a
// TrueType face. name is what pdf0 writes the font by, so it is what FontUsage
// reports and the /BaseFont of TextNamed output: forme's PostScript name for
// the face, "Embedded" where the font states none. A glyph index source does
// not have is ignored.
//
// The face is not read again where it has been: a face of m (nil for none)
// whose program source is (Measurer.ShapeFaceOf), else an embedded font
// (text.EmbeddedShapeFace), is a clone of the parse made once; any other font
// is read here.
//
// Only a face of TrueType outlines is subset: a CFF one, whose program a PDF
// carries differently, and one of bitmap glyphs only are refused. The subset
// is bounded as one svgpdf embeds is: its bytes by twice the font program and
// 64 KiB, charged to the ledger of ctx, its work by forme's default, and ctx
// stops it.
func SubsetFont(ctx context.Context, m *text.Measurer, source []byte, gids []uint16) (program []byte, name string, err error) {
	defer func() {
		if r := recover(); r != nil {
			program, err = nil, fmt.Errorf("svgpdf: internal error subsetting a font: %v", r)
		}
	}()
	var face *shape.Face
	ok := false
	if m != nil {
		face, ok = m.ShapeFaceOf(source)
	}
	if !ok {
		face, ok = text.EmbeddedShapeFace(source)
	}
	if !ok {
		if face, err = shape.Load(source); err != nil {
			return nil, "", err
		}
	}
	name = face.Name()
	switch {
	case face.IsCFF():
		return nil, name, errors.New("its outlines are CFF, not TrueType")
	case face.BitmapOnly():
		return nil, name, errors.New("its glyphs are bitmaps, with no outlines")
	}
	use := make([]int, len(gids))
	for i, g := range gids {
		use[i] = int(g)
	}
	face.Use(use...) // an index the face does not have is not recorded
	limits := shape.SubsetLimits{MaxBytes: 2*len(source) + 64<<10}
	l := budget.LedgerFrom(ctx)
	if err := l.Alloc(int64(limits.MaxBytes)); err != nil {
		return nil, name, err
	}
	defer l.Free(int64(limits.MaxBytes))
	program, _, err = face.SubsetGlyphsContext(ctx, limits)
	if errors.Is(err, shape.ErrSubsetLimit) {
		err = fmt.Errorf("%w: %w", budget.ErrLimit, err)
	}
	if err != nil {
		return nil, name, err
	}
	return program, name, nil
}

// allocator numbers the objects pdf0 writes for the fonts in the document's
// own sequence (fonts.Allocator), into the document's objects. The objects
// are carved out of blocks, a few allocations a document rather than one an
// object: a font is a dozen of them.
type allocator struct {
	objects map[int]*pdf0.IndirectObject
	next    int
	block   []pdf0.IndirectObject
}

func (a *allocator) Add(v pdf0.Object) pdf0.IndirectRef {
	if len(a.block) == 0 {
		a.block = make([]pdf0.IndirectObject, 16)
	}
	o := &a.block[0]
	a.block = a.block[1:]
	n := a.next
	a.next++
	*o = pdf0.IndirectObject{Number: n, Value: v}
	a.objects[n] = o
	return pdf0.IndirectRef{Number: n}
}

// buildFontObjects has pdf0 write every font of the catalog that a run
// recorded glyphs in, into objects, numbering them from nextNum: embedded as a
// subset (TextEmbed) or by name (TextNamed, and the fonts of runs drawn as
// outlines over invisible text). It returns the /Font resource
// dictionary, the next free object number, and the least PDF version the
// fonts need. A CFF program is written bare (CIDFontType0C, PDF 1.3), so the
// fonts need no more than the document's 1.4.
//
// Each subset is bounded: its bytes by twice the font program and 64 KiB,
// which is charged to the call's ledger while it is made, its work by
// forme's default, and the call's context stops it.
func buildFontObjects(catalog *fontCatalog, objects map[int]*pdf0.IndirectObject, nextNum int) (*pdf0.Dictionary, int, string, error) {
	alloc := &allocator{objects: objects, next: nextNum}
	fontRes := &pdf0.Dictionary{}
	version := "1.1"
	for _, f := range catalog.list {
		if len(f.pf.Used()) == 0 {
			continue
		}
		emb, err := catalog.embed(f, alloc)
		if err != nil {
			return nil, 0, "", err
		}
		fontRes.Set(pdf0.Name(f.res), emb.Horizontal)
		if emb.Version > version {
			version = emb.Version
		}
	}
	return fontRes, alloc.next, version, nil
}

// embed has pdf0 write f's font objects through alloc.
func (c *fontCatalog) embed(f *pdfFont, alloc *allocator) (emb fonts.Embedded, err error) {
	defer func() {
		if r := recover(); r != nil {
			emb, err = fonts.Embedded{}, fmt.Errorf("svgpdf: embedding a font: %v", r)
		}
	}()
	forms := fonts.Forms{Horizontal: true, BareCFF: true, Named: !f.embed}
	var limits fonts.SubsetLimits
	if f.embed {
		limits.MaxBytes = 2*len(f.data) + 64<<10
		l := budget.LedgerFrom(c.ctx)
		if err := l.Alloc(int64(limits.MaxBytes)); err != nil {
			return fonts.Embedded{}, err
		}
		defer l.Free(int64(limits.MaxBytes))
	}
	defer f.face.Guard()()
	emb, err = f.pf.EmbedFormsContext(c.ctx, alloc, forms, limits)
	if err != nil {
		if errors.Is(err, fonts.ErrSubsetLimit) {
			return fonts.Embedded{}, fmt.Errorf("svgpdf: embedding %s: %w: %w", f.postScriptName(), budget.ErrLimit, err)
		}
		return fonts.Embedded{}, fmt.Errorf("svgpdf: embedding %s: %w", f.postScriptName(), err)
	}
	return emb, nil
}

func flateCompress(data []byte) ([]byte, error) {
	b, err := compress(data)
	if err != nil {
		return nil, fmt.Errorf("svgpdf: compressing stream: %w", err)
	}
	return b, nil
}

// code is the two-byte character code glyph gid is shown with in f's font
// (fonts.Face.AppendCode): its glyph index, or its CID in a CID-keyed CFF
// font, the number /W, /CIDSet and the ToUnicode map are keyed by.
func (f *pdfFont) code(gid int) (uint16, error) {
	defer f.face.Guard()()
	var buf [2]byte
	b, err := f.pf.AppendCode(buf[:0], gid)
	if err != nil {
		return 0, fmt.Errorf("svgpdf: the code of glyph %d: %w", gid, err)
	}
	if len(b) != 2 {
		return 0, fmt.Errorf("svgpdf: glyph %d has a %d-byte code, not a composite font's two", gid, len(b))
	}
	return binary.BigEndian.Uint16(b), nil
}

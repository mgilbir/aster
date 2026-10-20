package text

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/budget"
)

// maxFontBytes bounds the size of a font program accepted from a caller.
const maxFontBytes = 64 << 20

// Face is one loaded font face: a forme shaping face plus the font program it
// was loaded from. It is what a shaped Run refers to, and what GlyphOutline
// reads outlines from. A Face is created by a Measurer and is safe for
// concurrent use through the Measurer's methods and GlyphOutline.
type Face struct {
	// ID is a stable identifier unique within its Measurer, such as
	// "liberation-sans-bold" or "custom-0-Acme".
	ID string
	// Family is the family name the face was registered under.
	Family string
	// Weight is the CSS weight (100..900) and Italic the CSS style the face
	// answers to.
	Weight int
	Italic bool

	// shape is the face as loaded. It is never shaped through directly:
	// shaping uses clones from clones, so goroutines shape concurrently.
	shape  *shape.Face
	src    []byte // the font file the face was loaded from
	index  int    // which face of src, a collection, it is; 0 for a single font
	mapped bool   // src is a system font file mapped into memory (see guard)
	// source names what the face is loaded from, the same each time it is
	// loaded: a system face's file and index (it is dropped and loaded
	// again, a new Face, past maxSystemFaces), any other face's ID. What is
	// cached of a face across its loads is keyed by it (C113).
	source string
	// palette is the CPAL palette its colour glyphs are painted with
	// (WithFontPalette).
	palette int
	upem    int

	progOnce sync.Once
	prog     []byte

	sizeOnce sync.Once
	size     int

	clones sync.Pool // of *shape.Face, clones of shape

	outlines outlineCache

	annexMu sync.Mutex
	annex   map[any]any // see Annex
}

// Program returns the font program (an sfnt, with any WOFF wrapper removed)
// the face was loaded from, suitable for subsetting and embedding. The slice
// is shared: callers must not modify it. A face of a collection is copied out
// into a font of its own on the first call, which for a large collection is a
// large copy: Table reads a table without it, and Size says how large it is.
func (f *Face) Program() []byte {
	f.progOnce.Do(func() {
		defer func() {
			if recover() != nil {
				f.prog = nil
			}
		}()
		defer f.guard()()
		f.prog = f.shape.Program()
		if f.mapped && f.prog != nil && len(f.prog) > 0 && len(f.src) > 0 && &f.prog[0] == &f.src[0] {
			// A single font's program is its file: copy it out of the
			// mapping, so that what is handed out cannot fault.
			f.prog = append([]byte(nil), f.prog...)
		}
	})
	return f.prog
}

// OutlineProgram returns the face's font with only the tables an embedder of
// its outlines needs, such as a PDF writer's subset: no colour or bitmap
// tables. It is a font of its own even for a face of a collection, and is
// made from the file where it is, without making Program, and anew on each
// call: Measurer.FontData keeps what it makes within a bound. It is nil for
// a face it cannot be made of (WOFF, read through Program instead, or a face
// whose tables overlap: see sfntRecords).
func (f *Face) OutlineProgram() (p []byte) {
	defer func() {
		if recover() != nil {
			p = nil
		}
	}()
	defer f.guard()()
	return outlineProgram(f.src, f.index)
}

// outlineSize is the size of OutlineProgram, read from the face's table
// directory without making it: 0 when there is none to make.
func (f *Face) outlineSize() (n int) {
	defer func() {
		if recover() != nil {
			n = 0
		}
	}()
	defer f.guard()()
	return outlineProgramSize(f.src, f.index)
}

// Table returns one table of the face's font, tag "hhea" say, read where it
// is in the file the face was loaded from, or nil. The slice is shared:
// callers must not modify it.
func (f *Face) Table(tag string) (t []byte) {
	defer func() {
		if recover() != nil {
			t = nil
		}
	}()
	defer f.guard()()
	if t := sfntTable(f.src, f.index, tag); t != nil {
		if f.mapped {
			// Out of the mapping, so that what is handed out cannot fault.
			return append([]byte(nil), t...)
		}
		return t
	}
	// A WOFF font's tables are only in its unwrapped program.
	if len(f.src) >= 4 && (string(f.src[:4]) == "wOFF" || string(f.src[:4]) == "wOF2") {
		return sfntTable(f.Program(), 0, tag)
	}
	return nil
}

// Size is the size in bytes of the face's font program, without making it.
// A file that cannot be read says it is as large as can be.
func (f *Face) Size() int {
	// The file does not change: read once (FontData asks on each render).
	f.sizeOnce.Do(func() {
		defer func() {
			if recover() != nil {
				f.size = math.MaxInt
			}
		}()
		defer f.guard()()
		f.size = sfntSize(f.src, f.index)
	})
	return f.size
}

// UnitsPerEm returns the face's design units per em.
func (f *Face) UnitsPerEm() int { return f.upem }

// Name returns the font's PostScript name, or "" when it has none.
func (f *Face) Name() (name string) {
	defer func() {
		if recover() != nil {
			name = ""
		}
	}()
	defer f.guard()()
	return f.shape.Name()
}

// HasRune reports whether the face's character map covers r.
func (f *Face) HasRune(r rune) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	defer f.guard()()
	_, ok = f.shape.GlyphID(r)
	return ok
}

// newFace loads data with forme. A weight of 0 or less reads the weight and
// style from the font itself. It never panics: a font that trips a bug in
// the parser is reported as an error like any other malformed font.
func newFace(id, family string, weight int, italic bool, data []byte) (f *Face, err error) {
	return newFaceAt(id, family, weight, italic, data, 0, false)
}

// newFaceInstance is newFace for face index of a font given with WithFont,
// its weight and style read from it, at the point axes names of its design
// space when it is not nil. An instance is a font program of its own, written for the point,
// which is what the face reads from and embeds.
func newFaceInstance(id, family string, data []byte, index int, axes map[string]float64) (f *Face, err error) {
	if axes == nil {
		return newFaceAt(id, family, 0, false, data, index, false)
	}
	if len(data) > maxFontBytes {
		return nil, fmt.Errorf("text: font %q is %d bytes, over the %d limit: %w", family, len(data), maxFontBytes, budget.ErrLimit)
	}
	defer func() {
		if r := recover(); r != nil {
			f, err = nil, fmt.Errorf("text: loading font %q: %v", family, r)
		}
	}()
	sf, err := shape.LoadCollectionInstance(data, index, axes)
	if err != nil {
		return nil, fmt.Errorf("text: loading font %q at %v: %w", family, axes, err)
	}
	f, err = faceOf(id, family, 0, false, sf, sf.Program(), 0, false)
	if err != nil {
		return nil, err
	}
	// The instance's weight and style are where it is on the axes that say
	// them, as CSS font-weight and font-style choose among a family's faces.
	f.Weight, f.Italic = instanceStyle(axes, fontAxes(data, index), f.Weight, f.Italic)
	return f, nil
}

// instanceStyle is the CSS weight and style of a variable font's instance
// at req on axes, the instance's own (its OS/2's) being weight and italic:
// where it is drawn, on the axes that say them. A value outside an axis's
// range is drawn at the range's end, and answers to that (C160): an
// instance asked for at wght 50 of a 100..900 font is drawn at 100 and is
// weight 100. A slant (slnt) other than zero is an oblique style, which is
// italic here; ital is italic from half way.
func instanceStyle(req map[string]float64, axes []shape.Axis, weight int, italic bool) (int, bool) {
	at := func(tag string) (float64, bool) {
		v, ok := req[tag]
		if !ok {
			return 0, false
		}
		for _, a := range axes {
			if a.Tag == tag {
				v = min(max(v, a.Min), a.Max)
			}
		}
		return v, true
	}
	if w, ok := at("wght"); ok && w >= 1 && w <= 1000 {
		weight = int(math.Round(w))
	}
	slnt, hasSlnt := at("slnt")
	ital, hasItal := at("ital")
	if hasSlnt || hasItal {
		italic = hasSlnt && slnt != 0 || hasItal && ital >= 0.5
	}
	return weight, italic
}

// fontAxes are the variation axes of face index of font file data, read
// from its fvar table; nil when it has none, or when data is not a plain
// sfnt (a WOFF), whose instance then answers to the values as asked.
func fontAxes(data []byte, index int) (axes []shape.Axis) {
	defer func() {
		if recover() != nil {
			axes = nil
		}
	}()
	t := sfntTable(data, index, "fvar")
	if len(t) < 16 {
		return nil
	}
	off := int(binary.BigEndian.Uint16(t[4:]))
	n := int(binary.BigEndian.Uint16(t[8:]))
	size := int(binary.BigEndian.Uint16(t[10:]))
	fixed := func(b []byte) float64 { return float64(int32(binary.BigEndian.Uint32(b))) / 65536 }
	for i := range n {
		r := off + i*size
		if size < 20 || r+20 > len(t) {
			break
		}
		axes = append(axes, shape.Axis{Tag: string(t[r : r+4]), Min: fixed(t[r+4:]), Default: fixed(t[r+8:]), Max: fixed(t[r+12:])})
	}
	return axes
}

// embeddedParses are the embedded fonts as forme parsed them, once per
// process, by id.
var embeddedParses struct {
	sync.Mutex
	m map[string]*shape.Face
}

// embeddedFace is the Face of an embedded font for one Measurer: a clone of
// the font as parsed once per process, which shares the parse and its
// caches. Reading the 13 embedded fonts again for each Measurer cost 3.4 ms
// and 4 MB.
func embeddedFace(id, family string, weight int, italic bool, data []byte) (*Face, error) {
	embeddedParses.Lock()
	sf, ok := embeddedParses.m[id]
	embeddedParses.Unlock()
	if !ok {
		parsed, err := shape.Load(data)
		if err != nil {
			return nil, fmt.Errorf("text: loading font %q: %w", family, err)
		}
		embeddedParses.Lock()
		if embeddedParses.m == nil {
			embeddedParses.m = map[string]*shape.Face{}
		}
		if seen, ok := embeddedParses.m[id]; ok {
			parsed = seen
		} else {
			embeddedParses.m[id] = parsed
		}
		embeddedParses.Unlock()
		sf = parsed
	}
	return faceOf(id, family, weight, italic, sf.Clone(), data, 0, false)
}

// newFaceAt is newFace for face index of data, which may be a collection,
// and which is a system font file mapped into memory when mapped is set.
func newFaceAt(id, family string, weight int, italic bool, data []byte, index int, mapped bool) (f *Face, err error) {
	limit := maxFontBytes
	if mapped {
		limit = maxSystemFontBytes
	}
	if len(data) > limit {
		return nil, fmt.Errorf("text: font %q is %d bytes, over the %d limit: %w", family, len(data), limit, budget.ErrLimit)
	}
	defer func() {
		if r := recover(); r != nil {
			f, err = nil, fmt.Errorf("text: loading font %q: %v", family, r)
		}
	}()
	if mapped {
		defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
	}
	sf, err := shape.LoadCollection(data, index)
	if err != nil {
		return nil, fmt.Errorf("text: loading font %q: %w", family, err)
	}
	return faceOf(id, family, weight, italic, sf, data, index, mapped)
}

// faceOf is the Face of sf, loaded from face index of src. A weight of 0 or
// less reads the weight and style from the font itself.
func faceOf(id, family string, weight int, italic bool, sf *shape.Face, src []byte, index int, mapped bool) (*Face, error) {
	upem := sf.UnitsPerEm()
	if upem <= 0 {
		return nil, fmt.Errorf("text: font %q has no units per em", family)
	}
	if weight <= 0 {
		weight, italic = styleOfFace(sf)
	}
	f := &Face{
		ID: id, Family: family, Weight: weight, Italic: italic,
		shape: sf, src: src, index: index, mapped: mapped, upem: upem,
	}
	f.clones.New = func() any { return sf.Clone() }
	return f, nil
}

// shapeGlyphs shapes s through a clone of the face, so that any number of
// goroutines may shape through one Face at once. It contains any panic a
// malformed font provokes. With a budget the run is shaped under it, and a
// run it refuses panics with a *budget.Stop (see ShapingBudget).
func (f *Face) shapeGlyphs(s string, b *ShapingBudget) (g []shape.Glyph, ok bool) {
	g, ok, err := f.shapeOnClone(s, b)
	if err != nil {
		panic(&budget.Stop{Err: err})
	}
	return g, ok
}

// shapeOnClone shapes s on a clone from the pool, under b when it is not nil.
// A clone that panicked mid-shaping is dropped; one whose run b refused is
// reusable, forme having cleared its budget.
func (f *Face) shapeOnClone(s string, b *ShapingBudget) (g []shape.Glyph, ok bool, err error) {
	c := f.clones.Get().(*shape.Face)
	defer func() {
		if recover() != nil {
			g, ok, err = nil, false, nil
			return
		}
		f.clones.Put(c)
	}()
	defer f.guard()()
	if b == nil {
		g, _ = c.ShapeGlyphs(s)
		return g, true, nil
	}
	g, err = b.shape(c, s, f.Family)
	return g, err == nil, err
}

// styleOfFace reads the CSS weight and style a font states about
// itself, from what forme reads of OS/2 (usWeightClass, fsSelection) and head
// (macStyle). It serves fonts registered with WithFont, whose file is the
// only source of the two. A font that states no weight is regular; forme does
// not expose head's macStyle bold bit, so a font with no OS/2 table is regular
// even when macStyle says bold.
func styleOfFace(sf *shape.Face) (weight int, italic bool) {
	d := sf.Descriptor()
	weight = weightNormal
	if d.Has(shape.MetricWeight) && d.Weight >= 1 && d.Weight <= 1000 {
		weight = d.Weight
	}
	return weight, d.Italic || d.Oblique
}

// normFamily is the comparison form of a family name: lower case with spaces
// and tabs removed, so "Liberation Sans" and "liberationsans" are one family.
var familyReplacer = strings.NewReplacer(" ", "", "\t", "")

func normFamily(s string) string { return familyReplacer.Replace(ASCIILower(s)) }

// ASCIILower is s with A-Z lowered and nothing else changed: CSS compares
// font-family names and keywords ASCII case-insensitively (CSS Fonts 4,
// 5.1), where Unicode case mapping would make U+0130 an i and the Kelvin
// sign a k. (csscolor.ASCIILower, of PR 13 of the audit of 2026-10-18, is
// the same; one of the two can go once both are in.)
func ASCIILower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; 'A' <= c && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; 'A' <= c && c <= 'Z' {
					b[j] = c + 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// sourceKey is f.source, or its ID for a face that is never loaded again.
func (f *Face) sourceKey() string {
	if f.source != "" {
		return f.source
	}
	return f.ID
}

// Annex returns the value kept with f under key, made by mk on the first
// call. What a drawer of the face derives from it once (the rasterizer's
// metrics and outlines of it, say) is kept here rather than in a cache of
// its own keyed by the face: it lives exactly as long as the face, so a face
// the Measurer drops (a system face past maxSystemFaces) takes it along,
// where a cache would keep both (C27).
func (f *Face) Annex(key any, mk func() any) any {
	f.annexMu.Lock()
	defer f.annexMu.Unlock()
	if v, ok := f.annex[key]; ok {
		return v
	}
	v := mk()
	if f.annex == nil {
		f.annex = map[any]any{}
	}
	f.annex[key] = v
	return v
}

// GlyphCode is the number a PDF's CIDFont addresses glyph gid of f by: its
// CID for a CID-keyed CFF font, whose charset maps the CID to the glyph,
// and gid itself for every other font.
func (f *Face) GlyphCode(gid int) (code int) {
	defer func() {
		if recover() != nil {
			code = gid
		}
	}()
	defer f.guard()()
	return f.shape.GlyphCode(gid)
}

// CharacterCollection is the registry, ordering and supplement of a
// CID-keyed CFF font, which its CIDFont states; ok is false for any other.
func (f *Face) CharacterCollection() (registry, ordering string, supplement int, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	defer f.guard()()
	return f.shape.CharacterCollection()
}

// SubsetProgram is a font program of f with the outlines of gids, .notdef
// and what they need (composite components, seac accents) and no others:
// forme's subsetter, which subsets CFF outlines too. kept are the glyphs it
// keeps (see shape.Face.SubsetGlyphs: a CID-keyed CFF's are renumbered, and
// addressed by GlyphCode, which the subset keeps). It never panics: a font
// that trips the subsetter is an error.
func (f *Face) SubsetProgram(gids []int) (program []byte, kept []int, err error) {
	defer func() {
		if r := recover(); r != nil {
			program, kept, err = nil, nil, fmt.Errorf("text: subsetting font %q: %v", f.Family, r)
		}
	}()
	defer f.guard()()
	// A clone of the face as loaded, which nothing has shaped through, so
	// that it has used no glyph but these.
	c := f.shape.Clone()
	c.Use(gids...)
	return c.SubsetGlyphs()
}

// IsCIDKeyed reports whether f's outlines are a CID-keyed CFF font, whose
// glyphs a PDF addresses by CID (GlyphCode).
func (f *Face) IsCIDKeyed() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	defer f.guard()()
	return f.shape.IsCIDKeyed()
}

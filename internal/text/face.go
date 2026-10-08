package text

import (
	"fmt"
	"math"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

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
	// shaping uses clones from clones, which each keep their own record of
	// the glyphs they returned, so goroutines shape concurrently. The glyphs
	// they return are merged into shape under usedMu, which is what Subset
	// keeps.
	shape  *shape.Face
	src    []byte // the font file the face was loaded from
	index  int    // which face of src, a collection, it is; 0 for a single font
	mapped bool   // src is a system font file mapped into memory (see guard)
	upem   int

	progOnce sync.Once
	prog     []byte

	outlineOnce sync.Once
	outline     []byte

	clones sync.Pool // of *shape.Face, clones of shape

	// seen is a bitset of the glyphs already recorded in shape; it is
	// read without the lock (once a glyph is recorded it stays so) and
	// written under usedMu.
	usedMu sync.Mutex
	seen   []atomic.Uint64

	outlines outlineCache
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
// made from the file where it is, without making Program. The slice is
// shared: callers must not modify it. It is nil for a face it cannot be made
// of (WOFF, read through Program instead).
func (f *Face) OutlineProgram() []byte {
	f.outlineOnce.Do(func() {
		defer func() {
			if recover() != nil {
				f.outline = nil
			}
		}()
		defer f.guard()()
		f.outline = outlineProgram(f.src, f.index)
	})
	return f.outline
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
func (f *Face) Size() (n int) {
	defer func() {
		if recover() != nil {
			n = math.MaxInt
		}
	}()
	defer f.guard()()
	return sfntSize(f.src, f.index)
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

// Subset returns a font program containing only the glyphs shaped through
// this face so far (plus .notdef), as forme records them. It is what a PDF
// writer embeds.
//
// forme v0.4.1's subsetter panicked on a malformed CFF font (found by fuzzing
// FuzzLoadFont, fixed in v0.4.2); a panic is still reported as an error.
func (f *Face) Subset() (out []byte, err error) {
	f.usedMu.Lock()
	defer f.usedMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("text: subsetting font %q: %v", f.Family, r)
		}
	}()
	defer f.guard()()
	return f.shape.Subset()
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

// newFaceInstance is newFace for a font given with WithFont, its weight and
// style read from it, at the point axes names of its design space when it is
// not nil. An instance is a font program of its own, written for the point,
// which is what the face reads from and embeds.
func newFaceInstance(id, family string, data []byte, axes map[string]float64) (f *Face, err error) {
	if axes == nil {
		return newFace(id, family, 0, false, data)
	}
	if len(data) > maxFontBytes {
		return nil, fmt.Errorf("text: font %q is %d bytes, over the %d limit: %w", family, len(data), maxFontBytes, budget.ErrLimit)
	}
	defer func() {
		if r := recover(); r != nil {
			f, err = nil, fmt.Errorf("text: loading font %q: %v", family, r)
		}
	}()
	sf, err := shape.LoadInstance(data, axes)
	if err != nil {
		return nil, fmt.Errorf("text: loading font %q at %v: %w", family, axes, err)
	}
	f, err = faceOf(id, family, 0, false, sf, sf.Program(), 0, false)
	if err != nil {
		return nil, err
	}
	// The instance's weight and style are where it is on the axes that say
	// them, as CSS font-weight and font-style choose among a family's faces.
	if w, ok := axes["wght"]; ok && w >= 1 && w <= 1000 {
		f.Weight = int(math.Round(w))
	}
	if i, ok := axes["ital"]; ok {
		f.Italic = i >= 0.5
	}
	return f, nil
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
		seen: make([]atomic.Uint64, (sf.NumGlyphs()+63)/64),
	}
	f.clones.New = func() any { return sf.Clone() }
	return f, nil
}

// shapeGlyphs shapes s through a clone of the face, so that any number of
// goroutines may shape through one Face at once, and records the glyphs
// returned for Subset. It contains any panic a malformed font provokes. With
// a budget the run is shaped under it, and a run it refuses panics with a
// *budget.Stop (see ShapingBudget).
func (f *Face) shapeGlyphs(s string, b *ShapingBudget) (g []shape.Glyph, ok bool) {
	g, ok, err := f.shapeOnClone(s, b)
	if err != nil {
		panic(&budget.Stop{Err: err})
	}
	if ok {
		f.record(g)
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

// record merges the glyphs of g into the base face's record of used glyphs.
// Shaping repeats the same few glyphs, so the common case takes no lock.
func (f *Face) record(g []shape.Glyph) {
	var fresh []int
	for i := range g {
		gid := g[i].GID
		if gid < 0 || gid/64 >= len(f.seen) {
			continue
		}
		if f.seen[gid/64].Load()&(uint64(1)<<(gid%64)) == 0 {
			fresh = append(fresh, gid)
		}
	}
	if len(fresh) == 0 {
		return
	}
	f.usedMu.Lock()
	for _, gid := range fresh {
		f.seen[gid/64].Or(uint64(1) << (gid % 64))
	}
	f.shape.Use(fresh...)
	f.usedMu.Unlock()
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

func normFamily(s string) string { return familyReplacer.Replace(strings.ToLower(s)) }

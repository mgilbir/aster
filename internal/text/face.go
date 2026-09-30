package text

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mgilbir/forme/shape"
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
	shape *shape.Face
	prog  []byte
	upem  int

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
// is shared: callers must not modify it.
func (f *Face) Program() []byte { return f.prog }

// UnitsPerEm returns the face's design units per em.
func (f *Face) UnitsPerEm() int { return f.upem }

// Name returns the font's PostScript name, or "" when it has none.
func (f *Face) Name() string { return f.shape.Name() }

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
	return f.shape.Subset()
}

// HasRune reports whether the face's character map covers r.
func (f *Face) HasRune(r rune) bool {
	_, ok := f.shape.GlyphID(r)
	return ok
}

// newFace loads data with forme. A weight of 0 or less reads the weight and
// style from the font itself. It never panics: a font that trips a bug in
// the parser is reported as an error like any other malformed font.
func newFace(id, family string, weight int, italic bool, data []byte) (f *Face, err error) {
	if len(data) > maxFontBytes {
		return nil, fmt.Errorf("text: font %q is %d bytes, over the %d limit", family, len(data), maxFontBytes)
	}
	defer func() {
		if r := recover(); r != nil {
			f, err = nil, fmt.Errorf("text: loading font %q: %v", family, r)
		}
	}()
	sf, err := shape.Load(data)
	if err != nil {
		return nil, fmt.Errorf("text: loading font %q: %w", family, err)
	}
	upem := sf.UnitsPerEm()
	if upem <= 0 {
		return nil, fmt.Errorf("text: font %q has no units per em", family)
	}
	if weight <= 0 {
		weight, italic = styleOfFace(sf)
	}
	f = &Face{
		ID: id, Family: family, Weight: weight, Italic: italic,
		shape: sf, prog: sf.Program(), upem: upem,
		seen: make([]atomic.Uint64, (sf.NumGlyphs()+63)/64),
	}
	f.clones.New = func() any { return sf.Clone() }
	return f, nil
}

// shapeGlyphs shapes s through a clone of the face, so that any number of
// goroutines may shape through one Face at once, and records the glyphs
// returned for Subset. It contains any panic a malformed font provokes.
func (f *Face) shapeGlyphs(s string) (g []shape.Glyph, ok bool) {
	c := f.clones.Get().(*shape.Face)
	defer func() {
		if recover() != nil {
			g, ok = nil, false
			return // a clone that panicked mid-shaping is dropped
		}
		f.clones.Put(c)
	}()
	g, _ = c.ShapeGlyphs(s)
	f.record(g)
	return g, true
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

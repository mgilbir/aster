package raster

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/text"
)

// OutlineSink receives glyph outlines in font units with the Y axis pointing
// down (screen orientation).
type OutlineSink interface {
	MoveTo(x, y float64)
	LineTo(x, y float64)
	QuadTo(x1, y1, x, y float64)
	CubicTo(x1, y1, x2, y2, x, y float64)
	Close()
}

// Face is one font face as seen by the rasterizer. Values must be comparable
// (pointers): outlines are cached per face and glyph.
type Face interface {
	// UnitsPerEm is the size of the em square in font units.
	UnitsPerEm() float64
	// Ascent and Descent are positive distances above and below the baseline
	// in font units, used only for dominant-baseline adjustments.
	Ascent() float64
	Descent() float64
	// Outline sends the glyph outline to sink; false means no monochrome
	// outline (blank or colour glyph).
	Outline(gid uint32, sink OutlineSink) bool
}

// FontRequest describes the font a text run asks for, as CSS resolved it.
type FontRequest struct {
	// Families is the font-family list in order, unquoted; generic families
	// ("sans-serif", "serif", ...) appear by name.
	Families []string
	Weight   int // 1..1000 (text.ParseFontWeight)
	Italic   bool
}

// ShapedGlyph is one positioned glyph of a shaped run.
type ShapedGlyph struct {
	Face    Face
	ID      uint32
	Advance float64 // pixels; pen movement after the glyph
	XOffset float64 // pixels; drawing offset from the pen, +x right
	YOffset float64 // pixels; drawing offset from the pen, +y up
}

// Shaper turns text into positioned glyphs at a font size. Letter and word
// spacing are added by the rasterizer afterwards. Implementations must be
// safe for concurrent use.
type Shaper interface {
	Shape(text string, req FontRequest, size float64) []ShapedGlyph
}

// FontData is a font file registered under a family name.
type FontData struct {
	Family string
	Data   []byte
}

// textShaper adapts internal/text (forme shaping, the engine's font
// resolution and fallback) to Shaper.
type textShaper struct {
	m     *text.Measurer
	known map[string]bool // lower-case family names that exist
	last  *knownMemo      // shared with the shaper's bound views
}

// knownMemo is anyKnown's answer for the family list asked last: a <text>
// shapes run after run in its element's list, which the document may make
// long, and each run would read it again.
type knownMemo struct {
	sync.Mutex
	families []string
	known    bool
}

// BoundShaper returns s shaping under b (see text.ShapingBudget) when s is
// one of this package's shapers. Any other Shaper, and a nil b, leave s as it
// is.
func BoundShaper(s Shaper, b *text.ShapingBudget) Shaper {
	ts, ok := s.(*textShaper)
	if !ok || b == nil {
		return s
	}
	return &textShaper{m: ts.m.Bounded(b), known: ts.known, last: ts.last}
}

var builtinFamilies = []string{
	"liberation sans", "liberation serif", "liberation mono", "noto emoji",
	"sans-serif", "serif", "monospace", "cursive", "fantasy", "system-ui",
}

// NewShaper returns the default Shaper: forme shaping over the embedded
// Liberation Sans/Serif/Mono and Noto Emoji faces plus the given fonts, with
// per-rune fallback across all of them. Text whose font-family names no
// registered family falls back to the serif family, as resvg does.
func NewShaper(fonts ...FontData) (Shaper, error) {
	// Exact advances at the exact CSS size: that is what resvg lays out with.
	opts := []text.Option{text.WithExactAdvances()}
	known := map[string]bool{}
	for _, f := range builtinFamilies {
		known[f] = true
	}
	for _, f := range fonts {
		if f.Family == "" || len(f.Data) == 0 {
			return nil, fmt.Errorf("raster: font with empty family or data")
		}
		opts = append(opts, text.WithFont(f.Family, f.Data))
		known[text.ASCIILower(f.Family)] = true
	}
	m, err := text.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("raster: creating text shaper: %w", err)
	}
	return &textShaper{m: m, known: known, last: &knownMemo{}}, nil
}

// NewShaperWithOptions returns a Shaper over a text.Measurer built from opts,
// so text resolves font families exactly as the engine's layout does:
// WithFont registrations, WithDefaultFontFamily / WithDefaultSerifFamily /
// WithDefaultMonospaceFamily and WithSystemFonts all apply. Exact advances
// (what resvg lays out with) are always enabled. Unlike NewShaper there is no
// serif substitution for unknown families: the measurer's own fallback chain
// decides, as it does for layout.
func NewShaperWithOptions(opts ...text.Option) (Shaper, error) {
	all := append([]text.Option{text.WithExactAdvances()}, opts...)
	m, err := text.New(all...)
	if err != nil {
		return nil, fmt.Errorf("raster: creating text shaper: %w", err)
	}
	return NewShaperFromMeasurer(m), nil
}

// NewShaperFromMeasurer wraps an existing Measurer (which is safe for
// concurrent use). Glyph advances come from whatever mode the measurer was
// built with; build it with text.WithExactAdvances for resvg-identical
// placement. Family resolution is entirely the measurer's.
func NewShaperFromMeasurer(m *text.Measurer) Shaper {
	return &textShaper{m: m}
}

var defaultShaper struct {
	once sync.Once
	s    Shaper
	err  error
}

func getDefaultShaper() (Shaper, error) {
	defaultShaper.once.Do(func() { defaultShaper.s, defaultShaper.err = NewShaper() })
	return defaultShaper.s, defaultShaper.err
}

func (s *textShaper) Shape(txt string, req FontRequest, size float64) []ShapedGlyph {
	if size <= 0 || txt == "" {
		return nil
	}
	// The font as the element states it, read by the text package as the
	// PDF writer reads it, and resolved to faces as layout resolves the
	// font it measured with: never through a font string to be read back,
	// so the weight is the element's own (C31).
	families := req.Families
	if s.known != nil && !s.anyKnown(families) {
		families = serif
	}
	runs, _ := s.m.ShapeFont(txt, text.CSSFont{Italic: req.Italic, Weight: req.Weight, Size: size, Family: families})
	n := 0
	for _, r := range runs {
		n += len(r.Glyphs)
	}
	out := make([]ShapedGlyph, 0, n)
	for _, r := range runs {
		face := faceOf(r.Face)
		k := 1.0
		if r.ShapedSize > 0 {
			k = r.Size / r.ShapedSize
		}
		for _, g := range r.Glyphs {
			out = append(out, ShapedGlyph{Face: face, ID: uint32(g.GID), Advance: g.Advance * k, XOffset: g.XOffset * k, YOffset: g.YOffset * k})
		}
	}
	return out
}

// serif is the family list of text NewShaper's shaper draws when its list
// names no registered family, as resvg does.
var serif = []string{"serif"}

// anyKnown reports whether one of the families the text package considers
// (the first text.MaxFamilies) is registered.
func (s *textShaper) anyKnown(families []string) bool {
	if len(families) == 0 {
		return false
	}
	s.last.Lock()
	defer s.last.Unlock()
	if len(s.last.families) == len(families) && &s.last.families[0] == &families[0] {
		return s.last.known
	}
	s.last.families, s.last.known = families, s.findKnown(families)
	return s.last.known
}

// findKnown is anyKnown, read.
func (s *textShaper) findKnown(families []string) bool {
	for i, f := range families {
		if i == text.MaxFamilies {
			break
		}
		if s.known[text.ASCIILower(strings.TrimSpace(f))] {
			return true
		}
	}
	return false
}

// faceOf is the Face f is drawn through. It is kept with f (text.Face.Annex)
// rather than in a cache of this package keyed by f, so that it goes when
// the Measurer drops f (a system face past its bound) instead of keeping it
// and its tables for the life of the converter (C27); and it is one value
// per face, which the outline caches key on.
func faceOf(f *text.Face) *textFace {
	return f.Annex(textFaceKey{}, func() any { return &textFace{f: f} }).(*textFace)
}

// textFaceKey is the key this package's Face of a text.Face is kept under.
type textFaceKey struct{}

type textFace struct {
	f    *text.Face
	once sync.Once
	fm   FaceMetrics
	fmOK bool
}

// Metrics parses the font's hhea, OS/2 and post tables once.
func (t *textFace) Metrics() (FaceMetrics, bool) {
	t.once.Do(func() {
		// The tables are read where they are: a face of a large collection is
		// not copied out into a program of its own.
		tables := map[string][]byte{}
		for _, tag := range []string{"hhea", "OS/2", "post"} {
			if tb := t.f.Table(tag); tb != nil {
				tables[tag] = tb
			}
		}
		t.fm, t.fmOK = faceMetricsOf(tables, t.UnitsPerEm())
	})
	return t.fm, t.fmOK
}

func (t *textFace) UnitsPerEm() float64 {
	if u := t.f.UnitsPerEm(); u > 0 {
		return float64(u)
	}
	return 1000
}
func (t *textFace) Ascent() float64 {
	if m, ok := t.Metrics(); ok {
		return m.Ascender
	}
	return 0.9 * t.UnitsPerEm()
}

func (t *textFace) Descent() float64 {
	if m, ok := t.Metrics(); ok {
		return -m.Descender
	}
	return 0.21 * t.UnitsPerEm()
}

func (t *textFace) Outline(gid uint32, sink OutlineSink) bool {
	segs, err := text.GlyphOutline(t.f, int(gid), t.UnitsPerEm())
	if err != nil {
		return false
	}
	for _, sg := range segs {
		switch sg.Kind {
		case text.MoveTo:
			sink.MoveTo(sg.P[0].X, sg.P[0].Y)
		case text.LineTo:
			sink.LineTo(sg.P[0].X, sg.P[0].Y)
		case text.QuadTo:
			sink.QuadTo(sg.P[0].X, sg.P[0].Y, sg.P[1].X, sg.P[1].Y)
		case text.CubicTo:
			sink.CubicTo(sg.P[0].X, sg.P[0].Y, sg.P[1].X, sg.P[1].Y, sg.P[2].X, sg.P[2].Y)
		case text.Close:
			sink.Close()
		}
	}
	return true
}

// Colour reports whether glyph gid is painted rather than filled from its
// outline: a COLR, SVG, sbix, CBDT or EBDT glyph.
func (t *textFace) Colour(gid uint32, ppem int) bool {
	return text.GlyphColour(t.f, int(gid), shape.PaintOptions{PPEM: ppem}) != shape.ColourNone
}

func (t *textFace) Paint(gid uint32, opts shape.PaintOptions, p shape.Painter) error {
	return text.PaintGlyph(t.f, int(gid), opts, p)
}

var emptyPath = &path{}

// closeContours inserts a close before every moveto and at the end.
func closeContours(p *path) *path {
	out := &path{verbs: make([]uint8, 0, len(p.verbs)+4), pts: p.pts}
	open := false
	for _, v := range p.verbs {
		if v == vMove && open {
			out.verbs = append(out.verbs, vClose)
		}
		if v == vClose {
			open = false
			out.verbs = append(out.verbs, v)
			continue
		}
		out.verbs = append(out.verbs, v)
		open = true
	}
	if open {
		out.verbs = append(out.verbs, vClose)
	}
	return out
}

// TextFace is the face of the text package a Face made by this package's
// shapers draws from.
func TextFace(f Face) (*text.Face, bool) {
	tf, ok := f.(*textFace)
	if !ok {
		return nil, false
	}
	return tf.f, true
}

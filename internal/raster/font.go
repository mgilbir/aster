package raster

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

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
	Weight   int // 100..900
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
	faces *faceCache      // shared with the shaper's bound views
}

// faceCache maps the measurer's faces to the Face each is drawn through.
type faceCache struct {
	mu sync.Mutex
	m  map[*text.Face]*textFace
}

// BoundShaper returns s shaping under b (see text.ShapingBudget) when s is
// one of this package's shapers. Any other Shaper, and a nil b, leave s as it
// is.
func BoundShaper(s Shaper, b *text.ShapingBudget) Shaper {
	ts, ok := s.(*textShaper)
	if !ok || b == nil {
		return s
	}
	return &textShaper{m: ts.m.Bounded(b), known: ts.known, faces: ts.faces}
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
		known[strings.ToLower(f.Family)] = true
	}
	m, err := text.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("raster: creating text shaper: %w", err)
	}
	return &textShaper{m: m, known: known, faces: &faceCache{m: map[*text.Face]*textFace{}}}, nil
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
	return &textShaper{m: m, faces: &faceCache{m: map[*text.Face]*textFace{}}}
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

// familyUnquote strips quotes from a font-family name and turns commas into
// spaces; a Replacer is safe for concurrent use and costly to build.
var familyUnquote = strings.NewReplacer("'", "", "\"", "", ",", " ")

func (s *textShaper) Shape(txt string, req FontRequest, size float64) []ShapedGlyph {
	if size <= 0 || txt == "" {
		return nil
	}
	fams := make([]string, 0, len(req.Families))
	found := false
	for _, f := range req.Families {
		f = familyUnquote.Replace(f)
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		fams = append(fams, "'"+f+"'")
		if s.known == nil || s.known[strings.ToLower(f)] {
			found = true
		}
	}
	if !found {
		fams = []string{"serif"}
	}
	w := (req.Weight + 50) / 100 * 100
	if w < 100 {
		w = 100
	} else if w > 900 {
		w = 900
	}
	css := ""
	if req.Italic {
		css = "italic "
	}
	css += strconv.Itoa(w) + " " + strconv.FormatFloat(size, 'f', -1, 64) + "px " + strings.Join(fams, ",")
	runs, _ := s.m.ShapeText(txt, css)
	n := 0
	for _, r := range runs {
		n += len(r.Glyphs)
	}
	out := make([]ShapedGlyph, 0, n)
	for _, r := range runs {
		face := s.face(r.Face)
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

func (s *textShaper) face(f *text.Face) *textFace {
	s.faces.mu.Lock()
	defer s.faces.mu.Unlock()
	if tf, ok := s.faces.m[f]; ok {
		return tf
	}
	tf := &textFace{f: f}
	s.faces.m[f] = tf
	return tf
}

type textFace struct {
	f    *text.Face
	once sync.Once
	fm   FaceMetrics
	fmOK bool
}

// Metrics parses the font's hhea, OS/2 and post tables once.
func (t *textFace) Metrics() (FaceMetrics, bool) {
	t.once.Do(func() { t.fm, t.fmOK = parseFaceMetrics(t.f.Program(), t.UnitsPerEm()) })
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

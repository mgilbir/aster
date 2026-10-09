// Package text measures and shapes text for layout, PNG rasterization and PDF
// output, using github.com/mgilbir/forme, with no cgo and no external font
// stack.
//
// A Measurer resolves a CSS font shorthand ("italic bold 14px Arial,
// sans-serif") to a list of registered faces, falls back face by face for
// runes a face does not cover, shapes each run with forme, and reports the
// advance width. Liberation Sans, Serif and Mono and Noto Emoji are embedded,
// so widths are reproducible on every machine.
//
// # Metrics
//
// By default advances follow the metric model of the go-text/typesetting
// shaper earlier versions of this module used before moving to forme (recorded in
// testdata/textmeasure_golden.json.gz), so widths agree with it to the last
// 1/64 px and existing layouts do not move: the shaping size is the CSS size rounded up to a whole pixel, and
// each glyph advance is rounded to 1/64 px. WithExactAdvances switches to
// unrounded advances at the exact CSS size, which is what a browser lays out
// with.
//
// # Concurrency
//
// A Measurer is safe for concurrent use. A mutex guards only the caches and
// the font lookup (CSS parsing, face selection, splitting text into runs per
// face); shaping itself runs outside it, through per-goroutine clones of the
// forme face (forme's Face.Clone shares the parsed font and layout caches and
// keeps its own record of used glyphs), so parallel cold measurements do not
// serialise. Repeated measurements are answered from a bounded cache without
// shaping.
package text

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/fonts/liberation"
	"github.com/mgilbir/aster/internal/fonts/notoemoji"
	"github.com/mgilbir/forme/shape"
)

// Limits that keep a hostile string or size from making a measurement
// arbitrarily expensive.
const (
	maxTextBytes  = 1 << 20 // longer text is measured up to this many bytes
	maxSizePx     = 1 << 16 // larger CSS sizes are clamped
	maxFamilies   = 32      // font-family entries considered per query
	maxCacheItems = 1 << 14 // entries in each of the measurement caches

	// maxWidthBytes bounds the width cache by what its entries hold: the key
	// strings and widthEntryBytes of map overhead each. Labels are short, so
	// the entry count usually ends the cache first; the bytes stop a few long
	// texts (up to a specification's string limit each) from pinning memory
	// that no render's budget accounts for, in a Measurer that outlives them.
	maxWidthBytes   = 4 << 20
	widthEntryBytes = 64
)

// Option configures a Measurer.
type Option func(*config)

type config struct {
	systemFonts     bool
	exact           bool
	pango           pangoMode
	fonts           []customFont
	fallbackFamily  string
	serifFamily     string
	monospaceFamily string
}

type customFont struct {
	family string
	data   []byte
	axes   map[string]float64 // where in its design space, for WithFontInstance
	index  int                // which face of a collection; every face when negative
}

// WithSystemFonts enables fonts installed on the machine. See system.go for what is and is not supported: family
// lookups by name work for plain .ttf/.otf files; there is no script-based
// fallback across system fonts.
func WithSystemFonts() Option { return func(c *config) { c.systemFonts = true } }

// WithFont registers a TrueType, OpenType or WOFF font under the given family
// name. Its weight and style are read from the font. Fonts registered later
// are tried after earlier ones when the same family name has several faces
// of equal fit.
//
// A font collection (.ttc, .otc) registers every face of it under family,
// each with its own weight and style, which CSS font-weight and font-style
// then choose among; WithFontFace registers one.
func WithFont(family string, ttf []byte) Option {
	return func(c *config) { c.fonts = append(c.fonts, customFont{family: family, data: ttf, index: -1}) }
}

// WithFontFace registers face index (from 0) of a font collection under
// family, as WithFont registers a font; index 0 of a single font is the font.
func WithFontFace(family string, ttc []byte, index int) Option {
	return func(c *config) {
		c.fonts = append(c.fonts, customFont{family: family, data: ttc, index: max(0, index)})
	}
}

// WithFontInstance registers a variable font under family as WithFont does,
// at one point of its design space: axes by tag, such as
// {"wght": 650}. Its outlines, metrics and colour glyphs (COLR's variable
// paints) are all the instance's; an axis not named stays at its default,
// one outside its range is clamped to it, and one the font does not have is
// an error from New. Of a collection, the first face is the one drawn;
// WithFontFaceInstance names another.
func WithFontInstance(family string, ttf []byte, axes map[string]float64) Option {
	return WithFontFaceInstance(family, ttf, 0, axes)
}

// WithFontFaceInstance is WithFontInstance for face index of a collection.
func WithFontFaceInstance(family string, ttc []byte, index int, axes map[string]float64) Option {
	return func(c *config) {
		c.fonts = append(c.fonts, customFont{family: family, data: ttc, index: max(0, index), axes: maps.Clone(axes)})
	}
}

// WithDefaultFontFamily sets the family generic CSS families such as
// "sans-serif" fall back to. Defaults to "Liberation Sans".
func WithDefaultFontFamily(family string) Option {
	return func(c *config) { c.fallbackFamily = family }
}

// WithDefaultSerifFamily sets the family "serif" resolves to. Defaults to
// "Liberation Serif".
func WithDefaultSerifFamily(family string) Option {
	return func(c *config) { c.serifFamily = family }
}

// WithDefaultMonospaceFamily sets the family "monospace" resolves to.
// Defaults to "Liberation Mono".
func WithDefaultMonospaceFamily(family string) Option {
	return func(c *config) { c.monospaceFamily = family }
}

// WithExactAdvances disables the reference metric model (whole-pixel shaping
// size, 1/64 px advance rounding) and reports unrounded advances at the exact
// CSS size.
func WithExactAdvances() Option { return func(c *config) { c.exact = true } }

// pangoMode is how advances are scaled in exact mode.
type pangoMode uint8

const (
	pangoNone  pangoMode = iota // unrounded
	pangoRound                  // HarfBuzz 3 and later: rounded half up to 1/1024 px
	pangoFloor                  // HarfBuzz 2: floored to 1/1024 px
)

// WithPangoAdvances is WithExactAdvances with every glyph advance and GPOS
// adjustment scaled the way Pango does when node-canvas measures text: HarfBuzz
// scales at size*1024, so each comes out a whole number of 1/1024 px. The
// rounding is the HarfBuzz version's: flooring before HarfBuzz 3 (the Pango
// 1.48 that node-canvas bundles on Linux), rounding to nearest after it
// (Pango 1.57 on macOS). Without it advances are unrounded, which is
// within 1/2048 px per glyph of either.
func WithPangoAdvances(floor bool) Option {
	return func(c *config) {
		c.exact = true
		c.pango = pangoRound
		if floor {
			c.pango = pangoFloor
		}
	}
}

// Glyph is one positioned glyph of a Run. Distances are in pixels at the
// run's metric (see Run.ShapedSize).
type Glyph struct {
	GID     int     // glyph index in the run's face
	Cluster int     // byte offset in the shaped text of the first source rune
	Advance float64 // pen movement after the glyph
	XOffset float64 // displacement from the pen, +x right
	YOffset float64 // displacement from the pen, +y up (font orientation)
}

// Run is a contiguous stretch of text shaped with a single face, in visual
// order.
type Run struct {
	Face   *Face
	Glyphs []Glyph
	// Size is the CSS font size in pixels the text was requested at.
	Size float64
	// ShapedSize is the size the advances were computed at: Size in exact
	// mode, Size rounded up to a whole pixel in the default metric model. A
	// glyph outline drawn at Size is advanced by Glyph.Advance regardless.
	ShapedSize float64
	// Width is the sum of the glyph advances.
	Width float64
}

// Measurer computes text widths and shapes text. It is safe for concurrent
// use. Bounded returns a view of it that shapes under a ShapingBudget; the
// views share the fonts and caches.
type Measurer struct {
	*measurerState
	// budget bounds the shaping done through this view; nil shapes without
	// bounds.
	budget *ShapingBudget
}

// measurerState is what every view of a Measurer shares.
type measurerState struct {
	mu    sync.RWMutex // guards the caches; the width cache is read under RLock
	exact bool
	pango pangoMode

	entries  []*entry // registration order
	fallback []*entry // entries in fallback order (see faces)
	byFam    map[string][]*entry
	first    *Face // the face runes nothing covers fall back to (.notdef)

	fallbackFamily  string
	serifFamily     string
	monospaceFamily string
	system          *systemIndex // nil unless WithSystemFonts
	sysEntries      map[sysKey]*entry

	cssCache   map[string]CSSFont
	listCache  map[listKey]*faceList
	widthCache map[widthKey]float64
	widthBytes int // what widthCache holds, as maxWidthBytes counts it
	nextID     int
}

type widthKey struct{ text, css string }

// New creates a Measurer with the embedded Liberation and Noto Emoji fonts.
func New(opts ...Option) (*Measurer, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	m := &Measurer{measurerState: &measurerState{
		exact:           cfg.exact,
		pango:           cfg.pango,
		byFam:           make(map[string][]*entry),
		cssCache:        make(map[string]CSSFont),
		listCache:       make(map[listKey]*faceList),
		widthCache:      make(map[widthKey]float64),
		fallbackFamily:  orDefault(cfg.fallbackFamily, "Liberation Sans"),
		serifFamily:     orDefault(cfg.serifFamily, "Liberation Serif"),
		monospaceFamily: orDefault(cfg.monospaceFamily, "Liberation Mono"),
	}}

	// Embedded fonts are registered first; Noto Emoji last among them so it
	// is only reached for runes nothing else covers.
	for _, e := range []struct {
		data   []byte
		id     string
		family string
		weight int
		italic bool
	}{
		{liberation.SansRegular, "liberation-sans", "Liberation Sans", 400, false},
		{liberation.SansBold, "liberation-sans-bold", "Liberation Sans", 700, false},
		{liberation.SansItalic, "liberation-sans-italic", "Liberation Sans", 400, true},
		{liberation.SansBoldItalic, "liberation-sans-bolditalic", "Liberation Sans", 700, true},
		{liberation.MonoRegular, "liberation-mono", "Liberation Mono", 400, false},
		{liberation.MonoBold, "liberation-mono-bold", "Liberation Mono", 700, false},
		{liberation.MonoItalic, "liberation-mono-italic", "Liberation Mono", 400, true},
		{liberation.MonoBoldItalic, "liberation-mono-bolditalic", "Liberation Mono", 700, true},
		{liberation.SerifRegular, "liberation-serif", "Liberation Serif", 400, false},
		{liberation.SerifBold, "liberation-serif-bold", "Liberation Serif", 700, false},
		{liberation.SerifItalic, "liberation-serif-italic", "Liberation Serif", 400, true},
		{liberation.SerifBoldItalic, "liberation-serif-bolditalic", "Liberation Serif", 700, true},
		{notoemoji.Regular, "noto-emoji", notoemoji.Family, 400, false},
	} {
		f, err := embeddedFace(e.id, e.family, e.weight, e.italic, e.data)
		if err != nil {
			return nil, fmt.Errorf("text: embedded font %s: %w", e.id, err)
		}
		m.add(&entry{face: f, family: e.family})
	}

	if cfg.systemFonts {
		m.system = loadSystemIndex()
	}

	for i, f := range cfg.fonts {
		if f.family == "" {
			return nil, errors.New("text: WithFont needs a family name")
		}
		// A collection is every face of it, or the one asked for.
		indexes := []int{f.index}
		if f.index < 0 {
			indexes = []int{0}
			if n := collectionSize(f.data); n > 1 && f.axes == nil {
				indexes = make([]int, n)
				for k := range indexes {
					indexes[k] = k
				}
			}
		}
		for _, index := range indexes {
			face, err := newFaceInstance(fmt.Sprintf("custom-%d-%d-%s", i, index, f.family), f.family, f.data, index, f.axes)
			if err != nil {
				return nil, err
			}
			m.add(&entry{face: face, family: f.family, custom: true})
		}
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].custom {
			m.fallback = append(m.fallback, m.entries[i])
		}
	}
	for _, e := range m.entries {
		if !e.custom {
			m.fallback = append(m.fallback, e)
		}
	}
	return m, nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (m *Measurer) add(e *entry) {
	e.norm = normFamily(e.family)
	if e.face != nil {
		e.weight, e.italic = e.face.Weight, e.face.Italic
		if m.first == nil {
			m.first = e.face
		}
	}
	m.entries = append(m.entries, e)
	m.byFam[e.norm] = append(m.byFam[e.norm], e)
}

// MeasureText returns the width in pixels of text set in the CSS font.
func (m *Measurer) MeasureText(text, cssFont string) float64 {
	// node-canvas hands the string to Pango as a C string: it ends at the first
	// NUL character.
	if i := strings.IndexByte(text, 0); i >= 0 {
		text = text[:i]
	}
	if len(text) == 0 {
		return 0
	}
	if strings.ContainsAny(text, "\n\r\u2028\u2029") {
		// The reference lays text out with Pango, which breaks it into lines at
		// these separators ("\r\n" is one break) and reports the widest line.
		return m.measureLines(text, cssFont)
	}
	if strings.IndexByte(text, '\t') >= 0 {
		return m.measureTabs(text, cssFont)
	}
	key := widthKey{text, cssFont}
	// Most measurements repeat (axis labels, legend entries): concurrent
	// renders on one Measurer hit the cache under a shared lock.
	m.mu.RLock()
	w, ok := m.widthCache[key]
	m.mu.RUnlock()
	if ok {
		return w
	}
	m.mu.Lock()
	if w, ok := m.widthCache[key]; ok {
		m.mu.Unlock()
		return w
	}
	p := m.plan(text, cssFont)
	m.mu.Unlock()

	_, w = m.shapePlan(&p, false)

	m.mu.Lock()
	// A text too large to share the cache with anything is not kept.
	if n := len(text) + len(cssFont) + widthEntryBytes; n <= maxWidthBytes {
		if len(m.widthCache) >= maxCacheItems || m.widthBytes+n > maxWidthBytes {
			clear(m.widthCache)
			m.widthBytes = 0
		}
		m.widthCache[key] = w
		m.widthBytes += n
	}
	m.mu.Unlock()
	return w
}

// measureLines is the width of the widest line of text split at line
// separators.
func (m *Measurer) measureLines(text, cssFont string) float64 {
	var widest float64
	for {
		i := strings.IndexAny(text, "\n\r\u2028\u2029")
		line := text
		if i >= 0 {
			line = text[:i]
		}
		if w := m.MeasureText(line, cssFont); w > widest {
			widest = w
		}
		if i < 0 {
			break
		}
		if strings.HasPrefix(text[i:], "\r\n") {
			i++
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		text = text[i+size:]
	}
	return widest
}

// measureTabs is the width of one line containing tabs, set as Pango sets
// them: tab stops lie at every eight spaces from the start of the line, and a
// tab advances to the first stop at least one space beyond the text so far
// (a stop closer than that is skipped).
func (m *Measurer) measureTabs(line, cssFont string) float64 {
	space := m.MeasureText(" ", cssFont)
	stop := 8 * space
	var x float64
	for {
		i := strings.IndexByte(line, '\t')
		if i < 0 {
			return x + m.MeasureText(line, cssFont)
		}
		x += m.MeasureText(line[:i], cssFont)
		if stop > 0 {
			x = stop * (math.Floor((x+space)/stop) + 1)
		}
		line = line[i+1:]
	}
}

// ShapeText shapes text with the font the CSS string selects and returns the
// runs, in visual order, and the total advance in pixels (equal to
// MeasureText). Runes the primary family lacks are set in the next face that
// covers them, each face change starting a new Run.
func (m *Measurer) ShapeText(text, cssFont string) ([]Run, float64) {
	if len(text) == 0 {
		return nil, 0
	}
	m.mu.Lock()
	p := m.plan(text, cssFont)
	m.mu.Unlock()
	return m.shapePlan(&p, true)
}

// FontData returns the font program of a face for embedding, or nil: its
// program, or for a face of a collection, or one larger than a font given in
// memory may be, its OutlineProgram, which a collection's face would
// otherwise be copied out whole for (Apple Color Emoji: 192 MB of bitmaps).
func (m *Measurer) FontData(f *Face) []byte {
	if f == nil {
		return nil
	}
	if f.index != 0 || len(f.src) >= 4 && string(f.src[:4]) == "ttcf" || f.Size() > maxFontBytes {
		if p := f.OutlineProgram(); p != nil {
			return p
		}
	}
	return f.Program()
}

// piece is a stretch of text set in one face.
type piece struct {
	face *Face
	a, b int
}

// planned is a measurement resolved down to faces: what plan produces under
// the mutex and shapePlan consumes without it.
type planned struct {
	text                string
	pieces              []piece
	size, shaped, scale float64
}

// plan resolves the CSS font and splits text into runs per face. It touches
// the caches and may load a system font, so m.mu is held.
func (m *Measurer) plan(text, cssFont string) planned {
	css := m.parse(cssFont)
	if len(text) > maxTextBytes {
		n := maxTextBytes
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		text = text[:n]
	}
	size := math.Min(css.Size, maxSizePx)
	list := m.faces(css)

	// The shaping size of the reference metric model: the size truncated to
	// 1/64 px, then rounded up to a whole pixel.
	shaped := size
	scale := size
	if !m.exact {
		shaped = math.Ceil(math.Floor(size*64) / 64)
		scale = shaped
	}

	p := planned{text: text, size: size, shaped: shaped, scale: scale}
	p.pieces = make([]piece, 0, 2)
	start := 0
	var cur *Face
	for i, r := range text {
		if ignoreFaceChange(r) && (cur != nil || i+utf8.RuneLen(r) < len(text)) {
			continue
		}
		f := list.resolve(m, r)
		if cur == nil {
			cur = f
		}
		if f == cur {
			continue
		}
		p.pieces = append(p.pieces, piece{cur, start, i})
		start, cur = i, f
	}
	p.pieces = append(p.pieces, piece{cur, start, len(text)})
	return p
}

// shapePlan shapes each piece of the plan. It reads only immutable state of
// the Measurer and shapes through per-goroutine face clones, so it needs no
// lock.
func (m *Measurer) shapePlan(p *planned, keep bool) ([]Run, float64) {
	var runs []Run
	var total float64
	for _, pc := range p.pieces {
		total += m.run(&runs, p.text, pc.a, pc.b, pc.face, p.size, p.shaped, p.scale, keep)
	}
	return runs, total
}

// run shapes text[a:b] in face f and appends it to runs when keep is set.
func (m *Measurer) run(runs *[]Run, text string, a, b int, f *Face, size, shaped, scale float64, keep bool) float64 {
	glyphs, ok := f.shapeGlyphs(text[a:b], m.budget)
	if !ok {
		return 0
	}
	var width float64
	var out []Glyph
	if keep {
		out = make([]Glyph, len(glyphs))
	}
	// Scale of the reference metric model in 26.6 units per em, as an
	// integer: HarfBuzz scales in integers.
	s64 := scale * 64
	upem := float64(f.upem)
	for i := range glyphs {
		g := &glyphs[i]
		var adv, xo, yo float64
		if m.exact && m.pango != pangoNone {
			trunc := m.pango == pangoFloor
			adv = pangoAdvance(g, scale, upem, trunc)
			xo = emScale(g.XOffset*upem/1000, scale, upem, trunc) / pangoUnit
			yo = emScale(g.YOffset*upem/1000, scale, upem, trunc) / pangoUnit
		} else if m.exact {
			adv = g.XAdvance * scale / 1000
			xo, yo = g.XOffset*scale/1000, g.YOffset*scale/1000
		} else {
			adv = refAdvance(g, s64, upem)
			// Offsets come from GPOS values, which HarfBuzz scales with
			// integer division: truncation toward zero.
			xo = math.Trunc(g.XOffset*upem/1000*s64/upem) / 64
			yo = math.Trunc(g.YOffset*upem/1000*s64/upem) / 64
		}
		width += adv
		if keep {
			out[i] = Glyph{GID: g.GID, Cluster: a + g.Cluster, Advance: adv, XOffset: xo, YOffset: yo}
		}
	}
	if keep {
		*runs = append(*runs, Run{Face: f, Glyphs: out, Size: size, ShapedSize: shaped, Width: width})
	}
	return width
}

func (m *Measurer) parse(s string) CSSFont {
	if c, ok := m.cssCache[s]; ok {
		return c
	}
	c := ParseCSSFont(s)
	if len(c.Family) > maxFamilies {
		c.Family = c.Family[:maxFamilies]
	}
	if len(m.cssCache) >= maxCacheItems {
		clear(m.cssCache)
	}
	m.cssCache[s] = c
	return c
}

// ignoreFaceChange reports runes that never select a face of their own: they
// join the neighbouring run (spaces, controls, default-ignorable format
// characters), because no one picks a font for a space.
func ignoreFaceChange(r rune) bool {
	if r < 0x80 {
		return r < 0x20 || r == 0x7f || r == ' '
	}
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cs, r) ||
		unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) ||
		(unicode.Is(unicode.Zs, r) && r != 0x1680) ||
		defaultIgnorable(r)
}

// defaultIgnorable follows HarfBuzz's notion of Default_Ignorable_Code_Point,
// which leaves out the Hangul fillers Unicode includes.
func defaultIgnorable(r rune) bool {
	switch {
	case r == 0xAD, r == 0x34F, r == 0x61C,
		r >= 0x17B4 && r <= 0x17B5,
		r >= 0x180B && r <= 0x180F,
		r >= 0x200B && r <= 0x200F,
		r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x206F,
		r >= 0xFE00 && r <= 0xFE0F, r == 0xFEFF,
		r >= 0xFFF0 && r <= 0xFFF8,
		r >= 0x1D173 && r <= 0x1D17A,
		r >= 0xE0000 && r <= 0xE0FFF:
		return true
	}
	return false
}

// refAdvance is a glyph's advance in pixels under the reference metric model.
// HarfBuzz rounds the glyph's own (nominal) advance to 1/64 px and adds the
// GPOS adjustment (kerning) scaled separately by truncation, and the sum
// cannot be rounded to the same result. forme reports the advance as the
// nominal one plus Glyph.XAdjust, so the two are split there.
func refAdvance(g *shape.Glyph, s64, upem float64) float64 {
	base := math.Round((g.XAdvance - g.XAdjust) * upem / 1000)
	kern := math.Round(g.XAdjust * upem / 1000)
	return (math.Round(base*s64/upem) + math.Trunc(kern*s64/upem)) / 64
}

// pangoUnit is Pango's unit: node-canvas shapes with HarfBuzz at a scale of
// size * 1024, so every glyph advance and every GPOS adjustment comes out a
// whole number of 1/1024 px.
const pangoUnit = 1024

// emScale scales v font units to 1/1024 px at size px, as hb_font_t does. The
// scale is size*1024 truncated to an integer. HarfBuzz 3 and later multiply
// by size*1024/upem as a 16.16 fixed-point number and round half up; before
// that the product was divided by upem and floored.
func emScale(v, size, upem float64, trunc bool) float64 {
	scale := int64(size * pangoUnit)
	if trunc {
		n, d := int64(v)*scale, int64(upem)
		q := n / d
		if n%d != 0 && n < 0 {
			q-- // floor, not toward zero: negative adjustments come out one unit longer
		}
		return float64(q)
	}
	mult := (scale << 16) / int64(upem)
	return float64((int64(v)*mult + 0x8000) >> 16)
}

// pangoAdvance is a glyph's advance in pixels as Pango measures it: HarfBuzz
// scales the glyph's own advance and its GPOS adjustment (kerning)
// separately, each to 1/1024 px, and the two are added.
func pangoAdvance(g *shape.Glyph, size, upem float64, trunc bool) float64 {
	base := math.Round((g.XAdvance - g.XAdjust) * upem / 1000)
	kern := math.Round(g.XAdjust * upem / 1000)
	return (emScale(base, size, upem, trunc) + emScale(kern, size, upem, trunc)) / pangoUnit
}

// Package textmeasure provides text width measurement using go-text/typesetting.
// It parses CSS font strings from Vega (e.g. "italic bold 14px Arial") and
// uses HarfBuzz-based shaping for accurate text metrics.
package textmeasure

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/fontscan"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/liberation"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/notoemoji"
	"golang.org/x/image/math/fixed"
)

// MeasurerOption configures a Measurer.
type MeasurerOption func(*measurerConfig)

type measurerConfig struct {
	systemFonts     bool
	fonts           []customFont
	fallbackFamily  string
	serifFamily     string
	monospaceFamily string
}

type customFont struct {
	family string
	data   []byte
}

// WithSystemFonts enables scanning of system-installed fonts.
func WithSystemFonts() MeasurerOption {
	return func(c *measurerConfig) {
		c.systemFonts = true
	}
}

// WithFont registers a custom TTF font with the given family name.
// Fonts added later take priority over earlier ones.
func WithFont(family string, ttf []byte) MeasurerOption {
	return func(c *measurerConfig) {
		c.fonts = append(c.fonts, customFont{family: family, data: ttf})
	}
}

// WithDefaultFontFamily sets the font family name used as the fallback when
// resolving "sans-serif" and other generic CSS font families. Defaults to
// "Liberation Sans" (the embedded font).
func WithDefaultFontFamily(family string) MeasurerOption {
	return func(c *measurerConfig) {
		c.fallbackFamily = family
	}
}

// WithDefaultSerifFamily sets the concrete font family the generic CSS "serif"
// family resolves to. Defaults to "Liberation Serif" (embedded). It mirrors the
// resvg rasterization pipeline's serif mapping so SVG layout measurement and
// PNG rendering agree on what "serif" means.
func WithDefaultSerifFamily(family string) MeasurerOption {
	return func(c *measurerConfig) {
		c.serifFamily = family
	}
}

// WithDefaultMonospaceFamily sets the concrete font family the generic CSS
// "monospace" family resolves to. Defaults to "Liberation Mono" (embedded).
// It mirrors the resvg rasterization pipeline's monospace mapping so SVG
// layout measurement and PNG rendering agree on what "monospace" means.
func WithDefaultMonospaceFamily(family string) MeasurerOption {
	return func(c *measurerConfig) {
		c.monospaceFamily = family
	}
}

// Measurer computes text widths using HarfBuzz shaping.
type Measurer struct {
	mu              sync.Mutex
	fontMap         *fontscan.FontMap
	shaper          shaping.HarfbuzzShaper
	fallbackFamily  string
	serifFamily     string
	monospaceFamily string
}

// New creates a Measurer with embedded Liberation Sans fonts for
// reproducible text metrics across all platforms. Liberation Sans is
// metrically compatible with Arial, matching vl-convert's text measurement.
func New(opts ...MeasurerOption) (*Measurer, error) {
	var cfg measurerConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	fm := fontscan.NewFontMap(nil)

	// Register embedded Liberation fonts first (always-present fallback).
	// Liberation Sans is metrically identical to Arial.
	embeddedFonts := []struct {
		data   []byte
		id     string
		family string
	}{
		{liberation.SansRegular, "liberation-sans", "Liberation Sans"},
		{liberation.SansBold, "liberation-sans-bold", "Liberation Sans"},
		{liberation.SansItalic, "liberation-sans-italic", "Liberation Sans"},
		{liberation.SansBoldItalic, "liberation-sans-bolditalic", "Liberation Sans"},
		{liberation.MonoRegular, "liberation-mono", "Liberation Mono"},
		{liberation.MonoBold, "liberation-mono-bold", "Liberation Mono"},
		{liberation.MonoItalic, "liberation-mono-italic", "Liberation Mono"},
		{liberation.MonoBoldItalic, "liberation-mono-bolditalic", "Liberation Mono"},
		{liberation.SerifRegular, "liberation-serif", "Liberation Serif"},
		{liberation.SerifBold, "liberation-serif-bold", "Liberation Serif"},
		{liberation.SerifItalic, "liberation-serif-italic", "Liberation Serif"},
		{liberation.SerifBoldItalic, "liberation-serif-bolditalic", "Liberation Serif"},
		// Monochrome Noto Emoji: always-present fallback so emoji codepoints
		// (which the Latin fonts lack) get correct advance widths and rasterize
		// in PNG. Registered after the text fonts, so it is only selected for
		// runes nothing else covers.
		{notoemoji.Regular, "noto-emoji", notoemoji.Family},
	}

	for _, f := range embeddedFonts {
		if err := fm.AddFont(bytes.NewReader(f.data), f.id, f.family); err != nil {
			return nil, fmt.Errorf("textmeasure: loading %s: %w", f.id, err)
		}
	}

	// Optionally scan system fonts.
	if cfg.systemFonts {
		if err := fm.UseSystemFonts(""); err != nil {
			return nil, fmt.Errorf("textmeasure: scanning system fonts: %w", err)
		}
	}

	// Register custom fonts (added last = highest priority among user fonts).
	for i, f := range cfg.fonts {
		id := fmt.Sprintf("custom-%d-%s", i, f.family)
		if err := fm.AddFont(bytes.NewReader(f.data), id, f.family); err != nil {
			return nil, fmt.Errorf("textmeasure: loading custom font %q: %w", f.family, err)
		}
	}

	fallback := cfg.fallbackFamily
	if fallback == "" {
		fallback = "Liberation Sans"
	}
	serif := cfg.serifFamily
	if serif == "" {
		serif = "Liberation Serif"
	}
	monospace := cfg.monospaceFamily
	if monospace == "" {
		monospace = "Liberation Mono"
	}

	return &Measurer{fontMap: fm, fallbackFamily: fallback, serifFamily: serif, monospaceFamily: monospace}, nil
}

// CSSFont represents a parsed CSS font shorthand string.
type CSSFont struct {
	Style  font.Style
	Weight font.Weight
	Size   float64 // in pixels
	Family []string
}

// MeasureText returns the width in pixels of the given text rendered with
// the specified CSS font string.
func (m *Measurer) MeasureText(text, cssFont string) float64 {
	parsed := ParseCSSFont(cssFont)
	if len(text) == 0 {
		return 0
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	families := make([]string, 0, len(parsed.Family)+3)
	for _, fam := range parsed.Family {
		// Resolve the generic CSS families to their configured concrete
		// families (tried first), mirroring the resvg pipeline so measurement
		// and rasterization agree on every generic.
		switch {
		case strings.EqualFold(fam, "serif") && m.serifFamily != "":
			families = append(families, m.serifFamily)
		case strings.EqualFold(fam, "monospace") && m.monospaceFamily != "":
			families = append(families, m.monospaceFamily)
		case strings.EqualFold(fam, "cursive") || strings.EqualFold(fam, "fantasy"):
			// No bundled cursive/fantasy face; both pipelines map these to the
			// sans-serif fallback (resvg points FamilyMapping.Cursive/Fantasy at
			// the sans family). Route them explicitly rather than relying on the
			// trailing fallback so the two pipelines stay in lockstep.
			families = append(families, m.fallbackFamily)
		}
		families = append(families, fam)
	}
	// Always add the configured fallback font family.
	families = append(families, m.fallbackFamily, fontscan.SansSerif)

	m.fontMap.SetQuery(fontscan.Query{
		Families: families,
		Aspect: font.Aspect{
			Style:  parsed.Style,
			Weight: parsed.Weight,
		},
	})
	m.fontMap.SetScript(language.Latin)

	runes := []rune(text)
	input := shaping.Input{
		Text:      runes,
		RunStart:  0,
		RunEnd:    len(runes),
		Direction: di.DirectionLTR,
		Size:      fixed.Int26_6(parsed.Size * 64),
		Script:    language.Latin,
		Language:  language.NewLanguage("en"),
	}

	// Split by font face for proper fallback handling.
	splits := shaping.SplitByFace(input, m.fontMap)

	var totalAdvance fixed.Int26_6
	for _, split := range splits {
		out := m.shaper.Shape(split)
		totalAdvance += out.Advance
	}

	return float64(totalAdvance) / 64.0
}

// cssFontRe matches CSS font shorthand: [style] [weight] size[px|pt|em] family[, family...]
var cssFontRe = regexp.MustCompile(
	`(?i)` +
		`(?:(italic|oblique)\s+)?` + // optional style
		`(?:(bold|bolder|lighter|[1-9]00)\s+)?` + // optional weight
		`([\d.]+)(px|pt|em)?\s+` + // size with optional unit (required)
		`(.+)`, // family (required)
)

// ParseCSSFont parses a CSS font shorthand string like "italic bold 14px Arial, sans-serif".
func ParseCSSFont(s string) CSSFont {
	result := CSSFont{
		Style:  font.StyleNormal,
		Weight: font.WeightNormal,
		Size:   11, // default
		Family: []string{"sans-serif"},
	}

	s = strings.TrimSpace(s)
	if s == "" {
		return result
	}

	matches := cssFontRe.FindStringSubmatch(s)
	if matches == nil {
		return result
	}

	// Style
	if matches[1] != "" {
		switch strings.ToLower(matches[1]) {
		case "italic", "oblique":
			result.Style = font.StyleItalic
		}
	}

	// Weight
	if matches[2] != "" {
		result.Weight = parseWeight(matches[2])
	}

	// Size, converted to pixels. Vega always emits px; pt/em only reach this
	// parser through direct API use.
	if size, err := strconv.ParseFloat(matches[3], 64); err == nil && size > 0 {
		switch strings.ToLower(matches[4]) {
		case "pt":
			size *= 96.0 / 72.0 // CSS: 1pt = 1/72in, 1px = 1/96in
		case "em":
			size *= 16 // relative to the CSS default root font size
		}
		result.Size = size
	}

	// Family
	if matches[5] != "" {
		result.Family = parseFamilies(matches[5])
	}

	return result
}

func parseWeight(s string) font.Weight {
	switch strings.ToLower(s) {
	case "bold", "bolder":
		return font.WeightBold
	case "lighter":
		return font.WeightLight
	default:
		if w, err := strconv.Atoi(s); err == nil {
			return font.Weight(w)
		}
		return font.WeightNormal
	}
}

func parseFamilies(s string) []string {
	parts := strings.Split(s, ",")
	families := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Remove surrounding quotes.
		p = strings.Trim(p, `"'`)
		p = strings.TrimSpace(p)
		if p != "" {
			families = append(families, p)
		}
	}
	if len(families) == 0 {
		return []string{"sans-serif"}
	}
	return families
}

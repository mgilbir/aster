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
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/math/fixed"
)

// Measurer computes text widths using HarfBuzz shaping.
type Measurer struct {
	mu      sync.Mutex
	fontMap *fontscan.FontMap
	shaper  shaping.HarfbuzzShaper
}

// New creates a Measurer with embedded Go fonts as fallbacks.
func New() (*Measurer, error) {
	fm := fontscan.NewFontMap(nil)

	// Register embedded Go fonts.
	fonts := []struct {
		data   []byte
		id     string
		family string
	}{
		{goregular.TTF, "goregular", "Go"},
		{gobold.TTF, "gobold", "Go"},
		{goitalic.TTF, "goitalic", "Go"},
		{gobolditalic.TTF, "gobolditalic", "Go"},
		{gomono.TTF, "gomono", "Go Mono"},
		{gomonobold.TTF, "gomonobold", "Go Mono"},
		{gomonobolditalic.TTF, "gomonobolditalic", "Go Mono"},
		{gomonoitalic.TTF, "gomonoitalic", "Go Mono"},
	}

	for _, f := range fonts {
		if err := fm.AddFont(bytes.NewReader(f.data), f.id, f.family); err != nil {
			return nil, fmt.Errorf("textmeasure: loading %s: %w", f.id, err)
		}
	}

	return &Measurer{fontMap: fm}, nil
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

	families := make([]string, 0, len(parsed.Family)+2)
	families = append(families, parsed.Family...)
	// Always add Go fonts as fallback.
	families = append(families, "Go", fontscan.SansSerif)

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

// cssFontRe matches CSS font shorthand: [style] [weight] size[px|em] family[, family...]
var cssFontRe = regexp.MustCompile(
	`(?i)` +
		`(?:(italic|oblique)\s+)?` + // optional style
		`(?:(bold|bolder|lighter|[1-9]00)\s+)?` + // optional weight
		`([\d.]+)(?:px|pt|em)?\s+` + // size (required)
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

	// Size
	if size, err := strconv.ParseFloat(matches[3], 64); err == nil && size > 0 {
		result.Size = size
	}

	// Family
	if matches[4] != "" {
		result.Family = parseFamilies(matches[4])
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

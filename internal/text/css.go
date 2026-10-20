package text

import (
	"math"
	"strings"
)

// CSSFont is a font as CSS states it: what ParseCSSFont reads from a font
// shorthand, and what a writer that holds the parts of a font (an SVG
// element's font-family, font-size, font-weight and font-style) hands to
// ShapeFont without ever writing them into a string.
type CSSFont struct {
	Italic bool // font-style italic or oblique (the two are one face here)
	Weight int  // 1..1000; bold is 700, normal 400
	Size   float64
	Family []string
}

// Default CSS font values, applied to whatever a shorthand does not state (or
// entirely, when it cannot be parsed).
const (
	defaultSize = 11
	// zeroSizeCanvas is the size node-canvas measures at for a font whose size is
	// zero or less.
	zeroSizeCanvas = 16
	weightNormal   = 400
	weightBold     = 700
	weightLighter  = 100 // node-canvas's FontParser: lighter and bolder are 100 and 700
)

// ParseCSSFont parses a CSS font shorthand such as
// "italic bold 14px Arial, sans-serif" by the grammar a canvas 2D context
// parses ctx.font with (node-canvas's FontParser; see ValidCanvasFont): up
// to three of font-style, font-variant and font-weight, a font-size with its
// unit, an optional line-height, then the family list. A numeric weight is a
// weight (450, 1000), a size needs its unit, and a number may carry an
// exponent (Vega writes 1e21 as "1e+21px").
//
// A string the grammar rejects yields the defaults: normal, 400, 11px,
// sans-serif. A size of zero or less, which node-canvas parses but does not
// apply, measures at 16px.
func ParseCSSFont(s string) CSSFont {
	f, ok := parseCanvasFont(s)
	if !ok {
		return CSSFont{Weight: weightNormal, Size: defaultSize, Family: []string{"sans-serif"}}
	}
	return f
}

// parseCanvasFont parses s as node-canvas's FontParser does, and reports
// whether it accepts it. The family list never makes a font invalid (see
// ValidCanvasFont); it is read as it always was here, split at commas with
// its quotes removed, so that family resolution does not change.
func parseCanvasFont(s string) (CSSFont, bool) {
	fam, hasFam := "", false
	if m := canvasFontFamilyRe.FindStringSubmatch(s); m != nil {
		s, fam, hasFam = m[1]+`"DejaVu Sans"`, m[2], true
	}
	p := &fontParser{tk: tokenizer{in: s}, weight: weightNormal}
	p.cur = p.tk.next()
	p.nxt = p.tk.next()
	if !p.parse() {
		return CSSFont{}, false
	}
	f := CSSFont{Italic: p.italic, Weight: p.weight, Size: p.size}
	switch {
	case math.IsInf(f.Size, 0):
		f.Size = defaultSize
	case !(f.Size > 0):
		// A size of zero or less is accepted and not applied: the context
		// measures at 16px (Pango's default), not at the size it held.
		f.Size = zeroSizeCanvas
	}
	switch {
	case hasFam:
		f.Family = parseFamilies(fam)
	case len(p.families) > 0:
		f.Family = p.families
	default:
		f.Family = []string{"sans-serif"}
	}
	return f, true
}

func parseFamilies(s string) []string {
	var families []string
	EachFontFamily(s, func(name string) bool {
		families = append(families, name)
		return true
	})
	if len(families) == 0 {
		return []string{"sans-serif"}
	}
	return families
}

// EachFontFamily calls f with each family name of a font-family list in
// order, until f returns false: the list split at commas, each name trimmed
// of white space and of surrounding quotes, empty names left out. It is the
// one reader of a family list (ParseFontFamily and the CSS font reader are
// built on it), and reads no further than f asks, so a caller that wants the
// first few names does not split a list the document made long.
func EachFontFamily(s string, f func(name string) bool) {
	for len(s) > 0 {
		var p string
		p, s, _ = strings.Cut(s, ",")
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`) // surrounding quotes
		p = strings.TrimSpace(p)
		if p != "" && !f(p) {
			return
		}
	}
}

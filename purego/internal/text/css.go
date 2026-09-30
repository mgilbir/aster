package text

import (
	"regexp"
	"strconv"
	"strings"
)

// CSSFont is a parsed CSS font shorthand.
type CSSFont struct {
	Italic bool // font-style italic or oblique (the two are one face here)
	Weight int  // 100..900; bold is 700, normal 400
	Size   float64
	Family []string
}

// Default CSS font values, applied to whatever a shorthand does not state (or
// entirely, when it cannot be parsed).
const (
	defaultSize   = 11
	weightNormal  = 400
	weightBold    = 700
	weightLighter = 300
)

// cssFontRe matches the CSS font shorthand:
// [style] [weight] size[px|pt|em] family[, family...].
//
// It is deliberately unanchored and permissive: it is the parsing rule the
// measurements this package must reproduce were made with, so a string that
// merely contains a well-formed font is accepted (leading "normal", for
// example, is skipped rather than rejected).
var cssFontRe = regexp.MustCompile(
	`(?i)` +
		`(?:(italic|oblique)\s+)?` + // optional style
		`(?:(?:normal|small-caps)\s+)?` + // optional variant: vega.font writes style, variant, weight in that order
		`(?:(bold|bolder|lighter|[1-9]00)\s+)?` + // optional weight
		`([\d.]+)(px|pt|em)?\s+` + // size with optional unit
		`(.+)`, // family list
)

// ParseCSSFont parses a CSS font shorthand such as
// "italic bold 14px Arial, sans-serif". Anything unparseable yields the
// defaults: normal, 400, 11px, sans-serif.
func ParseCSSFont(s string) CSSFont {
	result := CSSFont{Weight: weightNormal, Size: defaultSize, Family: []string{"sans-serif"}}

	s = strings.TrimSpace(s)
	if s == "" {
		return result
	}
	m := cssFontRe.FindStringSubmatch(s)
	if m == nil {
		return result
	}
	if m[1] != "" {
		result.Italic = true // "italic" and "oblique" alike
	}
	if m[2] != "" {
		result.Weight = parseWeight(m[2])
	}
	// Vega always emits px; pt and em only reach this through direct API use.
	if size, err := strconv.ParseFloat(m[3], 64); err == nil && size > 0 {
		switch strings.ToLower(m[4]) {
		case "pt":
			size *= 96.0 / 72.0 // CSS: 1pt = 1/72in, 1px = 1/96in
		case "em":
			size *= 16 // relative to the CSS default root font size
		}
		result.Size = size
	}
	if m[5] != "" {
		result.Family = parseFamilies(m[5])
	}
	return result
}

func parseWeight(s string) int {
	switch strings.ToLower(s) {
	case "bold", "bolder":
		return weightBold
	case "lighter":
		return weightLighter
	}
	if w, err := strconv.Atoi(s); err == nil {
		return w
	}
	return weightNormal
}

func parseFamilies(s string) []string {
	parts := strings.Split(s, ",")
	families := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`) // surrounding quotes
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

package text

import (
	"strconv"
	"strings"
)

// The font properties of an SVG element, read once, here, for both the PNG
// and the PDF writer: what each reads into the CSSFont it shapes with, and
// so the face it draws, is the same, and is the face layout measured the
// text in (C31). Layout reads the canvas font string vega-scenegraph builds
// of the same values (font(item) in util/text.js), so each reader agrees
// with how ParseCSSFont reads that string wherever SVG allows; where the
// two disagree, layout wins, so that a label is drawn in the face it was
// laid out in.

// MaxFamilies is how many families of a font-family list are considered:
// the rest change nothing a real chart draws, and would be read again for
// every run of text.
const MaxFamilies = 32

// InitialFontFamily is the font-family of SVG text that states none, for
// both writers: CSS leaves the initial value to the user agent, which is a
// serif in browsers, and usvg's default ("Times New Roman", Options::
// font_family), which the PNG output follows. A new slice each call: a
// writer's state may hold it.
func InitialFontFamily() []string { return []string{"serif"} }

// ParseFontFamily reads a font-family value as layout reads the family list
// of a canvas font: split at commas, each name trimmed of white space and of
// surrounding quotes. A quoted name with a comma in it is two names, as
// node-canvas (and so layout) takes it, where CSS would take one: no font
// has such a name, and the two then agree. ok is false for a value that
// names no family, which the element then does not take.
func ParseFontFamily(v string) (families []string, ok bool) {
	if strings.Trim(v, " \t\n\r\f,'\"") == "" {
		return nil, false
	}
	return parseFamilies(v), true
}

// ParseFontWeight reads a font-weight value, the element's parent's weight
// being inherited: normal, bold, bolder and lighter (relative to inherited,
// by CSS Fonts 4's table, 2.2.1), or a number. A number is truncated, as
// node-canvas (and so layout) reads the weight of a canvas font, and is a
// weight from 1 up to, not including, 1001. ok is false for any other value,
// which the element then does not take.
func ParseFontWeight(v string, inherited int) (weight int, ok bool) {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "normal":
		return weightNormal, true
	case "bold":
		return weightBold, true
	case "bolder":
		switch {
		case inherited < 350:
			return weightNormal, true
		case inherited < 550:
			return weightBold, true
		}
		return max(inherited, 900), true
	case "lighter":
		switch {
		case inherited < 550:
			return min(inherited, 100), true
		case inherited < 750:
			return weightNormal, true
		}
		return weightBold, true
	}
	n, ok := cssNumber(v)
	if !ok || !(n >= 1 && n < 1001) {
		return 0, false
	}
	return int(n), true
}

// cssNumber reads s as a CSS number: a sign, digits with a fraction or a
// fraction alone, and an exponent. strconv alone would take a hexadecimal
// float, an infinity or underscores, which CSS does not.
func cssNumber(s string) (float64, bool) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		frac := 0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			frac++
		}
		if frac == 0 {
			return 0, false
		}
		digits += frac
	}
	if digits == 0 {
		return 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := 0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			exp++
		}
		if exp == 0 {
			return 0, false
		}
	}
	if i != len(s) {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	return n, err == nil
}

// ParseFontStyle reads a font-style value: italic and oblique (with an
// angle or not) are italic, the one style here; normal is not. ok is false
// for any other value, which the element then does not take.
func ParseFontStyle(v string) (italic, ok bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case v == "normal":
		return false, true
	case v == "italic", v == "oblique", strings.HasPrefix(v, "oblique "):
		return true, true
	}
	return false, false
}

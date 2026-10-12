package svgpdf

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file is the one place attribute values are read. SVG's rule for a
// presentation attribute (and a CSS declaration) whose value does not parse
// is that it is ignored: the element is drawn as if the attribute were
// absent, with the inherited or initial value (C33). So every reader here
// reports ok false for an invalid value and its caller leaves the state as
// it was; only what the renderer does not support at all (an element, an
// attribute or a style property it does not know, a reference to nothing)
// is an error. A number read here is always finite (C57): NaN, infinities,
// and a number that overflows a float64 are not numbers in SVG's grammar.

// scanNumber reads a number at the start of s by SVG's grammar: an optional
// sign, digits with an optional fraction or a fraction alone, and an
// optional exponent ("1e" before "m" is the number 1 and the unit em). n is
// the bytes it spans; ok is false when s does not start with a number, or
// with one too large for a float64.
func scanNumber(s string) (v float64, n int, ok bool) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	whole := i - start
	frac := 0
	if i < len(s) && s[i] == '.' {
		dot := i
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if frac = i - dot - 1; frac == 0 && whole == 0 {
			i = dot
		}
	}
	if whole == 0 && frac == 0 {
		return 0, 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			i = j
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
		}
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || math.IsInf(v, 0) {
		return 0, i, false
	}
	return v, i, true
}

// isSpace is XML's whitespace, which separates the numbers of a list.
func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// xmlSpace is XML's whitespace, as a cutset.
const xmlSpace = " \t\n\r"

// parseNumber reads an attribute that is one number, with whitespace around
// it.
func parseNumber(s string) (float64, bool) {
	s = strings.Trim(s, xmlSpace)
	v, n, ok := scanNumber(s)
	return v, ok && n == len(s)
}

// parseNumberList reads numbers separated by whitespace, a comma, or both
// (one comma between two numbers), as viewBox and a transform's arguments
// are; a number that starts with a sign or a point needs no separator
// ("1-2", "0.5.5").
func parseNumberList(s string) ([]float64, bool) { return appendNumberList(nil, s) }

// appendNumberList is parseNumberList appending to out.
func appendNumberList(out []float64, s string) ([]float64, bool) {
	i, sep, comma := 0, true, false
	n0 := len(out)
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
			sep = true
		}
		if i == len(s) {
			return out, !comma
		}
		if s[i] == ',' {
			if comma || len(out) == n0 {
				return nil, false
			}
			i++
			sep, comma = true, true
			continue
		}
		if !sep && s[i] != '-' && s[i] != '+' && s[i] != '.' {
			return nil, false
		}
		v, n, ok := scanNumber(s[i:])
		if !ok {
			return nil, false
		}
		out = append(out, v)
		i += n
		sep, comma = false, false
	}
}

// pxPer are the CSS absolute length units, in px (the user unit). Constants
// with one rounding step each, as CSS defines them.
var pxPer = map[string]float64{
	"": 1, "px": 1, "pt": 96.0 / 72, "pc": 16, "in": 96, "cm": 96 / 2.54, "mm": 96 / 25.4, "q": 96 / 101.6,
}

// lengthCtx is what a relative length is relative to: the font size for em
// and ex, and the size a percentage is of (NaN where a percentage is not a
// length here).
type lengthCtx struct{ fontSize, percentOf float64 }

// parseLength reads a CSS length: a number and an optional unit.
func parseLength(s string, c lengthCtx) (float64, bool) {
	s = strings.Trim(s, xmlSpace)
	v, n, ok := scanNumber(s)
	if !ok {
		return 0, false
	}
	var px float64
	switch unit := strings.ToLower(s[n:]); unit {
	case "em":
		px = v * c.fontSize
	case "ex":
		px = v * c.fontSize / 2
	case "%":
		px = v / 100 * c.percentOf
	default:
		k, ok := pxPer[unit]
		if !ok {
			return 0, false
		}
		px = v * k
	}
	if math.IsNaN(px) || math.IsInf(px, 0) {
		return 0, false
	}
	return px, true
}

// parseOpacity reads an opacity, a number or a percentage, clamped to 0..1.
func parseOpacity(s string) (float64, bool) {
	s = strings.Trim(s, xmlSpace)
	pct := strings.HasSuffix(s, "%")
	v, ok := parseNumber(strings.TrimSuffix(s, "%"))
	if !ok {
		return 0, false
	}
	if pct {
		v /= 100
	}
	return clamp01(v), true
}

// maxQuoted bounds how much of a value an error message quotes: the value is
// the document's, of any length (X2).
const maxQuoted = 64

// quoted is s in quotes for an error message, cut to maxQuoted bytes.
func quoted(s string) string {
	if len(s) <= maxQuoted {
		return strconv.Quote(s)
	}
	n := maxQuoted
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strconv.Quote(s[:n]) + fmt.Sprintf("… (%d bytes)", len(s))
}

// prop is a presentation property: an attribute of that name, or a
// declaration in a style attribute.
type prop uint8

const (
	propNone prop = iota
	propFill
	propStroke
	propStrokeWidth
	propMiterLimit
	propLineCap
	propLineJoin
	propDashArray
	propDashOffset
	propOpacity
	propFillOpacity
	propStrokeOpacity
	propFillRule
	propClipPath
	propDisplay
	propColor
	propFontFamily
	propFontSize
	propFontWeight
	propFontStyle
	propFontVariant
	propTextAnchor
	propBlend
	propImageRendering
)

// props names the presentation properties the renderer applies. transform is
// an attribute, not a property (see element).
var props = map[string]prop{
	"fill": propFill, "stroke": propStroke, "stroke-width": propStrokeWidth,
	"stroke-miterlimit": propMiterLimit, "stroke-linecap": propLineCap, "stroke-linejoin": propLineJoin,
	"stroke-dasharray": propDashArray, "stroke-dashoffset": propDashOffset,
	"opacity": propOpacity, "fill-opacity": propFillOpacity, "stroke-opacity": propStrokeOpacity,
	"fill-rule": propFillRule, "clip-path": propClipPath, "display": propDisplay, "color": propColor,
	"font-family": propFontFamily, "font-size": propFontSize, "font-weight": propFontWeight,
	"font-style": propFontStyle, "font-variant": propFontVariant, "text-anchor": propTextAnchor,
	"mix-blend-mode": propBlend, "image-rendering": propImageRendering,
}

// styleOnly are the properties only a style declaration sets: as attributes
// they are not part of SVG 1.1, and Vega writes them in style.
var styleOnly = map[prop]bool{propBlend: true, propImageRendering: true}

// decl is one presentation property of an element and its value.
type decl struct {
	p     prop
	value string
}

// declarations are an element's presentation properties in the order they
// apply: its attributes, then its style's declarations, which win over them
// (CSS precedence); within each, a later one over an earlier. A style
// property the renderer does not know is an error, as an unknown attribute
// is (checkAttrs); a malformed declaration is ignored, as CSS ignores one.
//
// It is also where an element's attributes are checked: an attribute that is
// neither a presentation property nor one the element has (checkAttr) is
// an error, so that each attribute is looked up once.
func declarations(e *element, buf []decl) ([]decl, error) {
	out := buf[:0]
	geo := geometryAttrs[e.name]
	for _, a := range e.attrs {
		if p := props[a.name]; p != propNone && !styleOnly[p] {
			out = append(out, decl{p, a.value})
		} else if err := checkAttr(e, geo, a.name); err != nil {
			return nil, err
		}
	}
	style, ok := e.attr("style")
	if !ok {
		return out, nil
	}
	for d := range strings.SplitSeq(style, ";") {
		name, value, ok := strings.Cut(d, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if !ok {
			continue // empty, or no colon: a malformed declaration
		}
		p := props[name]
		if p == propNone {
			return nil, fmt.Errorf("svgpdf: unsupported style property %s on <%s>", quoted(name), e.name)
		}
		value = strings.TrimSpace(value)
		if v, ok := strings.CutSuffix(value, "!important"); ok {
			value = strings.TrimSpace(v)
		}
		out = append(out, decl{p, value})
	}
	return out, nil
}

// pdfBlendModes are CSS's mix-blend-mode values as PDF's /BM names. Normal is
// drawn as it is, with no group.
var pdfBlendModes = map[string]string{
	"multiply": "Multiply", "screen": "Screen", "overlay": "Overlay", "darken": "Darken",
	"lighten": "Lighten", "color-dodge": "ColorDodge", "color-burn": "ColorBurn",
	"hard-light": "HardLight", "soft-light": "SoftLight", "difference": "Difference",
	"exclusion": "Exclusion", "hue": "Hue", "saturation": "Saturation", "color": "Color",
	"luminosity": "Luminosity",
}

// fontSizeKeywords are CSS's absolute font-size keywords, in px.
var fontSizeKeywords = map[string]float64{
	"xx-small": 9, "x-small": 10, "small": 13, "medium": 16, "large": 18, "x-large": 24, "xx-large": 32,
}

// parseFontWeight reads a CSS font-weight given the inherited one: a keyword,
// a relative keyword, or a number from 1 to 1000.
func parseFontWeight(s string, inherited int) (int, bool) {
	switch s = strings.TrimSpace(s); s {
	case "normal":
		return 400, true
	case "bold":
		return 700, true
	case "bolder": // CSS Fonts 4, 2.2.1
		switch {
		case inherited < 350:
			return 400, true
		case inherited < 550:
			return 700, true
		}
		return max(inherited, 900), true
	case "lighter":
		switch {
		case inherited < 550:
			return min(inherited, 100), true
		case inherited < 750:
			return 400, true
		}
		return 700, true
	}
	v, ok := parseNumber(s)
	if !ok || v < 1 || v > 1000 {
		return 0, false
	}
	return int(v), true
}

// parseFontFamilies reads a CSS font-family list: names, quoted or not,
// separated by commas. ok is false for a list with no name.
func parseFontFamilies(s string) ([]string, bool) {
	var out []string
	for len(s) > 0 {
		s = strings.TrimLeft(s, " \t\n\r\f,")
		if s == "" {
			break
		}
		var name string
		if q := s[0]; q == '"' || q == '\'' {
			end := strings.IndexByte(s[1:], q)
			if end < 0 {
				name, s = s[1:], ""
			} else {
				name, s = s[1:1+end], s[end+2:]
			}
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				name, s = s, ""
			} else {
				name, s = s[:end], s[end+1:]
			}
			name = strings.TrimSpace(name)
			if strings.Contains(name, "  ") || strings.ContainsAny(name, "\t\n\r\f") {
				name = strings.Join(strings.Fields(name), " ")
			}
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out, len(out) > 0
}

// parseDashArray reads stroke-dasharray: none, or lengths of zero or more.
// A pattern of zeros is no pattern (SVG draws it solid).
func parseDashArray(s string, c lengthCtx) ([]float64, bool) {
	s = strings.TrimSpace(s)
	if s == "none" {
		return nil, true
	}
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r < 0x80 && isSpace(byte(r)) })
	if len(fields) == 0 || len(fields) > maxDashes {
		return nil, false
	}
	out := make([]float64, 0, len(fields))
	sum := 0.0
	for _, f := range fields {
		v, ok := parseLength(f, c)
		if !ok || v < 0 {
			return nil, false
		}
		out = append(out, v)
		sum += v
	}
	if !(sum > 0) || math.IsInf(sum, 0) {
		return nil, true
	}
	return out, true
}

// maxDashes bounds a dash pattern: a longer one is not a pattern anyone
// draws, and each element is written per stroke.
const maxDashes = 256

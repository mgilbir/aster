// Package color ports d3-color: CSS colour parsing and the rgb, hsl, lab, hcl
// and cubehelix colour spaces, with the exact arithmetic and string forms
// d3 produces (so that interpolated colours print identically).
//
// Colours are small value types. Fields are float64 and may be NaN: d3 uses
// NaN for "channel undefined" (for example the hue of a grey) and several
// rules below only make sense with that in mind.
//
// Products in the colour maths are wrapped in float64() so that the Go
// compiler cannot fuse them into multiply-adds; JavaScript rounds every
// intermediate, and the results must be bit-identical.
package color

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// Color is any d3 colour. The methods mirror d3-color's prototype.
type Color interface {
	// RGB converts to the rgb space.
	RGB() RGB
	// Displayable reports whether the colour is within the sRGB gamut.
	Displayable() bool
	// FormatHex is "#rrggbb"; channels are rounded and clamped.
	FormatHex() string
	// FormatHex8 is "#rrggbbaa".
	FormatHex8() string
	// FormatRgb is "rgb(r, g, b)" or, if opacity is not 1, "rgba(r, g, b, a)".
	FormatRgb() string
	// FormatHsl is "hsl(h, s%, l%)" or "hsla(h, s%, l%, a)".
	FormatHsl() string
	// String is d3's toString, which is FormatRgb for every colour space.
	String() string
	// Brighter multiplies (or, in lab/hcl, shifts) brightness by k. d3's
	// default when k is omitted is numerically k == 1.
	Brighter(k float64) Color
	// Darker is the inverse of Brighter.
	Darker(k float64) Color
	// Clamp forces channels into gamut. Upstream only defines it for rgb and
	// hsl; other spaces return themselves.
	Clamp() Color
}

// The variables are not constants on purpose: Go evaluates constant
// expressions exactly, JavaScript rounds each operation.
var (
	darker   = 0.7
	brighter = 1 / darker
	pi       = math.Pi
	degrees  = 180 / pi
	radians  = pi / 180
)

var nan = math.NaN()

// jsRound is JavaScript's Math.round: round half towards +Infinity.
func jsRound(x float64) float64 {
	r := math.Floor(x)
	if x-r >= 0.5 {
		return r + 1
	}
	return r
}

func clampi(v float64) float64 {
	r := jsRound(v)
	if r != r { // `|| 0`
		return 0
	}
	return math.Max(0, math.Min(255, r))
}

func clampa(o float64) float64 {
	if o != o {
		return 1
	}
	return math.Max(0, math.Min(1, o))
}

func clamph(v float64) float64 {
	if v != v {
		v = 0
	}
	v = math.Mod(v, 360)
	if v < 0 {
		return v + 360
	}
	return v
}

func clampt(v float64) float64 {
	if v != v {
		v = 0
	}
	return math.Max(0, math.Min(1, v))
}

func powK(base, k float64) float64 { return jsmath.Pow(base, k) }

const hexDigits = "0123456789abcdef"

func appendHex(dst []byte, v float64) []byte {
	n := int(clampi(v))
	return append(dst, hexDigits[n>>4], hexDigits[n&15])
}

// AppendHex appends "#rrggbb" for c to dst.
func AppendHex(dst []byte, c RGB) []byte {
	dst = append(dst, '#')
	dst = appendHex(dst, c.R)
	dst = appendHex(dst, c.G)
	return appendHex(dst, c.B)
}

// AppendRgb appends d3's rgb()/rgba() string for c to dst.
func AppendRgb(dst []byte, c RGB) []byte {
	a := clampa(c.Opacity)
	if a == 1 {
		dst = append(dst, "rgb("...)
	} else {
		dst = append(dst, "rgba("...)
	}
	dst = jsval.AppendJSNumber(dst, clampi(c.R))
	dst = append(dst, ", "...)
	dst = jsval.AppendJSNumber(dst, clampi(c.G))
	dst = append(dst, ", "...)
	dst = jsval.AppendJSNumber(dst, clampi(c.B))
	if a != 1 {
		dst = append(dst, ", "...)
		dst = jsval.AppendJSNumber(dst, a)
	}
	return append(dst, ')')
}

// ---- RGB ----------------------------------------------------------------

// RGB is the rgb space; R, G, B are nominally 0..255.
type RGB struct{ R, G, B, Opacity float64 }

// RGBOf is d3.rgb(r, g, b) with opacity 1.
func RGBOf(r, g, b float64) RGB { return RGB{r, g, b, 1} }

// NaNRGB is what d3.rgb returns for an unparseable string (`new Rgb` with no
// arguments): every channel, including opacity, is NaN.
func NaNRGB() RGB { return RGB{nan, nan, nan, nan} }

func (c RGB) RGB() RGB { return c }

func (c RGB) Brighter(k float64) Color { return c.scale(powK(brighter, k)) }
func (c RGB) Darker(k float64) Color   { return c.scale(powK(darker, k)) }

func (c RGB) scale(k float64) RGB {
	return RGB{c.R * k, c.G * k, c.B * k, c.Opacity}
}

func (c RGB) Clamp() Color {
	return RGB{clampi(c.R), clampi(c.G), clampi(c.B), clampa(c.Opacity)}
}

func (c RGB) Displayable() bool {
	return -0.5 <= c.R && c.R < 255.5 &&
		-0.5 <= c.G && c.G < 255.5 &&
		-0.5 <= c.B && c.B < 255.5 &&
		0 <= c.Opacity && c.Opacity <= 1
}

func (c RGB) FormatHex() string { return string(AppendHex(make([]byte, 0, 7), c)) }

func (c RGB) FormatHex8() string {
	b := AppendHex(make([]byte, 0, 9), c)
	o := c.Opacity
	if o != o {
		o = 1
	}
	return string(appendHex(b, o*255))
}

func (c RGB) FormatRgb() string { return string(AppendRgb(make([]byte, 0, 24), c)) }
func (c RGB) String() string    { return c.FormatRgb() }
func (c RGB) FormatHsl() string { return ToHSL(c).FormatHsl() }

// ---- HSL ----------------------------------------------------------------

// HSL is the hsl space: H in degrees, S and L in 0..1.
type HSL struct{ H, S, L, Opacity float64 }

// NaNHSL is what d3.hsl returns for an unparseable string.
func NaNHSL() HSL { return HSL{nan, nan, nan, nan} }

func (c HSL) Brighter(k float64) Color {
	return HSL{c.H, c.S, c.L * powK(brighter, k), c.Opacity}
}

func (c HSL) Darker(k float64) Color {
	return HSL{c.H, c.S, c.L * powK(darker, k), c.Opacity}
}

func (c HSL) RGB() RGB {
	h := math.Mod(c.H, 360)
	if c.H < 0 {
		h += 360
	}
	s := c.S
	if h != h || s != s {
		s = 0
	}
	l := c.L
	var t float64
	if l < 0.5 {
		t = l
	} else {
		t = 1 - l
	}
	m2 := l + float64(t*s)
	m1 := float64(2*l) - m2
	var hr, hb float64
	if h >= 240 {
		hr = h - 240
	} else {
		hr = h + 120
	}
	if h < 120 {
		hb = h + 240
	} else {
		hb = h - 120
	}
	return RGB{hsl2rgb(hr, m1, m2), hsl2rgb(h, m1, m2), hsl2rgb(hb, m1, m2), c.Opacity}
}

// hsl2rgb is FvD 13.37 / CSS Color Module Level 3.
func hsl2rgb(h, m1, m2 float64) float64 {
	var v float64
	switch {
	case h < 60:
		v = m1 + float64((m2-m1)*h)/60
	case h < 180:
		v = m2
	case h < 240:
		v = m1 + float64((m2-m1)*(240-h))/60
	default:
		v = m1
	}
	return v * 255
}

func (c HSL) Clamp() Color {
	return HSL{clamph(c.H), clampt(c.S), clampt(c.L), clampa(c.Opacity)}
}

func (c HSL) Displayable() bool {
	return (0 <= c.S && c.S <= 1 || c.S != c.S) &&
		0 <= c.L && c.L <= 1 &&
		0 <= c.Opacity && c.Opacity <= 1
}

func (c HSL) FormatHex() string  { return c.RGB().FormatHex() }
func (c HSL) FormatHex8() string { return c.RGB().FormatHex8() }
func (c HSL) FormatRgb() string  { return c.RGB().FormatRgb() }
func (c HSL) String() string     { return c.FormatRgb() }

func (c HSL) FormatHsl() string {
	a := clampa(c.Opacity)
	b := make([]byte, 0, 32)
	if a == 1 {
		b = append(b, "hsl("...)
	} else {
		b = append(b, "hsla("...)
	}
	b = jsval.AppendJSNumber(b, clamph(c.H))
	b = append(b, ", "...)
	b = jsval.AppendJSNumber(b, clampt(c.S)*100)
	b = append(b, "%, "...)
	b = jsval.AppendJSNumber(b, clampt(c.L)*100)
	b = append(b, '%')
	if a != 1 {
		b = append(b, ", "...)
		b = jsval.AppendJSNumber(b, a)
	}
	return string(append(b, ')'))
}

// hslFromRGB is hslConvert for a colour already reduced to rgb.
func hslFromRGB(o RGB) HSL {
	r, g, b := o.R/255, o.G/255, o.B/255
	mn := math.Min(r, math.Min(g, b))
	mx := math.Max(r, math.Max(g, b))
	h := nan
	s := mx - mn
	l := (mx + mn) / 2
	if s != 0 && s == s { // NaN is falsy in `if (s)`
		switch {
		case r == mx:
			h = (g - b) / s
			if g < b {
				h += 6
			}
		case g == mx:
			h = (b-r)/s + 2
		default:
			h = (r-g)/s + 4
		}
		if l < 0.5 {
			s /= mx + mn
		} else {
			s /= 2 - mx - mn
		}
		h *= 60
	} else if l > 0 && l < 1 {
		s = 0
	} else {
		s = h
	}
	return HSL{h, s, l, o.Opacity}
}

// ToHSL is d3.hsl(color): an HSL colour is returned as is, anything else goes
// through rgb.
func ToHSL(c Color) HSL {
	if h, ok := c.(HSL); ok {
		return h
	}
	return hslFromRGB(c.RGB())
}

// ---- construction -------------------------------------------------------

func hsla(h, s, l, a float64) HSL {
	switch {
	case a <= 0:
		h, s, l = nan, nan, nan
	case l <= 0 || l >= 1:
		h, s = nan, nan
	case s <= 0:
		h = nan
	}
	return HSL{h, s, l, a}
}

func rgba(r, g, b, a float64) RGB {
	if a <= 0 {
		r, g, b = nan, nan, nan
	}
	return RGB{r, g, b, a}
}

func rgbn(n uint32) RGB {
	return RGB{float64(n >> 16 & 0xff), float64(n >> 8 & 0xff), float64(n & 0xff), 1}
}

// Named returns the CSS keyword colour for an already lower-cased name.
func Named(name string) (RGB, bool) {
	n, ok := named[name]
	if !ok {
		return RGB{}, false
	}
	return rgbn(n), true
}

// ConvertRGB is d3.rgb(color).
func ConvertRGB(c Color) RGB { return c.RGB() }

// ParseRGB is d3.rgb(string): unparseable input yields NaNRGB.
func ParseRGB(s string) RGB {
	p, ok := parse(s)
	if !ok {
		return NaNRGB()
	}
	return p.color().RGB()
}

// ParseHSL is d3.hsl(string).
func ParseHSL(s string) HSL {
	p, ok := parse(s)
	if !ok {
		return NaNHSL()
	}
	return ToHSL(p.color())
}

// Parse is d3.color(string). The result is an HSL for hsl()/hsla() input and
// an RGB otherwise.
func Parse(s string) (Color, bool) {
	p, ok := parse(s)
	if !ok {
		return nil, false
	}
	return p.color(), true
}

type parsed struct {
	isHSL bool
	v     [4]float64
}

func (p parsed) color() Color {
	if p.isHSL {
		return HSL{p.v[0], p.v[1], p.v[2], p.v[3]}
	}
	return RGB{p.v[0], p.v[1], p.v[2], p.v[3]}
}

func rgbParsed(c RGB) (parsed, bool) { return parsed{v: [4]float64{c.R, c.G, c.B, c.Opacity}}, true }

// parse implements d3.color's string grammar without regexps. It accepts the
// same language as d3's regular expressions (including the odd corners:
// "5." and "1e" are not numbers, hsl() takes exactly three arguments, a
// percent sign is compulsory in rgb-percent and hsl saturation/lightness).
func parse(s string) (parsed, bool) {
	s = trimJS(s)
	s = lower(s)
	if len(s) > 0 && s[0] == '#' {
		return parseHex(s[1:])
	}
	var args [4]float64
	switch {
	case hasPrefix(s, "rgb("):
		if scanArgs(s[4:], "iii", &args) {
			return rgbParsed(RGB{args[0], args[1], args[2], 1})
		}
		if scanArgs(s[4:], "ppp", &args) {
			return rgbParsed(RGB{args[0] * 255 / 100, args[1] * 255 / 100, args[2] * 255 / 100, 1})
		}
	case hasPrefix(s, "rgba("):
		if scanArgs(s[5:], "iiin", &args) {
			return rgbParsed(rgba(args[0], args[1], args[2], args[3]))
		}
		if scanArgs(s[5:], "pppn", &args) {
			return rgbParsed(rgba(args[0]*255/100, args[1]*255/100, args[2]*255/100, args[3]))
		}
	case hasPrefix(s, "hsl("):
		if scanArgs(s[4:], "npp", &args) {
			h := hsla(args[0], args[1]/100, args[2]/100, 1)
			return parsed{true, [4]float64{h.H, h.S, h.L, h.Opacity}}, true
		}
	case hasPrefix(s, "hsla("):
		if scanArgs(s[5:], "nppn", &args) {
			h := hsla(args[0], args[1]/100, args[2]/100, args[3])
			return parsed{true, [4]float64{h.H, h.S, h.L, h.Opacity}}, true
		}
	}
	if c, ok := Named(s); ok {
		return rgbParsed(c)
	}
	if s == "transparent" {
		return rgbParsed(RGB{nan, nan, nan, 0})
	}
	return parsed{}, false
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func hexVal(c byte) (uint32, bool) {
	switch {
	case c >= '0' && c <= '9':
		return uint32(c - '0'), true
	case c >= 'a' && c <= 'f':
		return uint32(c-'a') + 10, true
	}
	return 0, false
}

// parseHex handles /^#([0-9a-f]{3,8})$/ then keeps only 3, 4, 6 and 8 digits.
func parseHex(h string) (parsed, bool) {
	n := len(h)
	if n < 3 || n > 8 {
		return parsed{}, false
	}
	var d [8]uint32
	for i := 0; i < n; i++ {
		v, ok := hexVal(h[i])
		if !ok {
			return parsed{}, false
		}
		d[i] = v
	}
	switch n {
	case 6:
		return rgbParsed(rgbn(d[0]<<20 | d[1]<<16 | d[2]<<12 | d[3]<<8 | d[4]<<4 | d[5]))
	case 3:
		return rgbParsed(RGB{float64(d[0] * 17), float64(d[1] * 17), float64(d[2] * 17), 1})
	case 8:
		return rgbParsed(rgba(float64(d[0]<<4|d[1]), float64(d[2]<<4|d[3]), float64(d[4]<<4|d[5]), float64(d[6]<<4|d[7])/255))
	case 4:
		return rgbParsed(rgba(float64(d[0]*17), float64(d[1]*17), float64(d[2]*17), float64(d[3]*17)/255))
	}
	return parsed{}, false
}

// isJSSpace is the set of characters matched by JavaScript's \s and removed
// by String.prototype.trim.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func trimJS(s string) string {
	start := 0
	for start < len(s) {
		r, w := decodeRune(s[start:])
		if !isJSSpace(r) {
			break
		}
		start += w
	}
	end := len(s)
	for end > start {
		r, w := decodeLastRune(s[start:end])
		if !isJSSpace(r) {
			break
		}
		end -= w
	}
	return s[start:end]
}

// scanArgs matches the argument list of one of d3's regexps after the opening
// parenthesis: comma separated items (kind i: integer, n: number, p: number
// followed by %), each surrounded by optional whitespace, then ")" and the
// end of input.
func scanArgs(s, kinds string, out *[4]float64) bool {
	i := 0
	for k := 0; k < len(kinds); k++ {
		if k > 0 {
			if i >= len(s) || s[i] != ',' {
				return false
			}
			i++
		}
		i = skipSpace(s, i)
		start := i
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		ds := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if kinds[k] == 'i' {
			if i == ds {
				return false
			}
		} else {
			if i < len(s) && s[i] == '.' {
				i++
				fs := i
				for i < len(s) && isDigit(s[i]) {
					i++
				}
				if i == fs {
					return false // "5." backtracks to "5" and then fails on "."
				}
			} else if i == ds {
				return false
			}
			if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
				j := i + 1
				if j < len(s) && (s[j] == '+' || s[j] == '-') {
					j++
				}
				es := j
				for j < len(s) && isDigit(s[j]) {
					j++
				}
				if j == es {
					return false
				}
				i = j
			}
		}
		out[k] = parseNum(s[start:i])
		if kinds[k] == 'p' {
			if i >= len(s) || s[i] != '%' {
				return false
			}
			i++
		}
		i = skipSpace(s, i)
	}
	return i+1 == len(s) && s[i] == ')'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func skipSpace(s string, i int) int {
	for i < len(s) {
		if c := s[i]; c < 0x80 {
			if !isJSSpace(rune(c)) {
				return i
			}
			i++
			continue
		}
		r, w := decodeRune(s[i:])
		if !isJSSpace(r) {
			return i
		}
		i += w
	}
	return i
}

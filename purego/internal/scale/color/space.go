package color

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

// CIE Lab constants (https://observablehq.com/@mbostock/lab-and-rgb). Vars,
// not consts, so each product is rounded to a double like JavaScript does.
var (
	labK  = 18.0
	labXn = 0.96422
	labYn = 1.0
	labZn = 0.82521
	labT0 = 4.0 / 29
	labT1 = 6.0 / 29
	labT2 = 3 * labT1 * labT1
	labT3 = labT1 * labT1 * labT1
)

// ---- Lab ----------------------------------------------------------------

// Lab is CIE L*a*b* (D50).
type Lab struct{ L, A, B, Opacity float64 }

// Gray is d3.gray(l): a Lab colour with a = b = 0.
func Gray(l float64) Lab { return Lab{l, 0, 0, 1} }

func (c Lab) Brighter(k float64) Color { return Lab{c.L + float64(labK*k), c.A, c.B, c.Opacity} }
func (c Lab) Darker(k float64) Color   { return Lab{c.L - float64(labK*k), c.A, c.B, c.Opacity} }
func (c Lab) Clamp() Color             { return c }
func (c Lab) Displayable() bool        { return c.RGB().Displayable() }
func (c Lab) FormatHex() string        { return c.RGB().FormatHex() }
func (c Lab) FormatHex8() string       { return c.RGB().FormatHex8() }
func (c Lab) FormatRgb() string        { return c.RGB().FormatRgb() }
func (c Lab) FormatHsl() string        { return ToHSL(c).FormatHsl() }
func (c Lab) String() string           { return c.FormatRgb() }

func (c Lab) RGB() RGB {
	y := (c.L + 16) / 116
	x, z := y, y
	if c.A == c.A {
		x = y + c.A/500
	}
	if c.B == c.B {
		z = y - c.B/200
	}
	x = labXn * lab2xyz(x)
	y = labYn * lab2xyz(y)
	z = labZn * lab2xyz(z)
	return RGB{
		lrgb2rgb(float64(3.1338561*x) - float64(1.6168667*y) - float64(0.4906146*z)),
		lrgb2rgb(float64(-0.9787684*x) + float64(1.9161415*y) + float64(0.0334540*z)),
		lrgb2rgb(float64(0.0719453*x) - float64(0.2289914*y) + float64(1.4052427*z)),
		c.Opacity,
	}
}

func xyz2lab(t float64) float64 {
	if t > labT3 {
		return jsmath.Pow(t, 1.0/3)
	}
	return t/labT2 + labT0
}

func lab2xyz(t float64) float64 {
	if t > labT1 {
		return t * t * t
	}
	return labT2 * (t - labT0)
}

func lrgb2rgb(x float64) float64 {
	if x <= 0.0031308 {
		return 255 * (12.92 * x)
	}
	return 255 * (float64(1.055*jsmath.Pow(x, 1/2.4)) - 0.055)
}

func rgb2lrgb(x float64) float64 {
	x /= 255
	if x <= 0.04045 {
		return x / 12.92
	}
	return jsmath.Pow((x+0.055)/1.055, 2.4)
}

// ToLab is d3.lab(color).
func ToLab(c Color) Lab {
	switch v := c.(type) {
	case Lab:
		return v
	case HCL:
		return v.lab()
	}
	return labFromRGB(c.RGB())
}

func labFromRGB(o RGB) Lab {
	r, g, b := rgb2lrgb(o.R), rgb2lrgb(o.G), rgb2lrgb(o.B)
	y := xyz2lab((float64(0.2225045*r) + float64(0.7168786*g) + float64(0.0606169*b)) / labYn)
	var x, z float64
	if r == g && g == b {
		x, z = y, y
	} else {
		x = xyz2lab((float64(0.4360747*r) + float64(0.3850649*g) + float64(0.1430804*b)) / labXn)
		z = xyz2lab((float64(0.0139322*r) + float64(0.0971045*g) + float64(0.7141733*b)) / labZn)
	}
	return Lab{float64(116*y) - 16, float64(500 * (x - y)), float64(200 * (y - z)), o.Opacity}
}

// ParseLab is d3.lab(string); unparseable input gives all-NaN.
func ParseLab(s string) Lab { return labFromRGB(ParseRGB(s)) }

// ---- HCL ----------------------------------------------------------------

// HCL is the cylindrical form of Lab (also known as LCh).
type HCL struct{ H, C, L, Opacity float64 }

// Lch is d3.lch(l, c, h, opacity).
func Lch(l, c, h, opacity float64) HCL { return HCL{h, c, l, opacity} }

func (c HCL) Brighter(k float64) Color { return HCL{c.H, c.C, c.L + float64(labK*k), c.Opacity} }
func (c HCL) Darker(k float64) Color   { return HCL{c.H, c.C, c.L - float64(labK*k), c.Opacity} }
func (c HCL) Clamp() Color             { return c }
func (c HCL) RGB() RGB                 { return c.lab().RGB() }
func (c HCL) Displayable() bool        { return c.RGB().Displayable() }
func (c HCL) FormatHex() string        { return c.RGB().FormatHex() }
func (c HCL) FormatHex8() string       { return c.RGB().FormatHex8() }
func (c HCL) FormatRgb() string        { return c.RGB().FormatRgb() }
func (c HCL) FormatHsl() string        { return ToHSL(c).FormatHsl() }
func (c HCL) String() string           { return c.FormatRgb() }

// lab is hcl2lab: an undefined hue means an achromatic colour.
func (c HCL) lab() Lab {
	if c.H != c.H {
		return Lab{c.L, 0, 0, c.Opacity}
	}
	h := c.H * radians
	return Lab{c.L, jsmath.Cos(h) * c.C, jsmath.Sin(h) * c.C, c.Opacity}
}

// ToHCL is d3.hcl(color).
func ToHCL(c Color) HCL {
	if v, ok := c.(HCL); ok {
		return v
	}
	return hclFromLab(ToLab(c))
}

func hclFromLab(o Lab) HCL {
	if o.A == 0 && o.B == 0 {
		ch := nan
		if 0 < o.L && o.L < 100 {
			ch = 0
		}
		return HCL{nan, ch, o.L, o.Opacity}
	}
	h := float64(jsmath.Atan2(o.B, o.A) * degrees)
	if h < 0 {
		h += 360
	}
	return HCL{h, math.Sqrt(float64(o.A*o.A) + float64(o.B*o.B)), o.L, o.Opacity}
}

// ParseHCL is d3.hcl(string).
func ParseHCL(s string) HCL { return hclFromLab(ParseLab(s)) }

// ---- Cubehelix ----------------------------------------------------------

var (
	chA    = -0.14861
	chB    = +1.78277
	chC    = -0.29227
	chD    = -0.90649
	chE    = +1.97294
	chED   = chE * chD
	chEB   = chE * chB
	chBCDA = float64(chB*chC) - float64(chD*chA)
)

// Cubehelix is Green's cubehelix colour scheme space.
type Cubehelix struct{ H, S, L, Opacity float64 }

func (c Cubehelix) Brighter(k float64) Color {
	return Cubehelix{c.H, c.S, c.L * powK(brighter, k), c.Opacity}
}

func (c Cubehelix) Darker(k float64) Color {
	return Cubehelix{c.H, c.S, c.L * powK(darker, k), c.Opacity}
}

func (c Cubehelix) Clamp() Color       { return c }
func (c Cubehelix) Displayable() bool  { return c.RGB().Displayable() }
func (c Cubehelix) FormatHex() string  { return c.RGB().FormatHex() }
func (c Cubehelix) FormatHex8() string { return c.RGB().FormatHex8() }
func (c Cubehelix) FormatRgb() string  { return c.RGB().FormatRgb() }
func (c Cubehelix) FormatHsl() string  { return ToHSL(c).FormatHsl() }
func (c Cubehelix) String() string     { return c.FormatRgb() }

func (c Cubehelix) RGB() RGB {
	h := 0.0
	if c.H == c.H {
		h = (c.H + 120) * radians
	}
	l := c.L
	a := 0.0
	if c.S == c.S {
		a = float64(c.S*l) * (1 - l)
	}
	cosh, sinh := jsmath.Cos(h), jsmath.Sin(h)
	return RGB{
		255 * (l + float64(a*(float64(chA*cosh)+float64(chB*sinh)))),
		255 * (l + float64(a*(float64(chC*cosh)+float64(chD*sinh)))),
		255 * (l + float64(a*float64(chE*cosh))),
		c.Opacity,
	}
}

// ToCubehelix is d3.cubehelix(color).
func ToCubehelix(c Color) Cubehelix {
	if v, ok := c.(Cubehelix); ok {
		return v
	}
	return cubehelixFromRGB(c.RGB())
}

func cubehelixFromRGB(o RGB) Cubehelix {
	r, g, b := o.R/255, o.G/255, o.B/255
	l := (float64(chBCDA*b) + float64(chED*r) - float64(chEB*g)) / (chBCDA + chED - chEB)
	bl := b - l
	k := (float64(chE*(g-l)) - float64(chC*bl)) / chD
	s := math.Sqrt(float64(k*k)+float64(bl*bl)) / float64(float64(chE*l)*(1-l)) // NaN if l=0 or l=1
	h := nan
	if s != 0 && s == s {
		h = float64(jsmath.Atan2(k, bl)*degrees) - 120
	}
	if h < 0 {
		h += 360
	}
	return Cubehelix{h, s, l, o.Opacity}
}

// ParseCubehelix is d3.cubehelix(string).
func ParseCubehelix(s string) Cubehelix { return cubehelixFromRGB(ParseRGB(s)) }

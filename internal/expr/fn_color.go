package expr

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale/color"
)

// colorCtor implements rgb, hsl, lab and hcl. With one argument the value is
// converted to the space (a colour object stays a colour, anything else is
// parsed as a CSS colour string and an unparseable value gives an all-NaN
// colour); with several they are channels, and opacity defaults to 1 when null
// or undefined.
func colorCtor(
	fromColor func(color.Color) color.Color,
	fromString func(string) color.Color,
	build func(a, b, c, opacity float64) color.Color,
) builtinFn {
	return func(s *Scope, args []jsval.Value) jsval.Value {
		if len(args) == 1 {
			if c, ok := asColor(args[0]); ok {
				return colorValue(fromColor(c))
			}
			return colorValue(fromString(s.str(args[0])))
		}
		op := 1.0
		if o := arg(args, 3); !o.IsNullish() {
			op = s.num(o)
		}
		return colorValue(build(s.num(arg(args, 0)), s.num(arg(args, 1)), s.num(arg(args, 2)), op))
	}
}

// toColor converts a value to a colour for luminance() and contrast().
func (s *Scope) toColor(v jsval.Value) color.RGB {
	if c, ok := asColor(v); ok {
		return c.RGB()
	}
	return color.ParseRGB(s.str(v))
}

// relLuminance is the WCAG relative luminance
// (https://www.w3.org/TR/2008/REC-WCAG20-20081211/#relativeluminancedef).
func relLuminance(c color.RGB) float64 {
	ch := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return jsmath.Pow((v+0.055)/1.055, 2.4)
	}
	return float64(0.2126*ch(c.R)) + float64(0.7152*ch(c.G)) + float64(0.0722*ch(c.B))
}

func init() {
	fn("rgb", colorCtor(
		func(c color.Color) color.Color { return color.ConvertRGB(c) },
		func(str string) color.Color { return color.ParseRGB(str) },
		func(a, b, c, o float64) color.Color { return color.RGB{R: a, G: b, B: c, Opacity: o} }))
	fn("hsl", colorCtor(
		func(c color.Color) color.Color { return color.ToHSL(c) },
		func(str string) color.Color { return color.ParseHSL(str) },
		func(a, b, c, o float64) color.Color { return color.HSL{H: a, S: b, L: c, Opacity: o} }))
	fn("lab", colorCtor(
		func(c color.Color) color.Color { return color.ToLab(c) },
		func(str string) color.Color { return color.ParseLab(str) },
		func(a, b, c, o float64) color.Color { return color.Lab{L: a, A: b, B: c, Opacity: o} }))
	fn("hcl", colorCtor(
		func(c color.Color) color.Color { return color.ToHCL(c) },
		func(str string) color.Color { return color.ParseHCL(str) },
		// d3.hcl(h, c, l, opacity)
		func(a, b, c, o float64) color.Color { return color.HCL{H: a, C: b, L: c, Opacity: o} }))
	fn("luminance", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(relLuminance(s.toColor(arg(args, 0))))
	})
	fn("contrast", func(s *Scope, args []jsval.Value) jsval.Value {
		l1, l2 := relLuminance(s.toColor(arg(args, 0))), relLuminance(s.toColor(arg(args, 1)))
		return jsval.Num((math.Max(l1, l2) + 0.05) / (math.Min(l1, l2) + 0.05))
	})
}

package color

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// Replay of d3-color's own tests (see internal/upstream). The engine's colour types have no
// JavaScript-style polymorphic constructors, so the adapter below spells out d3's: `rgb(x)` converts
// a colour or parses a string, `rgb(r, g, b, opacity)` coerces its channels with `+`.

// opacityOf is d3's `opacity == null ? 1 : +opacity`.
func opacityOf(v any) float64 {
	if v == nil || upstream.IsUndefined(v) {
		return 1
	}
	return upstream.Number(v)
}

// kOf is d3's `k == null ? 1 : k` for brighter and darker.
func kOf(v any) float64 { return opacityOf(v) }

// colorOf reads a colour argument: an instance recorded with its class, or anything d3.color parses
// after `+ ""`. ok is false where d3.color returns null.
func colorOf(v any) (Color, bool) {
	if m, isMap := v.(map[string]any); isMap {
		if class, has := m["$class"].(string); has {
			return decodeColor(class, m), true
		}
	}
	return Parse(upstream.String(v))
}

func decodeColor(class string, m map[string]any) Color {
	f := func(k string) float64 { return upstream.Number(m[k]) }
	switch class {
	case "Rgb":
		return RGB{f("r"), f("g"), f("b"), f("opacity")}
	case "Hsl":
		return HSL{f("h"), f("s"), f("l"), f("opacity")}
	case "Lab":
		return Lab{f("l"), f("a"), f("b"), f("opacity")}
	case "Hcl":
		return HCL{f("h"), f("c"), f("l"), f("opacity")}
	case "Cubehelix":
		return Cubehelix{f("h"), f("s"), f("l"), f("opacity")}
	}
	return nil
}

func encodeColor(c Color) any {
	e := upstream.Enc
	switch x := c.(type) {
	case RGB:
		return map[string]any{"$class": "Rgb", "r": e(x.R), "g": e(x.G), "b": e(x.B), "opacity": e(x.Opacity)}
	case HSL:
		return map[string]any{"$class": "Hsl", "h": e(x.H), "s": e(x.S), "l": e(x.L), "opacity": e(x.Opacity)}
	case Lab:
		return map[string]any{"$class": "Lab", "l": e(x.L), "a": e(x.A), "b": e(x.B), "opacity": e(x.Opacity)}
	case HCL:
		return map[string]any{"$class": "Hcl", "h": e(x.H), "c": e(x.C), "l": e(x.L), "opacity": e(x.Opacity)}
	case Cubehelix:
		return map[string]any{"$class": "Cubehelix", "h": e(x.H), "s": e(x.S), "l": e(x.L), "opacity": e(x.Opacity)}
	}
	return nil
}

// construct is d3's rgb, hsl, lab, hcl, lch, gray and cubehelix as called with args.
func construct(fn string, args []any) (Color, bool) {
	at := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return upstream.Undefined()
	}
	convert := func() Color {
		c, ok := colorOf(args[0])
		if !ok || c == nil {
			return NaNRGB()
		}
		return c
	}
	switch fn {
	case "rgb":
		if len(args) == 1 {
			return ConvertRGB(convert()), true
		}
		return RGB{upstream.Number(at(0)), upstream.Number(at(1)), upstream.Number(at(2)), opacityOf(at(3))}, true
	case "hsl":
		if len(args) == 1 {
			return ToHSL(convert()), true
		}
		return HSL{upstream.Number(at(0)), upstream.Number(at(1)), upstream.Number(at(2)), opacityOf(at(3))}, true
	case "lab":
		if len(args) == 1 {
			return ToLab(convert()), true
		}
		return Lab{upstream.Number(at(0)), upstream.Number(at(1)), upstream.Number(at(2)), opacityOf(at(3))}, true
	case "hcl":
		if len(args) == 1 {
			return ToHCL(convert()), true
		}
		return HCL{upstream.Number(at(0)), upstream.Number(at(1)), upstream.Number(at(2)), opacityOf(at(3))}, true
	case "lch":
		if len(args) == 1 {
			return ToHCL(convert()), true
		}
		return Lch(upstream.Number(at(0)), upstream.Number(at(1)), upstream.Number(at(2)), opacityOf(at(3))), true
	case "cubehelix":
		if len(args) == 1 {
			return ToCubehelix(convert()), true
		}
		return Cubehelix{upstream.Number(at(0)), upstream.Number(at(1)), upstream.Number(at(2)), opacityOf(at(3))}, true
	case "gray":
		return Lab{upstream.Number(at(0)), 0, 0, opacityOf(at(1))}, true
	case "color":
		return Parse(upstream.String(at(0)))
	}
	return nil, false
}

func TestUpstreamD3Color(t *testing.T) {
	r := upstream.Start(t, "d3-color")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		fn, isMethod := strings.CutSuffix(c.Fn, "()")
		if !isMethod {
			if unreplayable(c.Args) {
				r.Skip("a colour built from a test's own object")
				continue
			}
			col, ok := construct(fn, c.Args)
			if !ok {
				if fn == "color" {
					r.Check(c, nil, false)
				} else {
					r.Skip("unmapped " + fn)
				}
				continue
			}
			r.Check(c, encodeColor(col), false)
			continue
		}
		if unreplayable(c.ConstructedWith) {
			r.Skip("a colour built from a test's own object")
			continue
		}
		col, ok := construct(fn, c.ConstructedWith)
		if !ok {
			// color(spec).method(): upstream throws for a null colour.
			if fn == "color" {
				r.Check(c, nil, true)
				continue
			}
			r.Skip("unmapped " + fn)
			continue
		}
		for _, step := range c.ChainSteps() {
			field, isSet := strings.CutPrefix(step.Method, "set:")
			if !isSet {
				t.Fatalf("unmapped chain step %q", step.Method)
			}
			col = setField(col, field, upstream.Number(step.Args[0]))
		}
		supported := true
		for _, step := range c.ViaSteps() {
			next, isColor, ok := colorMethod(col, step.Method, step.Args)
			if !ok || !isColor {
				supported = false
				break
			}
			col = next.(Color)
		}
		if !supported {
			r.Skip("unmapped step")
			continue
		}
		got, isColor, ok := colorMethod(col, c.Method, c.Args)
		if !ok {
			r.Skip("unmapped method " + c.Method)
			continue
		}
		if isColor {
			got = encodeColor(got.(Color))
		}
		r.Check(c, got, false)
	}
	r.Done(450)
}

// unreplayable reports whether an argument is an instance of d3's base Color class, which a test
// builds from its own object with its own `rgb` method.
func unreplayable(args []any) bool {
	for _, a := range args {
		if m, ok := a.(map[string]any); ok && m["$class"] == "Color" {
			return true
		}
	}
	return false
}

// colorMethod calls a method of a colour. The answer is a Color when isColor is true.
func colorMethod(col Color, name string, args []any) (result any, isColor, ok bool) {
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return upstream.Undefined()
	}
	switch name {
	case "brighter":
		return col.Brighter(kOf(arg(0))), true, true
	case "darker":
		return col.Darker(kOf(arg(0))), true, true
	case "clamp":
		return col.Clamp(), true, true
	case "copy":
		if len(args) == 0 {
			return col, true, true
		}
		values, isMap := args[0].(map[string]any)
		if !isMap {
			return nil, false, false
		}
		for k, v := range values {
			col = setField(col, k, upstream.Number(v))
		}
		return col, true, true
	case "rgb":
		return col.RGB(), true, true
	case "displayable":
		return col.Displayable(), false, true
	case "formatHex", "hex":
		return col.FormatHex(), false, true
	case "formatHex8":
		return col.FormatHex8(), false, true
	case "formatRgb":
		return col.FormatRgb(), false, true
	case "formatHsl":
		return col.FormatHsl(), false, true
	case "toString":
		return col.String(), false, true
	}
	return nil, false, false
}

// setField is `color[field] = v`.
func setField(c Color, field string, v float64) Color {
	switch x := c.(type) {
	case RGB:
		switch field {
		case "r":
			x.R = v
		case "g":
			x.G = v
		case "b":
			x.B = v
		case "opacity":
			x.Opacity = v
		}
		return x
	case HSL:
		switch field {
		case "h":
			x.H = v
		case "s":
			x.S = v
		case "l":
			x.L = v
		case "opacity":
			x.Opacity = v
		}
		return x
	case Lab:
		switch field {
		case "l":
			x.L = v
		case "a":
			x.A = v
		case "b":
			x.B = v
		case "opacity":
			x.Opacity = v
		}
		return x
	case HCL:
		switch field {
		case "h":
			x.H = v
		case "c":
			x.C = v
		case "l":
			x.L = v
		case "opacity":
			x.Opacity = v
		}
		return x
	case Cubehelix:
		switch field {
		case "h":
			x.H = v
		case "s":
			x.S = v
		case "l":
			x.L = v
		case "opacity":
			x.Opacity = v
		}
		return x
	}
	return c
}

package svgpdf

import (
	"strings"

	"github.com/mgilbir/aster/internal/csscolor"
)

// Color is an RGB color with components in [0, 1].
type Color struct {
	R, G, B float64
}

// Paint is a fill or stroke value: a color and its alpha, "none", a
// gradient, or currentColor (resolved against the color property where the
// state is, see gstate.resolve).
type Paint struct {
	Color    Color
	Alpha    float64 // the color's own, multiplied into the fill or stroke opacity
	None     bool
	Gradient string // the id of the gradient a url(#id) paint refers to
	// Fallback is what a gradient paint draws when the gradient is missing:
	// SVG's "url(#id) <fallback>". Nil makes a missing one an error.
	Fallback *Paint
	// Current is currentColor: the color property's value.
	Current bool
}

// alpha is what the paint multiplies the opacity by: its colour's alpha, or 1
// for none, which paints nothing whatever the opacity.
func (p Paint) alpha() float64 {
	if p.None {
		return 1
	}
	return p.Alpha
}

// colorPaint is the paint of a colour of the given alpha: none when nothing
// of it shows.
func colorPaint(c Color, alpha float64) Paint {
	if alpha == 0 {
		return Paint{None: true} // transparent, or any colour with no alpha
	}
	return Paint{Color: c, Alpha: alpha}
}

// parsePaint reads an SVG fill or stroke value; ok is false for one that is
// not a paint, which the caller ignores.
func parsePaint(s string) (Paint, bool) {
	s = strings.TrimSpace(s)
	switch {
	case csscolor.ASCIIEqualFold(s, "none"):
		return Paint{None: true}, true
	case csscolor.ASCIIEqualFold(s, "currentColor"):
		return Paint{Current: true}, true
	case strings.HasPrefix(s, "url("):
		end := strings.IndexByte(s, ')')
		if end < 0 {
			return Paint{}, false
		}
		ref := strings.Trim(strings.TrimSpace(s[4:end]), `"'`)
		if !strings.HasPrefix(ref, "#") || len(ref) == 1 {
			return Paint{}, false
		}
		p := Paint{Gradient: ref[1:], Alpha: 1}
		if rest := strings.TrimSpace(s[end+1:]); rest != "" {
			// The fallback is a colour, none or currentColor: never a
			// reference itself, so this recurses once at most.
			if strings.HasPrefix(rest, "url(") {
				return Paint{}, false
			}
			fb, ok := parsePaint(rest)
			if !ok || fb.Gradient != "" {
				return Paint{}, false
			}
			p.Fallback = &fb
		}
		return p, true
	}
	c, alpha, ok := parseColor(s)
	if !ok {
		return Paint{}, false
	}
	return colorPaint(c, alpha), true
}

// parseColor parses a CSS colour (see csscolor): hex with or without alpha,
// rgb(), rgba(), hsl(), hsla(), transparent and every named colour. alpha is
// the colour's own.
func parseColor(s string) (c Color, alpha float64, ok bool) {
	v, ok := csscolor.Parse(s)
	if !ok {
		return Color{}, 0, false
	}
	return Color{float64(v.R) / 255, float64(v.G) / 255, float64(v.B) / 255}, float64(v.A), true
}

// clamp01 is v within 0..1. v is finite (see attrs.go).
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

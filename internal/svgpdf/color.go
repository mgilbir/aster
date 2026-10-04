package svgpdf

import (
	"fmt"
	"strings"

	"github.com/mgilbir/aster/internal/csscolor"
)

// Color is an RGB color with components in [0, 1].
type Color struct {
	R, G, B float64
}

// Paint is a fill or stroke value: a color and its alpha, "none", or unset
// (inherit).
type Paint struct {
	Color Color
	Alpha float64 // the color's own, multiplied into the fill or stroke opacity
	None  bool
}

// alpha is what the paint multiplies the opacity by: its colour's alpha, or 1
// for none, which paints nothing whatever the opacity.
func (p Paint) alpha() float64 {
	if p.None {
		return 1
	}
	return p.Alpha
}

// parsePaint parses an SVG fill/stroke attribute value.
func parsePaint(s string) (Paint, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "none") {
		return Paint{None: true}, nil
	}
	if strings.HasPrefix(s, "url(") {
		return Paint{}, fmt.Errorf("svgpdf: unsupported paint reference %q (gradients/patterns are not implemented)", s)
	}
	c, alpha, err := parseColor(s)
	if err != nil {
		return Paint{}, err
	}
	if alpha == 0 {
		return Paint{None: true}, nil // transparent, or any colour with no alpha
	}
	return Paint{Color: c, Alpha: alpha}, nil
}

// parseColor parses a CSS colour (see csscolor): hex with or without alpha,
// rgb(), rgba(), hsl(), hsla(), transparent and every named colour. alpha is
// the colour's own.
func parseColor(s string) (c Color, alpha float64, err error) {
	v, ok := csscolor.Parse(s)
	if !ok {
		return Color{}, 0, fmt.Errorf("svgpdf: unsupported color %q", strings.TrimSpace(s))
	}
	return Color{float64(v.R) / 255, float64(v.G) / 255, float64(v.B) / 255}, float64(v.A), nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

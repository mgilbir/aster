package svgpdf

import (
	"fmt"
	"strconv"
	"strings"
)

// Color is an RGB color with components in [0, 1].
type Color struct {
	R, G, B float64
}

// Paint is a fill or stroke value: a color, "none", or unset (inherit).
type Paint struct {
	Color Color
	None  bool
}

// namedColors covers the color keywords Vega emits in practice. Charts
// overwhelmingly use hex colors; keywords appear only for backgrounds and a
// few scheme defaults. Unknown keywords are an error (the caller falls back
// to raster output), never a silent substitute.
var namedColors = map[string]Color{
	"black":     {0, 0, 0},
	"white":     {1, 1, 1},
	"red":       {1, 0, 0},
	"green":     {0, 128.0 / 255, 0},
	"blue":      {0, 0, 1},
	"gray":      {128.0 / 255, 128.0 / 255, 128.0 / 255},
	"grey":      {128.0 / 255, 128.0 / 255, 128.0 / 255},
	"lightgray": {211.0 / 255, 211.0 / 255, 211.0 / 255},
	"lightgrey": {211.0 / 255, 211.0 / 255, 211.0 / 255},
	"darkgray":  {169.0 / 255, 169.0 / 255, 169.0 / 255},
	"darkgrey":  {169.0 / 255, 169.0 / 255, 169.0 / 255},
	"steelblue": {70.0 / 255, 130.0 / 255, 180.0 / 255},
	"firebrick": {178.0 / 255, 34.0 / 255, 34.0 / 255},
	"orange":    {1, 165.0 / 255, 0},
	"yellow":    {1, 1, 0},
	"purple":    {128.0 / 255, 0, 128.0 / 255},
}

// parsePaint parses an SVG fill/stroke attribute value.
func parsePaint(s string) (Paint, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "none", "transparent":
		return Paint{None: true}, nil
	}
	if strings.HasPrefix(s, "url(") {
		return Paint{}, fmt.Errorf("svgpdf: unsupported paint reference %q (gradients/patterns are not implemented)", s)
	}
	c, err := parseColor(s)
	if err != nil {
		return Paint{}, err
	}
	return Paint{Color: c}, nil
}

// parseColor parses #rgb, #rrggbb, rgb(r,g,b) and the named-color subset.
func parseColor(s string) (Color, error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)

	if c, ok := namedColors[lower]; ok {
		return c, nil
	}

	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		switch len(hex) {
		case 3:
			r, errR := strconv.ParseUint(strings.Repeat(string(hex[0]), 2), 16, 8)
			g, errG := strconv.ParseUint(strings.Repeat(string(hex[1]), 2), 16, 8)
			b, errB := strconv.ParseUint(strings.Repeat(string(hex[2]), 2), 16, 8)
			if errR != nil || errG != nil || errB != nil {
				return Color{}, fmt.Errorf("svgpdf: invalid hex color %q", s)
			}
			return Color{float64(r) / 255, float64(g) / 255, float64(b) / 255}, nil
		case 6:
			r, errR := strconv.ParseUint(hex[0:2], 16, 8)
			g, errG := strconv.ParseUint(hex[2:4], 16, 8)
			b, errB := strconv.ParseUint(hex[4:6], 16, 8)
			if errR != nil || errG != nil || errB != nil {
				return Color{}, fmt.Errorf("svgpdf: invalid hex color %q", s)
			}
			return Color{float64(r) / 255, float64(g) / 255, float64(b) / 255}, nil
		default:
			return Color{}, fmt.Errorf("svgpdf: invalid hex color %q", s)
		}
	}

	if strings.HasPrefix(lower, "rgb(") && strings.HasSuffix(s, ")") {
		inner := s[4 : len(s)-1]
		parts := strings.Split(inner, ",")
		if len(parts) != 3 {
			return Color{}, fmt.Errorf("svgpdf: invalid rgb() color %q", s)
		}
		var vals [3]float64
		for i, p := range parts {
			p = strings.TrimSpace(p)
			// rgb() percentages are legal SVG but Vega emits plain 0-255.
			if strings.HasSuffix(p, "%") {
				pct, err := strconv.ParseFloat(strings.TrimSuffix(p, "%"), 64)
				if err != nil {
					return Color{}, fmt.Errorf("svgpdf: invalid rgb() component %q", p)
				}
				vals[i] = clamp01(pct / 100)
				continue
			}
			v, err := strconv.ParseFloat(p, 64)
			if err != nil {
				return Color{}, fmt.Errorf("svgpdf: invalid rgb() component %q", p)
			}
			vals[i] = clamp01(v / 255)
		}
		return Color{vals[0], vals[1], vals[2]}, nil
	}

	return Color{}, fmt.Errorf("svgpdf: unsupported color %q", s)
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
